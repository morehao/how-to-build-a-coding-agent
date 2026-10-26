package approval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
)

// Rule is one allow/deny entry in .eino-agent/permissions.json. At least one
// matcher besides Tool must be set. Matching is intentionally literal: no
// shell evaluation, no regex, and path globs use path.Match semantics.
type Rule struct {
	Tool          string `json:"tool"`
	Command       string `json:"command,omitempty"`
	CommandPrefix string `json:"command_prefix,omitempty"`
	Path          string `json:"path,omitempty"`
	PathPrefix    string `json:"path_prefix,omitempty"`
	ArgsPrefix    string `json:"args_prefix,omitempty"`
}

// RuleSet is the on-disk permission policy. Deny wins over allow, and deny
// also applies in yolo mode so a repository can document "never run this".
type RuleSet struct {
	Allow []Rule `json:"allow,omitempty"`
	Deny  []Rule `json:"deny,omitempty"`
}

// LoadRules reads a rule file. A missing file is not an error and returns
// (nil, nil); malformed or empty rules fail closed.
func LoadRules(file string) (*RuleSet, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read permission rules: %w", err)
	}
	var rs RuleSet
	if err := json.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("parse permission rules %s: %w", file, err)
	}
	if err := rs.Validate(); err != nil {
		return nil, fmt.Errorf("permission rules %s: %w", file, err)
	}
	return &rs, nil
}

// Validate rejects broad or ambiguous entries. This is deliberately strict:
// a typo should disable the file, not silently allow everything.
func (rs *RuleSet) Validate() error {
	if rs == nil {
		return nil
	}
	for i, r := range rs.Allow {
		if err := r.validate(); err != nil {
			return fmt.Errorf("allow[%d]: %w", i, err)
		}
	}
	for i, r := range rs.Deny {
		if err := r.validate(); err != nil {
			return fmt.Errorf("deny[%d]: %w", i, err)
		}
	}
	return nil
}

func (r Rule) validate() error {
	if strings.TrimSpace(r.Tool) == "" {
		return errors.New("tool is required")
	}
	if r.Command == "" && r.CommandPrefix == "" && r.Path == "" && r.PathPrefix == "" && r.ArgsPrefix == "" {
		return errors.New("at least one of command, command_prefix, path, path_prefix or args_prefix is required")
	}
	return nil
}

// Denied returns the first deny rule matching req.
func (rs *RuleSet) Denied(req Request) (Rule, bool) {
	if rs == nil {
		return Rule{}, false
	}
	for _, r := range rs.Deny {
		if r.matches(req, false) {
			return r, true
		}
	}
	return Rule{}, false
}

// Allowed returns the first allow rule matching req. Multi-path edits only
// match when every path is covered by the same rule.
func (rs *RuleSet) Allowed(req Request) (Rule, bool) {
	if rs == nil {
		return Rule{}, false
	}
	for _, r := range rs.Allow {
		if r.matches(req, true) {
			return r, true
		}
	}
	return Rule{}, false
}

func (r Rule) matches(req Request, allPaths bool) bool {
	if r.Tool != "*" && r.Tool != req.Tool {
		return false
	}
	if r.Command != "" && r.Command != req.Command {
		return false
	}
	if r.CommandPrefix != "" && !strings.HasPrefix(req.Command, r.CommandPrefix) {
		return false
	}
	if r.ArgsPrefix != "" && !strings.HasPrefix(req.Args, r.ArgsPrefix) {
		return false
	}
	paths := requestPaths(req)
	if r.Path != "" || r.PathPrefix != "" {
		if len(paths) == 0 {
			return false
		}
		for _, p := range paths {
			if r.Path != "" && !matchPath(r.Path, p) {
				if allPaths {
					return false
				}
				continue
			}
			if r.PathPrefix != "" && !strings.HasPrefix(p, r.PathPrefix) {
				if allPaths {
					return false
				}
				continue
			}
			if !allPaths {
				return true
			}
		}
		if allPaths {
			return true
		}
	}
	return r.Path == "" && r.PathPrefix == ""
}

func requestPaths(req Request) []string {
	if len(req.Paths) > 0 {
		return req.Paths
	}
	if req.Path != "" {
		return []string{req.Path}
	}
	return nil
}

func matchPath(pattern, p string) bool {
	p = strings.TrimPrefix(strings.ReplaceAll(p, "\\", "/"), "./")
	pattern = strings.TrimPrefix(strings.ReplaceAll(pattern, "\\", "/"), "./")
	if pattern == p {
		return true
	}
	ok, err := path.Match(pattern, p)
	return err == nil && ok
}

// String is used in the approval transcript. Keep it compact and human-readable.
func (r Rule) String() string {
	var parts []string
	parts = append(parts, "tool="+r.Tool)
	if r.Command != "" {
		parts = append(parts, "command="+r.Command)
	}
	if r.CommandPrefix != "" {
		parts = append(parts, "command_prefix="+r.CommandPrefix)
	}
	if r.Path != "" {
		parts = append(parts, "path="+r.Path)
	}
	if r.PathPrefix != "" {
		parts = append(parts, "path_prefix="+r.PathPrefix)
	}
	if r.ArgsPrefix != "" {
		parts = append(parts, "args_prefix="+r.ArgsPrefix)
	}
	return strings.Join(parts, " ")
}
