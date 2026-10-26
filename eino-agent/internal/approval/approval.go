// Package approval implements the human-in-the-loop gate for privileged
// actions, currently shell commands and file edits.
package approval

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
)

// Decision is the outcome of an approval request.
type Decision int

const (
	// Deny rejects the action for this invocation.
	Deny Decision = iota
	// Once allows the action for this invocation only.
	Once
	// Always allows the same command or edit target for the rest of the
	// process.
	Always
)

// Kind identifies the kind of privileged action.
type Kind string

const (
	// KindBash is a shell command. It is also the zero-value kind for
	// backwards compatibility with callers that only set Command.
	KindBash Kind = "bash"
	// KindEdit is a file edit or creation.
	KindEdit Kind = "edit"
)

// Request describes a privileged action awaiting approval.
type Request struct {
	Kind    Kind
	Tool    string
	Command string
	Path    string
	Diff    string
	Summary string
	CWD     string
	Timeout time.Duration
}

// Approver decides whether a tool may perform a privileged action.
type Approver interface {
	Approve(ctx context.Context, req Request) (Decision, error)
}

// Terminal prompts on stdin/stdout. It is safe for concurrent use.
type Terminal struct {
	mode   string
	in     *bufio.Reader
	out    io.Writer
	isTTY  bool
	mu     sync.Mutex
	always map[string]struct{}
}

// New creates an approver for the given permission mode.
func New(mode string, in io.Reader, out io.Writer, isTTY bool) *Terminal {
	return &Terminal{
		mode:   mode,
		in:     bufio.NewReader(in),
		out:    out,
		isTTY:  isTTY,
		always: make(map[string]struct{}),
	}
}

// Approve applies the permission mode and, in ask mode, prompts the user.
func (t *Terminal) Approve(ctx context.Context, req Request) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Deny, err
	}
	kind := req.Kind
	if kind == "" {
		kind = KindBash
	}
	switch t.mode {
	case config.PermissionYolo:
		return Once, nil
	case config.PermissionReadOnly:
		fmt.Fprintf(t.out, "[approval] denied %s: privileged actions are disabled in read-only mode\n", kind)
		return Deny, nil
	}

	if !t.isTTY {
		fmt.Fprintf(t.out, "[approval] denied %s: non-interactive session; privileged actions require a TTY or --permission-mode yolo\n", kind)
		return Deny, nil
	}

	key := requestKey(kind, req)
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.always[key]; ok {
		return Always, nil
	}

	printRequest(t.out, kind, req)
	for attempt := 0; attempt < 3; attempt++ {
		prompt := "  allow? [y] once / [a] always this command / [N] no: "
		if kind == KindEdit {
			prompt = "  apply? [y] once / [a] always this file / [N] no: "
		}
		fmt.Fprint(t.out, prompt)
		line, err := t.in.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			fmt.Fprintln(t.out)
			return Deny, nil
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return Once, nil
		case "a", "always":
			t.always[key] = struct{}{}
			return Always, nil
		case "n", "no", "":
			return Deny, nil
		default:
			fmt.Fprintln(t.out, "  please answer y, a or n")
		}
	}
	return Deny, nil
}

func printRequest(out io.Writer, kind Kind, req Request) {
	switch kind {
	case KindEdit:
		fmt.Fprintf(out, "\n[approval] %s\n  path: %s\n", req.Tool, req.Path)
		if req.Summary != "" {
			fmt.Fprintf(out, "  change: %s\n", req.Summary)
		}
		if req.Diff != "" {
			fmt.Fprintf(out, "%s\n", truncate(req.Diff, 12*1024))
		}
	default:
		fmt.Fprintf(out, "\n[approval] %s\n  command: %s\n  cwd:     %s\n  timeout: %s\n",
			"bash", truncate(req.Command, 500), req.CWD, req.Timeout)
	}
}

func requestKey(kind Kind, req Request) string {
	if kind == KindEdit {
		sum := sha256.Sum256([]byte(req.Path))
		return "edit:" + hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256([]byte(req.Command))
	return "bash:" + hex.EncodeToString(sum[:])
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
