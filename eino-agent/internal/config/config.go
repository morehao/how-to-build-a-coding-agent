// Package config resolves CLI flags and environment variables into a validated
// runtime configuration for the Eino code agent.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/contextmgr"
)

// Version is the user-visible agent version.
const Version = "0.1.0"

// Permission modes.
const (
	PermissionAsk      = "ask"
	PermissionReadOnly = "read-only"
	PermissionYolo     = "yolo"
)

// Providers.
const (
	ProviderAuto   = "auto"
	ProviderOpenAI = "openai"
	ProviderClaude = "claude"
	ProviderArk    = "ark"
	ProviderFake   = "fake"
)

// Config is the fully resolved runtime configuration.
type Config struct {
	Provider       string
	Model          string
	BaseURL        string
	APIKey         string
	Workspace      string
	MaxSteps       int
	HistoryBytes   int
	ContextTokens  int
	PermissionMode string
	SessionDir     string
	Resume         string
	NoStream       bool
	Verbose        bool
	Prompt         string
	ShowVersion    bool

	// New in v2.
	Script            string
	Trace             string
	NoCheckpoints     bool
	NoReadBeforeEdit  bool
	PermissionRules   string
	NoPermissionRules bool
	HooksPath         string
	AllowSensitive    bool
	AllowEnv          bool
	MaxRetries        int
	RetryBaseMS       int
	MaxTotalTokens    int
	MaxTokens         int
	MCPConfig         string
	SkillsDir         string
	NoSkills          bool
}

// ErrHelp is returned when the user requested usage information.
var ErrHelp = flag.ErrHelp

// Parse parses args (without the program name) and the supplied environment
// lookup function. It validates the result unless --version was requested.
func Parse(args []string, getenv func(string) string) (*Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	fs := flag.NewFlagSet("eino-agent", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		provider         string
		modelName        string
		baseURL          string
		workspace        string
		maxSteps         int
		historyBytes     int
		contextTokens    int
		permissionMode   string
		sessionDir       string
		resume           string
		noStream         bool
		verbose          bool
		prompt           string
		showVersion      bool
		script           string
		tracePath        string
		noCheckpoints    bool
		noReadBeforeEdit bool
		permissionRules  string
		noRules          bool
		hooksPath        string
		allowSensitive   bool
		allowEnv         bool
		maxRetries       int
		retryBaseMS      int
		maxTotalTokens   int
		maxTokens        int
		mcpConfig        string
		skillsDir        string
		noSkills         bool
	)

	fs.StringVar(&provider, "provider", "", "model provider: auto|openai|claude|ark")
	fs.StringVar(&modelName, "model", "", "model name (or endpoint id for ark)")
	fs.StringVar(&baseURL, "base-url", "", "OpenAI-compatible or Anthropic base URL")
	fs.StringVar(&workspace, "workspace", "", "workspace directory (default: current directory)")
	fs.IntVar(&maxSteps, "max-steps", 0, "maximum agent steps / ADK iterations (default: 30)")
	fs.IntVar(&historyBytes, "history-bytes", 0, "conversation history budget in bytes (default: 262144)")
	fs.IntVar(&contextTokens, "context-tokens", 0, "approximate token budget for one model call (default: 32000)")
	fs.StringVar(&permissionMode, "permission-mode", "", "ask|read-only|yolo (default: ask)")
	fs.StringVar(&sessionDir, "session-dir", "", "directory for session snapshots")
	fs.StringVar(&resume, "resume", "", "resume a session by id or path")
	fs.BoolVar(&noStream, "no-stream", false, "disable streaming model output")
	fs.BoolVar(&verbose, "verbose", false, "verbose diagnostics on stderr")
	fs.StringVar(&prompt, "prompt", "", "run one prompt and exit")
	fs.StringVar(&prompt, "p", "", "shorthand for --prompt")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.StringVar(&script, "script", "", "fake-provider script (JSON) for --provider fake")
	fs.StringVar(&tracePath, "trace", "", "write a JSONL trace of this run to a file")
	fs.BoolVar(&noCheckpoints, "no-checkpoints", false, "disable file snapshots used by /undo")
	fs.BoolVar(&noReadBeforeEdit, "no-read-before-edit", false, "allow edit_file/apply_patch without a prior read")
	fs.StringVar(&permissionRules, "permission-rules", "", "JSON allow/deny rules (default: .eino-agent/permissions.json)")
	fs.BoolVar(&noRules, "no-permission-rules", false, "do not load permission rules")
	fs.StringVar(&hooksPath, "hooks", "", "opt-in JSON tool hooks file")
	fs.BoolVar(&allowSensitive, "allow-sensitive", false, "allow reading .env, keys and credentials files")
	fs.BoolVar(&allowEnv, "allow-env", false, "pass the full environment (including API keys) to bash")
	fs.IntVar(&maxRetries, "max-retries", 0, "retry model calls on 429/5xx/timeouts (default: 2)")
	fs.IntVar(&retryBaseMS, "retry-base-ms", 0, "base delay for retries in milliseconds (default: 500)")
	fs.IntVar(&maxTotalTokens, "max-total-tokens", 0, "abort after this many total tokens (0 = unlimited)")
	fs.IntVar(&maxTokens, "max-tokens", 0, "maximum completion tokens for Claude (default: 8192)")
	fs.StringVar(&mcpConfig, "mcp-config", "", "JSON file listing MCP servers to load")
	fs.StringVar(&skillsDir, "skills-dir", "", "directory of SKILL.md bundles (default: skills)")
	fs.BoolVar(&noSkills, "no-skills", false, "disable workspace skills")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q (use --prompt for one-shot input)", fs.Arg(0))
	}

	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })

	cfg := &Config{ShowVersion: showVersion}
	if showVersion {
		return cfg, nil
	}

	cfg.Provider = firstNonEmpty(provider, getenv("EINO_AGENT_PROVIDER"))
	if cfg.Provider == "" {
		cfg.Provider = ProviderAuto
	}
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))

	// Resolve auto provider from exactly one available credential family.
	if cfg.Provider == ProviderAuto {
		p, err := detectProvider(getenv)
		if err != nil {
			return nil, err
		}
		cfg.Provider = p
	}
	if !isKnownProvider(cfg.Provider) {
		return nil, fmt.Errorf("unknown provider %q (want auto|openai|claude|ark|fake)", cfg.Provider)
	}

	cfg.Script = firstNonEmpty(script, getenv("EINO_AGENT_SCRIPT"))
	if cfg.Script != "" {
		absScript, err := filepath.Abs(cfg.Script)
		if err != nil {
			return nil, fmt.Errorf("resolve script: %w", err)
		}
		if _, err := os.Stat(absScript); err != nil {
			return nil, fmt.Errorf("script %s: %w", absScript, err)
		}
		cfg.Script = absScript
	}
	if cfg.Provider == ProviderFake && cfg.Script == "" {
		return nil, fmt.Errorf("--provider fake requires --script <file.json>")
	}

	cfg.Model = firstNonEmpty(modelName, providerModelEnv(getenv, cfg.Provider), getenv("EINO_AGENT_MODEL"))
	if cfg.Model == "" && cfg.Provider == ProviderFake {
		cfg.Model = "scripted"
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("missing model name: pass --model or set %s", providerModelEnvName(cfg.Provider))
	}

	cfg.BaseURL = firstNonEmpty(baseURL, providerBaseURLEnv(getenv, cfg.Provider))
	if cfg.BaseURL == "" && cfg.Provider == ProviderArk {
		cfg.BaseURL = "https://ark.cn-beijing.volces.com/api/v3"
	}

	if cfg.Provider == ProviderFake {
		cfg.APIKey = "fake"
	} else {
		cfg.APIKey = providerAPIKey(getenv, cfg.Provider)
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("missing %s: export it before running eino-agent", providerAPIKeyEnvName(cfg.Provider))
		}
	}

	cfg.Workspace = firstNonEmpty(workspace, getenv("EINO_AGENT_WORKSPACE"), ".")
	absWorkspace, err := filepath.Abs(cfg.Workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(absWorkspace)
	if err != nil {
		return nil, fmt.Errorf("workspace %s: %w", absWorkspace, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace %s is not a directory", absWorkspace)
	}
	cfg.Workspace = absWorkspace

	cfg.MaxSteps, err = intValue(visited["max-steps"], maxSteps, getenv("EINO_AGENT_MAX_STEPS"), 30)
	if err != nil {
		return nil, fmt.Errorf("max-steps: %w", err)
	}
	if cfg.MaxSteps < 4 {
		return nil, fmt.Errorf("max-steps must be >= 4 (got %d)", cfg.MaxSteps)
	}

	cfg.HistoryBytes, err = intValue(visited["history-bytes"], historyBytes, getenv("EINO_AGENT_HISTORY_BYTES"), 256*1024)
	if err != nil {
		return nil, fmt.Errorf("history-bytes: %w", err)
	}
	if cfg.HistoryBytes < 1024 {
		return nil, fmt.Errorf("history-bytes must be >= 1024 (got %d)", cfg.HistoryBytes)
	}

	cfg.ContextTokens, err = intValue(visited["context-tokens"], contextTokens, getenv("EINO_AGENT_CONTEXT_TOKENS"), contextmgr.DefaultContextTokens)
	if err != nil {
		return nil, fmt.Errorf("context-tokens: %w", err)
	}
	if cfg.ContextTokens < 1024 {
		return nil, fmt.Errorf("context-tokens must be >= 1024 (got %d)", cfg.ContextTokens)
	}

	cfg.PermissionMode = strings.ToLower(firstNonEmpty(permissionMode, getenv("EINO_AGENT_PERMISSION_MODE"), PermissionAsk))
	switch cfg.PermissionMode {
	case PermissionAsk, PermissionReadOnly, PermissionYolo:
	default:
		return nil, fmt.Errorf("invalid permission-mode %q (want ask|read-only|yolo)", cfg.PermissionMode)
	}

	cfg.SessionDir = firstNonEmpty(sessionDir, getenv("EINO_AGENT_SESSION_DIR"))
	if cfg.SessionDir == "" {
		cfg.SessionDir = defaultSessionDir()
	}
	cfg.SessionDir, err = filepath.Abs(cfg.SessionDir)
	if err != nil {
		return nil, fmt.Errorf("resolve session-dir: %w", err)
	}

	cfg.Resume = firstNonEmpty(resume, getenv("EINO_AGENT_RESUME"))
	cfg.Prompt = firstNonEmpty(prompt, getenv("EINO_AGENT_PROMPT"))
	cfg.NoStream = boolValue(visited["no-stream"], noStream, getenv("EINO_AGENT_NO_STREAM"))
	cfg.Verbose = boolValue(visited["verbose"], verbose, getenv("EINO_AGENT_VERBOSE"))
	cfg.NoCheckpoints = boolValue(visited["no-checkpoints"], noCheckpoints, getenv("EINO_AGENT_NO_CHECKPOINTS"))
	cfg.NoReadBeforeEdit = boolValue(visited["no-read-before-edit"], noReadBeforeEdit, getenv("EINO_AGENT_NO_READ_BEFORE_EDIT"))
	cfg.AllowSensitive = boolValue(visited["allow-sensitive"], allowSensitive, getenv("EINO_AGENT_ALLOW_SENSITIVE"))
	cfg.AllowEnv = boolValue(visited["allow-env"], allowEnv, getenv("EINO_AGENT_ALLOW_ENV"))
	cfg.PermissionRules = firstNonEmpty(permissionRules, getenv("EINO_AGENT_PERMISSION_RULES"), ".eino-agent/permissions.json")
	cfg.NoPermissionRules = boolValue(visited["no-permission-rules"], noRules, getenv("EINO_AGENT_NO_PERMISSION_RULES"))
	cfg.HooksPath = firstNonEmpty(hooksPath, getenv("EINO_AGENT_HOOKS"))

	cfg.Trace = firstNonEmpty(tracePath, getenv("EINO_AGENT_TRACE"))
	if cfg.Trace != "" {
		absTrace, err := filepath.Abs(cfg.Trace)
		if err != nil {
			return nil, fmt.Errorf("resolve trace: %w", err)
		}
		cfg.Trace = absTrace
	}

	cfg.MaxRetries, err = intValue(visited["max-retries"], maxRetries, getenv("EINO_AGENT_MAX_RETRIES"), 2)
	if err != nil || cfg.MaxRetries < 0 || cfg.MaxRetries > 10 {
		return nil, fmt.Errorf("max-retries must be between 0 and 10")
	}
	cfg.RetryBaseMS, err = intValue(visited["retry-base-ms"], retryBaseMS, getenv("EINO_AGENT_RETRY_BASE_MS"), 500)
	if err != nil || cfg.RetryBaseMS < 0 || cfg.RetryBaseMS > 60000 {
		return nil, fmt.Errorf("retry-base-ms must be between 0 and 60000")
	}
	cfg.MaxTotalTokens, err = intValue(visited["max-total-tokens"], maxTotalTokens, getenv("EINO_AGENT_MAX_TOTAL_TOKENS"), 0)
	if err != nil || cfg.MaxTotalTokens < 0 {
		return nil, fmt.Errorf("max-total-tokens must be >= 0")
	}
	cfg.MaxTokens, err = intValue(visited["max-tokens"], maxTokens, getenv("EINO_AGENT_MAX_TOKENS"), 8192)
	if err != nil || cfg.MaxTokens < 1 {
		return nil, fmt.Errorf("max-tokens must be >= 1")
	}
	cfg.SkillsDir = firstNonEmpty(skillsDir, getenv("EINO_AGENT_SKILLS_DIR"), "skills")
	cfg.NoSkills = boolValue(visited["no-skills"], noSkills, getenv("EINO_AGENT_NO_SKILLS"))

	cfg.MCPConfig = firstNonEmpty(mcpConfig, getenv("EINO_AGENT_MCP_CONFIG"))
	if cfg.MCPConfig != "" {
		absMCP, err := filepath.Abs(cfg.MCPConfig)
		if err != nil {
			return nil, fmt.Errorf("resolve mcp-config: %w", err)
		}
		if _, err := os.Stat(absMCP); err != nil {
			return nil, fmt.Errorf("mcp-config %s: %w", absMCP, err)
		}
		cfg.MCPConfig = absMCP
	}

	return cfg, nil
}

// Usage returns the CLI help text.
func Usage() string {
	return `Eino Code Agent - a small coding agent built on CloudWeGo Eino.

Usage:
  eino-agent [flags]
  eino-agent -p "read go.mod and summarize it"

Core flags:
  --provider string         auto|openai|claude|ark|fake (default: auto)
  --model string            model name; for ark, the endpoint id
  --base-url string         OpenAI-compatible or Anthropic base URL
  --workspace string        workspace directory (default: current directory)
  --max-steps int           maximum agent steps / ADK iterations (default: 30)
  --history-bytes int       session history budget (default: 262144)
  --context-tokens int      approximate token budget per model call (default: 32000)
  --permission-mode string  ask|read-only|yolo (default: ask)
  --session-dir string      directory for session snapshots
  --resume string           resume a session by id or path
  --no-stream               disable streaming output
  --verbose                 verbose diagnostics on stderr
  -p, --prompt string       run one prompt and exit
  --version                 print version and exit
  -h, --help                print this help

Safety and reliability:
  --max-tokens int          maximum completion tokens for Claude (default: 8192)
  --max-total-tokens int    abort after this many total tokens (0 = unlimited)
  --max-retries int         retries for 429/5xx/timeouts (default: 2)
  --retry-base-ms int       base backoff delay in ms (default: 500)
  --no-checkpoints          disable file snapshots used by /undo
  --no-read-before-edit     allow edits without reading the current file first
  --allow-sensitive         allow reading .env, keys and credential files
  --allow-env               pass the full environment to bash (default: scrub secrets)

Observability and tools:
  --trace string            write a JSONL trace (turn/model/tool events)
  --script string           fake-provider script JSON (for --provider fake)
  --mcp-config string       JSON file listing stdio MCP servers
  --skills-dir string       directory of SKILL.md bundles (default: skills)
  --no-skills               disable workspace skills

Environment:
  EINO_AGENT_PROVIDER, EINO_AGENT_MODEL, EINO_AGENT_WORKSPACE,
  EINO_AGENT_MAX_STEPS, EINO_AGENT_HISTORY_BYTES, EINO_AGENT_CONTEXT_TOKENS,
  EINO_AGENT_PERMISSION_MODE, EINO_AGENT_SESSION_DIR, EINO_AGENT_RESUME,
  EINO_AGENT_NO_STREAM, EINO_AGENT_VERBOSE, EINO_AGENT_PROMPT,
  EINO_AGENT_TRACE, EINO_AGENT_SCRIPT, EINO_AGENT_MCP_CONFIG,
  EINO_AGENT_SKILLS_DIR, EINO_AGENT_NO_SKILLS,
  EINO_AGENT_MAX_RETRIES, EINO_AGENT_RETRY_BASE_MS, EINO_AGENT_MAX_TOTAL_TOKENS,
  EINO_AGENT_MAX_TOKENS, EINO_AGENT_NO_CHECKPOINTS, EINO_AGENT_NO_READ_BEFORE_EDIT,
  EINO_AGENT_ALLOW_SENSITIVE, EINO_AGENT_ALLOW_ENV,
  EINO_AGENT_PERMISSION_RULES, EINO_AGENT_NO_PERMISSION_RULES,
  EINO_AGENT_HOOKS

Provider credentials:
  openai:  OPENAI_API_KEY,  optional OPENAI_BASE_URL,  OPENAI_MODEL
  claude:  ANTHROPIC_API_KEY, optional ANTHROPIC_BASE_URL, ANTHROPIC_MODEL
  ark:     ARK_API_KEY,     optional ARK_BASE_URL,     ARK_MODEL
  fake:    no key; requires --script

Slash commands in the REPL:
  /help /clear /compact /plan /session /stats /undo /checkpoints /exit

Examples:
  export OPENAI_API_KEY=sk-...
  eino-agent --provider openai --model gpt-4o-mini --workspace .

  export ANTHROPIC_API_KEY=sk-ant-...
  eino-agent --provider claude --model <claude-model> --workspace .

  # Reproducible run: no network, scripted tool calls
  eino-agent --provider fake --script testdata/scenarios/read_edit.json --workspace .

  # Trace + eval
  eino-agent --trace .eino-agent/trace.jsonl --workspace .
  go test ./internal/agent -run TestEvalScenarios -v
`
}

func detectProvider(getenv func(string) string) (string, error) {
	var found []string
	if strings.TrimSpace(getenv("OPENAI_API_KEY")) != "" {
		found = append(found, ProviderOpenAI)
	}
	if strings.TrimSpace(getenv("ANTHROPIC_API_KEY")) != "" {
		found = append(found, ProviderClaude)
	}
	if strings.TrimSpace(getenv("ARK_API_KEY")) != "" {
		found = append(found, ProviderArk)
	}
	switch len(found) {
	case 0:
		return "", errors.New("no model API key found: export OPENAI_API_KEY, ANTHROPIC_API_KEY or ARK_API_KEY (or set --provider)")
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("multiple model API keys found (%s); pass --provider explicitly", strings.Join(found, ", "))
	}
}

func isKnownProvider(p string) bool {
	switch p {
	case ProviderOpenAI, ProviderClaude, ProviderArk, ProviderFake:
		return true
	default:
		return false
	}
}

func providerAPIKeyEnvName(p string) string {
	switch p {
	case ProviderClaude:
		return "ANTHROPIC_API_KEY"
	case ProviderArk:
		return "ARK_API_KEY"
	default:
		return "OPENAI_API_KEY"
	}
}

func providerAPIKey(getenv func(string) string, p string) string {
	return strings.TrimSpace(getenv(providerAPIKeyEnvName(p)))
}

func providerBaseURLEnvName(p string) string {
	switch p {
	case ProviderClaude:
		return "ANTHROPIC_BASE_URL"
	case ProviderArk:
		return "ARK_BASE_URL"
	default:
		return "OPENAI_BASE_URL"
	}
}

func providerBaseURLEnv(getenv func(string) string, p string) string {
	return strings.TrimSpace(getenv(providerBaseURLEnvName(p)))
}

func providerModelEnvName(p string) string {
	switch p {
	case ProviderClaude:
		return "ANTHROPIC_MODEL"
	case ProviderArk:
		return "ARK_MODEL"
	default:
		return "OPENAI_MODEL"
	}
}

func providerModelEnv(getenv func(string) string, p string) string {
	return strings.TrimSpace(getenv(providerModelEnvName(p)))
}

func defaultSessionDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		if home, homeErr := os.UserHomeDir(); homeErr == nil && home != "" {
			return filepath.Join(home, ".eino-agent", "sessions")
		}
		return filepath.Join(os.TempDir(), "eino-agent", "sessions")
	}
	return filepath.Join(base, "eino-agent", "sessions")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func intValue(flagSet bool, flagVal int, envVal string, def int) (int, error) {
	if flagSet {
		return flagVal, nil
	}
	if envVal == "" {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(envVal))
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q", envVal)
	}
	return n, nil
}

func boolValue(flagSet, flagVal bool, envVal string) bool {
	if flagSet {
		return flagVal
	}
	if envVal == "" {
		return flagVal
	}
	b, err := strconv.ParseBool(strings.TrimSpace(envVal))
	if err != nil {
		return flagVal
	}
	return b
}
