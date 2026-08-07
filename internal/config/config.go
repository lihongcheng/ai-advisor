package config

import (
	"os"
	"strconv"
)

// Config 全局配置，全部通过环境变量注入，密钥不落代码
type Config struct {
	Port string

	DeepSeekAPIKey  string
	DeepSeekBaseURL string
	DeepSeekModel   string

	EmbeddingAPIKey  string
	EmbeddingBaseURL string
	EmbeddingModel   string
	EmbeddingDim     int

	VectorStore       string // local | qdrant
	LocalStorePath    string
	QdrantURL         string
	QdrantCollection  string

	CacheThreshold float64 // 语义缓存命中阈值（余弦相似度）
	CacheTTLMin    int     // 缓存有效期（分钟）
	CacheMaxSize   int     // 缓存容量上限

	AnswerCache   string // local | redis
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	SessionTTLMin   int // 会话上下文有效期（分钟），滑动续期
	SessionMaxTurns int // 注入 Prompt 的最大历史轮数
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func Load() *Config {
	dim, _ := strconv.Atoi(env("EMBEDDING_DIM", "1024"))
	threshold, _ := strconv.ParseFloat(env("CACHE_THRESHOLD", "0.92"), 64)
	ttlMin, _ := strconv.Atoi(env("CACHE_TTL_MINUTES", "1440"))
	maxSize, _ := strconv.Atoi(env("CACHE_MAX_SIZE", "1000"))
	redisDB, _ := strconv.Atoi(env("REDIS_DB", "0"))
	sessionTTL, _ := strconv.Atoi(env("SESSION_TTL_MINUTES", "30"))
	sessionTurns, _ := strconv.Atoi(env("SESSION_MAX_TURNS", "5"))
	return &Config{
		Port:              env("PORT", "8080"),
		DeepSeekAPIKey:    env("DEEPSEEK_API_KEY", ""),
		DeepSeekBaseURL:   env("DEEPSEEK_BASE_URL", "https://api.deepseek.com"),
		DeepSeekModel:     env("DEEPSEEK_MODEL", "deepseek-chat"),
		EmbeddingAPIKey:   env("EMBEDDING_API_KEY", ""),
		EmbeddingBaseURL:  env("EMBEDDING_BASE_URL", "https://api.siliconflow.cn/v1"),
		EmbeddingModel:    env("EMBEDDING_MODEL", "BAAI/bge-m3"),
		EmbeddingDim:      dim,
		VectorStore:       env("VECTOR_STORE", "local"),
		LocalStorePath:    env("LOCAL_STORE_PATH", "data/vector_store.json"),
		QdrantURL:         env("QDRANT_URL", "http://localhost:6333"),
		QdrantCollection:  env("QDRANT_COLLECTION", "health_knowledge"),
		CacheThreshold:    threshold,
		CacheTTLMin:       ttlMin,
		CacheMaxSize:      maxSize,
		AnswerCache:       env("ANSWER_CACHE", "local"),
		RedisAddr:         env("REDIS_ADDR", "localhost:6379"),
		RedisPassword:     env("REDIS_PASSWORD", ""),
		RedisDB:           redisDB,
		SessionTTLMin:     sessionTTL,
		SessionMaxTurns:   sessionTurns,
	}
}
