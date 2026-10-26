package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type listFilesInput struct {
	Path  string `json:"path,omitempty" jsonschema_description:"Directory to list, relative to the workspace root. Default current directory."`
	Depth int    `json:"depth,omitempty" jsonschema_description:"Maximum directory depth. Default 2, max 5."`
	Limit int    `json:"limit,omitempty" jsonschema_description:"Maximum number of entries to return. Default 500, max 2000."`
}

func (o *Options) listFiles(_ context.Context, in listFilesInput) (string, error) {
	start, err := o.Workspace.Resolve(in.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(start)
	if err != nil {
		return "", fmt.Errorf("list %s: %w", o.Workspace.Rel(start), err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory; use read_file", o.Workspace.Rel(start))
	}

	depth := in.Depth
	if depth <= 0 {
		depth = 2
	}
	if depth > 5 {
		depth = 5
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 500
	}
	if limit > 2000 {
		limit = 2000
	}

	var lines []string
	truncated := false
	err = filepath.WalkDir(start, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == start {
			return nil
		}
		rel, relErr := filepath.Rel(start, path)
		if relErr != nil {
			return relErr
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
		entryDepth := strings.Count(rel, string(filepath.Separator)) + 1
		if entryDepth > depth {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if len(lines) >= limit {
			truncated = true
			return fs.SkipAll
		}
		display := filepath.ToSlash(rel)
		if d.IsDir() {
			display += "/"
		}
		lines = append(lines, display)
		if d.IsDir() && entryDepth >= depth {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("list %s: %w", o.Workspace.Rel(start), err)
	}
	if len(lines) == 0 {
		return fmt.Sprintf("No entries under %s (depth=%d)", o.Workspace.Rel(start), depth), nil
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Listing %s (depth=%d, %d entries):\n", o.Workspace.Rel(start), depth, len(lines))
	for _, line := range lines {
		out.WriteString(line)
		out.WriteByte('\n')
	}
	if truncated {
		fmt.Fprintf(&out, "... [truncated at %d entries; raise limit or list a subdirectory]\n", limit)
	}
	return out.String(), nil
}
