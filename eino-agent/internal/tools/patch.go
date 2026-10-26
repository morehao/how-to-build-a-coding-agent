package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/approval"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/config"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/patch"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/policy"
	"github.com/morehao/how-to-build-a-coding-agent/eino-agent/internal/workspace"
)

type applyPatchInput struct {
	Patch string `json:"patch" jsonschema:"required" jsonschema_description:"Unified diff with ---/+++ file headers and @@ hunks. Multiple files are allowed."`
}

type patchChange struct {
	abs    string
	rel    string
	old    string
	next   string
	exists bool
	delete bool
	mode   fs.FileMode
}

func (o *Options) applyPatch(ctx context.Context, in applyPatchInput) (string, error) {
	if o.Mode == config.PermissionReadOnly {
		return "", errors.New("apply_patch is disabled in read-only mode")
	}
	files, err := patch.Parse(in.Patch)
	if err != nil {
		return "", fmt.Errorf("parse patch: %w", err)
	}

	seen := map[string]struct{}{}
	var changes []patchChange
	for _, fp := range files {
		name := fp.Path()
		if name == "" || name == "/dev/null" {
			return "", errors.New("patch is missing a target path")
		}
		abs, err := o.Workspace.Resolve(name)
		if err != nil {
			return "", err
		}
		rel := o.Workspace.Rel(abs)
		if !o.AllowSensitive && policy.IsSensitive(rel) {
			return "", fmt.Errorf("refusing to patch sensitive file %s; pass --allow-sensitive to override", rel)
		}
		if _, ok := seen[rel]; ok {
			return "", fmt.Errorf("patch contains %s more than once", rel)
		}
		seen[rel] = struct{}{}

		info, statErr := os.Stat(abs)
		exists := statErr == nil
		switch {
		case statErr == nil && info.IsDir():
			return "", fmt.Errorf("%s is a directory", rel)
		case statErr != nil && !errors.Is(statErr, fs.ErrNotExist):
			return "", fmt.Errorf("stat %s: %w", rel, statErr)
		}
		if fp.NewFile && exists {
			return "", fmt.Errorf("%s already exists; use edit_file or a non-new file patch", rel)
		}
		if !fp.NewFile && !exists && !fp.DeleteFile {
			return "", fmt.Errorf("%s does not exist", rel)
		}
		if exists && o.Files != nil && o.EnforceReadBeforeEdit {
			if err := o.Files.Check(abs); err != nil {
				return "", fmt.Errorf("%s: %w", rel, err)
			}
		}

		old := ""
		mode := fs.FileMode(0o644)
		if exists {
			if info.Size() > 10*1024*1024 {
				return "", fmt.Errorf("%s is %s; refusing to patch files larger than 10 MiB", rel, humanBytes(info.Size()))
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return "", fmt.Errorf("read %s: %w", rel, err)
			}
			old = string(data)
			mode = info.Mode()
		}
		next, err := patch.Apply(old, fp)
		if err != nil {
			return "", err
		}
		if bytes.IndexByte([]byte(next), 0) >= 0 {
			return "", fmt.Errorf("%s: patch result appears to be binary; apply_patch only supports UTF-8 text", rel)
		}
		changes = append(changes, patchChange{abs: abs, rel: rel, next: next, delete: fp.DeleteFile, mode: mode})
	}

	added, removed := patchStats(files)
	paths := make([]string, 0, len(changes))
	rels := make([]string, 0, len(changes))
	for _, c := range changes {
		paths = append(paths, c.abs)
		rels = append(rels, c.rel)
	}
	if o.Approver == nil {
		return "", errors.New("apply_patch denied: no approver configured")
	}
	decision, err := o.Approver.Approve(ctx, approval.Request{
		Kind:    approval.KindPatch,
		Tool:    "apply_patch",
		Paths:   rels,
		Diff:    in.Patch,
		Summary: fmt.Sprintf("%d file(s), +%d/-%d lines", len(changes), added, removed),
	})
	if err != nil {
		return "", fmt.Errorf("approval failed: %w", err)
	}
	if decision == approval.Deny {
		return "", errors.New("apply_patch denied by user")
	}

	if o.Checkpoints != nil {
		if _, err := o.Checkpoints.Snapshot("apply_patch: "+fmt.Sprintf("%d files", len(changes)), paths); err != nil {
			return "", err
		}
	}
	for _, c := range changes {
		if c.delete {
			if err := os.Remove(c.abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("delete %s: %w", c.rel, err)
			}
			if o.Files != nil {
				o.Files.Forget(c.abs)
			}
			continue
		}
		if err := workspace.WriteFileAtomic(c.abs, []byte(c.next), c.mode); err != nil {
			return "", fmt.Errorf("write %s: %w", c.rel, err)
		}
		if o.Files != nil {
			_ = o.Files.MarkRead(c.abs)
		}
	}
	return fmt.Sprintf("OK: applied patch to %d file(s), +%d/-%d lines: %v", len(changes), added, removed, rels), nil
}

func patchStats(files []patch.FilePatch) (added, removed int) {
	for _, fp := range files {
		for _, h := range fp.Hunks {
			for _, l := range h.Lines {
				switch l.Kind {
				case '+':
					added++
				case '-':
					removed++
				}
			}
		}
	}
	return added, removed
}
