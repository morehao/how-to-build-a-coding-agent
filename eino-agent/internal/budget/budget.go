// Package budget tracks token usage for a session and optionally aborts when a
// configured ceiling is reached. Tokens, not currency: provider prices change
// too often to hard-code in a teaching agent.
package budget

import (
	"fmt"
	"sync"

	"github.com/cloudwego/eino/schema"
)

// Tracker is safe for concurrent use. A nil *Tracker is a no-op.
type Tracker struct {
	mu         sync.Mutex
	max        int
	prompt     int
	completion int
	total      int
}

// New creates a tracker. max <= 0 means unlimited.
func New(max int) *Tracker {
	return &Tracker{max: max}
}

// Max returns the configured ceiling (0 = unlimited).
func (t *Tracker) Max() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.max
}

// Check returns an error when the ceiling has already been reached.
func (t *Tracker) Check() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.max > 0 && t.total >= t.max {
		return fmt.Errorf("token budget exhausted: used %d of %d", t.total, t.max)
	}
	return nil
}

// Observe adds usage.
func (t *Tracker) Observe(u *schema.TokenUsage) {
	if t == nil || u == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prompt += u.PromptTokens
	t.completion += u.CompletionTokens
	total := u.TotalTokens
	if total == 0 {
		total = u.PromptTokens + u.CompletionTokens
	}
	t.total += total
}

// Stats returns prompt, completion and total tokens.
func (t *Tracker) Stats() (prompt, completion, total int) {
	if t == nil {
		return 0, 0, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.prompt, t.completion, t.total
}
