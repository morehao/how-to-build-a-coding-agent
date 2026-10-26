package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/approval"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/policy"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

type editFileInput struct {
	Path   string `json:"path" jsonschema:"required" jsonschema_description:"Path to the file, relative to the workspace root."`
	OldStr string `json:"old_str" jsonschema_description:"Exact text to replace. Must occur exactly once. Leave empty only when creating a new file."`
	NewStr string `json:"new_str" jsonschema_description:"Replacement text, or the full content when creating a new file."`
}

func (o *Options) editFile(ctx context.Context, in editFileInput) (string, error) {
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
	if !o.AllowSensitive && policy.IsSensitive(rel) {
		return "", fmt.Errorf("refusing to edit sensitive file %s; pass --allow-sensitive to override", rel)
	}

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
		if o.Files != nil && o.EnforceReadBeforeEdit {
			if err := o.Files.Check(path); err != nil {
				return "", fmt.Errorf("%s: %w", rel, err)
			}
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", rel, err)
		}
		if bytes.IndexByte(content, 0) >= 0 {
			return "", fmt.Errorf("%s appears to be binary; edit_file only supports UTF-8 text", rel)
		}
		count := strings.Count(string(content), in.OldStr)
		if count == 0 {
			return "", fmt.Errorf("old_str not found in %s; read the file again and copy an exact fragment", rel)
		}
		if count > 1 {
			return "", fmt.Errorf("old_str occurs %d times in %s; include more surrounding context to make it unique", count, rel)
		}
		updated := strings.Replace(string(content), in.OldStr, in.NewStr, 1)
		if err := approveEdit(ctx, o, rel, string(content), updated, in.OldStr, in.NewStr, false); err != nil {
			return "", err
		}
		if o.Checkpoints != nil {
			if _, err := o.Checkpoints.Snapshot("edit_file: "+rel, []string{path}); err != nil {
				return "", err
			}
		}
		if err := workspace.WriteFileAtomic(path, []byte(updated), info.Mode()); err != nil {
			return "", fmt.Errorf("write %s: %w", rel, err)
		}
		if o.Files != nil {
			_ = o.Files.MarkRead(path)
		}
		return fmt.Sprintf("OK: edited %s (%d -> %d bytes)", rel, len(content), len(updated)), nil

	case errors.Is(statErr, fs.ErrNotExist):
		if in.OldStr != "" {
			return "", fmt.Errorf("%s does not exist; set old_str to \"\" to create it", rel)
		}
		if err := approveEdit(ctx, o, rel, "", in.NewStr, "", in.NewStr, true); err != nil {
			return "", err
		}
		if o.Checkpoints != nil {
			if _, err := o.Checkpoints.Snapshot("edit_file create: "+rel, []string{path}); err != nil {
				return "", err
			}
		}
		if err := workspace.WriteFileAtomic(path, []byte(in.NewStr), 0o644); err != nil {
			return "", fmt.Errorf("create %s: %w", rel, err)
		}
		if o.Files != nil {
			_ = o.Files.MarkRead(path)
		}
		return fmt.Sprintf("OK: created %s (%d bytes)", rel, len(in.NewStr)), nil

	default:
		return "", fmt.Errorf("stat %s: %w", rel, statErr)
	}
}

func approveEdit(ctx context.Context, o *Options, rel, oldContent, newContent, oldStr, newStr string, create bool) error {
	if o.Approver == nil {
		return errors.New("edit denied: no approver configured")
	}
	summary := fmt.Sprintf("%d line(s) removed, %d line(s) added", len(splitDiffLines(oldStr)), len(splitDiffLines(newStr)))
	if create {
		summary = fmt.Sprintf("create file, %d line(s)", len(splitDiffLines(newStr)))
	}
	decision, err := o.Approver.Approve(ctx, approval.Request{
		Kind:    approval.KindEdit,
		Tool:    "edit_file",
		Path:    rel,
		Diff:    unifiedDiff(rel, oldContent, newContent, oldStr, newStr),
		Summary: summary,
	})
	if err != nil {
		return fmt.Errorf("approval failed: %w", err)
	}
	if decision == approval.Deny {
		return errors.New("edit denied by user")
	}
	return nil
}
