package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/budget"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/contextmgr"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/memory"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/session"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/ui"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
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

func TestFakeProviderLoadsSkillTool(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: review
description: Review a diff.
---

Read the diff before commenting.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(dir, "script.json")
	script := `{
  "name": "skill-smoke",
  "steps": [
    {"tool_calls": [{"name": "skill", "arguments": "{\"name\":\"review\"}"}]},
    {"content": "skill used"}
  ]
}`
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(dir, "trace.jsonl")
	var out, errOut bytes.Buffer
	code := run([]string{
		"--provider", "fake", "--script", scriptPath,
		"--workspace", dir, "--session-dir", filepath.Join(dir, "sessions"),
		"--no-stream", "--trace", tracePath, "--prompt", "review the diff",
	}, strings.NewReader(""), &out, &errOut, false)
	if code != 0 {
		t.Fatalf("code=%d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	traceData, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(traceData), `"tool":"skill"`) {
		t.Fatalf("skill tool was not called:\n%s", string(traceData))
	}
}

func TestSlashSessionsAndResume(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	current, err := session.New(dir, ws.Root(), "openai", "m")
	if err != nil {
		t.Fatal(err)
	}
	other, err := session.New(dir, ws.Root(), "openai", "m")
	if err != nil {
		t.Fatal(err)
	}
	_ = other.Add("user", "old task")
	_ = other.Add("assistant", "old answer")
	if _, err := other.Save(); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	d := &replDeps{
		sess:    current,
		cfg:     &config.Config{SessionDir: dir},
		ws:      ws,
		counter: contextmgr.NewCounter(),
		budget:  budget.New(0),
		u:       ui.New(&out, &errOut, false, false),
	}
	handled, code := handleSlashCommand(context.Background(), "/sessions", d)
	if !handled || code != -1 || !strings.Contains(out.String(), other.ID) {
		t.Fatalf("sessions handled=%v code=%d out=%q", handled, code, out.String())
	}
	out.Reset()
	handled, code = handleSlashCommand(context.Background(), "/resume "+other.ID, d)
	if !handled || code != -1 || d.sess.ID != other.ID {
		t.Fatalf("resume handled=%v code=%d session=%q out=%q", handled, code, d.sess.ID, out.String())
	}
}

func TestPermissionRulesAllowNonInteractiveBash(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, ".eino-agent")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "permissions.json"), []byte(`{
  "allow": [{"tool":"bash","command":"echo allowed"}]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(dir, "script.json")
	script := `{
  "name": "rules-smoke",
  "steps": [
    {"tool_calls": [{"name": "bash", "arguments": "{\"command\":\"echo allowed\"}"}]},
    {"content": "done"}
  ]
}`
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(dir, "trace.jsonl")
	var out, errOut bytes.Buffer
	code := run([]string{
		"--provider", "fake", "--script", scriptPath,
		"--workspace", dir, "--session-dir", filepath.Join(dir, "sessions"),
		"--no-stream", "--trace", tracePath, "--prompt", "run the allowed command",
	}, strings.NewReader(""), &out, &errOut, false)
	if code != 0 {
		t.Fatalf("code=%d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "allowed") || strings.Contains(string(data), "non-interactive session") {
		t.Fatalf("rule was not applied:\n%s", string(data))
	}
}

func TestHooksRunAroundToolCalls(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(hooksPath, []byte(`{
  "before_tool": [{"command": "printf before >> hook.log", "tools": ["read_file"]}],
  "after_tool": [{"command": "printf after >> hook.log", "tools": ["read_file"]}]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(dir, "script.json")
	script := `{
  "name": "hooks-smoke",
  "steps": [
    {"tool_calls": [{"name": "read_file", "arguments": "{\"path\":\"hello.txt\"}"}]},
    {"content": "done"}
  ]
}`
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := run([]string{
		"--provider", "fake", "--script", scriptPath,
		"--workspace", dir, "--session-dir", filepath.Join(dir, "sessions"),
		"--no-stream", "--hooks", hooksPath, "--prompt", "read hello",
	}, strings.NewReader(""), &out, &errOut, false)
	if code != 0 {
		t.Fatalf("code=%d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "hook.log"))
	if err != nil || string(data) != "beforeafter" {
		t.Fatalf("hook.log=%q err=%v", string(data), err)
	}
}

func TestSlashRecall(t *testing.T) {
	dir := t.TempDir()
	ws, err := workspace.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := memory.Save(ws, "decided to use WAL for sqlite"); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	d := &replDeps{
		sess:    &session.Session{ID: "current"},
		cfg:     &config.Config{SessionDir: dir},
		ws:      ws,
		counter: contextmgr.NewCounter(),
		budget:  budget.New(0),
		u:       ui.New(&out, &errOut, false, false),
	}
	handled, code := handleSlashCommand(context.Background(), "/recall WAL", d)
	if !handled || code != -1 || !strings.Contains(out.String(), "WAL") {
		t.Fatalf("handled=%v code=%d out=%q", handled, code, out.String())
	}
}

func TestSlashTraceSummary(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")
	raw := `{"ts":"2026-01-01T00:00:00Z","type":"turn_start"}
{"ts":"2026-01-01T00:00:01Z","type":"model_end","duration_ms":10,"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}
{"ts":"2026-01-01T00:00:02Z","type":"turn_end","usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}
`
	if err := os.WriteFile(tracePath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	d := &replDeps{
		cfg: &config.Config{Trace: tracePath},
		u:   ui.New(&out, &errOut, false, false),
	}
	handled, code := handleSlashCommand(context.Background(), "/trace", d)
	if !handled || code != -1 || !strings.Contains(out.String(), "tokens=10") {
		t.Fatalf("handled=%v code=%d out=%q", handled, code, out.String())
	}
}
