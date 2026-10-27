package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIgnorePatterns(t *testing.T) {
	dir := t.TempDir()
	content := `# comment
*.log
build/
!keep.log
/generated
src/*/gen.go
`
	if err := os.WriteFile(filepath.Join(dir, ".einoignore"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ig := LoadIgnore(dir)
	cases := map[string]bool{
		"a.log":              true,
		"keep.log":           false,
		"build":              true,
		"build/x.go":         true,
		"sub/build/x.go":     true,
		"generated/a.go":     true,
		"sub/generated/a.go": false,
		"src/a/gen.go":       true,
		"src/gen.go":         false,
	}
	for rel, want := range cases {
		if got := ig.Match(rel); got != want {
			t.Errorf("Match(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestWorkspaceIgnoredCombinesBuiltins(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".einoignore"), []byte("secret.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ws.Ignored("secret.txt") || !ws.Ignored("node_modules/x") {
		t.Fatal("expected ignored")
	}
	if ws.Ignored("main.go") {
		t.Fatal("main.go should not be ignored")
	}
}
