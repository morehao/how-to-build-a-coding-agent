package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

type readFileInput struct {
	Path   string `json:"path" jsonschema:"required" jsonschema_description:"Path to the file, relative to the workspace root."`
	Offset int    `json:"offset,omitempty" jsonschema_description:"1-based line number to start reading from. Default 1."`
	Limit  int    `json:"limit,omitempty" jsonschema_description:"Maximum number of lines to return. Default 400, max 2000."`
}

func (o *Options) readFile(_ context.Context, in readFileInput) (string, error) {
	path, err := o.Workspace.Resolve(in.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", o.Workspace.Rel(path), err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory; use list_files", o.Workspace.Rel(path))
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", o.Workspace.Rel(path))
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 400
	}
	if limit > 2000 {
		limit = 2000
	}
	offset := in.Offset
	if offset <= 0 {
		offset = 1
	}
	if info.Size() > 10*1024*1024 && in.Limit == 0 {
		return "", fmt.Errorf("%s is %s; pass limit (and optionally offset) to read a slice", o.Workspace.Rel(path), humanBytes(info.Size()))
	}

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", o.Workspace.Rel(path), err)
	}
	defer f.Close()

	head := make([]byte, 8192)
	n, _ := f.Read(head)
	if strings.IndexByte(string(head[:n]), 0) >= 0 {
		return "", fmt.Errorf("%s looks like a binary file", o.Workspace.Rel(path))
	}
	if _, err := f.Seek(0, 0); err != nil {
		return "", fmt.Errorf("seek %s: %w", o.Workspace.Rel(path), err)
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var out strings.Builder
	lineNo := 0
	written := 0
	more := false
	for scanner.Scan() {
		lineNo++
		if lineNo < offset {
			continue
		}
		if written >= limit {
			more = true
			break
		}
		line := scanner.Text()
		if !utf8.ValidString(line) {
			return "", fmt.Errorf("%s contains non-UTF-8 data near line %d", o.Workspace.Rel(path), lineNo)
		}
		line = truncateRunes(line, 2000)
		fmt.Fprintf(&out, "%6d | %s\n", lineNo, line)
		written++
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read %s: %w", o.Workspace.Rel(path), err)
	}
	if written == 0 {
		return fmt.Sprintf("No content in %s at offset %d (file has %d lines)", o.Workspace.Rel(path), offset, lineNo), nil
	}
	header := fmt.Sprintf("%s (lines %d-%d):\n", o.Workspace.Rel(path), offset, offset+written-1)
	result := header + out.String()
	if more {
		result += fmt.Sprintf("... [more lines available; continue with offset=%d]\n", offset+written)
	}
	return result, nil
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}

func humanBytes(n int64) string {
	switch {
	case n >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1024*1024*1024))
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
