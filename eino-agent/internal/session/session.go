// Package session persists a lightweight conversation snapshot. Only final
// user/assistant messages are stored; intermediate ReAct tool messages are
// deliberately not replayed across turns.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

// Version is the on-disk session format version.
const Version = 1

// Message is one persisted conversation turn.
type Message struct {
	Role    string    `json:"role"`
	Content string    `json:"content"`
	At      time.Time `json:"at"`
}

// Session is a conversation snapshot.
type Session struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Workspace string    `json:"workspace"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Summary   string    `json:"summary,omitempty"`
	Messages  []Message `json:"messages"`

	path string
}

// New creates a new in-memory session and ensures the session directory exists.
func New(dir, workspace, provider, modelName string) (*Session, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &Session{
		Version:   Version,
		ID:        id,
		Workspace: workspace,
		Provider:  provider,
		Model:     modelName,
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  make([]Message, 0, 8),
		path:      filepath.Join(dir, id+".json"),
	}, nil
}

// Load reads a session by id (resolved under dir) or by explicit path.
func Load(dir, idOrPath string) (*Session, error) {
	path := idOrPath
	if !strings.ContainsAny(idOrPath, `/\`) && !strings.HasSuffix(idOrPath, ".json") {
		path = filepath.Join(dir, idOrPath+".json")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve session path: %w", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read session %s: %w", abs, err)
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse session %s: %w", abs, err)
	}
	if s.Version != Version {
		return nil, fmt.Errorf("session %s has version %d; this binary supports %d", abs, s.Version, Version)
	}
	if s.ID == "" {
		s.ID = strings.TrimSuffix(filepath.Base(abs), ".json")
	}
	for i, m := range s.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			return nil, fmt.Errorf("session %s message %d has unsupported role %q", abs, i, m.Role)
		}
	}
	s.path = abs
	return &s, nil
}

// Path returns the snapshot path.
func (s *Session) Path() string { return s.path }

// Add appends a user or assistant message.
func (s *Session) Add(role, content string) error {
	if role != "user" && role != "assistant" {
		return fmt.Errorf("unsupported role %q", role)
	}
	s.Messages = append(s.Messages, Message{Role: role, Content: content, At: time.Now()})
	s.UpdatedAt = time.Now()
	return nil
}

// Clear removes all messages and the summary from memory. The session file is
// not deleted.
func (s *Session) Clear() {
	s.Messages = s.Messages[:0]
	s.Summary = ""
	s.UpdatedAt = time.Now()
}

// Compact stores a structured summary and keeps at most keepRecent messages.
// The retained slice is adjusted so it starts with a user message.
func (s *Session) Compact(summary string, keepRecent int) {
	s.Summary = summary
	if keepRecent < 0 {
		keepRecent = 0
	}
	if keepRecent > len(s.Messages) {
		keepRecent = len(s.Messages)
	}
	s.Messages = append([]Message(nil), s.Messages[len(s.Messages)-keepRecent:]...)
	for len(s.Messages) > 0 && s.Messages[0].Role != "user" {
		s.Messages = s.Messages[1:]
	}
	s.UpdatedAt = time.Now()
}

// Save atomically writes the session as indented JSON with 0600 permissions.
func (s *Session) Save() (string, error) {
	if s.path == "" {
		return "", fmt.Errorf("session has no path")
	}
	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode session: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create session directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return "", fmt.Errorf("create session temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("chmod session temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write session: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close session: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return "", fmt.Errorf("rename session: %w", err)
	}
	return s.path, nil
}

// Trim drops the oldest messages until the approximate byte budget is met. It
// always keeps at least one message, and ensures the surviving history starts
// with a user message.
func (s *Session) Trim(maxBytes int) int {
	if maxBytes <= 0 {
		return 0
	}
	dropped := 0
	for len(s.Messages) > 1 && s.bytes() > maxBytes {
		s.Messages = s.Messages[1:]
		dropped++
	}
	for len(s.Messages) > 0 && s.Messages[0].Role != "user" {
		s.Messages = s.Messages[1:]
		dropped++
	}
	return dropped
}

// Bytes returns the approximate serialized size of the message list.
func (s *Session) Bytes() int { return s.bytes() }

func (s *Session) bytes() int {
	total := len(s.Summary)
	for _, m := range s.Messages {
		total += len(m.Role) + len(m.Content) + 32
	}
	return total
}

// SchemaMessages converts the session into Eino messages. The returned slice is
// a copy; callers may append the current user message.
func (s *Session) SchemaMessages() []*schema.Message {
	out := make([]*schema.Message, 0, len(s.Messages)+1)
	for _, m := range s.Messages {
		switch m.Role {
		case "user":
			out = append(out, schema.UserMessage(m.Content))
		case "assistant":
			out = append(out, schema.AssistantMessage(m.Content, nil))
		}
	}
	return out
}

// SchemaMessagesWithSummary injects the persisted summary before the retained
// recent messages. The assistant acknowledgement keeps the role sequence
// friendly to providers that dislike consecutive user messages.
func (s *Session) SchemaMessagesWithSummary() []*schema.Message {
	out := make([]*schema.Message, 0, len(s.Messages)+3)
	if s.Summary != "" {
		out = append(out, schema.UserMessage("[Context summary from earlier conversation; treat it as background]\n\n"+s.Summary))
		out = append(out, schema.AssistantMessage("Understood. I will use the summary as background and continue from the retained messages.", nil))
	}
	return append(out, s.SchemaMessages()...)
}

func newID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:]), nil
}
