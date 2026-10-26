package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cloudwego/eino/schema"

	agentbase "github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/agent"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/agentadk"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/approval"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/model"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/session"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/tools"
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

	u := ui.New(stdout, stderr, isTTY && os.Getenv("NO_COLOR") == "", cfg.Verbose)
	approver := approval.New(cfg.PermissionMode, stdin, stdout, isTTY)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	toolList, err := tools.New(ctx, tools.Options{
		Workspace:      ws,
		Approver:       approver,
		Mode:           cfg.PermissionMode,
		MaxOutputBytes: 256 * 1024,
		BashTimeout:    120 * time.Second,
	})
	if err != nil {
		u.Error("error: %v", err)
		return 1
	}

	chatModel, err := model.New(ctx, cfg)
	if err != nil {
		u.Error("error: %v", err)
		return 2
	}

	systemPrompt, err := agentbase.BuildSystemPrompt(ws, cfg.PermissionMode)
	if err != nil {
		u.Error("error: %v", err)
		return 1
	}

	sess, err := loadOrCreateSession(cfg, ws, u)
	if err != nil {
		u.Error("error: %v", err)
		return 2
	}
	persist := cfg.Prompt == "" || cfg.Resume != ""

	runner, err := agentadk.New(ctx, agentadk.Config{
		Model:         chatModel,
		Tools:         toolList,
		SystemPrompt:  systemPrompt,
		MaxSteps:      cfg.MaxSteps,
		ContextTokens: cfg.ContextTokens,
		Stream:        !cfg.NoStream,
		UI:            u,
	})
	if err != nil {
		u.Error("error: %v", err)
		return 1
	}

	if cfg.Prompt != "" {
		return runOnce(ctx, cfg.Prompt, sess, persist, cfg, runner, u)
	}

	printBanner(u, cfg, ws, sess)
	return runREPL(ctx, stdin, stdout, sess, persist, cfg, runner, u)
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

func runOnce(ctx context.Context, prompt string, sess *session.Session, persist bool, cfg *config.Config, runner *agentadk.Agent, u *ui.UI) int {
	history := append(sess.SchemaMessages(), schema.UserMessage(prompt))
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

func runREPL(ctx context.Context, stdin io.Reader, stdout io.Writer, sess *session.Session, persist bool, cfg *config.Config, runner *agentadk.Agent, u *ui.UI) int {
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	lines := make(chan string)
	scanErr := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		scanErr <- scanner.Err()
	}()

	for {
		u.Prompt()
		select {
		case <-ctx.Done():
			fmt.Fprintln(stdout)
			return 130
		case err := <-scanErr:
			if err != nil {
				u.Error("input error: %v", err)
				return 1
			}
			fmt.Fprintln(stdout)
			return 0
		case line := <-lines:
			input := strings.TrimSpace(line)
			if input == "" {
				continue
			}
			if handled, code := handleSlashCommand(input, sess, u); handled {
				if code >= 0 {
					return code
				}
				continue
			}

			history := append(sess.SchemaMessages(), schema.UserMessage(input))
			final, err := runner.RunTurn(ctx, history)
			if err != nil {
				if ctx.Err() != nil {
					return 130
				}
				u.Error("error: %v", err)
				continue
			}
			_ = sess.Add("user", input)
			_ = sess.Add("assistant", final.Content)
			if dropped := sess.Trim(cfg.HistoryBytes); dropped > 0 {
				u.Verbosef("dropped %d old messages to stay under %d bytes", dropped, cfg.HistoryBytes)
			}
			if persist {
				if _, err := sess.Save(); err != nil {
					u.Warn("warning: could not save session: %v", err)
				}
			}
		}
	}
}

// handleSlashCommand returns handled=true when input was a command. code is -1
// to continue, or the process exit code to return.
func handleSlashCommand(input string, sess *session.Session, u *ui.UI) (bool, int) {
	switch input {
	case "/exit", "/quit":
		return true, 0
	case "/help":
		u.Info("commands: /help  /clear  /session  /exit")
		return true, -1
	case "/clear":
		sess.Clear()
		u.Info("conversation history cleared")
		return true, -1
	case "/session":
		u.Info("session: %s\nfile: %s", sess.ID, sess.Path())
		return true, -1
	default:
		if strings.HasPrefix(input, "/") {
			u.Warn("unknown command %q; try /help", input)
			return true, -1
		}
		return false, -1
	}
}

func printBanner(u *ui.UI, cfg *config.Config, ws *workspace.Workspace, sess *session.Session) {
	u.Banner(
		fmt.Sprintf("Eino Code Agent (ADK) %s", config.Version),
		fmt.Sprintf("provider: %s · model: %s", cfg.Provider, cfg.Model),
		fmt.Sprintf("workspace: %s", ws.Root()),
		fmt.Sprintf("permissions: %s · streaming: %t", cfg.PermissionMode, !cfg.NoStream),
		fmt.Sprintf("session: %s", sess.ID),
		"Type /help for commands, /exit to quit.",
	)
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
