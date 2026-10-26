package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type codeSearchInput struct {
	Pattern       string `json:"pattern" jsonschema:"required" jsonschema_description:"Regular expression to search for."`
	Path          string `json:"path,omitempty" jsonschema_description:"File or directory to search, relative to the workspace root. Default current directory."`
	FileType      string `json:"file_type,omitempty" jsonschema_description:"Optional ripgrep type filter, e.g. go, js, py, rust, md."`
	CaseSensitive bool   `json:"case_sensitive,omitempty" jsonschema_description:"Case-sensitive matching. Default false."`
	MaxResults    int    `json:"max_results,omitempty" jsonschema_description:"Maximum matches to return. Default 100, max 500."`
}

func (o *Options) codeSearch(ctx context.Context, in codeSearchInput) (string, error) {
	pattern := strings.TrimSpace(in.Pattern)
	if pattern == "" {
		return "", errors.New("pattern is empty")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regular expression: %w", err)
	}
	if !in.CaseSensitive {
		re, err = regexp.Compile("(?i)" + pattern)
		if err != nil {
			return "", fmt.Errorf("invalid regular expression: %w", err)
		}
	}

	target, err := o.Workspace.Resolve(in.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("search %s: %w", o.Workspace.Rel(target), err)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file or directory", o.Workspace.Rel(target))
	}

	maxResults := in.MaxResults
	if maxResults <= 0 {
		maxResults = 100
	}
	if maxResults > 500 {
		maxResults = 500
	}

	displayTarget := o.Workspace.Rel(target)
	if displayTarget == "" {
		displayTarget = "."
	}

	if o.RGPath != "" {
		out, err := o.searchWithRG(ctx, in, pattern, displayTarget, maxResults)
		if err == nil {
			return out, nil
		}
		if !errors.Is(err, errNoMatches) {
			return "", err
		}
		return "No matches found", nil
	}
	return o.searchWithWalk(ctx, target, in, re, maxResults)
}

var errNoMatches = errors.New("no matches")

func (o *Options) searchWithRG(ctx context.Context, in codeSearchInput, pattern, target string, maxResults int) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	args := []string{
		"--line-number", "--no-heading", "--color=never",
		"--max-columns=300", "--max-columns-preview",
		"--max-count=100",
	}
	if !in.CaseSensitive {
		args = append(args, "-i")
	}
	if in.FileType != "" {
		args = append(args, "--type", in.FileType)
	}
	args = append(args, "-e", pattern, target)

	cmd := exec.CommandContext(runCtx, o.RGPath, args...)
	cmd.Dir = o.Workspace.Root()
	out, err := cmd.CombinedOutput()
	text := strings.TrimRight(string(out), "\n")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", errNoMatches
		}
		if runCtx.Err() == context.DeadlineExceeded {
			return "", errors.New("ripgrep timed out after 60s")
		}
		if text != "" {
			return "", fmt.Errorf("ripgrep failed: %s", truncateRunes(text, 2000))
		}
		return "", fmt.Errorf("ripgrep failed: %w", err)
	}

	lines := strings.Split(text, "\n")
	var kept []string
	for _, line := range lines {
		line = strings.TrimPrefix(line, "./")
		if line == "" || strings.Contains(line, "binary file matches") {
			continue
		}
		kept = append(kept, line)
		if len(kept) >= maxResults {
			break
		}
	}
	if len(kept) == 0 {
		return "", errNoMatches
	}
	result := strings.Join(kept, "\n")
	if len(lines) > len(kept) || strings.Count(text, "\n")+1 > len(kept) {
		result += fmt.Sprintf("\n... [truncated at %d matches; refine the pattern or path]", maxResults)
	}
	return result, nil
}

func (o *Options) searchWithWalk(ctx context.Context, target string, in codeSearchInput, re *regexp.Regexp, maxResults int) (string, error) {
	var results []string
	truncated := false

	searchFile := func(path string) error {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 2*1024*1024 {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		head := make([]byte, 8192)
		n, _ := f.Read(head)
		if strings.IndexByte(string(head[:n]), 0) >= 0 {
			return nil
		}
		if _, err := f.Seek(0, 0); err != nil {
			return nil
		}

		rel := o.Workspace.Rel(path)
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		lineNo := 0
		for scanner.Scan() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lineNo++
			line := scanner.Text()
			if !re.MatchString(line) {
				continue
			}
			results = append(results, fmt.Sprintf("%s:%d: %s", rel, lineNo, truncateRunes(line, 300)))
			if len(results) >= maxResults {
				truncated = true
				return nil
			}
		}
		return nil
	}

	if info, _ := os.Stat(target); info != nil && !info.IsDir() {
		if in.FileType != "" && !matchesFileType(target, in.FileType) {
			return "No matches found", nil
		}
		if err := searchFile(target); err != nil {
			return "", err
		}
	} else {
		err := filepath.WalkDir(target, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == target {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fullRel := o.Workspace.Rel(path)
			if o.Workspace.Ignored(fullRel) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if in.FileType != "" && !matchesFileType(path, in.FileType) {
				return nil
			}
			if err := searchFile(path); err != nil {
				return err
			}
			if truncated {
				return fs.SkipAll
			}
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("search with fallback walker: %w", err)
		}
	}

	if len(results) == 0 {
		return "No matches found", nil
	}
	out := strings.Join(results, "\n")
	if truncated {
		out += fmt.Sprintf("\n... [truncated at %d matches; refine the pattern or path]", maxResults)
	}
	return out, nil
}

func matchesFileType(path, fileType string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch strings.ToLower(strings.TrimPrefix(fileType, ".")) {
	case "go":
		return ext == ".go"
	case "js", "javascript":
		return ext == ".js" || ext == ".jsx" || ext == ".mjs" || ext == ".cjs"
	case "ts", "typescript":
		return ext == ".ts" || ext == ".tsx"
	case "py", "python":
		return ext == ".py"
	case "rust":
		return ext == ".rs"
	case "java":
		return ext == ".java"
	case "md", "markdown":
		return ext == ".md" || ext == ".markdown"
	case "json":
		return ext == ".json"
	case "yaml":
		return ext == ".yaml" || ext == ".yml"
	case "sh", "shell":
		return ext == ".sh" || ext == ".bash" || ext == ".zsh"
	case "c":
		return ext == ".c" || ext == ".h"
	case "cpp", "c++":
		return ext == ".cpp" || ext == ".cc" || ext == ".cxx" || ext == ".hpp"
	default:
		return true
	}
}
