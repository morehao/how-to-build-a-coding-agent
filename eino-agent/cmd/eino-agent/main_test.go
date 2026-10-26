package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFakeProviderApprovalAndTrace(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.json")
	script := `{
  "name": "approval-smoke",
  "steps": [
    {"tool_calls": [{"name": "edit_file", "arguments": "{\"path\":\"note.txt\",\"old_str\":\"\",\"new_str\":\"hello\\n\"}"}]},
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
		"--no-stream", "--trace", tracePath,
	}

	pr, pw := io.Pipe()
	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run(args, pr, &out, &errOut, true) }()

	if _, err := fmt.Fprintln(pw, "create note.txt"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := fmt.Fprintln(pw, "y"); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "note.txt")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(target); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "hello\n" {
		t.Fatalf("note.txt = %q, %v\nstdout:\n%s\nstderr:\n%s", string(data), err, out.String(), errOut.String())
	}

	if _, err := fmt.Fprintln(pw, "/exit"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("run did not exit after /exit")
	}

	traceData, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	events := string(traceData)
	for _, want := range []string{`"type":"turn_start"`, `"type":"tool_start"`, `"tool":"edit_file"`, `"type":"tool_end"`, `"type":"turn_end"`} {
		if !strings.Contains(events, want) {
			t.Fatalf("trace missing %s:\n%s", want, events)
		}
	}
}

func TestVersionAndHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--version"}, strings.NewReader(""), &out, &errOut, false); code != 0 {
		t.Fatalf("version code = %d", code)
	}
	if !strings.Contains(out.String(), "eino-agent") {
		t.Fatalf("version output = %q", out.String())
	}
	out.Reset()
	if code := run([]string{"--help"}, strings.NewReader(""), &out, &errOut, false); code != 0 {
		t.Fatalf("help code = %d", code)
	}
	if !strings.Contains(out.String(), "Usage") {
		t.Fatalf("help output = %q", out.String())
	}
}

func TestUndoRestoresEditedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(dir, "script.json")
	script := `{
  "name": "undo-smoke",
  "steps": [
    {"tool_calls": [{"name": "read_file", "arguments": "{\"path\":\"a.txt\"}"}]},
    {"tool_calls": [{"name": "edit_file", "arguments": "{\"path\":\"a.txt\",\"old_str\":\"old\",\"new_str\":\"new\"}"}]},
    {"content": "done"}
  ]
}`
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--provider", "fake", "--script", scriptPath,
		"--workspace", dir, "--session-dir", filepath.Join(dir, "sessions"),
		"--no-stream",
	}
	pr, pw := io.Pipe()
	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run(args, pr, &out, &errOut, true) }()

	if _, err := fmt.Fprintln(pw, "edit a.txt"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := fmt.Fprintln(pw, "y"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "a.txt")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, _ := os.ReadFile(target); string(data) == "new\n" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if data, _ := os.ReadFile(target); string(data) != "new\n" {
		t.Fatalf("edit did not apply: %q\nstdout:\n%s\nstderr:\n%s", data, out.String(), errOut.String())
	}
	if _, err := fmt.Fprintln(pw, "/undo"); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		if data, _ := os.ReadFile(target); string(data) == "old\n" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if data, _ := os.ReadFile(target); string(data) != "old\n" {
		t.Fatalf("undo did not restore: %q\nstdout:\n%s\nstderr:\n%s", data, out.String(), errOut.String())
	}
	if _, err := fmt.Fprintln(pw, "/exit"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("run did not exit")
	}
}
