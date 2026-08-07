package cache

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"keyuan/ai-advisor/internal/redis"
	"keyuan/ai-advisor/internal/vectorstore"
)

// Redis 键位设计：
//
//	aiadvisor:cache:entry:<md5(question)>  STRING  缓存条目 JSON（带 EX 过期）
//	aiadvisor:cache:index                  ZSET    member=条目键，score=创建时间戳（用于容量淘汰）
//	aiadvisor:cache:hits / :misses         STRING  命中计数器（INCR）
//
// 语义匹配逻辑与 LocalCache 一致：Redis 只承担"存取"，余弦比较仍在 Go 侧做。
// 量大的演进方向：换 Redis Stack 的向量检索（FT.SEARCH + HNSW），把比对下沉到 Redis 内。
const (
	redisEntryPrefix = "aiadvisor:cache:entry:"
	redisIndexKey    = "aiadvisor:cache:index"
	redisHitsKey     = "aiadvisor:cache:hits"
	redisMissesKey   = "aiadvisor:cache:misses"
)

// RedisCache Redis 版语义缓存，生产形态。
type RedisCache struct {
	client    *redis.Client
	threshold float64
	ttl       time.Duration
	maxSize   int
}

type redisEntry struct {
	Question  string                      `json:"question"`
	Vector    []float32                   `json:"vector"`
	Answer    string                      `json:"answer"`
	Sources   []vectorstore.SearchResult  `json:"sources"`
	CreatedAt int64                       `json:"created_at"` // Unix 秒
}

func NewRedisCache(client *redis.Client, threshold float64, ttl time.Duration, maxSize int) *RedisCache {
	return &RedisCache{client: client, threshold: threshold, ttl: ttl, maxSize: maxSize}
}

func entryKey(question string) string {
	sum := md5.Sum([]byte(question))
	return redisEntryPrefix + hex.EncodeToString(sum[:])
}

func (c *RedisCache) Lookup(query []float32) (string, []vectorstore.SearchResult, float64, bool) {
	// 1. 取出全部缓存条目键
	resp, err := c.client.Do("ZRANGE", redisIndexKey, "0", "-1")
	if err != nil {
		return "", nil, 0, false // Redis 故障时降级为 miss，不影响主链路
	}
	keys, _ := resp.([]any)

	// 2. 逐条取出并做余弦比对（与 LocalCache 全量扫描同一逻辑）
	now := time.Now().Unix()
	bestScore, best := -1.0, (*redisEntry)(nil)
	for _, k := range keys {
		key, _ := k.(string)
		raw, err := c.client.Do("GET", key)
		if err != nil {
			continue
		}
		str, ok := raw.(string)
		if !ok {
			_ = c.zrem(key) // 索引里残留但条目已过期，顺手清理
			continue
		}
		var e redisEntry
		if json.Unmarshal([]byte(str), &e) != nil {
			continue
		}
		if c.ttl > 0 && now-e.CreatedAt > int64(c.ttl.Seconds()) {
			continue
		}
		if score := vectorstore.CosineSimilarity(query, e.Vector); score > bestScore {
			bestScore, best = score, &e
		}
	}

	// 3. 判定 + 计数
	if best != nil && bestScore >= c.threshold {
		_, _ = c.client.Do("INCR", redisHitsKey)
		return best.Answer, best.Sources, bestScore, true
	}
	_, _ = c.client.Do("INCR", redisMissesKey)
	return "", nil, bestScore, false
}

func (c *RedisCache) Store(question string, query []float32, answer string, sources []vectorstore.SearchResult) {
	key := entryKey(question)
	e := redisEntry{
		Question: question, Vector: query, Answer: answer,
		Sources: sources, CreatedAt: time.Now().Unix(),
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}

	// 写入条目（带 EX 过期）并登记到索引 ZSET
	args := []string{"SET", key, string(raw)}
	if c.ttl > 0 {
		args = append(args, "EX", strconv.Itoa(int(c.ttl.Seconds())))
	}
	if _, err := c.client.Do(args...); err != nil {
		return
	}
	_, _ = c.client.Do("ZADD", redisIndexKey, strconv.FormatInt(e.CreatedAt, 10), key)

	// 容量淘汰：超出 maxSize 时删掉最旧的
	if resp, err := c.client.Do("ZCARD", redisIndexKey); err == nil {
		if size, ok := resp.(int64); ok && int(size) > c.maxSize {
			excess := int(size) - c.maxSize
			if old, err := c.client.Do("ZRANGE", redisIndexKey, "0", strconv.Itoa(excess-1)); err == nil {
				if keys, ok := old.([]any); ok {
					for _, k := range keys {
						if ks, ok := k.(string); ok {
							_, _ = c.client.Do("DEL", ks)
							_ = c.zrem(ks)
						}
					}
				}
			}
		}
	}
}

func (c *RedisCache) zrem(key string) error {
	_, err := c.client.Do("ZREM", redisIndexKey, key)
	return err
}

func (c *RedisCache) Stats() (int, int64, int64) {
	var size, hits, misses int64
	if resp, err := c.client.Do("ZCARD", redisIndexKey); err == nil {
		size, _ = resp.(int64)
	}
	if resp, err := c.client.Do("GET", redisHitsKey); err == nil {
		if s, ok := resp.(string); ok {
			hits, _ = strconv.ParseInt(s, 10, 64)
		}
	}
	if resp, err := c.client.Do("GET", redisMissesKey); err == nil {
		if s, ok := resp.(string); ok {
			misses, _ = strconv.ParseInt(s, 10, 64)
		}
	}
	return int(size), hits, misses
}

// Ping 连通性检查（启动时打日志用）
func (c *RedisCache) Ping() error {
	resp, err := c.client.Do("PING")
	if err != nil {
		return fmt.Errorf("redis PING 失败: %w", err)
	}
	if s, ok := resp.(string); !ok || s != "PONG" {
		return fmt.Errorf("redis PING 响应异常: %v", resp)
	}
	return nil
}
