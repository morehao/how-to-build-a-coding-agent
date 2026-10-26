package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

func TestLoadProjectInstructionsExpandsImports(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Rules\n@docs/rules.md\ntail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "rules.md"), []byte("nested rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	name, content := LoadProjectInstructions(ws)
	if name != "AGENTS.md" || !strings.Contains(content, "nested rule") || !strings.Contains(content, "tail") {
		t.Fatalf("name=%q content=%q", name, content)
	}
}
