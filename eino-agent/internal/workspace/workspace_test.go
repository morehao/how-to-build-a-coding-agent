package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveKeepsPathsInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	ws, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ws.Resolve("sub/new.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "sub", "new.txt")
	if got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}

	for _, p := range []string{"../outside.txt", "../../etc/passwd", filepath.Join(root, "..", "outside.txt")} {
		if _, err := ws.Resolve(p); err == nil {
			t.Fatalf("Resolve(%q) succeeded, want escape error", p)
		}
	}
}

func TestResolveRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	ws, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Resolve("escape/secret.txt"); err == nil {
		t.Fatal("Resolve followed symlink outside workspace")
	}
	if _, err := ws.Resolve("escape/new.txt"); err == nil {
		t.Fatal("Resolve accepted a new file below an escaping symlink")
	}
}

func TestWriteFileAtomic(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a", "b", "file.txt")
	if err := WriteFileAtomic(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("content = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestIsIgnoredRel(t *testing.T) {
	for _, p := range []string{"node_modules/x.js", "src/.git/config", ".dsh/tmp/cache", "dist/app.js"} {
		if !IsIgnoredRel(p) {
			t.Errorf("IsIgnoredRel(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"src/main.go", "docs/specs/a.md", "README.md"} {
		if IsIgnoredRel(p) {
			t.Errorf("IsIgnoredRel(%q) = true, want false", p)
		}
	}
}
