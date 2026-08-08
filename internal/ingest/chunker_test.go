package ingest

import (
	"strings"
	"testing"
)

func TestChunkTextShortDoc(t *testing.T) {
	doc := "第一段：高血压要控盐。\n\n第二段：糖尿病要控糖。"
	chunks := ChunkText(doc)
	if len(chunks) != 1 {
		t.Fatalf("短文档应合并为 1 个切片，实际 %d 个", len(chunks))
	}
	if !strings.Contains(chunks[0], "控盐") || !strings.Contains(chunks[0], "控糖") {
		t.Fatalf("切片应包含两段内容: %q", chunks[0])
	}
}

func TestChunkTextParagraphSplit(t *testing.T) {
	// 构造多段落，总长度超过窗口，验证按段落边界切分
	var paras []string
	for i := 0; i < 6; i++ {
		paras = append(paras, strings.Repeat("段落内容", 40)) // 每段 160 rune
	}
	chunks := ChunkText(strings.Join(paras, "\n\n"))
	if len(chunks) < 2 {
		t.Fatalf("长文档应切为多个切片，实际 %d 个", len(chunks))
	}
	for _, c := range chunks {
		if len([]rune(c)) > MaxChunkRunes {
			t.Fatalf("切片超长: %d runes", len([]rune(c)))
		}
	}
}

func TestChunkTextLongParagraphOverlap(t *testing.T) {
	// 单段落超长，验证硬切 + 重叠
	long := strings.Repeat("一二三四五六七八九十", 200) // 2000 runes
	chunks := ChunkText(long)
	if len(chunks) < 2 {
		t.Fatalf("超长单段应硬切为多个切片，实际 %d 个", len(chunks))
	}
	// 相邻切片应有重叠内容
	first := []rune(chunks[0])
	second := []rune(chunks[1])
	tail := string(first[len(first)-OverlapRunes:])
	if !strings.Contains(string(second), tail) {
		t.Fatalf("相邻切片应保留 %d 字重叠", OverlapRunes)
	}
}

func TestChunkTextEmpty(t *testing.T) {
	if chunks := ChunkText("   \n\n  "); len(chunks) != 0 {
		t.Fatalf("空文档应返回 0 个切片，实际 %d 个", len(chunks))
	}
}

func TestChunkTextFAQEntries(t *testing.T) {
	doc := `# 高血压人群饮食 FAQ

## 高血压人群能吃盐吗？

需要严格控盐，每日不超过 5 克。

## 高血压人群能喝酒吗？

建议戒酒或严格限量。`
	chunks := ChunkText(doc)
	if len(chunks) != 2 {
		t.Fatalf("FAQ 应按条目切为 2 个切片，实际 %d 个: %v", len(chunks), chunks)
	}
	for _, c := range chunks {
		if !strings.HasPrefix(c, "高血压人群饮食 FAQ\n## ") {
			t.Fatalf("每个切片应带文档标题前缀并以条目开头: %q", c)
		}
	}
	if !strings.Contains(chunks[0], "控盐") || strings.Contains(chunks[0], "戒酒") {
		t.Fatalf("条目不应被合并或切散: %q", chunks[0])
	}
}

func TestChunkTextFAQLongEntryHardSplit(t *testing.T) {
	doc := "## 超长条目？\n\n" + strings.Repeat("一二三四五六七八九十", 100) // 1000 runes
	chunks := ChunkText(doc)
	if len(chunks) < 2 {
		t.Fatalf("超长条目应硬切为多个切片，实际 %d 个", len(chunks))
	}
	first := []rune(chunks[0])
	tail := string(first[len(first)-OverlapRunes:])
	if !strings.Contains(chunks[1], tail) {
		t.Fatalf("超长条目硬切后相邻切片应保留 %d 字重叠", OverlapRunes)
	}
}

func TestChunkTextNoEntryFallsBack(t *testing.T) {
	// 含 "##" 但不是行首条目结构（如行内出现），应回退段落切分
	doc := "说明：评分标准是 100 ## 制。\n\n第二段内容。"
	chunks := ChunkText(doc)
	if len(chunks) != 1 || !strings.Contains(chunks[0], "## 制") {
		t.Fatalf("无条目结构应回退通用切分: %v", chunks)
	}
}
