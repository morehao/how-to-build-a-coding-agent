package memory

import (
	"path/filepath"
	"testing"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/session"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

func TestSearchPlanAndSessions(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Save(ws, "## Decisions\nUse WAL mode for SQLite.\n"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "sessions")
	s, err := session.New(dir, root, "fake", "m")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Add("user", "How should we configure SQLite?")
	_ = s.Add("assistant", "Enable WAL and set busy_timeout.")
	if _, err := s.Save(); err != nil {
		t.Fatal(err)
	}

	other, err := session.New(dir, filepath.Join(root, "other"), "fake", "m")
	if err != nil {
		t.Fatal(err)
	}
	_ = other.Add("user", "WAL belongs to another workspace")
	if _, err := other.Save(); err != nil {
		t.Fatal(err)
	}

	hits, err := Search(ws, dir, "WAL sqlite", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 3 {
		t.Fatalf("expected plan + two session hits, got %+v", hits)
	}
	seenPlan, seenUser, seenAssistant := false, false, false
	for _, h := range hits {
		if h.SessionID == other.ID {
			t.Fatalf("searched another workspace: %+v", h)
		}
		if h.Source == RelativePath {
			seenPlan = true
		}
		if h.SessionID == s.ID && h.Role == "user" {
			seenUser = true
		}
		if h.SessionID == s.ID && h.Role == "assistant" {
			seenAssistant = true
		}
	}
	if !seenPlan || !seenUser || !seenAssistant {
		t.Fatalf("missing hits: plan=%v user=%v assistant=%v hits=%+v", seenPlan, seenUser, seenAssistant, hits)
	}
}

func TestSearchLimitAndEmptyQuery(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Save(ws, "alpha beta gamma"); err != nil {
		t.Fatal(err)
	}
	hits, err := Search(ws, "", "alpha beta gamma", 1)
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
	if _, err := Search(ws, "", " ", 5); err == nil {
		t.Fatal("empty query should fail")
	}
}
