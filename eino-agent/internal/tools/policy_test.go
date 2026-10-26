package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/filestate"
)

func TestSensitiveReadDeniedUnlessAllowed(t *testing.T) {
	o, dir := testOptions(t)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("OPENAI_API_KEY=sk-test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.readFile(context.Background(), readFileInput{Path: ".env"}); err == nil || !strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("err = %v", err)
	}
	o.AllowSensitive = true
	if _, err := o.readFile(context.Background(), readFileInput{Path: ".env"}); err != nil {
		t.Fatal(err)
	}
}

func TestBashScrubsSecretEnvByDefault(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	o, _ := testOptions(t)
	out, err := o.bash(context.Background(), bashInput{Command: `printf '%s' "${OPENAI_API_KEY:-unset}"`, TimeoutSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sk-test") {
		t.Fatalf("secret leaked: %q", out)
	}
	o.AllowEnv = true
	out, err = o.bash(context.Background(), bashInput{Command: `printf '%s' "${OPENAI_API_KEY:-unset}"`, TimeoutSeconds: 5})
	if err != nil || !strings.Contains(out, "sk-test") {
		t.Fatalf("allow-env output = %q, %v", out, err)
	}
}

func TestEditRequiresReadWhenEnforced(t *testing.T) {
	o, dir := testOptions(t)
	o.Files = filestate.New()
	o.EnforceReadBeforeEdit = true
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.editFile(context.Background(), editFileInput{Path: "a.txt", OldStr: "hello", NewStr: "bye"}); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("err = %v", err)
	}
	if _, err := o.readFile(context.Background(), readFileInput{Path: "a.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.editFile(context.Background(), editFileInput{Path: "a.txt", OldStr: "hello", NewStr: "bye"}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "bye\n" {
		t.Fatalf("file = %q", data)
	}
}
