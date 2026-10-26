package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

type editFileInput struct {
	Path   string `json:"path" jsonschema:"required" jsonschema_description:"Path to the file, relative to the workspace root."`
	OldStr string `json:"old_str" jsonschema_description:"Exact text to replace. Must occur exactly once. Leave empty only when creating a new file."`
	NewStr string `json:"new_str" jsonschema_description:"Replacement text, or the full content when creating a new file."`
}

func (o *Options) editFile(_ context.Context, in editFileInput) (string, error) {
	if o.Mode == config.PermissionReadOnly {
		return "", errors.New("edit_file is disabled in read-only mode")
	}
	if in.OldStr != "" && in.OldStr == in.NewStr {
		return "", errors.New("old_str and new_str are identical; nothing to change")
	}
	path, err := o.Workspace.Resolve(in.Path)
	if err != nil {
		return "", err
	}
	rel := o.Workspace.Rel(path)

	info, statErr := os.Stat(path)
	switch {
	case statErr == nil && info.IsDir():
		return "", fmt.Errorf("%s is a directory", rel)
	case statErr == nil:
		if in.OldStr == "" {
			return "", fmt.Errorf("%s already exists; provide old_str to replace text (empty old_str only creates new files)", rel)
		}
		if info.Size() > 10*1024*1024 {
			return "", fmt.Errorf("%s is %s; refusing to edit files larger than 10 MiB", rel, humanBytes(info.Size()))
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", rel, err)
		}
		count := strings.Count(string(content), in.OldStr)
		if count == 0 {
			return "", fmt.Errorf("old_str not found in %s; read the file again and copy an exact fragment", rel)
		}
		if count > 1 {
			return "", fmt.Errorf("old_str occurs %d times in %s; include more surrounding context to make it unique", count, rel)
		}
		updated := strings.Replace(string(content), in.OldStr, in.NewStr, 1)
		if err := workspace.WriteFileAtomic(path, []byte(updated), info.Mode()); err != nil {
			return "", fmt.Errorf("write %s: %w", rel, err)
		}
		return fmt.Sprintf("OK: edited %s (%d -> %d bytes)", rel, len(content), len(updated)), nil

	case errors.Is(statErr, fs.ErrNotExist):
		if in.OldStr != "" {
			return "", fmt.Errorf("%s does not exist; set old_str to \"\" to create it", rel)
		}
		if err := workspace.WriteFileAtomic(path, []byte(in.NewStr), 0o644); err != nil {
			return "", fmt.Errorf("create %s: %w", rel, err)
		}
		return fmt.Sprintf("OK: created %s (%d bytes)", rel, len(in.NewStr)), nil

	default:
		return "", fmt.Errorf("stat %s: %w", rel, statErr)
	}
}
