package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Load(ws); err != nil || got != "" {
		t.Fatalf("missing load = %q, %v", got, err)
	}
	if _, err := Save(ws, "## Goal\nship it\n"); err != nil {
		t.Fatal(err)
	}
	got, err := Load(ws)
	if err != nil || !strings.Contains(got, "ship it") {
		t.Fatalf("load = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".eino-agent", "plan.md")); err != nil {
		t.Fatal(err)
	}
	msg := HistoryMessage(ws)
	if msg == nil || !strings.Contains(msg.Content, "ship it") {
		t.Fatalf("history message = %+v", msg)
	}
}
