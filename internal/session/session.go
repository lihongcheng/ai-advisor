package session

import (
	"sync"
	"time"

	"keyuan/ai-advisor/internal/llm"
)

// Store 会话上下文存储抽象：保存每个会话的原始问答对（不含 RAG 注入的参考资料），
// 生成时注入 system 与当前问题之间，让模型能理解"那他呢""还有吗"这类追问。
// local 内存版用于开发，redis 版用于生产（多实例共享会话）。
type Store interface {
	// Get 取会话最近 maxTurns 轮历史（按时间正序）；过期或不存在返回 nil
	Get(id string) []llm.Message
	// Append 追加消息（通常成对：一条 user 一条 assistant），并刷新 TTL
	Append(id string, msgs ...llm.Message) error
}

type localEntry struct {
	msgs      []llm.Message
	updatedAt time.Time
}

// LocalStore 内存版会话存储：map + TTL + 窗口截断
type LocalStore struct {
	mu       sync.Mutex
	ttl      time.Duration
	maxTurns int
	data     map[string]*localEntry
}

func NewLocalStore(ttl time.Duration, maxTurns int) *LocalStore {
	return &LocalStore{ttl: ttl, maxTurns: maxTurns, data: make(map[string]*localEntry)}
}

func (s *LocalStore) Get(id string) []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[id]
	if !ok {
		return nil
	}
	if s.ttl > 0 && time.Since(e.updatedAt) > s.ttl {
		delete(s.data, id)
		return nil
	}
	return trim(e.msgs, s.maxTurns)
}

func (s *LocalStore) Append(id string, msgs ...llm.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[id]
	if !ok {
		e = &localEntry{}
		s.data[id] = e
	}
	e.msgs = append(e.msgs, msgs...)
	e.msgs = trim(e.msgs, s.maxTurns) // 超窗丢弃最旧的轮次
	e.updatedAt = time.Now()
	return nil
}

// trim 只保留最近 maxTurns 轮（一轮 = user + assistant 两条）
func trim(msgs []llm.Message, maxTurns int) []llm.Message {
	maxLen := maxTurns * 2
	if maxTurns > 0 && len(msgs) > maxLen {
		return msgs[len(msgs)-maxLen:]
	}
	return msgs
}
