package ingest

import "strings"

const (
	// MaxChunkRunes 单个切片最大长度（按 rune 计，约 500 字）
	MaxChunkRunes = 500
	// OverlapRunes 相邻切片重叠长度，缓解跨切片语义断裂
	OverlapRunes = 50
)

// ChunkText 把一篇文档切成若干知识切片，自动识别文档结构选择策略：
//   - 含 "## " 条目标题（FAQ/问答类文档）→ 按条目整体切分（chunkByEntries）
//   - 无结构长文本 → 段落感知的滑动窗口，优先按空行段落聚合，单段落超长时硬切并保留重叠
func ChunkText(text string) []string {
	if chunks, ok := chunkByEntries(text); ok {
		return chunks
	}
	return chunkByParagraphs(text)
}

// chunkByEntries 按 "## " 条目切分：一个条目（问题+答案）一个切片，不切散不合并。
// 文档的 "# 一级标题" 会作为上下文前缀拼进每个切片，避免条目脱离文档主题后语义残缺
// （例如 "学不会可以退款吗？" 单独向量化会丢失"课程"这个主题）。
// 单个条目超限时退化为硬切 + 重叠；不含 "## " 时返回 ok=false 走通用切分。
func chunkByEntries(text string) ([]string, bool) {
	if !strings.Contains(text, "## ") {
		return nil, false
	}

	var docTitle string
	var entries []string
	var buf []string

	flush := func() {
		entry := strings.TrimSpace(strings.Join(buf, "\n"))
		if entry != "" {
			entries = append(entries, entry)
		}
		buf = buf[:0]
	}

	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "# ") && docTitle == "" && len(entries) == 0 && len(buf) == 0:
			docTitle = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		case strings.HasPrefix(line, "## "):
			flush()
			buf = append(buf, line)
		default:
			buf = append(buf, line)
		}
	}
	flush()

	if len(entries) == 0 || !strings.HasPrefix(entries[0], "## ") {
		return nil, false // 没有识别到条目结构，回退通用切分
	}

	chunks := make([]string, 0, len(entries))
	for _, entry := range entries {
		chunk := entry
		if docTitle != "" {
			chunk = docTitle + "\n" + entry
		}
		if len([]rune(chunk)) > MaxChunkRunes {
			chunks = append(chunks, hardSplit(chunk)...)
			continue
		}
		chunks = append(chunks, chunk)
	}
	return chunks, true
}

// chunkByParagraphs 通用切分：段落感知的滑动窗口 —— 优先按空行段落聚合，单段落超长时硬切并保留重叠。
func chunkByParagraphs(text string) []string {
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
