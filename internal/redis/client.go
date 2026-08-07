package redis

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client 极简 Redis 客户端，手写 RESP2 协议（不引入第三方依赖）。
// 仅实现本缓存用到的命令子集；生产环境建议换 go-redis（连接池、Pipeline、哨兵/集群支持）。
// 面试口径：协议本身很简单 —— 命令按 "*参数个数\r\n$长度\r\n参数\r\n" 编码，
// 响应首字节标识类型（+单行 -错误 :整数 $批量 *数组）。
type Client struct {
	addr     string
	password string
	db       int
	mu       sync.Mutex // 串行化命令，单连接复用
	conn     net.Conn
	reader   *bufio.Reader
}

func NewClient(addr, password string, db int) *Client {
	return &Client{addr: addr, password: password, db: db}
}

// connect 惰性建立连接并完成 AUTH / SELECT
func (c *Client) connect() error {
	if c.conn != nil {
		return nil
	}
	conn, err := net.DialTimeout("tcp", c.addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("redis 连接失败: %w", err)
	}
	c.conn = conn
	c.reader = bufio.NewReader(conn)

	if c.password != "" {
		if _, err := c.doLocked("AUTH", c.password); err != nil {
			c.closeLocked()
			return err
		}
	}
	if c.db > 0 {
		if _, err := c.doLocked("SELECT", strconv.Itoa(c.db)); err != nil {
			c.closeLocked()
			return err
		}
	}
	return nil
}

func (c *Client) closeLocked() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
		c.reader = nil
	}
}

// Close 关闭连接
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeLocked()
}

// Do 执行一条 Redis 命令，返回解析后的响应（string / int64 / []any / nil）。
// 网络层出错时自动重连重试一次。
func (c *Client) Do(args ...string) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	resp, err := c.doLocked(args...)
	if err != nil && isNetError(err) {
		c.closeLocked()
		resp, err = c.doLocked(args...) // 重连后重试一次
	}
	return resp, err
}

func isNetError(err error) bool {
	return !strings.HasPrefix(err.Error(), "redis 错误:")
}

func (c *Client) doLocked(args ...string) (any, error) {
	if err := c.connect(); err != nil {
		return nil, err
	}
	// 编码命令：*N\r\n$len\r\narg\r\n...
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := c.conn.Write([]byte(b.String())); err != nil {
		return nil, err
	}
	return c.parseReply()
}

// parseReply 按首字节解析 RESP2 响应
func (c *Client) parseReply() (any, error) {
	line, err := c.reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 3 {
		return nil, fmt.Errorf("redis 响应格式异常: %q", line)
	}
	body := line[1 : len(line)-2] // 去掉类型字节和 \r\n

	switch line[0] {
	case '+': // 简单字符串，如 +OK
		return body, nil
	case '-': // 错误，如 -WRONGTYPE ...
		return nil, fmt.Errorf("redis 错误: %s", body)
	case ':': // 整数
		return strconv.ParseInt(body, 10, 64)
	case '$': // 批量字符串
		n, _ := strconv.Atoi(body)
		if n < 0 {
			return nil, nil // $-1 = nil
		}
		buf := make([]byte, n+2) // 含末尾 \r\n
		if _, err := readFull(c.reader, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*': // 数组
		n, _ := strconv.Atoi(body)
		if n < 0 {
			return nil, nil
		}
		arr := make([]any, 0, n)
		for i := 0; i < n; i++ {
			item, err := c.parseReply()
			if err != nil {
				return nil, err
			}
			arr = append(arr, item)
		}
		return arr, nil
	}
	return nil, fmt.Errorf("redis 未知响应类型: %q", line)
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
