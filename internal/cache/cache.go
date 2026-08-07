package cache

import (
	"sync"
	"time"

	"keyuan/ai-advisor/internal/vectorstore"
)

// AnswerCache 热点答案缓存抽象。
// 语义级缓存：按"问题向量"的相似度匹配，而非字符串精确匹配——
// "怎么联系指导师" / "如何联系指导老师" 命中同一条缓存。
// MVP 提供 LocalCache（内存）实现，生产可替换为 Redis 实现（向量存 Redis，逻辑相同）。
type AnswerCache interface {
	// Lookup 按查询向量找最相似的缓存问题，相似度 >= 阈值视为命中
	Lookup(query []float32) (answer string, sources []vectorstore.SearchResult, score float64, ok bool)
	// Store 缓存一条问答（含问题向量与当时的引用来源）
	Store(question string, query []float32, answer string, sources []vectorstore.SearchResult)
	// Stats 返回 (缓存条数, 命中次数, 未命中次数)
	Stats() (size int, hits, misses int64)
}

type entry struct {
	question  string
	vector    []float32
	answer    string
	sources   []vectorstore.SearchResult
	createdAt time.Time
}

// LocalCache 内存版语义缓存：全量余弦匹配 + TTL 过期 + 容量淘汰（FIFO）。
// 缓存条数千级以内性能足够；真实项目换 Redis 后由 Redis 承担容量与淘汰。
type LocalCache struct {
	mu        sync.RWMutex
	threshold float64
	ttl       time.Duration
	maxSize   int
	entries   []entry
	hits      int64
	misses    int64
}

func NewLocalCache(threshold float64, ttl time.Duration, maxSize int) *LocalCache {
	return &LocalCache{threshold: threshold, ttl: ttl, maxSize: maxSize}
}

func (c *LocalCache) Lookup(query []float32) (string, []vectorstore.SearchResult, float64, bool) {
	c.mu.Lock() // 要更新命中计数，用写锁
	defer c.mu.Unlock()

	now := time.Now()
	bestScore, bestIdx := -1.0, -1
	for i, e := range c.entries {
		if c.ttl > 0 && now.Sub(e.createdAt) > c.ttl {
			continue // 过期条目不参与匹配（写入侧淘汰时统一清理）
		}
		score := vectorstore.CosineSimilarity(query, e.vector)
		if score > bestScore {
			bestScore, bestIdx = score, i
		}
	}
	if bestIdx >= 0 && bestScore >= c.threshold {
		c.hits++
		e := c.entries[bestIdx]
		return e.answer, e.sources, bestScore, true
	}
	c.misses++
	return "", nil, bestScore, false
}

func (c *LocalCache) Store(question string, query []float32, answer string, sources []vectorstore.SearchResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 先清理过期条目，再按容量淘汰最旧的
	if c.ttl > 0 {
		now := time.Now()
		kept := c.entries[:0]
		for _, e := range c.entries {
			if now.Sub(e.createdAt) <= c.ttl {
				kept = append(kept, e)
			}
		}
		c.entries = kept
	}
	for len(c.entries) >= c.maxSize {
		c.entries = c.entries[1:]
	}
	c.entries = append(c.entries, entry{
		question:  question,
		vector:    query,
		answer:    answer,
		sources:   sources,
		createdAt: time.Now(),
	})
}

func (c *LocalCache) Stats() (int, int64, int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries), c.hits, c.misses
}
