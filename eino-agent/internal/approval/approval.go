// Package approval implements the human-in-the-loop gate for shell commands.
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
	// Deny rejects the command for this invocation.
	Deny Decision = iota
	// Once allows the command for this invocation only.
	Once
	// Always allows the same command for the rest of the process.
	Always
)

// Request describes a shell command awaiting approval.
type Request struct {
	Command string
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
	switch t.mode {
	case config.PermissionYolo:
		return Once, nil
	case config.PermissionReadOnly:
		fmt.Fprintln(t.out, "[approval] denied: bash is disabled in read-only mode")
		return Deny, nil
	}

	if !t.isTTY {
		fmt.Fprintln(t.out, "[approval] denied: non-interactive session; use --permission-mode yolo to allow bash")
		return Deny, nil
	}

	key := commandKey(req.Command)
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.always[key]; ok {
		return Always, nil
	}

	fmt.Fprintf(t.out, "\n[approval] bash\n  command: %s\n  cwd:     %s\n  timeout: %s\n",
		truncate(req.Command, 500), req.CWD, req.Timeout)
	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprint(t.out, "  allow? [y] once / [a] always this command / [N] no: ")
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

func commandKey(cmd string) string {
	sum := sha256.Sum256([]byte(cmd))
	return hex.EncodeToString(sum[:])
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
