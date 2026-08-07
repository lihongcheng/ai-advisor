package cache

import (
	"testing"
	"time"

	"keyuan/ai-advisor/internal/vectorstore"
)

func TestLookupHitSameVector(t *testing.T) {
	c := NewLocalCache(0.92, time.Hour, 100)
	vec := []float32{1, 0, 0}
	c.Store("怎么联系指导师", vec, "课程群内答疑", nil)

	answer, _, score, ok := c.Lookup(vec)
	if !ok {
		t.Fatal("相同问题向量应命中缓存")
	}
	if answer != "课程群内答疑" || score < 0.99 {
		t.Fatalf("命中结果不符: answer=%q score=%f", answer, score)
	}
}

func TestLookupHitSimilarVector(t *testing.T) {
	c := NewLocalCache(0.9, time.Hour, 100)
	c.Store("怎么联系指导师", []float32{1, 0.1, 0}, "答案A", nil)

	// 高度相似但非完全相同的向量，应命中
	_, _, score, ok := c.Lookup([]float32{1, 0.12, 0})
	if !ok || score < 0.9 {
		t.Fatalf("相似问题应命中: ok=%v score=%f", ok, score)
	}
}

func TestLookupMissDifferentVector(t *testing.T) {
	c := NewLocalCache(0.92, time.Hour, 100)
	c.Store("怎么联系指导师", []float32{1, 0, 0}, "答案A", nil)

	// 正交向量（完全不相关的问题），不应命中
	if _, _, _, ok := c.Lookup([]float32{0, 1, 0}); ok {
		t.Fatal("不相关问题不应命中缓存")
	}
}

func TestLookupExpired(t *testing.T) {
	c := NewLocalCache(0.92, 10*time.Millisecond, 100)
	vec := []float32{1, 0, 0}
	c.Store("问题", vec, "答案", nil)

	time.Sleep(20 * time.Millisecond)
	if _, _, _, ok := c.Lookup(vec); ok {
		t.Fatal("过期缓存不应命中")
	}
}

func TestEvictByMaxSize(t *testing.T) {
	c := NewLocalCache(0.92, time.Hour, 2)
	c.Store("q1", []float32{1, 0, 0}, "a1", nil)
	c.Store("q2", []float32{0, 1, 0}, "a2", nil)
	c.Store("q3", []float32{0, 0, 1}, "a3", nil)

	size, _, _ := c.Stats()
	if size != 2 {
		t.Fatalf("超出容量应淘汰到 maxSize，实际 %d", size)
	}
	// 最旧的 q1 已被淘汰
	if _, _, _, ok := c.Lookup([]float32{1, 0, 0}); ok {
		t.Fatal("最旧的条目应被淘汰")
	}
}

func TestStats(t *testing.T) {
	c := NewLocalCache(0.92, time.Hour, 100)
	vec := []float32{1, 0, 0}
	c.Store("q", vec, "a", []vectorstore.SearchResult{{Score: 0.9}})

	c.Lookup(vec)              // hit
	c.Lookup([]float32{0, 1, 0}) // miss

	size, hits, misses := c.Stats()
	if size != 1 || hits != 1 || misses != 1 {
		t.Fatalf("统计不符: size=%d hits=%d misses=%d", size, hits, misses)
	}
}
