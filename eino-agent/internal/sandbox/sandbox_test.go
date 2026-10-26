package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDarwinProfileLimitsWrites(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS-only")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not installed")
	}
	root := t.TempDir()
	r, err := New(root, false)
	if err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "inside.txt")
	cmd := r.Wrap(context.Background(), "printf ok > "+shellQuote(inside))
	if out, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(out), "Operation not permitted") {
			t.Skipf("sandbox-exec cannot be applied in this environment: %s", out)
		}
		t.Fatalf("inside write failed: %v\n%s", err, out)
	}
	if b, err := os.ReadFile(inside); err != nil || string(b) != "ok" {
		t.Fatalf("inside=%q err=%v", b, err)
	}

	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	cmd = r.Wrap(context.Background(), "printf bad > "+shellQuote(outside))
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("outside write unexpectedly succeeded: %s", out)
	}
}

func TestWrapWithoutRunnerUsesBash(t *testing.T) {
	var r *Runner
	cmd := r.Wrap(context.Background(), "printf hi")
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "hi" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestDarwinProfileContainsBoundary(t *testing.T) {
	p := darwinProfile("/tmp/work", false)
	for _, want := range []string{`(deny default)`, `(subpath "/tmp/work")`, `(subpath "/tmp")`} {
		if !strings.Contains(p, want) {
			t.Fatalf("profile missing %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "network*") {
		t.Fatalf("network should be denied:\n%s", p)
	}
	p = darwinProfile("/tmp/work", true)
	if !strings.Contains(p, "(allow network*)") {
		t.Fatalf("network allow missing:\n%s", p)
	}
}
