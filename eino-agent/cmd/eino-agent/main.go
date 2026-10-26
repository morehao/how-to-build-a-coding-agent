package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/agent"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/approval"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/budget"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/checkpoint"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/contextmgr"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/filestate"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/hooks"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/input"
	mcpbridge "github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/mcp"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/memory"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/model"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/retry"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/session"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/skill"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/subagent"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/summary"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/tools"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/trace"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/ui"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

func main() {
	isTTY := isTerminal(os.Stdin)
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, isTTY))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, isTTY bool) int {
	cfg, err := config.Parse(args, os.Getenv)
	if err != nil {
		if errors.Is(err, config.ErrHelp) {
			fmt.Fprint(stdout, config.Usage())
			return 0
		}
		fmt.Fprintf(stderr, "error: %v\n\n%s", err, config.Usage())
		return 2
	}
	if cfg.ShowVersion {
		fmt.Fprintf(stdout, "eino-agent %s\n", config.Version)
		return 0
	}

	ws, err := workspace.New(cfg.Workspace)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	color := isTTY && writerIsTerminal(stdout) && os.Getenv("NO_COLOR") == ""
	u := ui.New(stdout, stderr, color, cfg.Verbose)
	rec, err := trace.Open(cfg.Trace)
	if err != nil {
		u.Error("error: %v", err)
		return 1
	}
	defer rec.Close()

	lines := input.New(stdin)
	defer lines.Close()
	approver := approval.NewWithSource(cfg.PermissionMode, lines, stdout, isTTY)
	if !cfg.NoPermissionRules {
		rulesPath := cfg.PermissionRules
		if !filepath.IsAbs(rulesPath) {
			rulesPath = filepath.Join(ws.Root(), rulesPath)
		}
		rules, rulesErr := approval.LoadRules(rulesPath)
		if rulesErr != nil {
			u.Error("permission rules: %v", rulesErr)
			return 1
		}
		if rules != nil {
			approver.SetRules(rules)
			u.Verbosef("permission rules: %d allow, %d deny", len(rules.Allow), len(rules.Deny))
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	if cfg.Prompt != "" {
		go func() {
			select {
			case <-sigCh:
				cancel()
			case <-ctx.Done():
			}
		}()
	}

	var checkpoints *checkpoint.Manager
	if !cfg.NoCheckpoints {
		checkpoints, err = checkpoint.New(ws, "")
		if err != nil {
			u.Error("error: %v", err)
			return 1
		}
	}
	files := filestate.New()
	toolList, err := tools.New(ctx, tools.Options{
		Workspace:             ws,
		Approver:              approver,
		Mode:                  cfg.PermissionMode,
		MaxOutputBytes:        256 * 1024,
		BashTimeout:           120 * time.Second,
		Checkpoints:           checkpoints,
		Files:                 files,
		EnforceReadBeforeEdit: !cfg.NoReadBeforeEdit,
		AllowSensitive:        cfg.AllowSensitive,
		AllowEnv:              cfg.AllowEnv,
	})
	if err != nil {
		u.Error("error: %v", err)
		return 1
	}
	var skillTool tool.BaseTool
	var skillPrompt string
	if !cfg.NoSkills {
		skillsBackend, skillErr := skill.NewFilesystemBackend(ws, []string{cfg.SkillsDir})
		if skillErr != nil {
			u.Warn("warning: skills disabled: %v", skillErr)
		} else if defs, listErr := skillsBackend.List(ctx); listErr != nil {
			u.Warn("warning: skills disabled: %v", listErr)
		} else if len(defs) > 0 {
			st, toolErr := skill.NewTool(skill.ToolOptions{Backend: skillsBackend})
			if toolErr != nil {
				u.Warn("warning: skills disabled: %v", toolErr)
			} else {
				skillTool = st
				skillPrompt = skill.PromptSection(defs)
				toolList = append(toolList, skillTool)
				u.Verbosef("skills: %d loaded from %s", len(defs), cfg.SkillsDir)
			}
		}
	}
	if cfg.MCPConfig != "" {
		mcpCfg, err := mcpbridge.Load(cfg.MCPConfig)
		if err != nil {
			u.Error("error: %v", err)
			return 1
		}
		mcpTools, cleanup, err := mcpbridge.Start(ctx, mcpCfg, approver, cfg.PermissionMode)
		if err != nil {
			u.Error("error: %v", err)
			return 1
		}
		defer func() { _ = cleanup() }()
		toolList = append(toolList, mcpTools...)
		u.Verbosef("MCP: %d tool(s) from %d server(s)", len(mcpTools), len(mcpCfg.Servers))
	}
	if cfg.HooksPath != "" {
		hooksPath := cfg.HooksPath
		if !filepath.IsAbs(hooksPath) {
			hooksPath = filepath.Join(ws.Root(), hooksPath)
		}
		hookSet, hookErr := hooks.Load(ws, hooksPath)
		if hookErr != nil {
			u.Error("hooks: %v", hookErr)
			return 1
		}
		if hookSet != nil {
			toolList = hookSet.Wrap(ctx, toolList)
			u.Verbosef("hooks: %d before, %d after", len(hookSet.Before), len(hookSet.After))
		}
	}

	counter := contextmgr.NewCounter()
	tracker := budget.New(cfg.MaxTotalTokens)
	chatModel, err := model.New(ctx, cfg)
	if err != nil {
		u.Error("error: %v", err)
		return 2
	}
	chatModel = model.NewRetryModel(chatModel, retry.Config{
		MaxAttempts: cfg.MaxRetries + 1,
		BaseDelay:   time.Duration(cfg.RetryBaseMS) * time.Millisecond,
		MaxDelay:    8 * time.Second,
		Jitter:      0.2,
	})
	chatModel = model.NewUsageModel(chatModel, counter, tracker)

	subCfg := subagent.Config{
		Workspace:      ws,
		Model:          chatModel,
		Trace:          rec,
		MaxSteps:       6,
		SkillTool:      skillTool,
		SkillPrompt:    skillPrompt,
		MaxTotalTokens: cfg.SubagentMaxTokens,
	}
	newSub := subagent.RunnerFactory(func() (subagent.Runner, error) {
		return subagent.NewReActRunner(ctx, subCfg)
	})
	subRunner, err := newSub()
	if err != nil {
		u.Warn("warning: task tool disabled: %v", err)
	} else {
		if taskTool, taskErr := subagent.NewTaskTool(subRunner); taskErr != nil {
			u.Warn("warning: task tool disabled: %v", taskErr)
		} else {
			toolList = append(toolList, taskTool)
		}
		if tasksTool, tasksErr := subagent.NewTasksTool(newSub, cfg.SubagentMaxTasks); tasksErr != nil {
			u.Warn("warning: tasks tool disabled: %v", tasksErr)
		} else {
			toolList = append(toolList, tasksTool)
		}
	}
	if st, ok := skillTool.(*skill.Tool); ok && subRunner != nil {
		st.SetRunner(subRunner)
	}

	systemPrompt, err := agent.BuildSystemPrompt(ws, cfg.PermissionMode)
	if err != nil {
		u.Error("error: %v", err)
		return 1
	}
	systemPrompt += skillPrompt

	sess, err := loadOrCreateSession(cfg, ws, u)
	if err != nil {
		u.Error("error: %v", err)
		return 2
	}
	persist := cfg.Prompt == "" || cfg.Resume != ""

	runner, err := agent.New(ctx, agent.Config{
		Model:         chatModel,
		Tools:         toolList,
		SystemPrompt:  systemPrompt,
		MaxSteps:      cfg.MaxSteps,
		ContextTokens: cfg.ContextTokens,
		Counter:       counter,
		Stream:        !cfg.NoStream,
		UI:            u,
		Trace:         rec,
	})
	if err != nil {
		u.Error("error: %v", err)
		return 1
	}

	if cfg.Prompt != "" {
		return runOnce(ctx, cfg.Prompt, sess, persist, cfg, ws, runner, u)
	}

	printBanner(u, cfg, ws, sess)
	return runREPL(ctx, cancel, sigCh, lines, stdout, sess, persist, cfg, ws, chatModel, counter, runner, realDeps(checkpoints, files, tracker), u)
}

func loadOrCreateSession(cfg *config.Config, ws *workspace.Workspace, u *ui.UI) (*session.Session, error) {
	if cfg.Resume != "" {
		s, err := session.Load(cfg.SessionDir, cfg.Resume)
		if err != nil {
			return nil, err
		}
		if s.Workspace != "" && s.Workspace != ws.Root() {
			u.Warn("warning: session was created in %s; current workspace is %s", s.Workspace, ws.Root())
		}
		return s, nil
	}
	s, err := session.New(cfg.SessionDir, ws.Root(), cfg.Provider, cfg.Model)
	if err != nil {
		return nil, fmt.Errorf("%w (pass --session-dir to choose a writable directory)", err)
	}
	return s, nil
}

func runOnce(ctx context.Context, prompt string, sess *session.Session, persist bool, cfg *config.Config, ws *workspace.Workspace, runner *agent.Agent, u *ui.UI) int {
	history := append(historyFor(sess, ws), schema.UserMessage(prompt))
	final, err := runner.RunTurn(ctx, history)
	if err != nil {
		if ctx.Err() != nil {
			return 130
		}
		u.Error("error: %v", err)
		return 1
	}
	if persist {
		_ = sess.Add("user", prompt)
		_ = sess.Add("assistant", final.Content)
		sess.Trim(cfg.HistoryBytes)
		if path, saveErr := sess.Save(); saveErr != nil {
			u.Warn("warning: could not save session: %v", saveErr)
		} else {
			u.Verbosef("session saved to %s", path)
		}
	}
	return 0
}

type replDeps struct {
	sess        *session.Session
	cfg         *config.Config
	ws          *workspace.Workspace
	model       einomodel.BaseModel[*schema.Message]
	counter     *contextmgr.Counter
	u           *ui.UI
	checkpoints *checkpoint.Manager
	files       *filestate.State
	budget      *budget.Tracker
}

func realDeps(checkpoints *checkpoint.Manager, files *filestate.State, tracker *budget.Tracker) *replDeps {
	return &replDeps{checkpoints: checkpoints, files: files, budget: tracker}
}

func runREPL(ctx context.Context, cancel context.CancelFunc, sigCh <-chan os.Signal, lines *input.Source, stdout io.Writer, sess *session.Session, persist bool, cfg *config.Config, ws *workspace.Workspace, chatModel einomodel.BaseModel[*schema.Message], counter *contextmgr.Counter, runner *agent.Agent, extra *replDeps, u *ui.UI) int {
	deps := &replDeps{sess: sess, cfg: cfg, ws: ws, model: chatModel, counter: counter, u: u}
	if extra != nil {
		deps.checkpoints = extra.checkpoints
		deps.files = extra.files
		deps.budget = extra.budget
	}

	var mu sync.Mutex
	var turnCancel context.CancelFunc
	interrupted := false
	go func() {
		for {
			select {
			case sig, ok := <-sigCh:
				if !ok {
					return
				}
				mu.Lock()
				cancelTurn := turnCancel
				active := cancelTurn != nil
				if active {
					turnCancel = nil
					interrupted = true
				}
				mu.Unlock()
				if active {
					cancelTurn()
					if sig == syscall.SIGTERM {
						cancel()
						return
					}
					continue
				}
				cancel()
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		u.Prompt()
		line, readErr := lines.ReadLine(ctx)
		if readErr != nil {
			if ctx.Err() != nil {
				fmt.Fprintln(stdout)
				return 130
			}
			if errors.Is(readErr, io.EOF) {
				fmt.Fprintln(stdout)
				return 0
			}
			u.Error("input error: %v", readErr)
			return 1
		}
		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}
		if handled, code := handleSlashCommand(ctx, input, deps); handled {
			if code >= 0 {
				return code
			}
			continue
		}

		history := append(historyFor(deps.sess, ws), schema.UserMessage(input))
		turnCtx, turnDone := context.WithCancel(ctx)
		mu.Lock()
		turnCancel = turnDone
		interrupted = false
		mu.Unlock()
		final, err := runner.RunTurn(turnCtx, history)
		turnDone()
		mu.Lock()
		turnCancel = nil
		wasInterrupted := interrupted
		mu.Unlock()
		if err != nil {
			if wasInterrupted && ctx.Err() == nil {
				u.Warn("turn interrupted; partial work was not saved")
				continue
			}
			if ctx.Err() != nil {
				return 130
			}
			u.Error("error: %v", err)
			continue
		}
		_ = deps.sess.Add("user", input)
		_ = deps.sess.Add("assistant", final.Content)
		if dropped := deps.sess.Trim(cfg.HistoryBytes); dropped > 0 {
			u.Verbosef("dropped %d old messages to stay under %d bytes", dropped, cfg.HistoryBytes)
		}
		if persist {
			if _, err := deps.sess.Save(); err != nil {
				u.Warn("warning: could not save session: %v", err)
			}
		}
	}
}

// handleSlashCommand returns handled=true when input was a command. code is -1
// to continue, or the process exit code to return.
func handleSlashCommand(ctx context.Context, input string, d *replDeps) (bool, int) {
	switch input {
	case "/exit", "/quit":
		return true, 0
	case "/help":
		d.u.Info("commands: /help  /clear  /compact  /plan  /session  /sessions  /resume <id>  /stats  /undo  /checkpoints  /exit")
		return true, -1
	case "/clear":
		d.sess.Clear()
		d.u.Info("conversation history and summary cleared")
		return true, -1
	case "/compact":
		if err := compactSession(ctx, d); err != nil {
			d.u.Error("compact: %v", err)
		}
		return true, -1
	case "/plan":
		path, _ := memory.Path(d.ws)
		content, err := memory.Load(d.ws)
		if err != nil {
			d.u.Error("plan: %v", err)
		} else if strings.TrimSpace(content) == "" {
			d.u.Info("working memory is empty: %s", path)
		} else {
			d.u.Info("working memory: %s\n\n%s", path, content)
		}
		return true, -1
	case "/session":
		d.u.Info("session: %s\nfile: %s", d.sess.ID, d.sess.Path())
		return true, -1
	case "/sessions":
		infos, err := session.List(d.cfg.SessionDir)
		if err != nil {
			d.u.Error("sessions: %v", err)
			return true, -1
		}
		if len(infos) == 0 {
			d.u.Info("no saved sessions in %s", d.cfg.SessionDir)
			return true, -1
		}
		for i, info := range infos {
			if i >= 10 {
				d.u.Info("... and %d older session(s)", len(infos)-10)
				break
			}
			marker := " "
			if info.ID == d.sess.ID {
				marker = "*"
			}
			if info.Error != "" {
				d.u.Info("%s %s  (unreadable: %s)", marker, info.ID, info.Error)
				continue
			}
			d.u.Info("%s %s  %s  %d msg  %s", marker, info.ID, info.UpdatedAt.Format("2006-01-02 15:04"), info.Messages, info.Workspace)
		}
		return true, -1
	case "/stats":
		prompt, completion, total := d.budget.Stats()
		max := d.budget.Max()
		limit := "unlimited"
		if max > 0 {
			limit = fmt.Sprintf("%d", max)
		}
		d.u.Info("tokens: prompt=%d completion=%d total=%d (max %s)\ncontext calibration: %.2f\nsession: %d bytes",
			prompt, completion, total, limit, d.counter.Ratio(), d.sess.Bytes())
		return true, -1
	case "/undo":
		if d.checkpoints == nil {
			d.u.Warn("checkpoints are disabled; start without --no-checkpoints")
			return true, -1
		}
		restored, err := d.checkpoints.UndoLast()
		if err != nil {
			d.u.Error("undo: %v", err)
		} else {
			d.u.Info("restored %d file(s): %s", len(restored), strings.Join(restored, ", "))
		}
		return true, -1
	case "/checkpoints":
		if d.checkpoints == nil {
			d.u.Warn("checkpoints are disabled")
			return true, -1
		}
		entries, err := d.checkpoints.List()
		if err != nil {
			d.u.Error("checkpoints: %v", err)
			return true, -1
		}
		if len(entries) == 0 {
			d.u.Info("no checkpoints")
			return true, -1
		}
		for i, e := range entries {
			if i >= 10 {
				d.u.Info("... and %d older checkpoint(s)", len(entries)-10)
				break
			}
			d.u.Info("%s  %s  %d file(s)", e.ID, e.Label, len(e.Files))
		}
		return true, -1
	default:
		if strings.HasPrefix(input, "/resume ") {
			id := strings.TrimSpace(strings.TrimPrefix(input, "/resume "))
			if id == "" {
				d.u.Warn("usage: /resume <session-id>")
				return true, -1
			}
			loaded, err := session.Load(d.cfg.SessionDir, id)
			if err != nil {
				d.u.Error("resume: %v", err)
				return true, -1
			}
			if loaded.Workspace != "" && loaded.Workspace != d.ws.Root() {
				d.u.Warn("warning: session was created in %s; current workspace is %s", loaded.Workspace, d.ws.Root())
			}
			d.sess = loaded
			d.u.Info("resumed session %s (%d messages)", loaded.ID, len(loaded.Messages))
			return true, -1
		}
		if strings.HasPrefix(input, "/") {
			d.u.Warn("unknown command %q; try /help", input)
			return true, -1
		}
		return false, -1
	}
}

func compactSession(ctx context.Context, d *replDeps) error {
	before := d.sess.Bytes()
	history := d.sess.SchemaMessagesWithSummary()
	text, err := summary.Generate(ctx, d.model, history, d.cfg.ContextTokens/2, d.counter)
	if err != nil {
		return err
	}
	d.sess.Compact(text, 2)
	d.sess.Trim(d.cfg.HistoryBytes)
	if _, err := d.sess.Save(); err != nil {
		return fmt.Errorf("save compacted session: %w", err)
	}
	d.u.Info("compacted session: %d -> %d bytes; summary saved", before, d.sess.Bytes())
	return nil
}

func historyFor(sess *session.Session, ws *workspace.Workspace) []*schema.Message {
	out := sess.SchemaMessagesWithSummary()
	if msg := memory.HistoryMessage(ws); msg != nil {
		out = append([]*schema.Message{msg}, out...)
	}
	return out
}

func printBanner(u *ui.UI, cfg *config.Config, ws *workspace.Workspace, sess *session.Session) {
	u.Banner(
		fmt.Sprintf("Eino Code Agent %s", config.Version),
		fmt.Sprintf("provider: %s · model: %s", cfg.Provider, cfg.Model),
		fmt.Sprintf("workspace: %s", ws.Root()),
		fmt.Sprintf("permissions: %s · streaming: %t", cfg.PermissionMode, !cfg.NoStream),
		fmt.Sprintf("trace: %s", traceLabel(cfg.Trace)),
		fmt.Sprintf("session: %s", sess.ID),
		"Type /help for commands, /exit to quit.",
	)
}

func traceLabel(path string) string {
	if path == "" {
		return "disabled (pass --trace <file>)"
	}
	return path
}

func writerIsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isTerminal(f)
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
