package tools

import (
	"context"
	"fmt"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/memory"
)

type updatePlanInput struct {
	Content string `json:"content" jsonschema:"required" jsonschema_description:"Full replacement for the working-memory plan. Keep it concise: goal, decisions, changed files, commands/results, open errors, next steps."`
}

func (o *Options) updatePlan(_ context.Context, in updatePlanInput) (string, error) {
	path, err := memory.Save(o.Workspace, in.Content)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("saved working memory to %s (%d bytes)", path, len(in.Content)), nil
}
