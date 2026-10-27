// Package ui renders the terminal interaction. It intentionally keeps the
// output small and dependency-free: a handful of ANSI colors and a streaming
// line writer are enough for a local coding agent.
package ui

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBlue   = "\x1b[94m"
	ansiYellow = "\x1b[93m"
	ansiCyan   = "\x1b[96m"
	ansiGreen  = "\x1b[92m"
	ansiRed    = "\x1b[91m"
	ansiGray   = "\x1b[90m"
)

// UI writes user-facing output. All methods are safe for concurrent use.
type UI struct {
	out     io.Writer
	err     io.Writer
	color   bool
	verbose bool

	mu        sync.Mutex
	streaming bool
}

// New creates a UI. color should be false when stdout is not a TTY or NO_COLOR
// is set.
func New(out, errOut io.Writer, color, verbose bool) *UI {
	return &UI{out: out, err: errOut, color: color, verbose: verbose}
}

// Prompt prints the user input prompt.
func (u *UI) Prompt() {
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Fprintf(u.out, "\n%sYou%s: ", u.paint(ansiBlue), u.paint(ansiReset))
}

// Banner prints startup information.
func (u *UI) Banner(lines ...string) {
	for _, line := range lines {
		fmt.Fprintln(u.out, line)
	}
}

// Info prints a neutral informational line.
func (u *UI) Info(format string, args ...any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Fprintf(u.out, "%s%s%s\n", u.paint(ansiGray), fmt.Sprintf(format, args...), u.paint(ansiReset))
}

// Warn prints a warning line.
func (u *UI) Warn(format string, args ...any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Fprintf(u.out, "%s%s%s\n", u.paint(ansiYellow), fmt.Sprintf(format, args...), u.paint(ansiReset))
}

// Error prints an error line.
func (u *UI) Error(format string, args ...any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.finishStreamLocked()
	fmt.Fprintf(u.out, "%s%s%s\n", u.paint(ansiRed), fmt.Sprintf(format, args...), u.paint(ansiReset))
}

// Verbosef prints diagnostics only in verbose mode.
func (u *UI) Verbosef(format string, args ...any) {
	if !u.verbose {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Fprintf(u.err, "%s[verbose] %s%s\n", u.paint(ansiGray), fmt.Sprintf(format, args...), u.paint(ansiReset))
}

// AgentStart starts a streamed assistant answer.
func (u *UI) AgentStart() {
	u.mu.Lock()
	defer u.mu.Unlock()
	fmt.Fprintf(u.out, "\n%sAgent%s: ", u.paint(ansiYellow), u.paint(ansiReset))
	u.streaming = true
}

// AgentChunk writes one streamed chunk.
func (u *UI) AgentChunk(chunk string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.streaming {
		fmt.Fprintf(u.out, "\n%sAgent%s: ", u.paint(ansiYellow), u.paint(ansiReset))
		u.streaming = true
	}
	fmt.Fprint(u.out, chunk)
}

// AgentEnd terminates a streamed answer.
func (u *UI) AgentEnd() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.finishStreamLocked()
}

// AgentMessage prints a complete assistant answer (non-streaming mode).
func (u *UI) AgentMessage(content string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.finishStreamLocked()
	fmt.Fprintf(u.out, "\n%sAgent%s: %s\n", u.paint(ansiYellow), u.paint(ansiReset), content)
}

// ToolStart prints a tool invocation.
func (u *UI) ToolStart(name, args string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.finishStreamLocked()
	fmt.Fprintf(u.out, "%s→ %s%s %s\n", u.paint(ansiCyan), name, u.paint(ansiReset), oneLine(args, 240))
}

// ToolEnd prints a tool result summary.
func (u *UI) ToolEnd(name, result string, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if err != nil {
		fmt.Fprintf(u.out, "%s✗ %s: %s%s\n", u.paint(ansiRed), name, oneLine(err.Error(), 240), u.paint(ansiReset))
		return
	}
	fmt.Fprintf(u.out, "%s✓ %s (%s)%s\n", u.paint(ansiGreen), name, byteCount(len(result)), u.paint(ansiReset))
}

// Usage prints token usage when the provider reports it.
func (u *UI) Usage(promptTokens, completionTokens, totalTokens int) {
	if totalTokens == 0 && promptTokens == 0 && completionTokens == 0 {
		return
	}
	u.Verbosef("tokens: prompt=%d completion=%d total=%d", promptTokens, completionTokens, totalTokens)
}

func (u *UI) finishStreamLocked() {
	if u.streaming {
		fmt.Fprintln(u.out)
		u.streaming = false
	}
}

func (u *UI) paint(code string) string {
	if !u.color {
		return ""
	}
	return code
}

func oneLine(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func byteCount(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
