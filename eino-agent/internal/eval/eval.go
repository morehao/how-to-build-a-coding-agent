// Package eval runs deterministic agent scenarios against a scripted model.
// It is intentionally provider-agnostic: a scenario supplies files, a fake
// model script, a user input and expectations; the caller supplies the runtime.
package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/model"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/trace"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

// Scenario is one reproducible agent task.
type Scenario struct {
	Name           string            `json:"name"`
	Files          map[string]string `json:"files,omitempty"`
	Script         model.FakeScript  `json:"script"`
	Input          string            `json:"input"`
	PermissionMode string            `json:"permission_mode,omitempty"`
	MaxSteps       int               `json:"max_steps,omitempty"`
	ContextTokens  int               `json:"context_tokens,omitempty"`
	Expect         Expect            `json:"expect"`
}

// Expect lists the observable outcomes of a scenario.
type Expect struct {
	FinalContains    []string          `json:"final_contains,omitempty"`
	FinalNotContains []string          `json:"final_not_contains,omitempty"`
	Tools            []string          `json:"tools,omitempty"`
	ToolCounts       map[string]int    `json:"tool_counts,omitempty"`
	ForbiddenTools   []string          `json:"forbidden_tools,omitempty"`
	Files            map[string]string `json:"files,omitempty"`
	FilesContain     map[string]string `json:"files_contain,omitempty"`
	ErrorContains    string            `json:"error_contains,omitempty"`
	ExpectError      bool              `json:"expect_error,omitempty"`
	TraceContains    []string          `json:"trace_contains,omitempty"`
	TraceNotContains []string          `json:"trace_not_contains,omitempty"`
	MinModelCalls    int               `json:"min_model_calls,omitempty"`
	MaxModelCalls    int               `json:"max_model_calls,omitempty"`
	MinToolCalls     int               `json:"min_tool_calls,omitempty"`
	MaxToolCalls     int               `json:"max_tool_calls,omitempty"`
}

// Runner is the part of an agent runtime the harness needs.
type Runner interface {
	RunTurn(ctx context.Context, history []*schema.Message) (*schema.Message, error)
}

// Factory builds a runner over the scenario workspace, fake model and trace
// recorder. Tests provide the ReAct or ADK implementation.
type Factory func(ctx context.Context, ws *workspace.Workspace, fake *model.FakeModel, rec *trace.Recorder) (Runner, error)

// Result is the outcome of one scenario.
type Result struct {
	Name     string
	Passed   bool
	Errors   []string
	Final    string
	Tools    []string
	Trace    string
	Stats    trace.Stats
	Duration time.Duration
}

// Run executes sc in a fresh temporary workspace.
func Run(ctx context.Context, sc Scenario, factory Factory) Result {
	start := time.Now()
	res := Result{Name: sc.Name}
	fail := func(format string, args ...any) {
		res.Errors = append(res.Errors, fmt.Sprintf(format, args...))
	}

	if sc.Name == "" {
		fail("scenario name is required")
	}
	if sc.Input == "" {
		fail("input is required")
	}
	if factory == nil {
		fail("factory is required")
	}
	if len(res.Errors) > 0 {
		res.Duration = time.Since(start)
		return res
	}

	dir, err := os.MkdirTemp("", "eino-agent-eval-")
	if err != nil {
		fail("temp workspace: %v", err)
		res.Duration = time.Since(start)
		return res
	}
	defer os.RemoveAll(dir)

	for name, content := range sc.Files {
		path, err := safeJoin(dir, name)
		if err != nil {
			fail("fixture %q: %v", name, err)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fail("fixture %q: %v", name, err)
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			fail("fixture %q: %v", name, err)
		}
	}

	ws, err := workspace.New(dir)
	if err != nil {
		fail("workspace: %v", err)
		res.Duration = time.Since(start)
		return res
	}
	fake, err := model.NewFakeModel(&sc.Script)
	if err != nil {
		fail("script: %v", err)
		res.Duration = time.Since(start)
		return res
	}

	var buf bytes.Buffer
	rec := trace.New(&buf)
	runner, err := factory(ctx, ws, fake, rec)
	if err != nil {
		fail("build runner: %v", err)
		res.Trace = buf.String()
		res.Duration = time.Since(start)
		return res
	}

	final, runErr := runner.RunTurn(ctx, []*schema.Message{schema.UserMessage(sc.Input)})
	if final != nil {
		res.Final = final.Content
	}
	res.Trace = buf.String()
	res.Tools = toolNamesFromTrace(res.Trace)
	res.Stats, _ = trace.Summarize(strings.NewReader(res.Trace))

	if sc.Expect.ExpectError {
		if runErr == nil {
			fail("expected an error, got final %q", res.Final)
		}
	} else if runErr != nil {
		fail("run: %v", runErr)
	}
	if sc.Expect.ErrorContains != "" {
		if runErr == nil || !strings.Contains(runErr.Error(), sc.Expect.ErrorContains) {
			fail("error %v does not contain %q", runErr, sc.Expect.ErrorContains)
		}
	}
	for _, want := range sc.Expect.FinalContains {
		if !strings.Contains(res.Final, want) {
			fail("final %q does not contain %q", res.Final, want)
		}
	}
	for _, bad := range sc.Expect.FinalNotContains {
		if strings.Contains(res.Final, bad) {
			fail("final %q contains forbidden %q", res.Final, bad)
		}
	}
	checkToolOrder(&res, sc.Expect.Tools)
	for name, want := range sc.Expect.ToolCounts {
		got := 0
		for _, n := range res.Tools {
			if n == name {
				got++
			}
		}
		if got != want {
			fail("tool %q count = %d, want %d (tools: %v)", name, got, want, res.Tools)
		}
	}
	for _, bad := range sc.Expect.ForbiddenTools {
		for _, n := range res.Tools {
			if n == bad {
				fail("forbidden tool %q was called", bad)
			}
		}
	}
	for name, want := range sc.Expect.Files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			fail("file %q: %v", name, err)
			continue
		}
		if string(got) != want {
			fail("file %q = %q, want %q", name, string(got), want)
		}
	}
	for name, want := range sc.Expect.FilesContain {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			fail("file %q: %v", name, err)
			continue
		}
		if !strings.Contains(string(got), want) {
			fail("file %q = %q, want substring %q", name, string(got), want)
		}
	}
	for _, want := range sc.Expect.TraceContains {
		if !strings.Contains(res.Trace, want) {
			fail("trace does not contain %q", want)
		}
	}
	for _, bad := range sc.Expect.TraceNotContains {
		if strings.Contains(res.Trace, bad) {
			fail("trace contains forbidden %q", bad)
		}
	}
	if sc.Expect.MinModelCalls > 0 && res.Stats.ModelCalls < sc.Expect.MinModelCalls {
		fail("model calls = %d, want >= %d", res.Stats.ModelCalls, sc.Expect.MinModelCalls)
	}
	if sc.Expect.MaxModelCalls > 0 && res.Stats.ModelCalls > sc.Expect.MaxModelCalls {
		fail("model calls = %d, want <= %d", res.Stats.ModelCalls, sc.Expect.MaxModelCalls)
	}
	if sc.Expect.MinToolCalls > 0 && res.Stats.ToolCalls < sc.Expect.MinToolCalls {
		fail("tool calls = %d, want >= %d", res.Stats.ToolCalls, sc.Expect.MinToolCalls)
	}
	if sc.Expect.MaxToolCalls > 0 && res.Stats.ToolCalls > sc.Expect.MaxToolCalls {
		fail("tool calls = %d, want <= %d", res.Stats.ToolCalls, sc.Expect.MaxToolCalls)
	}

	res.Passed = len(res.Errors) == 0
	res.Duration = time.Since(start)
	return res
}

func safeJoin(root, name string) (string, error) {
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	p := filepath.Join(root, filepath.FromSlash(name))
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace")
	}
	return p, nil
}

func checkToolOrder(res *Result, want []string) {
	pos := 0
	for _, name := range want {
		found := false
		for pos < len(res.Tools) {
			if res.Tools[pos] == name {
				found = true
				pos++
				break
			}
			pos++
		}
		if !found {
			res.Errors = append(res.Errors, fmt.Sprintf("tool %q not found in order (tools: %v)", name, res.Tools))
			return
		}
	}
}

func toolNamesFromTrace(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev struct {
			Type string `json:"type"`
			Tool string `json:"tool"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == "tool_start" && ev.Tool != "" {
			out = append(out, ev.Tool)
		}
	}
	return out
}

// LoadDir reads all *.json scenarios in dir.
func LoadDir(dir string) ([]Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Scenario
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var sc Scenario
		if err := json.Unmarshal(data, &sc); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := sc.Script.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, sc)
	}
	return out, nil
}

// FormatResults renders a compact table for `go test -v` or a CLI.
func FormatResults(results []Result) string {
	var b strings.Builder
	for _, r := range results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(&b, "%s  %-24s %6dms  tools=%s models=%d tokens=%d tool_errors=%d\n",
			status, r.Name, r.Duration.Milliseconds(), strings.Join(r.Tools, ","),
			r.Stats.ModelCalls, r.Stats.TotalTokens, r.Stats.ToolErrors)
		for _, e := range r.Errors {
			fmt.Fprintf(&b, "      %s\n", e)
		}
	}
	return b.String()
}
