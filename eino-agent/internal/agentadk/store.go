package agentadk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// FileStore is a small file-backed adk.CheckPointStore. Checkpoint IDs are
// hashed so an ID cannot escape the directory.
type FileStore struct {
	dir string
}

// NewFileStore creates the directory if needed.
func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("agentadk: checkpoint directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create checkpoint dir: %w", err)
	}
	return &FileStore{dir: dir}, nil
}

func (s *FileStore) path(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json")
}

// Get implements adk.CheckPointStore.
func (s *FileStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	data, err := os.ReadFile(s.path(id))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read checkpoint: %w", err)
	}
	return data, true, nil
}

// Set implements adk.CheckPointStore.
func (s *FileStore) Set(_ context.Context, id string, data []byte) error {
	tmp, err := os.CreateTemp(s.dir, ".ckpt-*")
	if err != nil {
		return fmt.Errorf("create checkpoint temp file: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write checkpoint: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, s.path(id)); err != nil {
		return fmt.Errorf("rename checkpoint: %w", err)
	}
	return nil
}

// Delete implements adk.CheckPointDeleter.
func (s *FileStore) Delete(_ context.Context, id string) error {
	err := os.Remove(s.path(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
