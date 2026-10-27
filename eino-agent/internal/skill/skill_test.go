package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

func writeSkill(t *testing.T, root, dir, body string) {
	t.Helper()
	path := filepath.Join(root, "skills", dir)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testBackend(t *testing.T) (*FilesystemBackend, string) {
	t.Helper()
	root := t.TempDir()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	writeSkill(t, root, "review", `---
name: review
description: Review a Go diff for correctness and missing tests.
---

# Review

Read the diff, then check error paths.
`)
	writeSkill(t, root, "explain", `---
name: explain
description: Explain a code path from entry point to side effect.
context: inline
---

Walk the call chain and cite files.
`)
	return mustBackend(t, ws), root
}

func mustBackend(t *testing.T, ws *workspace.Workspace) *FilesystemBackend {
	t.Helper()
	b, err := NewFilesystemBackend(ws, []string{"skills"})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestListAndGet(t *testing.T) {
	b, _ := testBackend(t)
	defs, err := b.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 2 || defs[0].Name != "explain" || defs[1].Name != "review" {
		t.Fatalf("defs = %+v", defs)
	}
	if !strings.Contains(defs[1].Content, "error paths") {
		t.Fatalf("content = %q", defs[1].Content)
	}
	if defs[1].Dir == "" || !filepath.IsAbs(defs[1].Dir) {
		t.Fatalf("dir = %q, want absolute", defs[1].Dir)
	}
	if _, err := b.Get(context.Background(), "missing"); err == nil {
		t.Fatal("missing skill did not error")
	}
}

func TestMissingSkillsDirectoryIsEmpty(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	defs, err := mustBackend(t, ws).List(context.Background())
	if err != nil || len(defs) != 0 {
		t.Fatalf("defs=%+v err=%v", defs, err)
	}
}

func TestInvalidAndDuplicateSkills(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	writeSkill(t, root, "bad", "no frontmatter\n")
	if _, err := mustBackend(t, ws).List(context.Background()); err == nil || !strings.Contains(err.Error(), "frontmatter") {
		t.Fatalf("err = %v", err)
	}

	root = t.TempDir()
	ws, err = workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	writeSkill(t, root, "one", "---\nname: same\ndescription: first\n---\nbody\n")
	writeSkill(t, root, "two", "---\nname: same\ndescription: second\n---\nbody\n")
	if _, err := mustBackend(t, ws).List(context.Background()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v", err)
	}
}

func TestPromptSectionDoesNotLeakBody(t *testing.T) {
	b, _ := testBackend(t)
	defs, err := b.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	section := PromptSection(defs)
	for _, want := range []string{"## Skills", "`review`", "Review a Go diff", "`explain`"} {
		if !strings.Contains(section, want) {
			t.Fatalf("section missing %q:\n%s", want, section)
		}
	}
	if strings.Contains(section, "error paths") {
		t.Fatalf("prompt leaked skill body:\n%s", section)
	}
}

func TestToolInfoAndInlineInvocation(t *testing.T) {
	b, _ := testBackend(t)
	tl, err := NewTool(ToolOptions{Backend: b})
	if err != nil {
		t.Fatal(err)
	}
	info, err := tl.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != DefaultToolName || !strings.Contains(info.Desc, "review") {
		t.Fatalf("info = %+v", info)
	}
	out, err := tl.InvokableRun(context.Background(), `{"name":"review"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Base directory:") || !strings.Contains(out, "error paths") {
		t.Fatalf("out = %q", out)
	}
	out, err = tl.InvokableRun(context.Background(), `{"name":"missing"}`)
	if err != nil || !strings.HasPrefix(out, "ERROR:") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

type fakeRunner struct{ prompt string }

func (f *fakeRunner) Run(_ context.Context, prompt string) (string, error) {
	f.prompt = prompt
	return "subagent report", nil
}

func TestForkSkillUsesRunner(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	writeSkill(t, root, "forked", `---
name: forked
description: Run in a subagent.
context: fork
---

Do the thing.
`)
	r := &fakeRunner{}
	tl, err := NewTool(ToolOptions{Backend: mustBackend(t, ws), Runner: r})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tl.InvokableRun(context.Background(), `{"name":"forked"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "subagent report") || !strings.Contains(r.prompt, "Do the thing") {
		t.Fatalf("out=%q prompt=%q", out, r.prompt)
	}
}

func TestToolOutputCap(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	writeSkill(t, root, "big", "---\nname: big\ndescription: big body\n---\n"+strings.Repeat("x", 200)+"\n")
	tl, err := NewTool(ToolOptions{Backend: mustBackend(t, ws), MaxOutputBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tl.InvokableRun(context.Background(), `{"name":"big"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "truncated") || len(out) > 120 {
		t.Fatalf("out length=%d: %q", len(out), out)
	}
}

func TestWithoutRunnerDisablesFork(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	writeSkill(t, root, "forked", "---\nname: forked\ndescription: fork\ncontext: fork\n---\nbody\n")
	parent, err := NewTool(ToolOptions{Backend: mustBackend(t, ws), Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	nested := parent.WithoutRunner()
	out, err := nested.InvokableRun(context.Background(), `{"name":"forked"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "requires a subagent runner") {
		t.Fatalf("out = %q", out)
	}
}
