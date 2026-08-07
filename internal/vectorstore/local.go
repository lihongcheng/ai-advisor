package vectorstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// LocalStore 本地 JSON 持久化向量库。
// 全量余弦检索，万级切片以内性能足够，面试演示/开发调试开箱即用。
type LocalStore struct {
	path string
	mu   sync.RWMutex
	data []Chunk
}

func NewLocalStore(path string) (*LocalStore, error) {
	s := &LocalStore{path: path}
	if _, err := os.Stat(path); err == nil {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &s.data); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

func (s *LocalStore) Upsert(chunks []Chunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	index := make(map[string]int, len(s.data))
	for i, c := range s.data {
		index[c.ID] = i
	}
	for _, c := range chunks {
		if i, ok := index[c.ID]; ok {
			s.data[i] = c // 同 ID 覆盖，支持知识更新
		} else {
			s.data = append(s.data, c)
		}
	}

	if dir := filepath.Dir(s.path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o644)
}

func (s *LocalStore) Search(query []float32, topK int) ([]SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]SearchResult, 0, len(s.data))
	for _, c := range s.data {
		score := CosineSimilarity(query, c.Vector)
		results = append(results, SearchResult{Chunk: c, Score: score})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })

	if topK > len(results) {
		topK = len(results)
	}
	return results[:topK], nil
}

func (s *LocalStore) Count() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data), nil
}

// List 返回全部切片的拷贝（含向量）
func (s *LocalStore) List() ([]Chunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Chunk, len(s.data))
	copy(out, s.data)
	return out, nil
}
