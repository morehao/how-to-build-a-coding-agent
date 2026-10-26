// Package retry provides a small, context-aware exponential backoff helper for
// transient provider errors. It deliberately does not retry 4xx auth/validation
// failures or a canceled parent context.
package retry

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"time"
)

// Config controls retries. MaxAttempts includes the first try.
type Config struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Jitter      float64
}

// Default is a conservative policy for model calls.
func Default() Config {
	return Config{MaxAttempts: 3, BaseDelay: 500 * time.Millisecond, MaxDelay: 8 * time.Second, Jitter: 0.2}
}

// Do runs fn until it succeeds, returns a non-retryable error, exhausts
// attempts, or the context is canceled.
func Do[T any](ctx context.Context, cfg Config, fn func(context.Context) (T, error)) (T, error) {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 500 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 8 * time.Second
	}
	var zero T
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		val, err := fn(ctx)
		if err == nil {
			return val, nil
		}
		if !IsRetryable(err) || attempt == cfg.MaxAttempts || ctx.Err() != nil {
			return zero, err
		}
		delay := cfg.BaseDelay
		for i := 1; i < attempt; i++ {
			delay *= 2
			if delay >= cfg.MaxDelay {
				delay = cfg.MaxDelay
				break
			}
		}
		if cfg.Jitter > 0 {
			delta := int64(float64(delay) * cfg.Jitter)
			if delta > 0 {
				delay += time.Duration(rand.Int63n(delta*2+1)) - time.Duration(delta)
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
	return zero, errors.New("retry: exhausted")
}

// IsRetryable classifies provider/network errors by their text. The adapters
// do not expose typed HTTP errors consistently, so this is intentionally
// conservative and documented as heuristic.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, bad := range []string{"401", "403", "400", "invalid api key", "unauthorized", "forbidden", "invalid_request_error"} {
		if strings.Contains(s, bad) {
			return false
		}
	}
	for _, good := range []string{
		"429", "500", "502", "503", "504",
		"rate limit", "rate_limit", "overloaded", "temporarily unavailable",
		"timeout", "timed out", "connection reset", "connection refused",
		"broken pipe", "unexpected eof", "server error", "service unavailable",
		"temporary failure", "try again",
	} {
		if strings.Contains(s, good) {
			return true
		}
	}
	return false
}
