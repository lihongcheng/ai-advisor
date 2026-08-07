package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"keyuan/ai-advisor/internal/cache"
	"keyuan/ai-advisor/internal/ingest"
	"keyuan/ai-advisor/internal/llm"
	"keyuan/ai-advisor/internal/rag"
	"keyuan/ai-advisor/internal/session"
	"keyuan/ai-advisor/internal/vectorstore"
)

// Handler HTTP 接口层，仅用标准库实现（面试时可说明：为控制依赖体积，
// MVP 用 net/http + Flusher 原生实现 SSE；生产环境迁移到 Gin 只是路由层的替换）
type Handler struct {
	pipeline *rag.Pipeline
	ingester *ingest.Service
	store    vectorstore.Store
	cache    cache.AnswerCache
	sessions session.Store // 会话上下文存储，nil 表示未启用多轮
}

func NewHandler(pipeline *rag.Pipeline, ingester *ingest.Service, store vectorstore.Store, c cache.AnswerCache, sessions session.Store) *Handler {
	return &Handler{pipeline: pipeline, ingester: ingester, store: store, cache: c, sessions: sessions}
}

// Routes 注册路由（Go 1.21 的 ServeMux 不支持方法匹配，方法校验放在各 handler 内）
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/health", h.Health)
	mux.HandleFunc("/api/v1/ingest", h.Ingest)
	mux.HandleFunc("/api/v1/chat", h.Chat)
	mux.HandleFunc("/api/v1/chunks", h.Chunks)
	mux.HandleFunc("/api/v1/debug/search", h.DebugSearch)
}

func methodGuard(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "仅支持 " + method})
		return false
	}
	return true
}

// backendName 返回组件当前实际使用的后端名（Redis PING 失败回退 local 时也能反映真实状态）
func backendName(v any) string {
	switch v.(type) {
	case *vectorstore.LocalStore:
		return "local"
	case *vectorstore.QdrantStore:
		return "qdrant"
	case *cache.LocalCache:
		return "local"
	case *cache.RedisCache:
		return "redis"
	}
	return "unknown"
}

// Health 健康检查 + 后端类型 + 库内切片数 + 缓存命中率
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	count, _ := h.store.Count()
	resp := map[string]any{
		"status":        "ok",
		"chunks":        count,
		"vector_store":  backendName(h.store),
		"answer_cache":  backendName(h.cache),
	}
	if h.cache != nil {
		size, hits, misses := h.cache.Stats()
		var hitRate float64
		if total := hits + misses; total > 0 {
			hitRate = float64(hits) / float64(total)
		}
		resp["cache"] = map[string]any{
			"size": size, "hits": hits, "misses": misses,
			"hit_rate": fmt.Sprintf("%.1f%%", hitRate*100),
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type ingestRequest struct {
	// 二选一：直接给文档内容，或给服务器上的目录路径批量导入
	Source  string `json:"source"`
	Content string `json:"content"`
	Dir     string `json:"dir"`
}

// Ingest 知识库导入接口：切片 -> 向量化 -> 写入向量数据库
func (h *Handler) Ingest(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req ingestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体不是合法 JSON"})
		return
	}

	if req.Dir != "" {
		detail, err := h.ingester.IngestDir(req.Dir)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"imported": detail})
		return
	}

	if req.Source == "" || strings.TrimSpace(req.Content) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "需提供 dir，或 source + content"})
		return
	}
	n, err := h.ingester.IngestDocument(req.Source, req.Content)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": map[string]int{req.Source: n}})
}

type chatRequest struct {
	Question  string `json:"question"`
	SessionID string `json:"session_id"` // 空 = 单轮模式；多轮由前端生成并复用
}

type sourceVO struct {
	Source  string  `json:"source"`
	Score   float64 `json:"score"`
	Content string  `json:"content"`
}

// Chat 问答接口，SSE 流式输出：
//
//	event: sources  -> 命中的知识切片（先推送，前端可展示引用来源）
//	event: token    -> 模型逐 token 输出
//	event: done     -> 结束，附完整答案
func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodPost) {
		return
	}
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Question) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "question 不能为空"})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "服务端不支持流式响应"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	sendEvent := func(event string, data any) bool {
		raw, _ := json.Marshal(data)
		_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
		if err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// 多轮上下文：有 session_id 且启用了会话存储时，取出历史注入 Prompt
	var history []llm.Message
	if h.sessions != nil && req.SessionID != "" {
		history = h.sessions.Get(req.SessionID)
	}

	contexts, answer, err := h.pipeline.Answer(req.Question, history, func(token string) error {
		if !sendEvent("token", map[string]string{"token": token}) {
			return io.ErrClosedPipe
		}
		return nil
	})

	// 先把引用来源推给前端（即使生成失败也能看到检索结果）
	sources := make([]sourceVO, 0, len(contexts))
	for _, c := range contexts {
		sources = append(sources, sourceVO{Source: c.Source, Score: c.Score, Content: c.Content})
	}
	sendEvent("sources", sources)

	// 生成成功后把本轮原始问答写入会话历史（只存原文，不存注入的参考资料）
	if err == nil && answer != "" && h.sessions != nil && req.SessionID != "" {
		_ = h.sessions.Append(req.SessionID,
			llm.Message{Role: "user", Content: req.Question},
			llm.Message{Role: "assistant", Content: answer},
		)
	}

	if err != nil {
		sendEvent("error", map[string]string{"message": err.Error()})
		return
	}
	sendEvent("done", map[string]string{"answer": answer})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Chunks 列出向量库全部切片（含向量），供管理台可视化
func (h *Handler) Chunks(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	chunks, err := h.store.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	dim := 0
	if len(chunks) > 0 {
		dim = len(chunks[0].Vector)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total":  len(chunks),
		"dim":    dim,
		"chunks": chunks,
	})
}

// DebugSearch 检索调试：输入问题，返回未过滤的 TopK 结果、分数与阈值，
// 管理台据此展示"这个问题会命中哪些切片、哪些会因低分被过滤"
func (h *Handler) DebugSearch(w http.ResponseWriter, r *http.Request) {
	if !methodGuard(w, r, http.MethodGet) {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "q 不能为空"})
		return
	}
	topK := 10
	if v := r.URL.Query().Get("top_k"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
			topK = n
		}
	}

	embedMs, results, err := h.pipeline.DebugSearch(q, topK)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	type item struct {
		Source  string  `json:"source"`
		Content string  `json:"content"`
		Score   float64 `json:"score"`
		Adopted bool    `json:"adopted"` // 是否过置信度阈值、会被注入 Prompt
	}
	items := make([]item, 0, len(results))
	for _, r := range results {
		items = append(items, item{
			Source:  r.Source,
			Content: r.Content,
			Score:   r.Score,
			Adopted: r.Score >= rag.MinScore,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"question":  q,
		"embed_ms":  embedMs,
		"threshold": rag.MinScore,
		"results":   items,
	})
}
