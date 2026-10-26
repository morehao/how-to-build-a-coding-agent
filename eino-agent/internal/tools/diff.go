package tools

import (
	"fmt"
	"strings"
)

const maxApprovalDiffBytes = 64 * 1024

// unifiedDiff renders a compact line-oriented diff for approval. edit_file
// guarantees that oldStr occurs exactly once, so the changed fragment is
// already known; we do not need a general-purpose diff algorithm.
func unifiedDiff(path, oldContent, newContent, oldStr, newStr string) string {
	if oldStr == "" {
		return newFileDiff(path, newContent)
	}
	idx := strings.Index(oldContent, oldStr)
	if idx < 0 {
		return fmt.Sprintf("--- a/%s\n+++ b/%s\n@@ -1,1 +1,1 @@\n- (old content unavailable)\n+ (new content)\n", path, path)
	}

	// Expand a partial-fragment replacement to whole lines so the preview
	// does not contain artificial blank lines when oldStr/newStr omit the
	// surrounding newline.
	lineStart := strings.LastIndexByte(oldContent[:idx], '\n') + 1
	oldLineEnd := lineEndAfter(oldContent, idx+len(oldStr))
	newLineEnd := lineEndAfter(newContent, idx+len(newStr))
	oldBlock := oldContent[lineStart:oldLineEnd]
	newBlock := newContent[lineStart:newLineEnd]

	beforeLines := splitDiffLines(oldContent[:lineStart])
	afterLines := splitDiffLines(oldContent[oldLineEnd:])
	const context = 3
	if len(beforeLines) > context {
		beforeLines = beforeLines[len(beforeLines)-context:]
	}
	if len(afterLines) > context {
		afterLines = afterLines[:context]
	}
	start := len(splitDiffLines(oldContent[:lineStart])) + 1 - len(beforeLines)
	if start < 1 {
		start = 1
	}
	oldLines := splitDiffLines(oldBlock)
	newLines := splitDiffLines(newBlock)
	oldCount := len(beforeLines) + len(oldLines) + len(afterLines)
	newCount := len(beforeLines) + len(newLines) + len(afterLines)

	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	if oldCount == 0 {
		fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", newCount)
	} else {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", start, oldCount, start, newCount)
	}
	writeDiffLines(&b, " ", beforeLines)
	writeDiffLines(&b, "-", oldLines)
	writeDiffLines(&b, "+", newLines)
	writeDiffLines(&b, " ", afterLines)
	return capDiff(b.String())
}

func lineEndAfter(s string, pos int) int {
	if pos > 0 && s[pos-1] == '\n' {
		return pos
	}
	if j := strings.IndexByte(s[pos:], '\n'); j >= 0 {
		return pos + j + 1
	}
	return len(s)
}

func newFileDiff(path, content string) string {
	lines := splitDiffLines(content)
	var b strings.Builder
	fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", path, len(lines))
	writeDiffLines(&b, "+", lines)
	return capDiff(b.String())
}

func writeDiffLines(b *strings.Builder, prefix string, lines []string) {
	for _, line := range lines {
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// A trailing newline is represented by an empty final element; it does
	// not add a visible diff line.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func capDiff(s string) string {
	if len(s) <= maxApprovalDiffBytes {
		return s
	}
	return s[:maxApprovalDiffBytes] + "\n... [diff truncated for approval]\n"
}
