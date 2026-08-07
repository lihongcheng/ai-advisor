package vectorstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// QdrantStore Qdrant 向量数据库实现，直接走 REST API。
// 启动：docker run -p 6333:6333 qdrant/qdrant
type QdrantStore struct {
	baseURL    string
	collection string
	dim        int
	client     *http.Client
}

func NewQdrantStore(baseURL, collection string, dim int) *QdrantStore {
	return &QdrantStore{
		baseURL:    baseURL,
		collection: collection,
		dim:        dim,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
}

// EnsureCollection 幂等创建集合（已存在时 Qdrant 返回错误，忽略即可）
func (q *QdrantStore) EnsureCollection() error {
	body := map[string]any{
		"vectors": map[string]any{
			"size":     q.dim,
			"distance": "Cosine",
		},
	}
	_, err := q.do(http.MethodPut, "/collections/"+q.collection, body)
	// 已存在会报 409，这里简化处理：任何错误都打印但可用，Search/Upsert 会再次暴露真实问题
	if err != nil {
		fmt.Printf("[qdrant] create collection: %v（若已存在可忽略）\n", err)
	}
	return nil
}

func (q *QdrantStore) Upsert(chunks []Chunk) error {
	points := make([]map[string]any, 0, len(chunks))
	for _, c := range chunks {
		points = append(points, map[string]any{
			"id":     c.ID, // 要求 UUID 或 uint64，这里用 UUID
			"vector": c.Vector,
			"payload": map[string]any{
				"source":  c.Source,
				"content": c.Content,
			},
		})
	}
	_, err := q.do(http.MethodPut, "/collections/"+q.collection+"/points", map[string]any{"points": points})
	return err
}

func (q *QdrantStore) Search(query []float32, topK int) ([]SearchResult, error) {
	respBody, err := q.do(http.MethodPost, "/collections/"+q.collection+"/points/search", map[string]any{
		"vector":       query,
		"limit":        topK,
		"with_payload": true,
	})
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Result []struct {
			ID      any     `json:"id"`
			Score   float64 `json:"score"`
			Payload struct {
				Source  string `json:"source"`
				Content string `json:"content"`
			} `json:"payload"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(parsed.Result))
	for _, r := range parsed.Result {
		results = append(results, SearchResult{
			Chunk: Chunk{
				ID:      fmt.Sprint(r.ID),
				Source:  r.Payload.Source,
				Content: r.Payload.Content,
			},
			Score: r.Score,
		})
	}
	return results, nil
}

func (q *QdrantStore) Count() (int, error) {
	respBody, err := q.do(http.MethodPost, "/collections/"+q.collection+"/points/count", map[string]any{"exact": true})
	if err != nil {
		return 0, err
	}
	var parsed struct {
		Result struct {
			Count int `json:"count"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return 0, err
	}
	return parsed.Result.Count, nil
}

// List 通过 scroll API 分页拉取全部切片（含向量与 payload）
func (q *QdrantStore) List() ([]Chunk, error) {
	var all []Chunk
	var offset any // nil 表示第一页；之后传 Qdrant 返回的 next_page_offset
	for {
		body := map[string]any{
			"limit":        256,
			"with_payload": true,
			"with_vector":  true,
		}
		if offset != nil {
			body["offset"] = offset
		}
		respBody, err := q.do(http.MethodPost, "/collections/"+q.collection+"/points/scroll", body)
		if err != nil {
			return nil, err
		}
		var parsed struct {
			Result struct {
				Points []struct {
					ID      any       `json:"id"`
					Vector  []float32 `json:"vector"`
					Payload struct {
						Source  string `json:"source"`
						Content string `json:"content"`
					} `json:"payload"`
				} `json:"points"`
				NextPageOffset any `json:"next_page_offset"`
			} `json:"result"`
		}
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			return nil, err
		}
		for _, p := range parsed.Result.Points {
			all = append(all, Chunk{
				ID:      fmt.Sprint(p.ID),
				Source:  p.Payload.Source,
				Content: p.Payload.Content,
				Vector:  p.Vector,
			})
		}
		if parsed.Result.NextPageOffset == nil {
			break
		}
		offset = parsed.Result.NextPageOffset
	}
	return all, nil
}

func (q *QdrantStore) do(method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, q.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := q.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("qdrant %s %s -> %d: %s", method, path, resp.StatusCode, string(raw))
	}
	return raw, nil
}
