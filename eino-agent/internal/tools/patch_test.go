package tools

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/approval"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/checkpoint"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/filestate"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

func newPatchOptions(t *testing.T, root, mode string) Options {
	t.Helper()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		Workspace: ws,
		Approver:  approval.New(mode, strings.NewReader(""), io.Discard, false),
		Mode:      mode,
	}
}

func TestApplyPatchMultiFileAndUndo(t *testing.T) {
	root := t.TempDir()
	opts := newPatchOptions(t, root, config.PermissionYolo)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cp, _ := checkpoint.New(opts.Workspace, "")
	opts.Checkpoints = cp
	p := `--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-old
+new
--- /dev/null
+++ b/b.txt
@@ -0,0 +1 @@
+created
`
	out, err := opts.applyPatch(context.Background(), applyPatchInput{Patch: p})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2 file(s)") {
		t.Fatalf("out = %q", out)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(data) != "new\n" {
		t.Fatalf("a = %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "b.txt")); string(data) != "created\n" {
		t.Fatalf("b = %q", data)
	}
	if _, err := cp.UndoLast(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(data) != "old\n" {
		t.Fatalf("a after undo = %q", data)
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("b still exists: %v", err)
	}
}

func TestApplyPatchRejectsMismatchWithoutWriting(t *testing.T) {
	root := t.TempDir()
	opts := newPatchOptions(t, root, config.PermissionYolo)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("actual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := `--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-expected
+new
`
	if _, err := opts.applyPatch(context.Background(), applyPatchInput{Patch: p}); err == nil {
		t.Fatal("expected context mismatch")
	}
	if data, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(data) != "actual\n" {
		t.Fatalf("file was modified: %q", data)
	}
}

func TestApplyPatchReadBeforeEdit(t *testing.T) {
	root := t.TempDir()
	opts := newPatchOptions(t, root, config.PermissionYolo)
	opts.Files = filestate.New()
	opts.EnforceReadBeforeEdit = true
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := `--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-old
+new
`
	if _, err := opts.applyPatch(context.Background(), applyPatchInput{Patch: p}); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("err = %v", err)
	}
	if err := opts.Files.MarkRead(path); err != nil {
		t.Fatal(err)
	}
	if _, err := opts.applyPatch(context.Background(), applyPatchInput{Patch: p}); err != nil {
		t.Fatal(err)
	}
}

func TestApplyPatchDeniedInReadOnly(t *testing.T) {
	root := t.TempDir()
	opts := newPatchOptions(t, root, config.PermissionReadOnly)
	if _, err := opts.applyPatch(context.Background(), applyPatchInput{Patch: "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n"}); err == nil {
		t.Fatal("expected read-only error")
	}
}
