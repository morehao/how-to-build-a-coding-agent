// Package tools defines the built-in tools exposed to the agent. Each
// tool is a typed Go function registered with utils.InferTool, so the JSON
// schema seen by the model is derived from the input struct.
package tools

import (
	"context"
	"fmt"
	"os/exec"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/approval"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/checkpoint"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/filestate"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/sandbox"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

// Options wires tool implementations to the workspace, approval gate and
// resource limits.
type Options struct {
	Workspace      *workspace.Workspace
	Approver       approval.Approver
	Mode           string
	MaxOutputBytes int
	BashTimeout    time.Duration
	RGPath         string

	// v2 capabilities.
	Checkpoints           *checkpoint.Manager
	Files                 *filestate.State
	EnforceReadBeforeEdit bool
	AllowSensitive        bool
	AllowEnv              bool
	// Sandbox optionally wraps bash in an OS-level sandbox. Nil means plain bash.
	Sandbox *sandbox.Runner
}

// New builds the tool list. The returned tools are safe to share across turns;
// they hold no per-conversation state.
func New(ctx context.Context, opts Options) ([]tool.BaseTool, error) {
	if opts.Workspace == nil {
		return nil, fmt.Errorf("tools: workspace is required")
	}
	if opts.Mode == "" {
		opts.Mode = config.PermissionAsk
	}
	if opts.MaxOutputBytes <= 0 {
		opts.MaxOutputBytes = 256 * 1024
	}
	if opts.BashTimeout <= 0 {
		opts.BashTimeout = 120 * time.Second
	}
	if opts.RGPath == "" {
		if p, err := exec.LookPath("rg"); err == nil {
			opts.RGPath = p
		}
	}

	readTool, err := utils.InferTool(
		"read_file",
		"Read a UTF-8 text file and return its contents with line numbers. Use offset/limit for large files.",
		wrapTool(&opts, opts.readFile),
	)
	if err != nil {
		return nil, fmt.Errorf("register read_file: %w", err)
	}

	listTool, err := utils.InferTool(
		"list_files",
		"List files and directories under a workspace path. Skips VCS, dependency and build directories.",
		wrapTool(&opts, opts.listFiles),
	)
	if err != nil {
		return nil, fmt.Errorf("register list_files: %w", err)
	}

	editTool, err := utils.InferTool(
		"edit_file",
		"Edit an existing file by replacing one uniquely occurring fragment, or create a new file with an empty old_str. Read the file before editing.",
		wrapTool(&opts, opts.editFile),
	)
	if err != nil {
		return nil, fmt.Errorf("register edit_file: %w", err)
	}

	patchTool, err := utils.InferTool(
		"apply_patch",
		"Apply a unified diff across one or more files. Include ---/+++ headers and @@ hunks. Prefer this over edit_file for multi-file or multi-hunk changes.",
		wrapTool(&opts, opts.applyPatch),
	)
	if err != nil {
		return nil, fmt.Errorf("register apply_patch: %w", err)
	}

	repoMapTool, err := utils.InferTool(
		"repo_map",
		"Return a compact directory/file/Go-symbol map of the repository. Honors .einoignore; use it instead of dumping large trees.",
		wrapTool(&opts, opts.repoMap),
	)
	if err != nil {
		return nil, fmt.Errorf("register repo_map: %w", err)
	}

	bashTool, err := utils.InferTool(
		"bash",
		"Run a shell command in the workspace root with bash -c. Requires user approval unless permission mode is yolo. Use for builds, tests and git inspection.",
		wrapTool(&opts, opts.bash),
	)
	if err != nil {
		return nil, fmt.Errorf("register bash: %w", err)
	}

	planTool, err := utils.InferTool(
		"update_plan",
		"Create or replace the persistent working-memory plan at .eino-agent/plan.md. Use it to preserve goals, decisions, changed files, command results, open errors and next steps across context compaction.",
		wrapTool(&opts, opts.updatePlan),
	)
	if err != nil {
		return nil, fmt.Errorf("register update_plan: %w", err)
	}

	searchTool, err := utils.InferTool(
		"code_search",
		"Search file contents with a regular expression. Prefers ripgrep and falls back to a Go walker when rg is unavailable.",
		wrapTool(&opts, opts.codeSearch),
	)
	if err != nil {
		return nil, fmt.Errorf("register code_search: %w", err)
	}

	return []tool.BaseTool{readTool, listTool, editTool, patchTool, repoMapTool, bashTool, searchTool, planTool}, nil
}

// wrapTool turns operational errors into tool output ("ERROR: ...") so the
// model can recover within the ReAct loop. Eino's ToolsNode aborts the graph on
// a Go error; that is appropriate for cancellation, not for a missing file or
// a denied command.
func wrapTool[T any](o *Options, fn func(context.Context, T) (string, error)) func(context.Context, T) (string, error) {
	return func(ctx context.Context, in T) (string, error) {
		out, err := fn(ctx, in)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return o.capOutput("ERROR: " + err.Error()), nil
		}
		return o.capOutput(out), nil
	}
}

func (o *Options) capOutput(s string) string {
	max := o.MaxOutputBytes
	if max <= 0 || len(s) <= max {
		return s
	}
	return truncateToolOutput(s, max) + "\n... [tool output truncated]"
}

func truncateToolOutput(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.ValidString(s[:cut]) {
		cut--
	}
	return s[:cut]
}
