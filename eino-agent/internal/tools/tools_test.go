package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/approval"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

type stubApprover struct {
	decision approval.Decision
	err      error
	calls    int
	last     approval.Request
}

func (s *stubApprover) Approve(_ context.Context, req approval.Request) (approval.Decision, error) {
	s.calls++
	s.last = req
	return s.decision, s.err
}

func testOptions(t *testing.T) (*Options, string) {
	t.Helper()
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &Options{
		Workspace:      ws,
		Mode:           config.PermissionAsk,
		Approver:       &stubApprover{decision: approval.Once},
		MaxOutputBytes: 256 * 1024,
		BashTimeout:    5 * time.Second,
	}, dir
}

func TestReadFile(t *testing.T) {
	o, dir := testOptions(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := o.readFile(context.Background(), readFileInput{Path: "a.txt", Offset: 2, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "lines 2-2") || !strings.Contains(out, "     2 | two") || strings.Contains(out, "one") {
		t.Fatalf("unexpected slice:\n%s", out)
	}

	out, err = o.readFile(context.Background(), readFileInput{Path: "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "one") || !strings.Contains(out, "three") {
		t.Fatalf("unexpected full read:\n%s", out)
	}

	if _, err := o.readFile(context.Background(), readFileInput{Path: "missing.txt"}); err == nil {
		t.Fatal("missing file did not error")
	}
	if _, err := o.readFile(context.Background(), readFileInput{Path: "."}); err == nil {
		t.Fatal("directory did not error")
	}

	if err := os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.readFile(context.Background(), readFileInput{Path: "bin.dat"}); err == nil {
		t.Fatal("binary file did not error")
	}
}

func TestEditFile(t *testing.T) {
	o, dir := testOptions(t)
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := o.editFile(context.Background(), editFileInput{Path: "a.txt", OldStr: "world", NewStr: "there"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "edited a.txt") {
		t.Fatalf("output = %q", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello there\n" {
		t.Fatalf("content = %q", data)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode changed to %o", info.Mode().Perm())
	}

	if _, err := o.editFile(context.Background(), editFileInput{Path: "a.txt", OldStr: "missing", NewStr: "x"}); err == nil {
		t.Fatal("missing old_str did not error")
	}
	if err := os.WriteFile(path, []byte("dup dup\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.editFile(context.Background(), editFileInput{Path: "a.txt", OldStr: "dup", NewStr: "x"}); err == nil {
		t.Fatal("duplicate old_str did not error")
	}
	if _, err := o.editFile(context.Background(), editFileInput{Path: "a.txt", OldStr: "", NewStr: "x"}); err == nil {
		t.Fatal("empty old_str on existing file did not error")
	}

	out, err = o.editFile(context.Background(), editFileInput{Path: "new/dir/file.txt", OldStr: "", NewStr: "created\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "created new/dir/file.txt") {
		t.Fatalf("output = %q", out)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "new/dir/file.txt"))
	if string(data) != "created\n" {
		t.Fatalf("created content = %q", data)
	}

	readOnly := *o
	readOnly.Mode = config.PermissionReadOnly
	if _, err := readOnly.editFile(context.Background(), editFileInput{Path: "a.txt", OldStr: "dup", NewStr: "x"}); err == nil {
		t.Fatal("read-only edit did not error")
	}
}

func TestListFiles(t *testing.T) {
	o, dir := testOptions(t)
	for _, p := range []string{"src/main.go", "src/deep/file.go", "node_modules/x.js", ".git/config"} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := o.listFiles(context.Background(), listFilesInput{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "src/") || strings.Contains(out, "src/main.go") {
		t.Fatalf("depth 1 output:\n%s", out)
	}
	if strings.Contains(out, "node_modules") || strings.Contains(out, ".git") {
		t.Fatalf("ignored dirs leaked:\n%s", out)
	}

	out, err = o.listFiles(context.Background(), listFilesInput{Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "src/deep/file.go") {
		t.Fatalf("depth 3 output:\n%s", out)
	}

	if _, err := o.listFiles(context.Background(), listFilesInput{Path: "src/main.go"}); err == nil {
		t.Fatal("listing a file did not error")
	}

	out, err = o.listFiles(context.Background(), listFilesInput{Depth: 3, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("limit output:\n%s", out)
	}
}

func TestBash(t *testing.T) {
	o, _ := testOptions(t)
	approver := &stubApprover{decision: approval.Once}
	o.Approver = approver

	out, err := o.bash(context.Background(), bashInput{Command: "printf 'hello\\n'; exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "exit code: 3") || !strings.Contains(out, "hello") {
		t.Fatalf("output = %q", out)
	}
	if approver.calls != 1 || approver.last.CWD != o.Workspace.Root() {
		t.Fatalf("approval = %+v", approver)
	}

	o.Approver = &stubApprover{decision: approval.Deny}
	out, err = o.bash(context.Background(), bashInput{Command: "echo nope"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ERROR: command denied by user") {
		t.Fatalf("denied output = %q", out)
	}

	readOnly := *o
	readOnly.Mode = config.PermissionReadOnly
	if _, err := readOnly.bash(context.Background(), bashInput{Command: "echo nope"}); err == nil {
		t.Fatal("read-only bash did not error")
	}

	o.Approver = &stubApprover{decision: approval.Once}
	o.BashTimeout = 100 * time.Millisecond
	out, err = o.bash(context.Background(), bashInput{Command: "sleep 2"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "timed out after 100ms") {
		t.Fatalf("timeout output = %q", out)
	}

	o.BashTimeout = 5 * time.Second
	o.MaxOutputBytes = 1024
	out, err = o.bash(context.Background(), bashInput{Command: "seq 1 1000"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "output truncated") {
		t.Fatalf("truncation output length=%d", len(out))
	}
}

func TestCodeSearchFallback(t *testing.T) {
	o, dir := testOptions(t)
	o.RGPath = "" // force the Go walker
	files := map[string]string{
		"src/main.go":       "package main\nfunc Hello() {}\n",
		"src/util.py":       "def hello(): pass\n",
		"README.md":         "Hello world\n",
		"node_modules/x.js": "Hello dependency\n",
	}
	for p, content := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := o.codeSearch(context.Background(), codeSearchInput{Pattern: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "src/main.go") || !strings.Contains(out, "src/util.py") || !strings.Contains(out, "README.md") {
		t.Fatalf("search output:\n%s", out)
	}
	if strings.Contains(out, "node_modules") {
		t.Fatalf("ignored dir leaked:\n%s", out)
	}

	out, err = o.codeSearch(context.Background(), codeSearchInput{Pattern: "Hello", CaseSensitive: true, FileType: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "src/main.go") || strings.Contains(out, "README.md") {
		t.Fatalf("file-type search:\n%s", out)
	}

	out, err = o.codeSearch(context.Background(), codeSearchInput{Pattern: "hello", MaxResults: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("max results output:\n%s", out)
	}

	if _, err := o.codeSearch(context.Background(), codeSearchInput{Pattern: "["}); err == nil {
		t.Fatal("invalid regex did not error")
	}
	out, err = o.codeSearch(context.Background(), codeSearchInput{Pattern: "zzz-not-there"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No matches found") {
		t.Fatalf("no-match output = %q", out)
	}
}

func TestWrapToolTurnsErrorIntoOutput(t *testing.T) {
	o, _ := testOptions(t)
	fn := wrapTool(o, func(context.Context, struct{}) (string, error) {
		return "", errors.New("boom")
	})
	out, err := fn(context.Background(), struct{}{})
	if err != nil {
		t.Fatalf("soft error leaked: %v", err)
	}
	if out != "ERROR: boom" {
		t.Fatalf("out = %q", out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fn = wrapTool(o, func(ctx context.Context, _ struct{}) (string, error) {
		return "", errors.New("boom")
	})
	if _, err := fn(ctx, struct{}{}); err == nil {
		t.Fatal("cancellation did not propagate")
	}
}
