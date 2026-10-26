package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/approval"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
)

type bashInput struct {
	Command        string `json:"command" jsonschema:"required" jsonschema_description:"Shell command to run with bash -c. Prefer read_file/list_files/code_search for file inspection."`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema_description:"Timeout in seconds. Default 120, max 600."`
}

func (o *Options) bash(ctx context.Context, in bashInput) (string, error) {
	if o.Mode == config.PermissionReadOnly {
		return "", errors.New("bash is disabled in read-only mode")
	}
	command := strings.TrimSpace(in.Command)
	if command == "" {
		return "", errors.New("command is empty")
	}
	timeout := time.Duration(in.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = o.BashTimeout
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if timeout > 600*time.Second {
		timeout = 600 * time.Second
	}

	if o.Approver == nil {
		return "", errors.New("command denied: no approver configured")
	}
	decision, err := o.Approver.Approve(ctx, approval.Request{
		Command: command,
		CWD:     o.Workspace.Root(),
		Timeout: timeout,
	})
	if err != nil {
		return "", fmt.Errorf("approval failed: %w", err)
	}
	if decision == approval.Deny {
		return "ERROR: command denied by user", nil
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-c", command)
	cmd.Dir = o.Workspace.Root()
	cmd.Env = append(os.Environ(), "TERM=dumb", "PAGER=cat", "GIT_PAGER=cat")
	buf := newCappedBuffer(o.MaxOutputBytes/2, o.MaxOutputBytes/2)
	cmd.Stdout = buf
	cmd.Stderr = buf
	// Stdin is intentionally nil: commands read EOF instead of hanging on a
	// terminal that the agent does not own.
	cmd.Stdin = nil

	runErr := cmd.Run()
	output := buf.String()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("ERROR: command timed out after %s\n%s", timeout, output), nil
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return fmt.Sprintf("exit code: %d\n%s", exitErr.ExitCode(), output), nil
		}
		return "", fmt.Errorf("run command: %w", runErr)
	}
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	return "exit code: 0\n" + output, nil
}

// cappedBuffer keeps the first headLimit bytes and the last tailLimit bytes.
// It bounds memory for commands that produce huge output while preserving the
// beginning (usually setup/build errors) and the end (final failure).
type cappedBuffer struct {
	head      []byte
	tail      []byte
	headLimit int
	tailLimit int
	truncated bool
}

func newCappedBuffer(headLimit, tailLimit int) *cappedBuffer {
	if headLimit <= 0 {
		headLimit = 128 * 1024
	}
	if tailLimit <= 0 {
		tailLimit = 128 * 1024
	}
	return &cappedBuffer{headLimit: headLimit, tailLimit: tailLimit}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(b.head) < b.headLimit {
		room := b.headLimit - len(b.head)
		take := n
		if take > room {
			take = room
		}
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		b.truncated = true
		b.tail = append(b.tail, p...)
		if len(b.tail) > b.tailLimit {
			drop := len(b.tail) - b.tailLimit
			b.tail = append([]byte(nil), b.tail[drop:]...)
		}
	}
	return n, nil
}

func (b *cappedBuffer) String() string {
	var out strings.Builder
	out.Write(b.head)
	if b.truncated {
		out.WriteString("\n... [output truncated; showing head and tail] ...\n")
		out.Write(b.tail)
	}
	return strings.TrimRight(out.String(), "\n")
}
