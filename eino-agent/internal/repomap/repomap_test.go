package repomap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

func TestBuildIncludesSymbolsAndHonorsIgnore(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".einoignore"), []byte("ignored/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n\ntype Config struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "x.go"), []byte("package pkg\n\nfunc X() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignored", "secret.go"), []byte("package secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Build(ws, ws.Root(), Options{IncludeSymbols: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"main.go", "package main", "func main", "type Config", "pkg/", "x.go", "func X"} {
		if !strings.Contains(out, want) {
			t.Fatalf("map missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret.go") {
		t.Fatalf("ignored file leaked:\n%s", out)
	}
}
