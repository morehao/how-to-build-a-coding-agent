package budget

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestBudget(t *testing.T) {
	tr := New(10)
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
	tr.Observe(&schema.TokenUsage{PromptTokens: 4, CompletionTokens: 3})
	if p, c, total := tr.Stats(); p != 4 || c != 3 || total != 7 {
		t.Fatalf("stats = %d %d %d", p, c, total)
	}
	tr.Observe(&schema.TokenUsage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3})
	if err := tr.Check(); err == nil {
		t.Fatal("expected budget error")
	}
}

func TestNilTracker(t *testing.T) {
	var tr *Tracker
	tr.Observe(nil)
	if tr.Check() != nil || tr.Max() != 0 {
		t.Fatal("nil tracker should be a no-op")
	}
}
