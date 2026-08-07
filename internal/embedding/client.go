package embedding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client OpenAI 兼容的 Embedding 接口客户端。
// DeepSeek 没有 Embedding 接口，MVP 默认接硅基流动的 BGE-M3（中文检索效果好、有免费额度），
// 也可换成任何 OpenAI 兼容的 Embedding 服务（阿里云百炼、OpenAI text-embedding-3 等）。
type Client struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

func NewClient(apiKey, baseURL, model string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed 批量向量化文本，返回与输入一一对应的向量
func (c *Client) Embed(texts []string) ([][]float32, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("EMBEDDING_API_KEY 未配置")
	}
	body, _ := json.Marshal(embedRequest{Model: c.model, Input: texts})
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding API %d: %s", resp.StatusCode, string(raw))
	}
	var parsed embedResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embedding 返回数量 %d 与输入 %d 不一致", len(parsed.Data), len(texts))
	}
	vectors := make([][]float32, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		vectors = append(vectors, d.Embedding)
	}
	return vectors, nil
}

// EmbedOne 单文本向量化（查询侧用）
func (c *Client) EmbedOne(text string) ([]float32, error) {
	vectors, err := c.Embed([]string{text})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}
