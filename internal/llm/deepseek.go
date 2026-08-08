package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message 一条对话消息
type Message struct {
	Role    string `json:"role"` // system / user / assistant
	Content string `json:"content"`
}

// DeepSeekClient DeepSeek Chat API 客户端（OpenAI 兼容协议）
type DeepSeekClient struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

// 流式请求的超时设计：
//   - ResponseHeaderTimeout：连接建立后等待响应头（≈首 token 前置耗时）的上限，
//     DeepSeek 高峰期排队僵死时快速失败，而不是无限等待；
//   - 整体 ctx 超时：流式生成中途僵死（连接活着但不吐字）的兜底，
//     正常长回答一般几十秒内完成，120s 足够宽裕。
const (
	headerTimeout  = 30 * time.Second
	overallTimeout = 120 * time.Second
)

func NewDeepSeekClient(apiKey, baseURL, model string) *DeepSeekClient {
	return &DeepSeekClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		http: &http.Client{
			Transport: &http.Transport{ResponseHeaderTimeout: headerTimeout},
		},
	}
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

// ChatStream 流式调用 DeepSeek，token 通过回调逐个吐出，返回完整答案。
// 对应 SSE 场景：用户端逐字输出，体感响应快。
func (c *DeepSeekClient) ChatStream(messages []Message, onToken func(token string) error) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("DEEPSEEK_API_KEY 未配置")
	}
	body, _ := json.Marshal(chatRequest{Model: c.model, Messages: messages, Stream: true})
	ctx, cancel := context.WithTimeout(context.Background(), overallTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("deepseek API %d: %s", resp.StatusCode, string(raw))
	}

	var full strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		token := chunk.Choices[0].Delta.Content
		if token == "" {
			continue
		}
		full.WriteString(token)
		if onToken != nil {
			if err := onToken(token); err != nil {
				return full.String(), err
			}
		}
	}
	return full.String(), scanner.Err()
}
