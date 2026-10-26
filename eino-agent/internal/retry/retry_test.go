package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetriesTransientThenSucceeds(t *testing.T) {
	calls := 0
	cfg := Config{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
	got, err := Do(context.Background(), cfg, func(context.Context) (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("503 service unavailable")
		}
		return "ok", nil
	})
	if err != nil || got != "ok" || calls != 3 {
		t.Fatalf("got=%q err=%v calls=%d", got, err, calls)
	}
}

func TestDoesNotRetryAuth(t *testing.T) {
	calls := 0
	cfg := Config{MaxAttempts: 3, BaseDelay: time.Millisecond}
	_, err := Do(context.Background(), cfg, func(context.Context) (string, error) {
		calls++
		return "", errors.New("401 unauthorized")
	})
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestHonorsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	_, err := Do(ctx, Config{MaxAttempts: 3, BaseDelay: time.Second}, func(context.Context) (string, error) {
		calls++
		return "", errors.New("503")
	})
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
