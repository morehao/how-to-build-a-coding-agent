// Package approval implements the human-in-the-loop gate for privileged
// actions, currently shell commands and file edits.
package approval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/input"
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
	// KindPatch is a multi-file patch.
	KindPatch Kind = "patch"
	// KindMCP is a tool call served by an MCP server.
	KindMCP Kind = "mcp"
)

// Request describes a privileged action awaiting approval.
type Request struct {
	Kind    Kind
	Tool    string
	Command string
	Args    string
	Path    string
	Paths   []string
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
	in     input.LineReader
	out    io.Writer
	isTTY  bool
	mu     sync.Mutex
	always map[string]struct{}
	rules  *RuleSet
}

// New creates an approver for the given permission mode. It owns a private
// line source; the CLI should use NewWithSource so the REPL and approvals
// consume the same input stream.
func New(mode string, in io.Reader, out io.Writer, isTTY bool) *Terminal {
	return NewWithSource(mode, input.New(in), out, isTTY)
}

// NewWithSource creates an approver that reads from an existing line source.
// This is the constructor used by the REPL: there must be exactly one reader
// for stdin.
func NewWithSource(mode string, lines input.LineReader, out io.Writer, isTTY bool) *Terminal {
	return &Terminal{
		mode:   mode,
		in:     lines,
		out:    out,
		isTTY:  isTTY,
		always: make(map[string]struct{}),
	}
}

// SetRules installs persistent allow/deny rules. Call during startup. Deny
// rules are evaluated before the permission mode, so they also constrain yolo.
func (t *Terminal) SetRules(rules *RuleSet) { t.rules = rules }

// Approve applies the permission mode and, in ask mode, prompts the user.
func (t *Terminal) Approve(ctx context.Context, req Request) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Deny, err
	}
	kind := req.Kind
	if kind == "" {
		kind = KindBash
	}
	if t.rules != nil {
		if rule, ok := t.rules.Denied(req); ok {
			fmt.Fprintf(t.out, "[approval] denied by rule: %s\n", sanitize(rule.String()))
			return Deny, nil
		}
	}
	switch t.mode {
	case config.PermissionYolo:
		return Once, nil
	case config.PermissionReadOnly:
		fmt.Fprintf(t.out, "[approval] denied %s: privileged actions are disabled in read-only mode\n", kind)
		return Deny, nil
	}

	if t.rules != nil {
		if rule, ok := t.rules.Allowed(req); ok {
			fmt.Fprintf(t.out, "[approval] allowed by rule: %s\n", sanitize(rule.String()))
			return Once, nil
		}
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

	if !printRequest(t.out, kind, req) {
		return Deny, nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		prompt := "  allow? [y] once / [a] always this command / [N] no: "
		switch kind {
		case KindEdit:
			prompt = "  apply? [y] once / [a] always this file / [N] no: "
		case KindPatch:
			prompt = "  apply? [y] once / [a] always this patch / [N] no: "
		}
		fmt.Fprint(t.out, prompt)
		line, err := t.in.ReadLine(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return Deny, ctx.Err()
			}
			if strings.TrimSpace(line) == "" {
				fmt.Fprintln(t.out)
				return Deny, nil
			}
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

// maxReviewBytes is the largest command or patch we are willing to put in
// front of a human. Anything larger is denied rather than truncated: the
// approver must be able to see exactly what will run or be written.
const maxReviewBytes = 256 * 1024

func printRequest(out io.Writer, kind Kind, req Request) bool {
	payload := req.Command
	switch kind {
	case KindEdit, KindPatch:
		payload = req.Diff
	case KindMCP:
		payload = req.Args
	}
	if len(payload) > maxReviewBytes {
		fmt.Fprintf(out, "\n[approval] denied %s: action is too large to review (%d bytes; limit %d). Split it or use --permission-mode yolo.\n",
			kind, len(payload), maxReviewBytes)
		return false
	}
	switch kind {
	case KindMCP:
		fmt.Fprintf(out, "\n[approval] %s (MCP tool)\n  args: %s\n", sanitize(req.Tool), sanitize(req.Args))
	case KindEdit, KindPatch:
		fmt.Fprintf(out, "\n[approval] %s\n", sanitize(req.Tool))
		if len(req.Paths) > 1 {
			fmt.Fprintf(out, "  paths: %s\n", sanitize(strings.Join(req.Paths, ", ")))
		} else if req.Path != "" {
			fmt.Fprintf(out, "  path: %s\n", sanitize(req.Path))
		}
		if req.Summary != "" {
			fmt.Fprintf(out, "  change: %s\n", sanitize(req.Summary))
		}
		if req.Diff != "" {
			fmt.Fprintf(out, "%s\n", sanitize(req.Diff))
		}
	default:
		fmt.Fprintf(out, "\n[approval] bash\n  command: %s\n  cwd:     %s\n  timeout: %s\n",
			sanitize(req.Command), sanitize(req.CWD), req.Timeout)
	}
	return true
}

func requestKey(kind Kind, req Request) string {
	switch kind {
	case KindEdit, KindPatch:
		sum := sha256.Sum256([]byte(req.Path + "\x00" + strings.Join(req.Paths, "\x00") + "\x00" + req.Diff))
		return string(kind) + ":" + hex.EncodeToString(sum[:])
	case KindMCP:
		sum := sha256.Sum256([]byte(req.Tool + "\x00" + req.Args))
		return "mcp:" + hex.EncodeToString(sum[:])
	default:
		sum := sha256.Sum256([]byte(req.Command))
		return "bash:" + hex.EncodeToString(sum[:])
	}
}

// sanitize removes ANSI escape sequences and control characters so model
// output cannot hide or rewrite the approval prompt on a terminal.
func sanitize(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if i+1 < len(s) && s[i+1] == '[' {
				i += 2
				for i < len(s) {
					c := s[i]
					i++
					if c >= 0x40 && c <= 0x7e {
						break
					}
				}
				continue
			}
			i += 2
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			b.WriteRune('·')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
