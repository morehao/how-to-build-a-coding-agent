package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/budget"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/contextmgr"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/session"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/ui"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
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

func TestADKFakeProviderLoadsSkillTool(t *testing.T) {
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
  "name": "adk-skill-smoke",
  "steps": [
    {"tool_calls": [{"name": "skill", "arguments": "{\"name\":\"review\"}"}]},
    {"content": "skill used"}
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
		"--prompt", "review the diff",
	}
	var out, errOut bytes.Buffer
	if code := run(args, strings.NewReader(""), &out, &errOut, false); code != 0 {
		t.Fatalf("run code = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	traceData, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(traceData), `"tool":"skill"`) {
		t.Fatalf("skill tool was not called:\n%s", string(traceData))
	}
}

func TestADKSlashSessionsAndResume(t *testing.T) {
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
