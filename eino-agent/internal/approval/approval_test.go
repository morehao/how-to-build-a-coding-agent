package approval

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/input"
)

func TestApprovalModes(t *testing.T) {
	req := Request{Command: "echo hi", CWD: "/tmp", Timeout: 1}

	d, err := New(config.PermissionYolo, strings.NewReader(""), &bytes.Buffer{}, true).Approve(context.Background(), req)
	if err != nil || d != Once {
		t.Fatalf("yolo = %v, %v", d, err)
	}

	var out bytes.Buffer
	d, err = New(config.PermissionReadOnly, strings.NewReader(""), &out, true).Approve(context.Background(), req)
	if err != nil || d != Deny || !strings.Contains(out.String(), "read-only") {
		t.Fatalf("read-only = %v, %v, %q", d, err, out.String())
	}

	out.Reset()
	d, err = New(config.PermissionAsk, strings.NewReader(""), &out, false).Approve(context.Background(), req)
	if err != nil || d != Deny || !strings.Contains(out.String(), "non-interactive") {
		t.Fatalf("non-tty = %v, %v, %q", d, err, out.String())
	}
}

func TestApprovalInteractive(t *testing.T) {
	req := Request{Command: "echo hi", CWD: "/tmp", Timeout: 1}

	var out bytes.Buffer
	a := New(config.PermissionAsk, strings.NewReader("a\ny\nn\n"), &out, true)
	d, err := a.Approve(context.Background(), req)
	if err != nil || d != Always {
		t.Fatalf("always = %v, %v", d, err)
	}
	d, err = a.Approve(context.Background(), req)
	if err != nil || d != Always {
		t.Fatalf("remembered always = %v, %v", d, err)
	}
	if strings.Count(out.String(), "allow?") != 1 {
		t.Fatalf("prompt repeated for remembered command:\n%s", out.String())
	}

	var onceOut bytes.Buffer
	a = New(config.PermissionAsk, strings.NewReader("y\n"), &onceOut, true)
	if d, err := a.Approve(context.Background(), Request{Command: "other"}); err != nil || d != Once {
		t.Fatalf("once = %v, %v", d, err)
	}

	var denyOut bytes.Buffer
	a = New(config.PermissionAsk, strings.NewReader("n\n"), &denyOut, true)
	if d, err := a.Approve(context.Background(), Request{Command: "third"}); err != nil || d != Deny {
		t.Fatalf("deny = %v, %v", d, err)
	}
}

func TestApprovalEditShowsDiffAndRemembersPath(t *testing.T) {
	var out bytes.Buffer
	a := New(config.PermissionAsk, strings.NewReader("a\ny\n"), &out, true)
	req := Request{
		Kind: KindEdit, Tool: "edit_file", Path: "internal/foo.go",
		Diff:    "--- a/internal/foo.go\n+++ b/internal/foo.go\n@@ -1,1 +1,1 @@\n-old\n+new\n",
		Summary: "1 line(s) removed, 1 line(s) added",
	}
	d, err := a.Approve(context.Background(), req)
	if err != nil || d != Always {
		t.Fatalf("edit already = %v, %v", d, err)
	}
	if !strings.Contains(out.String(), "[approval] edit_file") || !strings.Contains(out.String(), "-old") || !strings.Contains(out.String(), "+new") {
		t.Fatalf("edit prompt missing diff:\n%s", out.String())
	}
	// A second edit to the same path is allowed without another prompt.
	out.Reset()
	d, err = a.Approve(context.Background(), req)
	if err != nil || d != Always {
		t.Fatalf("remembered edit = %v, %v", d, err)
	}
	if strings.Contains(out.String(), "apply?") {
		t.Fatalf("prompt repeated for remembered path:\n%s", out.String())
	}
}

func TestApprovalEditNonTTYDenied(t *testing.T) {
	var out bytes.Buffer
	a := New(config.PermissionAsk, strings.NewReader(""), &out, false)
	d, err := a.Approve(context.Background(), Request{Kind: KindEdit, Tool: "edit_file", Path: "a.go"})
	if err != nil || d != Deny {
		t.Fatalf("non-tty edit = %v, %v", d, err)
	}
	if !strings.Contains(out.String(), "non-interactive") {
		t.Fatalf("missing non-interactive warning: %q", out.String())
	}
}

func TestApprovalRefusesOversizedPayload(t *testing.T) {
	var out bytes.Buffer
	lines := input.New(strings.NewReader("y\n"))
	a := NewWithSource(config.PermissionAsk, lines, &out, true)
	d, err := a.Approve(context.Background(), Request{Kind: KindBash, Command: strings.Repeat("x", maxReviewBytes+1)})
	if err != nil || d != Deny {
		t.Fatalf("oversized = %v, %v", d, err)
	}
	if !strings.Contains(out.String(), "too large") {
		t.Fatalf("missing refusal: %s", out.String())
	}
}
