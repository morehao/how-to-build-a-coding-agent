package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestADKFakeProviderTrace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(dir, "script.json")
	script := `{
  "name": "adk-smoke",
  "steps": [
    {"tool_calls": [{"name": "read_file", "arguments": "{\"path\":\"hello.txt\"}"}]},
    {"tool_calls": [{"name": "edit_file", "arguments": "{\"path\":\"hello.txt\",\"old_str\":\"hello\",\"new_str\":\"goodbye\"}"}]},
    {"content": "done"}
  ]
}`
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(dir, "trace.jsonl")
	args := []string{
		"--provider", "fake", "--script", scriptPath,
		"--workspace", dir, "--session-dir", filepath.Join(dir, "sessions"),
		"--permission-mode", "yolo", "--no-stream", "--trace", tracePath,
		"--prompt", "change hello to goodbye",
	}
	var out, errOut bytes.Buffer
	if code := run(args, strings.NewReader(""), &out, &errOut, false); code != 0 {
		t.Fatalf("run code = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil || string(data) != "goodbye\n" {
		t.Fatalf("hello.txt = %q, %v\nstdout:\n%s\nstderr:\n%s", string(data), err, out.String(), errOut.String())
	}
	traceData, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	events := string(traceData)
	for _, want := range []string{`"type":"turn_start"`, `"tool":"read_file"`, `"tool":"edit_file"`, `"type":"tool_end"`, `"type":"turn_end"`} {
		if !strings.Contains(events, want) {
			t.Fatalf("trace missing %s:\n%s", want, events)
		}
	}
}
