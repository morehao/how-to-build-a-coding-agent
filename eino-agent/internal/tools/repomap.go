package tools

import (
	"context"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/repomap"
)

type repoMapInput struct {
	Path           string `json:"path,omitempty" jsonschema_description:"Directory to map, relative to the workspace root. Default current directory."`
	MaxFiles       int    `json:"max_files,omitempty" jsonschema_description:"Maximum files to list. Default 200, max 2000."`
	MaxDepth       int    `json:"max_depth,omitempty" jsonschema_description:"Maximum directory depth. Default 6, max 12."`
	DisableSymbols bool   `json:"disable_symbols,omitempty" jsonschema_description:"Omit Go symbol extraction to save tokens."`
}

func (o *Options) repoMap(_ context.Context, in repoMapInput) (string, error) {
	start, err := o.Workspace.Resolve(in.Path)
	if err != nil {
		return "", err
	}
	return repomap.Build(o.Workspace, start, repomap.Options{
		MaxFiles:       in.MaxFiles,
		MaxDepth:       in.MaxDepth,
		IncludeSymbols: !in.DisableSymbols,
	})
}
