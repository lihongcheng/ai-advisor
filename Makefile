# AI 健康顾问智能客服 —— 常用操作入口
# make help 查看全部命令

ifneq (,$(wildcard .env))
include .env
export
endif

# 本机 Go 1.21 在新版 macOS 上内部链接器有兼容问题，统一用外部链接
GOFLAGS := -ldflags=-linkmode=external
BIN     := bin/ai-advisor
BASE    := http://localhost:8080

.DEFAULT_GOAL := help

## help: 查看全部命令
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | column -t -s ':'

## build: 编译服务二进制
build:
	go build $(GOFLAGS) -o $(BIN) ./cmd/server

## run: 本地模式启动（local 向量库 + local 缓存，无需 Docker）
run: build
	./$(BIN)

## run-prod: 生产模式启动（Qdrant + Redis，需先 make up）
run-prod: build
	VECTOR_STORE=qdrant ANSWER_CACHE=redis ./$(BIN)

## test: 运行全部单元测试
test:
	go test $(GOFLAGS) ./internal/...

## vet: 静态检查
vet:
	go vet ./...

## up: 启动基础设施（Qdrant + Redis）
up:
	docker compose up -d

## down: 停止基础设施（数据保留在卷中）
down:
	docker compose down

## ps: 查看基础设施状态
ps:
	docker compose ps

## logs: 跟踪基础设施日志
logs:
	docker compose logs -f

## ingest: 导入示例知识库（需服务已启动）
ingest:
	curl -s -X POST $(BASE)/api/v1/ingest \
	  -H 'Content-Type: application/json' \
	  -d '{"dir": "data/docs"}'

## health: 健康检查（含后端类型与缓存命中率）
health:
	@curl -s $(BASE)/api/v1/health | python3 -m json.tool

## chat: 命令行试聊（SSE 流式）
chat:
	curl -s -N -X POST $(BASE)/api/v1/chat \
	  -H 'Content-Type: application/json' \
	  -d '{"question": "糖尿病人能吃红薯吗", "session_id": "makefile-demo"}'

## verify-qdrant: 核验 Qdrant 中的向量数据
verify-qdrant:
	@curl -s http://localhost:6333/collections/health_knowledge | \
	  python3 -c "import json,sys; d=json.load(sys.stdin)['result']; print('向量数:', d['points_count'], '| 维度:', d['config']['params']['vectors']['size'])"

## verify-redis: 核验 Redis 中的缓存与会话数据
verify-redis:
	@docker exec ai-advisor-redis redis-cli ZRANGE aiadvisor:cache:index 0 -1
	@docker exec ai-advisor-redis redis-cli MGET aiadvisor:cache:hits aiadvisor:cache:misses

## clean: 清理编译产物
clean:
	rm -rf bin
