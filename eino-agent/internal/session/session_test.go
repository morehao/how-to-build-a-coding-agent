package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestSessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, "/workspace", "openai", "gpt-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add("user", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("assistant", "hi"); err != nil {
		t.Fatal(err)
	}
	path, err := s.Save()
	if err != nil {
		t.Fatal(err)
	}
	if path != s.Path() {
		t.Fatalf("path = %q, session path = %q", path, s.Path())
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %o, want 600", info.Mode().Perm())
		}
	}

	loaded, err := Load(dir, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Workspace != "/workspace" || loaded.Provider != "openai" || loaded.Model != "gpt-test" {
		t.Fatalf("loaded metadata = %+v", loaded)
	}
	if len(loaded.Messages) != 2 || loaded.Messages[0].Content != "hello" || loaded.Messages[1].Role != "assistant" {
		t.Fatalf("loaded messages = %+v", loaded.Messages)
	}

	byPath, err := Load(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	if byPath.ID != s.ID {
		t.Fatalf("by-path id = %q, want %q", byPath.ID, s.ID)
	}
}

func TestTrimDropsOldestAndKeepsUserFirst(t *testing.T) {
	s, err := New(t.TempDir(), "/ws", "openai", "m")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ role, content string }{
		{"user", strings.Repeat("a", 200)},
		{"assistant", strings.Repeat("b", 200)},
		{"user", strings.Repeat("c", 200)},
		{"assistant", strings.Repeat("d", 200)},
		{"user", "latest"},
	} {
		if err := s.Add(m.role, m.content); err != nil {
			t.Fatal(err)
		}
	}
	dropped := s.Trim(300)
	if dropped == 0 {
		t.Fatal("expected messages to be dropped")
	}
	if len(s.Messages) == 0 {
		t.Fatal("all messages dropped")
	}
	if s.Messages[0].Role != "user" {
		t.Fatalf("first role = %q, want user", s.Messages[0].Role)
	}
	if s.Messages[len(s.Messages)-1].Content != "latest" {
		t.Fatalf("latest message lost: %+v", s.Messages)
	}
}

func TestLoadRejectsUnsupportedVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"id":"bad","messages":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "bad"); err == nil || !strings.Contains(err.Error(), "version 99") {
		t.Fatalf("error = %v", err)
	}
}

func TestSchemaMessages(t *testing.T) {
	s, _ := New(t.TempDir(), "/ws", "openai", "m")
	_ = s.Add("user", "u")
	_ = s.Add("assistant", "a")
	msgs := s.SchemaMessages()
	if len(msgs) != 2 || msgs[0].Role != schema.User || msgs[1].Role != schema.Assistant {
		t.Fatalf("messages = %+v", msgs)
	}
}
