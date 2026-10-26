package approval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/input"
)

func TestLoadRulesAndMatch(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "permissions.json")
	if err := os.WriteFile(file, []byte(`{
  "allow": [
    {"tool": "bash", "command": "go test ./..."},
    {"tool": "edit_file", "path": "docs/*.md"},
    {"tool": "apply_patch", "path_prefix": "internal/"}
  ],
  "deny": [
    {"tool": "bash", "command_prefix": "rm -rf"},
    {"tool": "mcp__shell__run", "args_prefix": "rm "}
  ]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rules, err := LoadRules(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rules.Allowed(Request{Tool: "bash", Command: "go test ./..."}); !ok {
		t.Fatal("exact command allow did not match")
	}
	if _, ok := rules.Allowed(Request{Tool: "bash", Command: "go test ./... -run X"}); ok {
		t.Fatal("exact command allow matched a longer command")
	}
	if _, ok := rules.Allowed(Request{Tool: "edit_file", Path: "docs/readme.md"}); !ok {
		t.Fatal("path glob allow did not match")
	}
	if _, ok := rules.Allowed(Request{Tool: "edit_file", Path: "docs/readme.go"}); ok {
		t.Fatal("path glob allow matched wrong suffix")
	}
	if _, ok := rules.Allowed(Request{Tool: "apply_patch", Paths: []string{"internal/a.go", "internal/b.go"}}); !ok {
		t.Fatal("multi-path allow did not match")
	}
	if _, ok := rules.Allowed(Request{Tool: "apply_patch", Paths: []string{"internal/a.go", "cmd/b.go"}}); ok {
		t.Fatal("multi-path allow matched when one path escaped")
	}
	if _, ok := rules.Denied(Request{Tool: "bash", Command: "rm -rf build"}); !ok {
		t.Fatal("deny prefix did not match")
	}
	if _, ok := rules.Denied(Request{Tool: "mcp__shell__run", Args: `{"cmd":"rm x"}`}); ok {
		t.Fatal("args prefix deny matched non-prefix")
	}
	if _, ok := rules.Denied(Request{Tool: "mcp__shell__run", Args: "rm x"}); !ok {
		t.Fatal("args prefix deny did not match")
	}
}

func TestLoadRulesMissingAndInvalid(t *testing.T) {
	rules, err := LoadRules(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || rules != nil {
		t.Fatalf("missing: rules=%+v err=%v", rules, err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(file, []byte(`{"allow":[{"tool":"bash"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRules(file); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("err = %v", err)
	}
}

func TestRulesAllowNonTTYWithoutPrompt(t *testing.T) {
	tl := NewWithSource("ask", input.New(strings.NewReader("")), os.Stdout, false)
	tl.SetRules(&RuleSet{Allow: []Rule{{Tool: "bash", Command: "go test ./..."}}})
	decision, err := tl.Approve(context.Background(), Request{Tool: "bash", Kind: KindBash, Command: "go test ./..."})
	if err != nil || decision != Once {
		t.Fatalf("decision=%v err=%v", decision, err)
	}

	tl.SetRules(&RuleSet{Deny: []Rule{{Tool: "bash", CommandPrefix: "rm "}}})
	decision, err = tl.Approve(context.Background(), Request{Tool: "bash", Kind: KindBash, Command: "rm -rf x"})
	if err != nil || decision != Deny {
		t.Fatalf("deny decision=%v err=%v", decision, err)
	}
}

func TestDiffStat(t *testing.T) {
	added, removed := diffStat("--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n+extra\n")
	if added != 2 || removed != 1 {
		t.Fatalf("added=%d removed=%d", added, removed)
	}
}
