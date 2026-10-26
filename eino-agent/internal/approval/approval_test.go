package approval

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
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
