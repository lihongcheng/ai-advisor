package ingest

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"keyuan/ai-advisor/internal/embedding"
	"keyuan/ai-advisor/internal/vectorstore"
)

// Service 知识库导入服务：文档读取 -> 切分 -> 向量化 -> 写入向量库
type Service struct {
	embedder *embedding.Client
	store    vectorstore.Store
}

func NewService(embedder *embedding.Client, store vectorstore.Store) *Service {
	return &Service{embedder: embedder, store: store}
}

// chunkID 由文档名+切片序号生成稳定 UUID（同文档重复导入时覆盖旧切片）
func chunkID(source string, idx int) string {
	sum := md5.Sum([]byte(fmt.Sprintf("%s#%d", source, idx)))
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// IngestDocument 导入单篇文档，返回写入的切片数
func (s *Service) IngestDocument(source, content string) (int, error) {
	pieces := ChunkText(content)
	if len(pieces) == 0 {
		return 0, fmt.Errorf("文档 %s 切分结果为空", source)
	}

	// 批量向量化（真实项目数据量大时应走 MQ 异步 + 分批，MVP 单批即可）
	vectors, err := s.embedder.Embed(pieces)
	if err != nil {
		return 0, fmt.Errorf("向量化失败: %w", err)
	}

	chunks := make([]vectorstore.Chunk, 0, len(pieces))
	for i, piece := range pieces {
		chunks = append(chunks, vectorstore.Chunk{
			ID:      chunkID(source, i),
			Source:  source,
			Content: piece,
			Vector:  vectors[i],
		})
	}
	if err := s.store.Upsert(chunks); err != nil {
		return 0, fmt.Errorf("写入向量库失败: %w", err)
	}
	return len(chunks), nil
}

// IngestDir 导入目录下全部 .md / .txt 文档，返回 "文档名:切片数" 明细
func (s *Service) IngestDir(dir string) (map[string]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	result := make(map[string]int)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".md" && ext != ".txt" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return result, fmt.Errorf("读取 %s 失败: %w", name, err)
		}
		n, err := s.IngestDocument(name, string(raw))
		if err != nil {
			return result, err
		}
		result[name] = n
	}
	if len(result) == 0 {
		return result, fmt.Errorf("目录 %s 下没有可导入的 .md/.txt 文档", dir)
	}
	return result, nil
}
