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
