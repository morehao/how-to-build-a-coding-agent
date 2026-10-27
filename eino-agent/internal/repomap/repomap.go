// Package repomap builds a compact, token-bounded view of a repository:
// directories, files, sizes and top-level Go symbols. It is a context
// selection aid, not a parser; a real language server would be the next step.
package repomap

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

// Options controls the size of the map.
type Options struct {
	MaxFiles       int
	MaxDepth       int
	IncludeSymbols bool
	MaxFileBytes   int64
}

type entry struct {
	rel     string
	dir     bool
	size    int64
	symbols []string
	binary  bool
}

var (
	goPackage = regexp.MustCompile(`^package\s+([A-Za-z_]\w*)`)
	goFunc    = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)`)
	goType    = regexp.MustCompile(`^type\s+([A-Za-z_]\w*)`)
)

// Build walks start and renders the map. start must be inside ws.
func Build(ws *workspace.Workspace, start string, opts Options) (string, error) {
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 200
	}
	if opts.MaxFiles > 2000 {
		opts.MaxFiles = 2000
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 6
	}
	if opts.MaxDepth > 12 {
		opts.MaxDepth = 12
	}
	if opts.MaxFileBytes <= 0 {
		opts.MaxFileBytes = 512 * 1024
	}
	info, err := os.Stat(start)
	if err != nil {
		return "", fmt.Errorf("repo_map %s: %w", ws.Rel(start), err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", ws.Rel(start))
	}

	var entries []entry
	dirs := 0
	truncated := false
	err = filepath.WalkDir(start, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == start {
			return nil
		}
		rel, relErr := filepath.Rel(ws.Root(), path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if ws.Ignored(rel) {
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
		depth := strings.Count(rel, "/")
		if depth > opts.MaxDepth {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			dirs++
			return nil
		}
		if len(entries) >= opts.MaxFiles {
			truncated = true
			return fs.SkipAll
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		e := entry{rel: rel, size: fi.Size()}
		if opts.IncludeSymbols && strings.HasSuffix(strings.ToLower(rel), ".go") && fi.Size() <= opts.MaxFileBytes {
			e.symbols, e.binary = goSymbols(path)
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("repo_map: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })

	var b strings.Builder
	fmt.Fprintf(&b, "repo map: %d file(s), %d dir(s); max_files=%d max_depth=%d\n", len(entries), dirs, opts.MaxFiles, opts.MaxDepth)
	lastDir := ""
	for _, e := range entries {
		dir := filepath.ToSlash(filepath.Dir(e.rel))
		if dir != lastDir {
			if dir == "." {
				b.WriteString("./\n")
			} else {
				b.WriteString(dir + "/\n")
			}
			lastDir = dir
		}
		name := filepath.Base(e.rel)
		fmt.Fprintf(&b, "  %s (%s)", name, humanSize(e.size))
		if e.binary {
			b.WriteString(" [binary]")
		} else if len(e.symbols) > 0 {
			fmt.Fprintf(&b, "  [%s]", strings.Join(e.symbols, "; "))
		}
		b.WriteByte('\n')
	}
	if truncated {
		fmt.Fprintf(&b, "... [truncated at %d files; narrow the path or raise max_files]\n", opts.MaxFiles)
	}
	return b.String(), nil
}

func goSymbols(path string) ([]string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	head := make([]byte, 8192)
	n, _ := f.Read(head)
	if strings.IndexByte(string(head[:n]), 0) >= 0 {
		return nil, true
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, false
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var pkg string
	var funcs, types []string
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || strings.HasPrefix(line, "//") {
			continue
		}
		if m := goPackage.FindStringSubmatch(line); m != nil {
			pkg = "package " + m[1]
			continue
		}
		if m := goFunc.FindStringSubmatch(line); m != nil {
			if len(funcs) < 12 {
				funcs = append(funcs, m[1])
			}
			continue
		}
		if m := goType.FindStringSubmatch(line); m != nil {
			if len(types) < 12 {
				types = append(types, m[1])
			}
		}
	}
	var out []string
	if pkg != "" {
		out = append(out, pkg)
	}
	if len(funcs) > 0 {
		out = append(out, "func "+strings.Join(funcs, ", "))
	}
	if len(types) > 0 {
		out = append(out, "type "+strings.Join(types, ", "))
	}
	return out, false
}

func humanSize(n int64) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1fMiB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1fKiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}
