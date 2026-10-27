// Package patch parses and applies a small, strict subset of unified diffs.
// It intentionally supports the format models actually emit: multiple files,
// multiple hunks, context lines, new files and deletions. It rejects malformed
// patches instead of guessing.
package patch

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// FilePatch is one file's changes.
type FilePatch struct {
	OldPath    string
	NewPath    string
	Hunks      []Hunk
	NewFile    bool
	DeleteFile bool
	NoNewline  bool
}

func (f FilePatch) Path() string {
	if f.NewPath != "" && f.NewPath != "/dev/null" {
		return f.NewPath
	}
	return f.OldPath
}

// Hunk is one @@ block.
type Hunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Lines    []HunkLine
}

// HunkLine is one line in a hunk. Kind is ' ', '-' or '+'.
type HunkLine struct {
	Kind byte
	Text string
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// Parse parses a unified diff. It accepts git-style a/ and b/ prefixes and
// ignores diff/index metadata lines.
func Parse(input string) ([]FilePatch, error) {
	lines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	var files []FilePatch
	var cur *FilePatch
	var hunk *Hunk

	flushHunk := func() error {
		if hunk == nil {
			return nil
		}
		if err := validateHunk(hunk); err != nil {
			return err
		}
		cur.Hunks = append(cur.Hunks, *hunk)
		hunk = nil
		return nil
	}
	flushFile := func() error {
		if cur == nil {
			return nil
		}
		if err := flushHunk(); err != nil {
			return err
		}
		if len(cur.Hunks) == 0 && !cur.NewFile && !cur.DeleteFile {
			return fmt.Errorf("%s: no hunks", cur.Path())
		}
		files = append(files, *cur)
		cur = nil
		return nil
	}

	for i, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		switch {
		case strings.HasPrefix(line, "--- "):
			if err := flushFile(); err != nil {
				return nil, err
			}
			p := parsePath(strings.TrimPrefix(line, "--- "))
			cur = &FilePatch{OldPath: p}
			if p == "/dev/null" {
				cur.NewFile = true
			}
		case strings.HasPrefix(line, "+++ "):
			if cur == nil {
				return nil, fmt.Errorf("line %d: +++ without ---", i+1)
			}
			p := parsePath(strings.TrimPrefix(line, "+++ "))
			cur.NewPath = p
			if p == "/dev/null" {
				cur.DeleteFile = true
			}
		case strings.HasPrefix(line, "@@"):
			if cur == nil {
				return nil, fmt.Errorf("line %d: hunk without file header", i+1)
			}
			if err := flushHunk(); err != nil {
				return nil, err
			}
			m := hunkHeader.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("line %d: malformed hunk header %q", i+1, line)
			}
			h := &Hunk{
				OldStart: atoi(m[1]),
				OldLines: count(m[2]),
				NewStart: atoi(m[3]),
				NewLines: count(m[4]),
			}
			hunk = h
		case strings.HasPrefix(line, `\ No newline at end of file`):
			if cur != nil {
				cur.NoNewline = true
			}
		case line == "":
			// Trailing newline at the end of the patch is not a hunk line.
			if hunk != nil && i != len(lines)-1 {
				return nil, fmt.Errorf("line %d: empty line inside hunk", i+1)
			}
		default:
			if hunk == nil {
				// Ignore git metadata such as "diff --git", "index", "new file mode".
				continue
			}
			if len(line) == 0 {
				return nil, fmt.Errorf("line %d: empty hunk line", i+1)
			}
			kind := line[0]
			if kind != ' ' && kind != '-' && kind != '+' {
				return nil, fmt.Errorf("line %d: unexpected hunk line %q", i+1, line)
			}
			hunk.Lines = append(hunk.Lines, HunkLine{Kind: kind, Text: line[1:]})
		}
	}
	if err := flushFile(); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("no file patches found")
	}
	return files, nil
}

func parsePath(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "a/")
	s = strings.TrimPrefix(s, "b/")
	return s
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func count(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}

func validateHunk(h *Hunk) error {
	oldCount, newCount := 0, 0
	for _, l := range h.Lines {
		switch l.Kind {
		case ' ':
			oldCount++
			newCount++
		case '-':
			oldCount++
		case '+':
			newCount++
		default:
			return fmt.Errorf("invalid hunk line kind %q", l.Kind)
		}
	}
	if h.OldLines != oldCount || h.NewLines != newCount {
		return fmt.Errorf("hunk @@ -%d,%d +%d,%d @@ has %d old / %d new lines", h.OldStart, h.OldLines, h.NewStart, h.NewLines, oldCount, newCount)
	}
	if oldCount == 0 && newCount == 0 {
		return errors.New("empty hunk")
	}
	return nil
}

// Apply applies one file patch to existing content and returns the new content.
func Apply(existing string, fp FilePatch) (string, error) {
	if fp.DeleteFile {
		return "", nil
	}
	lines, finalNewline := splitLines(existing)
	offset := 0
	for _, h := range fp.Hunks {
		var oldPart, newPart []string
		for _, l := range h.Lines {
			switch l.Kind {
			case ' ':
				oldPart = append(oldPart, l.Text)
				newPart = append(newPart, l.Text)
			case '-':
				oldPart = append(oldPart, l.Text)
			case '+':
				newPart = append(newPart, l.Text)
			}
		}
		idx := h.OldStart - 1 + offset
		if h.OldStart == 0 {
			idx = 0
		}
		if idx < 0 || idx > len(lines) {
			return "", fmt.Errorf("%s: hunk starts at line %d, file has %d lines", fp.Path(), h.OldStart, len(lines))
		}
		if idx+len(oldPart) > len(lines) {
			return "", fmt.Errorf("%s: hunk needs %d lines at %d, file has %d", fp.Path(), len(oldPart), idx+1, len(lines))
		}
		for j := range oldPart {
			if lines[idx+j] != oldPart[j] {
				return "", fmt.Errorf("%s: context mismatch at line %d: want %q, got %q", fp.Path(), idx+j+1, oldPart[j], lines[idx+j])
			}
		}
		updated := make([]string, 0, len(lines)-len(oldPart)+len(newPart))
		updated = append(updated, lines[:idx]...)
		updated = append(updated, newPart...)
		updated = append(updated, lines[idx+len(oldPart):]...)
		lines = updated
		offset += len(newPart) - len(oldPart)
	}
	out := strings.Join(lines, "\n")
	if len(lines) == 0 {
		out = ""
	} else if (finalNewline || fp.NewFile) && !fp.NoNewline {
		out += "\n"
	}
	return out, nil
}

func splitLines(s string) ([]string, bool) {
	if s == "" {
		return nil, false
	}
	final := strings.HasSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return []string{""}, final
	}
	return strings.Split(s, "\n"), final
}
