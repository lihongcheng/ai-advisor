package main

import (
	"log"
	"net/http"
	"time"

	"keyuan/ai-advisor/internal/api"
	"keyuan/ai-advisor/internal/cache"
	"keyuan/ai-advisor/internal/config"
	"keyuan/ai-advisor/internal/embedding"
	"keyuan/ai-advisor/internal/ingest"
	"keyuan/ai-advisor/internal/llm"
	"keyuan/ai-advisor/internal/rag"
	"keyuan/ai-advisor/internal/redis"
	"keyuan/ai-advisor/internal/session"
	"keyuan/ai-advisor/internal/vectorstore"
)

func main() {
	cfg := config.Load()

	// 1. 向量数据库：接口抽象，local / qdrant 通过配置切换
	var store vectorstore.Store
	switch cfg.VectorStore {
	case "qdrant":
		qs := vectorstore.NewQdrantStore(cfg.QdrantURL, cfg.QdrantCollection, cfg.EmbeddingDim)
		if err := qs.EnsureCollection(); err != nil {
			log.Printf("[warn] qdrant 集合初始化: %v", err)
		}
		store = qs
		log.Printf("[init] 向量数据库: Qdrant %s / %s", cfg.QdrantURL, cfg.QdrantCollection)
	default:
		ls, err := vectorstore.NewLocalStore(cfg.LocalStorePath)
		if err != nil {
			log.Fatalf("[fatal] 本地向量库初始化失败: %v", err)
		}
		store = ls
		log.Printf("[init] 向量数据库: local (%s)", cfg.LocalStorePath)
	}

	// 2. 热点答案缓存 + 会话上下文：redis 生产版（共用一条连接）/ local 内存版
	var answerCache cache.AnswerCache
	var sessions session.Store
	cacheTTL := time.Duration(cfg.CacheTTLMin) * time.Minute
	sessionTTL := time.Duration(cfg.SessionTTLMin) * time.Minute
	redisOK := false
	if cfg.AnswerCache == "redis" {
		rc := redis.NewClient(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
		redisCache := cache.NewRedisCache(rc, cfg.CacheThreshold, cacheTTL, cfg.CacheMaxSize)
		if err := redisCache.Ping(); err != nil {
			log.Printf("[warn] %v，缓存与会话回退 local 内存实现", err)
		} else {
			answerCache = redisCache
			sessions = session.NewRedisStore(rc, sessionTTL, cfg.SessionMaxTurns)
			redisOK = true
			log.Printf("[init] 热点答案缓存: redis %s/db%d (阈值=%.2f, TTL=%dmin, 容量=%d)",
				cfg.RedisAddr, cfg.RedisDB, cfg.CacheThreshold, cfg.CacheTTLMin, cfg.CacheMaxSize)
			log.Printf("[init] 会话上下文: redis (TTL=%dmin, 最大 %d 轮)", cfg.SessionTTLMin, cfg.SessionMaxTurns)
		}
	}
	if !redisOK {
		answerCache = cache.NewLocalCache(cfg.CacheThreshold, cacheTTL, cfg.CacheMaxSize)
		sessions = session.NewLocalStore(sessionTTL, cfg.SessionMaxTurns)
		log.Printf("[init] 热点答案缓存: local (阈值=%.2f, TTL=%dmin, 容量=%d)", cfg.CacheThreshold, cfg.CacheTTLMin, cfg.CacheMaxSize)
		log.Printf("[init] 会话上下文: local (TTL=%dmin, 最大 %d 轮)", cfg.SessionTTLMin, cfg.SessionMaxTurns)
	}

	// 3. 各依赖组件
	embedder := embedding.NewClient(cfg.EmbeddingAPIKey, cfg.EmbeddingBaseURL, cfg.EmbeddingModel)
	deepseek := llm.NewDeepSeekClient(cfg.DeepSeekAPIKey, cfg.DeepSeekBaseURL, cfg.DeepSeekModel)
	pipeline := rag.NewPipeline(embedder, store, deepseek, answerCache)
	ingester := ingest.NewService(embedder, store)
	handler := api.NewHandler(pipeline, ingester, store, answerCache, sessions)

	// 4. 路由：API + 演示页面
	mux := http.NewServeMux()
	handler.Routes(mux)
	mux.Handle("/", http.FileServer(http.Dir("web")))

	log.Printf("[start] AI 健康顾问服务已启动: http://localhost:%s", cfg.Port)
	log.Printf("[start] 演示页面: http://localhost:%s/  |  问答接口: POST /api/v1/chat", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, mux); err != nil {
		log.Fatal(err)
	}
}
