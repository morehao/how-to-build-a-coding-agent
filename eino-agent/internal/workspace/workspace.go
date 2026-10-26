// Package workspace owns the filesystem boundary for the agent. Every path a
// tool receives is resolved through Resolve, which keeps file operations inside
// the configured workspace root (including through symlinks).
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Workspace is a resolved, symlink-free root directory.
type Workspace struct {
	root   string
	ignore *Ignore
}

// New resolves root to an absolute path, evaluates symlinks on the root itself,
// and verifies that it is a directory.
func New(root string) (*Workspace, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root %s: %w", abs, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("stat workspace root %s: %w", real, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root %s is not a directory", real)
	}
	return &Workspace{root: real, ignore: LoadIgnore(real)}, nil
}

// Root returns the resolved absolute workspace root.
func (w *Workspace) Root() string { return w.root }

// Resolve turns a model-supplied path into a safe absolute path inside the
// workspace. It rejects lexical escapes and symlinks that point outside the
// workspace, including symlinked parents of not-yet-existing files.
func (w *Workspace) Resolve(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		p = "."
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Join(w.root, p)
	}

	if !isWithin(w.root, abs) {
		return "", fmt.Errorf("path escapes workspace: %s", p)
	}

	resolved, err := resolveExistingAncestor(abs)
	if err != nil {
		return "", err
	}
	if !isWithin(w.root, resolved) {
		return "", fmt.Errorf("path escapes workspace via symlink: %s", p)
	}
	return resolved, nil
}

// Rel returns a workspace-relative slash path for display. If p is outside the
// workspace, the absolute path is returned.
func (w *Workspace) Rel(p string) string {
	rel, err := filepath.Rel(w.root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return filepath.ToSlash(rel)
}

// IsIgnoredDir reports whether a directory name should be skipped while
// listing or searching. The list is deliberately conservative: generated and
// VCS directories that would flood the model's context.
func IsIgnoredDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn",
		"node_modules", "bower_components",
		".venv", "venv", "env",
		"__pycache__", ".mypy_cache", ".pytest_cache", ".ruff_cache",
		"dist", "build", "target", ".next", ".nuxt", ".cache",
		"vendor",
		".idea",
		".eino-agent",
		".DS_Store":
		return true
	default:
		return false
	}
}

// IsIgnoredRel reports whether a workspace-relative path is ignored. The
// ".dsh/tmp" prefix is special-cased because .dsh also contains docs the agent
// may legitimately read.
func IsIgnoredRel(rel string) bool {
	rel = filepath.ToSlash(strings.TrimPrefix(rel, "./"))
	if rel == ".dsh/tmp" || strings.HasPrefix(rel, ".dsh/tmp/") {
		return true
	}
	for _, seg := range strings.Split(rel, "/") {
		if IsIgnoredDir(seg) {
			return true
		}
	}
	return false
}

// Ignored reports whether rel is ignored by the built-in list or .einoignore.
func (w *Workspace) Ignored(rel string) bool {
	if IsIgnoredRel(rel) {
		return true
	}
	if w == nil || w.ignore == nil {
		return false
	}
	return w.ignore.Match(rel)
}

// WriteFileAtomic writes data to path via a same-directory temporary file and
// rename. It creates parent directories when missing and preserves mode on the
// final file.
func WriteFileAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".eino-edit-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(mode.Perm()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

func isWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// resolveExistingAncestor evaluates symlinks for the longest existing prefix of
// abs, then reattaches the non-existing suffix. This is what prevents creating
// a file through a symlinked directory that points outside the workspace.
func resolveExistingAncestor(abs string) (string, error) {
	current := abs
	var suffix []string
	for {
		real, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			return filepath.Clean(real), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("resolve path %s: %w", abs, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolve path %s: no existing ancestor", abs)
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}
