package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBashTimeoutKillsProcessGroup(t *testing.T) {
	o, _ := testOptions(t)
	o.BashTimeout = 200 * time.Millisecond
	start := time.Now()
	out, err := o.bash(context.Background(), bashInput{Command: "sleep 5 & wait", TimeoutSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "timed out") {
		t.Fatalf("out = %q", out)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took %s; child may have survived", elapsed)
	}
}
