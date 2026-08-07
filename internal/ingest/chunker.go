package ingest

import "strings"

const (
	// MaxChunkRunes 单个切片最大长度（按 rune 计，约 500 字）
	MaxChunkRunes = 500
	// OverlapRunes 相邻切片重叠长度，缓解跨切片语义断裂
	OverlapRunes = 50
)

// ChunkText 把一篇文档切成若干知识切片。
// 策略：段落感知的滑动窗口 —— 优先按空行段落聚合，单段落超长时硬切并保留重叠。
// 真实项目里 FAQ/食谱这类结构化内容会按条目整体入库，这里演示通用切分。
func ChunkText(text string) []string {
	paragraphs := splitParagraphs(text)
	var chunks []string
	var buf []rune

	flush := func() {
		if len(strings.TrimSpace(string(buf))) > 0 {
			chunks = append(chunks, strings.TrimSpace(string(buf)))
		}
	}

	for _, p := range paragraphs {
		pr := []rune(p)
		// 单段落本身就超长：先把缓冲区落库，再对长段落硬切（带重叠）
		if len(pr) > MaxChunkRunes {
			flush()
			buf = buf[:0]
			chunks = append(chunks, hardSplit(p)...)
			continue
		}
		if len(buf)+len(pr)+1 > MaxChunkRunes {
			flush()
			buf = buf[:0]
		}
		if len(buf) > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, pr...)
	}
	flush()
	return chunks
}

// splitParagraphs 按空行切段，去掉空段落
func splitParagraphs(text string) []string {
	parts := strings.Split(text, "\n\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// hardSplit 超长段落按窗口硬切，相邻切片保留 OverlapRunes 重叠
func hardSplit(text string) []string {
	runes := []rune(strings.TrimSpace(text))
	var chunks []string
	step := MaxChunkRunes - OverlapRunes
	for start := 0; start < len(runes); start += step {
		end := start + MaxChunkRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
		if end == len(runes) {
			break
		}
	}
	return chunks
}
