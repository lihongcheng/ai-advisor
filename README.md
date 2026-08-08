# AI 健康顾问智能客服（RAG MVP）

基于公司内部知识库的智能问答系统，面向科原健康中老年营养食疗课程用户。
技术栈：**Golang + DeepSeek API + Embedding（BGE-M3）+ 向量数据库（local / Qdrant）+ 语义缓存与会话（local / Redis）+ SSE 流式输出**。

## 系统架构

```
用户提问（带 session_id）
   │
   ▼
POST /api/v1/chat ───────────────────────────┐
   │                                         │
   ▼                                         │
问题向量化（Embedding API，一次调用两处复用）    │
   │                                         │
   ▼                                         │
语义答案缓存查询（local/Redis，按问题向量相似度） │
   │                                         │
   ├─ 命中（≥0.92 且首轮/单轮）→ 直接返回缓存答案 ┤
   │                                         │
   ▼ 未命中 / 有会话历史（skip）                │
向量数据库 TopK 检索 + 阈值过滤                │
   │                                         │
   ▼                                         │
Prompt 组装（系统人设 + 多轮历史 + 知识注入）    │
   │                                         │
   ▼                                         │
DeepSeek API 流式生成 ────────────────────────┘
   │
   ├─ 首轮/单轮 → 写入语义缓存
   └─ 写入会话历史（Redis LIST，滑动 TTL）
   │
   ▼
SSE 逐 token 推送给前端（附引用来源）

离线链路：文档 → 结构感知切分(FAQ按条目/长文按段落,500字+50重叠) → 批量向量化 → 写入向量库
```

## 目录结构

```
ai-advisor/
├── cmd/server/main.go          # 入口：依赖装配 + 路由
├── internal/
│   ├── config/                 # 环境变量配置
│   ├── embedding/              # Embedding API 客户端（OpenAI 兼容协议）
│   ├── vectorstore/            # 向量库抽象接口 + local/qdrant 两种实现
│   ├── cache/                  # 语义缓存抽象接口 + local/redis 两种实现（含手写 RESP 客户端）
│   ├── ingest/                 # 文档切分 + 导入（切分→向量化→入库）
│   ├── llm/                    # DeepSeek 流式调用客户端
│   ├── rag/                    # RAG 编排：缓存→检索→Prompt 组装→生成
│   └── api/                    # HTTP 接口（ingest / chat / health）
├── data/docs/                  # 示例知识库文档（高血压/糖尿病 FAQ、课程问答）
├── web/index.html              # 演示聊天页面
└── .env.example                # 配置模板
```

## 快速开始

```bash
# 1. 配置密钥（DeepSeek 开放平台 + 硅基流动，均有免费额度）
cp .env.example .env   # 填入 DEEPSEEK_API_KEY 和 EMBEDDING_API_KEY
export $(grep -v '^#' .env | xargs)

# 2. 启动服务（注意：本机 Go 1.21 在新版 macOS 上需外部链接模式）
go run -ldflags='-linkmode=external' ./cmd/server

# 3. 导入示例知识库（切分 → 向量化 → 写入向量库）
curl -X POST http://localhost:8080/api/v1/ingest \
  -H 'Content-Type: application/json' \
  -d '{"dir": "data/docs"}'

# 4. 问答测试
curl -N -X POST http://localhost:8080/api/v1/chat \
  -H 'Content-Type: application/json' \
  -d '{"question": "糖尿病人能吃红薯吗？"}'

# 5. 浏览器打开演示页面
open http://localhost:8080/
```

## 生产模式（Docker 起 Qdrant + Redis）

默认配置（local 向量库 + local 缓存）开箱即用；切到生产组件只需两个容器 + 两个环境变量：

```bash
# 1. 拉起基础设施（compose 编排，含数据卷持久化与健康检查）
make up          # = docker compose up -d

# 2. 切换实现并启动
make run-prod    # = VECTOR_STORE=qdrant ANSWER_CACHE=redis ./bin/ai-advisor

# 3. 导入知识库 → 提问（数据落在 compose 管理的容器和卷里）
make ingest
make chat

# 4. 核验数据真实落盘
make verify-qdrant   # 向量数 / 维度
make verify-redis    # 缓存条目 / 命中计数
```

常用命令一览（`make help` 随时查看）：

| 命令 | 作用 |
|---|---|
| `make run` | 本地模式启动（无需 Docker） |
| `make run-prod` | 生产模式启动（Qdrant + Redis） |
| `make build` / `make test` / `make vet` | 编译 / 单测 / 静态检查 |
| `make up` / `make down` / `make ps` / `make logs` | 基础设施生命周期 |
| `make ingest` / `make chat` / `make health` | 导入 / 试聊 / 健康检查 |
| `make verify-qdrant` / `make verify-redis` | 外部核验容器内数据 |

## API

| 接口 | 方法 | 说明 |
|---|---|---|
| `/api/v1/health` | GET | 健康检查，返回库内切片数 + 缓存命中率 |
| `/api/v1/ingest` | POST | 知识导入：`{"dir": "data/docs"}` 或 `{"source": "xxx.md", "content": "..."}` |
| `/api/v1/chunks` | GET | 列出向量库全部切片（含向量），管理台数据源 |
| `/api/v1/debug/search?q=...` | GET | 检索调试：未过滤 TopK 结果 + 分数 + 阈值，可视化"哪些切片会被采用" |

页面入口：问答页 `/`，知识库管理台 `/admin.html`（概览统计 + 来源分布 + 检索调试器 + 切片向量预览）。
| `/api/v1/chat` | POST | 问答（SSE 流式）：`{"question": "..."}`，事件流为 `token` → `sources` → `done` / `error` |

## 关键设计点（面试讲解要点）

1. **向量库接口抽象**（`vectorstore.Store`）：local 实现（JSON 持久化 + 全量余弦检索）用于开发演示，Qdrant 实现（REST API + ANN）用于生产，一行配置切换，上层 RAG 流程无感知。
2. **热点答案语义缓存**（`cache.AnswerCache`）：按问题向量相似度（而非字符串）匹配缓存——"怎么联系指导师"和"如何联系指导老师"（相似度 0.944）命中同一条缓存；命中后跳过检索与 LLM，实测响应从 2.7s 降到 0.13s（约 21 倍），并省去一次 DeepSeek 调用成本。带 TTL 过期、容量淘汰、命中率统计（见 `/api/v1/health`）。**上下文安全**：仅在无会话历史（单轮/首轮）时查缓存与写缓存——追问句的答案依赖上下文，按问题向量命中会造成跨会话错配。**双实现**：local 内存版开箱即用；redis 版（条目 JSON + ZSET 索引 + INCR 计数，含手写 RESP2 协议客户端，Redis 故障自动降级为 miss 不影响主链路）——`ANSWER_CACHE=redis` 切换。
3. **多轮对话上下文**（`session.Store`）：会话历史存 Redis LIST（`aiadvisor:session:<id>`，TTL 30 分钟滑动续期、最多注入最近 5 轮），前端 localStorage 持久化 session_id。"那他们的答疑时段是几点"这类带指代的追问，靠历史注入让模型正确理解。历史只存原始问答对，不存注入的参考资料，避免过期知识污染后续上下文；Redis 故障降级为单轮。local 内存版供开发模式使用。
4. **结构感知切分**：自动识别文档结构选策略——含 `## ` 条目的 FAQ 文档按条目整体入库（一条问答一个切片，不切散不合并，文档一级标题作为前缀补全条目主题）；无结构长文本回退段落感知滑动窗口（空行段落聚合到 500 字窗口，单段超长硬切并保留 50 字重叠，缓解跨切片语义断裂）。
5. **合规安全**：系统 Prompt 中硬性约束——禁止诊断/用药建议、禁止疗效承诺、固定附免责声明；检索置信度低于阈值时不注入资料，模型明确回复"超出资料范围"，控制幻觉。
6. **SSE 流式输出**：DeepSeek `stream=true`，token 逐个透传给前端，首字延迟从整段生成的数秒降到 1 秒内。
7. **来源可溯源**：SSE 单独推送 `sources` 事件（命中切片 + 相关度分数），前端展示引用来源，便于运营审核答案可靠性。
8. **全链路分阶段计时**：每次问答输出 `embed / cache / search / ttft / gen / total` 耗时日志，慢请求一眼定位慢在向量化、检索还是 LLM。
9. **超时与降级设计**：任何外部依赖故障都不让服务整体不可用——DeepSeek 响应头超时 30s（高峰僵死快速失败）+ 整体 120s（流式中途僵死兜底）；Redis 每条命令 3s 读写 deadline（防 goroutine 永久阻塞），故障时缓存降级为 miss、会话降级为单轮，恢复后自动重连；Embedding 20s 超时 + 失败重试一次；Qdrant 30s 超时，失败走 SSE error 事件。实测：停掉 Redis 容器期间服务照常应答，恢复后自动重连。
9. **可扩展方向**（二期）：混合检索（向量+BM25）、Rerank 重排、追问的查询改写（用历史把"那他呢"重写为完整问题再检索）、超长历史的 LLM 摘要压缩、RocketMQ 异步向量化、DeepSeek 超时熔断降级。

## 已知限制（MVP 边界）

- 追问场景（"那他呢"）的检索仍以原句向量化，召回可能偏差；二期用查询改写解决（见上）。
- local 向量库为全量扫描，万级切片以内可用；生产切 Qdrant/Milvus。
- 无鉴权限流（生产在 Nginx 网关层做）。
