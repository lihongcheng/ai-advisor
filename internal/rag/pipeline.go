package rag

import (
	"fmt"
	"log"
	"strings"
	"time"

	"keyuan/ai-advisor/internal/cache"
	"keyuan/ai-advisor/internal/embedding"
	"keyuan/ai-advisor/internal/llm"
	"keyuan/ai-advisor/internal/vectorstore"
)

// TopK 检索返回的切片数
const TopK = 4

// MinScore 召回置信度阈值，低于该分数认为知识库没有相关内容
const MinScore = 0.35

// SystemPrompt 健康顾问人设 + 合规约束，健康类产品的关键设计
const SystemPrompt = `你是科原健康的 AI 健康顾问，服务对象是中老年营养食疗课程的用户。

回答要求：
1. 严格基于"参考资料"回答，参考资料没有的内容不要编造，可以坦诚说明"这个问题超出了我掌握的资料范围"。
2. 语气温和、通俗，避免专业术语堆砌，适合中老年人阅读。
3. 绝对禁止给出疾病诊断、处方、用药建议；涉及疾病治疗的问题，建议用户咨询医生。
4. 不得承诺任何疗效，不使用"根治""治愈""保证有效"等绝对化表述。
5. 回答结尾固定附上："以上内容仅供参考，不构成医疗建议。"`

// Pipeline RAG 主流程：缓存 -> 检索 -> 组装 Prompt -> LLM 生成
type Pipeline struct {
	embedder *embedding.Client
	store    vectorstore.Store
	llm      *llm.DeepSeekClient
	cache    cache.AnswerCache // 热点答案缓存，可为 nil（关闭缓存）
}

func NewPipeline(embedder *embedding.Client, store vectorstore.Store, llmClient *llm.DeepSeekClient, c cache.AnswerCache) *Pipeline {
	return &Pipeline{embedder: embedder, store: store, llm: llmClient, cache: c}
}

// searchByVector 向量库 TopK 检索，过滤低分结果
func (p *Pipeline) searchByVector(queryVec []float32) ([]vectorstore.SearchResult, error) {
	results, err := p.store.Search(queryVec, TopK)
	if err != nil {
		return nil, fmt.Errorf("向量检索失败: %w", err)
	}
	filtered := results[:0]
	for _, r := range results {
		if r.Score >= MinScore {
			filtered = append(filtered, r)
		}
	}
	return filtered, nil
}

// Retrieve 问题向量化 + 向量库检索（对外保留的独立检索入口）
func (p *Pipeline) Retrieve(question string) ([]vectorstore.SearchResult, error) {
	queryVec, err := p.embedder.EmbedOne(question)
	if err != nil {
		return nil, fmt.Errorf("问题向量化失败: %w", err)
	}
	return p.searchByVector(queryVec)
}

// DebugSearch 检索调试：返回未做阈值过滤的 TopK 结果与向量化耗时，供管理台可视化
func (p *Pipeline) DebugSearch(question string, topK int) (int64, []vectorstore.SearchResult, error) {
	start := time.Now()
	queryVec, err := p.embedder.EmbedOne(question)
	if err != nil {
		return 0, nil, fmt.Errorf("问题向量化失败: %w", err)
	}
	embedMs := time.Since(start).Milliseconds()
	results, err := p.store.Search(queryVec, topK)
	if err != nil {
		return embedMs, nil, fmt.Errorf("向量检索失败: %w", err)
	}
	return embedMs, results, nil
}

// BuildMessages 组装 Prompt：系统人设 + 多轮历史 + 知识注入 + 当前问题。
// 历史里只放用户的原始问题和模型的原始回答（不含注入的参考资料），
// 避免历史把过期知识带进新问题的上下文。
func (p *Pipeline) BuildMessages(question string, contexts []vectorstore.SearchResult, history []llm.Message) []llm.Message {
	var refs strings.Builder
	if len(contexts) == 0 {
		refs.WriteString("（知识库中未检索到相关资料）")
	}
	for i, c := range contexts {
		fmt.Fprintf(&refs, "【资料%d】（来源：%s）\n%s\n\n", i+1, c.Source, c.Content)
	}

	userContent := fmt.Sprintf("参考资料：\n%s\n用户问题：%s", refs.String(), question)
	messages := []llm.Message{{Role: "system", Content: SystemPrompt}}
	messages = append(messages, history...)
	messages = append(messages, llm.Message{Role: "user", Content: userContent})
	return messages
}

// Answer 完整问答：缓存查询 -> 检索 -> 流式生成 -> 缓存写入。
// history 为该会话的多轮历史（原始问答对），无会话时传 nil 即单轮模式。
// 全程分阶段计时并打日志：
// 慢请求定位顺序 = embed（向量化）-> cache -> search（检索）-> ttft（首token）-> gen（生成总长）
func (p *Pipeline) Answer(question string, history []llm.Message, onToken func(token string) error) ([]vectorstore.SearchResult, string, error) {
	totalStart := time.Now()

	// 1. 问题向量化（缓存匹配与向量检索共用，一次调用）
	embedStart := time.Now()
	queryVec, err := p.embedder.EmbedOne(question)
	if err != nil {
		return nil, "", fmt.Errorf("问题向量化失败: %w", err)
	}
	embedDur := time.Since(embedStart)

	// 2. 热点答案缓存：语义相似命中则直接返回，跳过检索 + LLM，省一次 DeepSeek 调用
	if p.cache != nil {
		if answer, sources, score, ok := p.cache.Lookup(queryVec); ok {
			log.Printf("[rag] q=%q cache=hit score=%.3f total=%dms（跳过检索与LLM）",
				question, score, time.Since(totalStart).Milliseconds())
			if onToken != nil {
				_ = onToken(answer) // 缓存答案一次性推给前端
			}
			return sources, answer, nil
		}
	}

	// 3. 向量检索
	searchStart := time.Now()
	contexts, err := p.searchByVector(queryVec)
	if err != nil {
		return nil, "", err
	}
	searchDur := time.Since(searchStart)

	// 4. 组装 Prompt（含多轮历史）+ DeepSeek 流式生成
	messages := p.BuildMessages(question, contexts, history)

	llmStart := time.Now()
	var ttft time.Duration
	answer, err := p.llm.ChatStream(messages, func(token string) error {
		if ttft == 0 {
			ttft = time.Since(llmStart) // 首个 token 到达时间
		}
		if onToken != nil {
			return onToken(token)
		}
		return nil
	})
	genDur := time.Since(llmStart)
	if err != nil {
		return contexts, "", err
	}

	// 5. 写缓存：空答案不缓存，避免把异常响应沉淀进缓存
	if p.cache != nil && strings.TrimSpace(answer) != "" {
		p.cache.Store(question, queryVec, answer, contexts)
	}

	log.Printf("[rag] q=%q cache=miss embed=%dms search=%dms hits=%d ttft=%dms gen=%dms total=%dms",
		question, embedDur.Milliseconds(), searchDur.Milliseconds(), len(contexts),
		ttft.Milliseconds(), genDur.Milliseconds(), time.Since(totalStart).Milliseconds())
	return contexts, answer, nil
}
