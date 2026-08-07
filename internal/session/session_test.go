package session

import (
	"testing"
	"time"

	"keyuan/ai-advisor/internal/llm"
)

func TestLocalStoreAppendAndGet(t *testing.T) {
	s := NewLocalStore(time.Hour, 5)
	s.Append("s1", llm.Message{Role: "user", Content: "怎么联系指导师"})
	s.Append("s1", llm.Message{Role: "assistant", Content: "课程群内答疑"})

	msgs := s.Get("s1")
	if len(msgs) != 2 || msgs[0].Content != "怎么联系指导师" {
		t.Fatalf("历史不符: %+v", msgs)
	}
}

func TestLocalStoreTrimWindow(t *testing.T) {
	s := NewLocalStore(time.Hour, 2) // 最多 2 轮 = 4 条
	for i := 0; i < 4; i++ {
		s.Append("s1",
			llm.Message{Role: "user", Content: string(rune('a' + i))},
			llm.Message{Role: "assistant", Content: string(rune('A' + i))})
	}
	msgs := s.Get("s1")
	if len(msgs) != 4 {
		t.Fatalf("应截断到 4 条，实际 %d", len(msgs))
	}
	if msgs[0].Content != "c" { // 最旧的 a/A 轮被丢弃
		t.Fatalf("应丢弃最旧轮次，首条为 %q", msgs[0].Content)
	}
}

func TestLocalStoreExpiry(t *testing.T) {
	s := NewLocalStore(10*time.Millisecond, 5)
	s.Append("s1", llm.Message{Role: "user", Content: "hi"})
	time.Sleep(20 * time.Millisecond)
	if msgs := s.Get("s1"); msgs != nil {
		t.Fatalf("过期会话应返回 nil，实际 %+v", msgs)
	}
}

func TestLocalStoreIsolation(t *testing.T) {
	s := NewLocalStore(time.Hour, 5)
	s.Append("s1", llm.Message{Role: "user", Content: "问题1"})
	if msgs := s.Get("s2"); msgs != nil {
		t.Fatalf("不同会话应隔离，实际 %+v", msgs)
	}
}
