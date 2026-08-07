package vectorstore

import "math"

// Chunk 一条知识切片
type Chunk struct {
	ID      string    `json:"id"`
	Source  string    `json:"source"`  // 来源文档名
	Content string    `json:"content"` // 切片原文
	Vector  []float32 `json:"vector"`
}

// SearchResult 检索命中结果
type SearchResult struct {
	Chunk
	Score float64 `json:"score"` // 余弦相似度，越大越相关
}

// Store 向量数据库抽象。
// MVP 提供 local（JSON 文件）实现，生产可无缝切换 qdrant 实现，
// 上层 RAG 流程不感知底层是哪种向量数据库。
type Store interface {
	// Upsert 批量写入（或覆盖）切片
	Upsert(chunks []Chunk) error
	// Search 按查询向量做 ANN/全量余弦检索，返回 TopK
	Search(query []float32, topK int) ([]SearchResult, error)
	// List 返回库内全部切片（含向量），用于可视化/运维后台
	List() ([]Chunk, error)
	// Count 当前库内切片总数
	Count() (int, error)
}

// CosineSimilarity 余弦相似度
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
