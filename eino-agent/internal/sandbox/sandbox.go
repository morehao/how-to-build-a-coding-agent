// Package sandbox wraps bash commands in an OS-level sandbox when the host
// provides one. It is intentionally opt-in: the default agent still relies on
// approval and workspace checks, which are not a security boundary.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Runner builds commands under a platform sandbox.
type Runner struct {
	kind         string
	bin          string
	root         string
	allowNetwork bool
	profile      string
}

// Name identifies the active backend for logs.
func (r *Runner) Name() string {
	if r == nil {
		return "none"
	}
	return r.kind
}

// New detects and validates a sandbox backend. It fails closed when sandboxing
// is requested but no backend exists.
func New(root string, allowNetwork bool) (*Runner, error) {
	if root == "" {
		return nil, errors.New("sandbox: workspace root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	r := &Runner{root: abs, allowNetwork: allowNetwork}
	switch runtime.GOOS {
	case "darwin":
		bin, err := exec.LookPath("sandbox-exec")
		if err != nil {
			return nil, errors.New("sandbox: sandbox-exec not found; install Xcode command line tools or run without --sandbox")
		}
		r.kind, r.bin = "sandbox-exec", bin
		r.profile = darwinProfile(abs, allowNetwork)
	case "linux":
		bin, err := exec.LookPath("bwrap")
		if err != nil {
			return nil, errors.New("sandbox: bwrap not found (install bubblewrap) or run without --sandbox")
		}
		r.kind, r.bin = "bwrap", bin
	default:
		return nil, fmt.Errorf("sandbox: not supported on %s", runtime.GOOS)
	}
	return r, nil
}

// Wrap returns a command that runs command through the sandbox.
func (r *Runner) Wrap(ctx context.Context, command string) *exec.Cmd {
	if r == nil {
		return exec.CommandContext(ctx, "bash", "-c", command)
	}
	if r.kind == "bwrap" {
		args := []string{
			"--ro-bind", "/", "/",
			"--bind", r.root, r.root,
			"--tmpfs", "/tmp",
			"--proc", "/proc",
			"--dev", "/dev",
			"--die-with-parent",
			"--chdir", r.root,
		}
		if !r.allowNetwork {
			args = append(args, "--unshare-net")
		}
		args = append(args, "--", "bash", "-c", command)
		return exec.CommandContext(ctx, r.bin, args...)
	}
	return exec.CommandContext(ctx, r.bin, "-p", r.profile, "bash", "-c", command)
}

func darwinProfile(root string, allowNetwork bool) string {
	// Deny by default, then allow reads and process execution. Writes are
	// limited to the workspace, temporary directories and /dev. Reads outside
	// the workspace are still allowed because toolchains need SDKs and module
	// caches; this is a write/network boundary, not a data-exfiltration wall.
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n")
	b.WriteString("(allow process*)\n")
	b.WriteString("(allow file-read*)\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow mach-lookup)\n")
	b.WriteString("(allow file-write*\n")
	for _, p := range []string{root, "/tmp", "/private/tmp", "/dev", "/private/var/folders"} {
		b.WriteString("  (subpath \"")
		b.WriteString(sbplQuote(p))
		b.WriteString("\")\n")
	}
	b.WriteString(")\n")
	if allowNetwork {
		b.WriteString("(allow network*)\n")
	}
	return b.String()
}

func sbplQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}
