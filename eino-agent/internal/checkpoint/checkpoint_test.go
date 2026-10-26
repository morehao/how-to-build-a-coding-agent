package checkpoint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

func TestSnapshotUndoModifyCreateAndList(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(ws, "")
	if err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(root, "a.txt")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(root, "b.txt")

	entry, err := m.Snapshot("edit", []string{existing, created})
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil || len(entry.Files) != 2 {
		t.Fatalf("entry = %+v", entry)
	}
	if err := os.WriteFile(existing, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created, []byte("created"), 0o644); err != nil {
		t.Fatal(err)
	}

	restored, err := m.UndoLast()
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 {
		t.Fatalf("restored = %v", restored)
	}
	if data, _ := os.ReadFile(existing); string(data) != "old" {
		t.Fatalf("existing = %q", data)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("created file still exists: %v", err)
	}
	entries, err := m.List()
	if err != nil || len(entries) != 0 {
		t.Fatalf("list after undo = %v, %v", entries, err)
	}
}

func TestUndoWithoutCheckpoints(t *testing.T) {
	root := t.TempDir()
	ws, _ := workspace.New(root)
	m, _ := New(ws, "")
	if _, err := m.UndoLast(); err == nil {
		t.Fatal("expected error")
	}
}
