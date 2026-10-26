package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/memory"
)

type recallInput struct {
	Query string `json:"query" jsonschema:"required" jsonschema_description:"Keywords to search in the working-memory plan and recent saved sessions."`
	Limit int    `json:"limit,omitempty" jsonschema_description:"Maximum hits. Default 5, max 20."`
}

func (o *Options) recall(_ context.Context, in recallInput) (string, error) {
	if strings.TrimSpace(in.Query) == "" {
		return "", errors.New("query is empty")
	}
	hits, err := memory.Search(o.Workspace, o.SessionDir, in.Query, in.Limit)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "no memory hits for " + fmt.Sprintf("%q", in.Query), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "memory hits for %q:", in.Query)
	for _, h := range hits {
		b.WriteString("\n\n")
		switch h.Source {
		case "session":
			when := ""
			if !h.When.IsZero() {
				when = " " + h.When.Format("2006-01-02 15:04")
			}
			fmt.Fprintf(&b, "[session %s%s %s] %s", h.SessionID, when, h.Role, h.Snippet)
		default:
			fmt.Fprintf(&b, "[%s %s] %s", h.Source, h.Role, h.Snippet)
		}
	}
	return b.String(), nil
}
