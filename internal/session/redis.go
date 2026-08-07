package session

import (
	"encoding/json"
	"strconv"
	"time"

	"keyuan/ai-advisor/internal/llm"
	"keyuan/ai-advisor/internal/redis"
)

// Redis 键位：aiadvisor:session:<id>  LIST，元素为消息 JSON，整体 EX 过期（滑动续期）
const redisSessionPrefix = "aiadvisor:session:"

// RedisStore Redis 版会话存储：多实例部署时共享会话上下文
type RedisStore struct {
	client   *redis.Client
	ttl      time.Duration
	maxTurns int
}

func NewRedisStore(client *redis.Client, ttl time.Duration, maxTurns int) *RedisStore {
	return &RedisStore{client: client, ttl: ttl, maxTurns: maxTurns}
}

func (s *RedisStore) Get(id string) []llm.Message {
	resp, err := s.client.Do("LRANGE", redisSessionPrefix+id, "0", "-1")
	if err != nil {
		return nil // Redis 故障时降级为无历史单轮问答，不影响主链路
	}
	items, _ := resp.([]any)
	msgs := make([]llm.Message, 0, len(items))
	for _, it := range items {
		raw, ok := it.(string)
		if !ok {
			continue
		}
		var m llm.Message
		if json.Unmarshal([]byte(raw), &m) == nil && m.Role != "" {
			msgs = append(msgs, m)
		}
	}
	return trim(msgs, s.maxTurns)
}

func (s *RedisStore) Append(id string, msgs ...llm.Message) error {
	key := redisSessionPrefix + id
	for _, m := range msgs {
		raw, err := json.Marshal(m)
		if err != nil {
			continue
		}
		if _, err := s.client.Do("RPUSH", key, string(raw)); err != nil {
			return err
		}
	}
	// 窗口截断：只保留最近 maxTurns*2 条
	if s.maxTurns > 0 {
		_, _ = s.client.Do("LTRIM", key, strconv.Itoa(-s.maxTurns*2), "-1")
	}
	// 滑动续期：每次有交互就重新计时
	if s.ttl > 0 {
		_, _ = s.client.Do("EXPIRE", key, strconv.Itoa(int(s.ttl.Seconds())))
	}
	return nil
}
