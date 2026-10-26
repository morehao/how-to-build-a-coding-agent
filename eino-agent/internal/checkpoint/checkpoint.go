// Package checkpoint stores the pre-edit contents of files so a turn can be
// rolled back with /undo. It deliberately covers file tools only: arbitrary
// shell side effects are outside its contract.
package checkpoint

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

const maxSnapshotBytes = 10 * 1024 * 1024

// File is one pre-edit snapshot.
type File struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
	Backup  string `json:"backup,omitempty"`
	Mode    uint32 `json:"mode,omitempty"`
}

// Entry is one checkpoint (usually one tool call).
type Entry struct {
	ID    string    `json:"id"`
	Time  time.Time `json:"time"`
	Label string    `json:"label"`
	Files []File    `json:"files"`
}

// Manager owns the checkpoint directory under a workspace.
type Manager struct {
	root string
	dir  string
	mu   sync.Mutex
}

// New creates a manager. dir defaults to <workspace>/.eino-agent/checkpoints.
func New(ws *workspace.Workspace, dir string) (*Manager, error) {
	if ws == nil {
		return nil, errors.New("checkpoint: workspace is required")
	}
	if dir == "" {
		dir = filepath.Join(ws.Root(), ".eino-agent", "checkpoints")
	}
	return &Manager{root: ws.Root(), dir: dir}, nil
}

// Dir returns the checkpoint root.
func (m *Manager) Dir() string {
	if m == nil {
		return ""
	}
	return m.dir
}

// Snapshot copies paths (absolute) before a write. The returned entry is also
// persisted; callers do not need to commit it.
func (m *Manager) Snapshot(label string, paths []string) (*Entry, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	paths = unique(paths)
	if len(paths) == 0 {
		return nil, nil
	}
	id := fmt.Sprintf("%020d-%s", time.Now().UnixNano(), randomSuffix())
	dir := filepath.Join(m.dir, id)
	filesDir := filepath.Join(dir, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return nil, fmt.Errorf("create checkpoint: %w", err)
	}
	entry := &Entry{ID: id, Time: time.Now(), Label: label}
	for _, abs := range paths {
		rel, err := filepath.Rel(m.root, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("checkpoint: path outside workspace: %s", abs)
		}
		rel = filepath.ToSlash(rel)
		f := File{Path: rel}
		info, statErr := os.Stat(abs)
		switch {
		case statErr == nil && info.IsDir():
			return nil, fmt.Errorf("checkpoint: %s is a directory", rel)
		case statErr == nil:
			if info.Size() > maxSnapshotBytes {
				return nil, fmt.Errorf("checkpoint: %s is %d bytes; refusing to snapshot files larger than %d", rel, info.Size(), maxSnapshotBytes)
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return nil, fmt.Errorf("checkpoint: read %s: %w", rel, err)
			}
			name := hashPath(rel)
			if err := os.WriteFile(filepath.Join(filesDir, name), data, 0o600); err != nil {
				return nil, fmt.Errorf("checkpoint: write backup for %s: %w", rel, err)
			}
			f.Existed = true
			f.Backup = name
			f.Mode = uint32(info.Mode().Perm())
		case errors.Is(statErr, fs.ErrNotExist):
			f.Existed = false
		default:
			return nil, fmt.Errorf("checkpoint: stat %s: %w", rel, statErr)
		}
		entry.Files = append(entry.Files, f)
	}
	if err := writeManifest(dir, entry); err != nil {
		return nil, err
	}
	return entry, nil
}

// UndoLast restores the newest checkpoint and removes it. It returns the
// restored workspace-relative paths.
func (m *Manager) UndoLast() ([]string, error) {
	if m == nil {
		return nil, errors.New("checkpoints are disabled")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := m.listLocked()
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("no checkpoints to undo")
	}
	entry := entries[0]
	dir := filepath.Join(m.dir, entry.ID)
	restored := make([]string, 0, len(entry.Files))
	for _, f := range entry.Files {
		abs := filepath.Join(m.root, filepath.FromSlash(f.Path))
		if !within(m.root, abs) {
			return restored, fmt.Errorf("checkpoint %s: path escapes workspace: %s", entry.ID, f.Path)
		}
		if f.Existed {
			data, err := os.ReadFile(filepath.Join(dir, "files", f.Backup))
			if err != nil {
				return restored, fmt.Errorf("checkpoint %s: read backup for %s: %w", entry.ID, f.Path, err)
			}
			if err := workspace.WriteFileAtomic(abs, data, fs.FileMode(f.Mode)); err != nil {
				return restored, fmt.Errorf("checkpoint %s: restore %s: %w", entry.ID, f.Path, err)
			}
		} else {
			if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return restored, fmt.Errorf("checkpoint %s: remove created file %s: %w", entry.ID, f.Path, err)
			}
		}
		restored = append(restored, f.Path)
	}
	if err := os.RemoveAll(dir); err != nil {
		return restored, fmt.Errorf("checkpoint %s: cleanup: %w", entry.ID, err)
	}
	return restored, nil
}

// List returns checkpoints newest first.
func (m *Manager) List() ([]Entry, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listLocked()
}

func (m *Manager) listLocked() ([]Entry, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read checkpoint dir: %w", err)
	}
	var out []Entry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.dir, e.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(data, &entry); err != nil {
			continue
		}
		if entry.ID == "" {
			entry.ID = e.Name()
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func writeManifest(dir string, entry *Entry) error {
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600)
}

func hashPath(rel string) string {
	sum := sha256.Sum256([]byte(rel))
	return hex.EncodeToString(sum[:])
}

func randomSuffix() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func unique(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = filepath.Clean(p)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
