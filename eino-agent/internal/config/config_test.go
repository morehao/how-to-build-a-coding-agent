package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestParseOpenAIDefaults(t *testing.T) {
	ws := t.TempDir()
	sessions := filepath.Join(t.TempDir(), "sessions")
	cfg, err := Parse(nil, env(map[string]string{
		"OPENAI_API_KEY":         "sk-test",
		"OPENAI_MODEL":           "gpt-test",
		"EINO_AGENT_WORKSPACE":   ws,
		"EINO_AGENT_SESSION_DIR": sessions,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != ProviderOpenAI || cfg.Model != "gpt-test" || cfg.APIKey != "sk-test" {
		t.Fatalf("provider/model/key = %q/%q/%q", cfg.Provider, cfg.Model, cfg.APIKey)
	}
	if cfg.Workspace != ws {
		t.Fatalf("workspace = %q, want %q", cfg.Workspace, ws)
	}
	if cfg.PermissionMode != PermissionAsk || cfg.MaxSteps != 30 || cfg.HistoryBytes != 256*1024 || cfg.ContextTokens != 32000 {
		t.Fatalf("defaults = mode:%q steps:%d history:%d context:%d", cfg.PermissionMode, cfg.MaxSteps, cfg.HistoryBytes, cfg.ContextTokens)
	}
	if cfg.SessionDir != sessions {
		t.Fatalf("session dir = %q, want %q", cfg.SessionDir, sessions)
	}
	if cfg.NoStream || cfg.Verbose || cfg.Prompt != "" {
		t.Fatalf("unexpected bools: %+v", cfg)
	}
}

func TestParseFlagsOverrideEnvironment(t *testing.T) {
	ws := t.TempDir()
	cfg, err := Parse([]string{
		"--provider", "claude",
		"--model", "claude-test",
		"--base-url", "https://example.invalid",
		"--workspace", ws,
		"--max-steps", "12",
		"--history-bytes", "4096",
		"--context-tokens", "24000",
		"--permission-mode", "yolo",
		"--no-stream",
		"--verbose",
		"-p", "hello",
	}, env(map[string]string{
		"ANTHROPIC_API_KEY":          "sk-ant",
		"ANTHROPIC_MODEL":            "ignored",
		"EINO_AGENT_PERMISSION_MODE": "read-only",
		"EINO_AGENT_MAX_STEPS":       "99",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != ProviderClaude || cfg.Model != "claude-test" {
		t.Fatalf("provider/model = %q/%q", cfg.Provider, cfg.Model)
	}
	if cfg.BaseURL != "https://example.invalid" || cfg.MaxSteps != 12 || cfg.HistoryBytes != 4096 || cfg.ContextTokens != 24000 {
		t.Fatalf("flags not applied: %+v", cfg)
	}
	if cfg.PermissionMode != PermissionYolo || !cfg.NoStream || !cfg.Verbose || cfg.Prompt != "hello" {
		t.Fatalf("flags not applied: %+v", cfg)
	}
}

func TestParseRejectsAmbiguousProvider(t *testing.T) {
	_, err := Parse([]string{"--model", "m", "--workspace", t.TempDir()}, env(map[string]string{
		"OPENAI_API_KEY":    "a",
		"ANTHROPIC_API_KEY": "b",
	}))
	if err == nil || !strings.Contains(err.Error(), "multiple model API keys") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseRequiresModel(t *testing.T) {
	_, err := Parse(nil, env(map[string]string{
		"OPENAI_API_KEY":       "sk",
		"EINO_AGENT_WORKSPACE": t.TempDir(),
	}))
	if err == nil || !strings.Contains(err.Error(), "missing model name") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseRejectsInvalidMode(t *testing.T) {
	_, err := Parse([]string{"--model", "m", "--permission-mode", "danger", "--workspace", t.TempDir()}, env(map[string]string{
		"OPENAI_API_KEY": "sk",
	}))
	if err == nil || !strings.Contains(err.Error(), "invalid permission-mode") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseVersionSkipsValidation(t *testing.T) {
	cfg, err := Parse([]string{"--version"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ShowVersion {
		t.Fatal("ShowVersion = false")
	}
}

func TestParseHelp(t *testing.T) {
	_, err := Parse([]string{"--help"}, env(nil))
	if err != ErrHelp {
		t.Fatalf("error = %v, want ErrHelp", err)
	}
}

func TestParseInvalidEnvInt(t *testing.T) {
	_, err := Parse(nil, env(map[string]string{
		"OPENAI_API_KEY":       "sk",
		"OPENAI_MODEL":         "m",
		"EINO_AGENT_WORKSPACE": t.TempDir(),
		"EINO_AGENT_MAX_STEPS": "many",
	}))
	if err == nil || !strings.Contains(err.Error(), "invalid integer") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseRejectsTooSmallContextBudget(t *testing.T) {
	_, err := Parse([]string{"--model", "m", "--context-tokens", "512", "--workspace", t.TempDir()}, env(map[string]string{
		"OPENAI_API_KEY": "sk",
	}))
	if err == nil || !strings.Contains(err.Error(), "context-tokens must be >= 1024") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseSkillsDefaultsAndFlags(t *testing.T) {
	base := map[string]string{
		"OPENAI_API_KEY":       "sk",
		"OPENAI_MODEL":         "m",
		"EINO_AGENT_WORKSPACE": t.TempDir(),
	}
	cfg, err := Parse(nil, env(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SkillsDir != "skills" || cfg.NoSkills {
		t.Fatalf("skills defaults = %q/%v", cfg.SkillsDir, cfg.NoSkills)
	}

	base["EINO_AGENT_SKILLS_DIR"] = "team-skills"
	cfg, err = Parse(nil, env(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SkillsDir != "team-skills" {
		t.Fatalf("skills env = %q", cfg.SkillsDir)
	}

	cfg, err = Parse([]string{"--skills-dir", "custom", "--no-skills"}, env(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SkillsDir != "custom" || !cfg.NoSkills {
		t.Fatalf("skills flags = %q/%v", cfg.SkillsDir, cfg.NoSkills)
	}
}

func TestParsePermissionRulesAndHooks(t *testing.T) {
	base := map[string]string{
		"OPENAI_API_KEY":       "sk",
		"OPENAI_MODEL":         "m",
		"EINO_AGENT_WORKSPACE": t.TempDir(),
	}
	cfg, err := Parse(nil, env(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PermissionRules != ".eino-agent/permissions.json" || cfg.NoPermissionRules || cfg.HooksPath != "" {
		t.Fatalf("policy defaults: %+v", cfg)
	}
	base["EINO_AGENT_PERMISSION_RULES"] = "team/rules.json"
	base["EINO_AGENT_HOOKS"] = "team/hooks.json"
	cfg, err = Parse(nil, env(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PermissionRules != "team/rules.json" || cfg.HooksPath != "team/hooks.json" {
		t.Fatalf("policy env: %+v", cfg)
	}
	cfg, err = Parse([]string{"--permission-rules", "x.json", "--no-permission-rules", "--hooks", "h.json"}, env(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PermissionRules != "x.json" || !cfg.NoPermissionRules || cfg.HooksPath != "h.json" {
		t.Fatalf("policy flags: %+v", cfg)
	}
}

func TestParseSandbox(t *testing.T) {
	base := map[string]string{
		"OPENAI_API_KEY":       "sk",
		"OPENAI_MODEL":         "m",
		"EINO_AGENT_WORKSPACE": t.TempDir(),
	}
	cfg, err := Parse(nil, env(base))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sandbox || cfg.SandboxAllowNetwork {
		t.Fatalf("sandbox defaults: %+v", cfg)
	}
	cfg, err = Parse([]string{"--sandbox", "--sandbox-allow-network"}, env(base))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Sandbox || !cfg.SandboxAllowNetwork {
		t.Fatalf("sandbox flags: %+v", cfg)
	}
	if _, err := Parse([]string{"--sandbox-allow-network"}, env(base)); err == nil {
		t.Fatal("expected sandbox-allow-network without sandbox to fail")
	}
}
