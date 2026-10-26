// Package filestate remembers which files the model has read and whether they
// changed since. edit_file and apply_patch use it to enforce read-before-edit
// and to catch external modifications between read and write.
package filestate

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Stamp is the file identity captured at read time.
type Stamp struct {
	Size    int64
	ModTime time.Time
	Hash    [32]byte
}

// State is safe for concurrent use. A nil *State is a no-op (enforcement off).
type State struct {
	mu    sync.Mutex
	files map[string]Stamp
}

// New creates an empty state.
func New() *State {
	return &State{files: make(map[string]Stamp)}
}

// MarkRead records the current identity of path. It should be called after a
// successful read. The hash covers the whole file, so an edit after a partial
// read still catches external changes.
func (s *State) MarkRead(path string) error {
	if s == nil {
		return nil
	}
	stamp, err := stampFor(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.files[path] = stamp
	s.mu.Unlock()
	return nil
}

// Check verifies that path was read and has not changed since.
func (s *State) Check(path string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	stamp, ok := s.files[path]
	s.mu.Unlock()
	if !ok {
		return errors.New("read the file before editing it")
	}
	current, err := stampFor(path)
	if err != nil {
		return err
	}
	if current.Size != stamp.Size || !current.ModTime.Equal(stamp.ModTime) || current.Hash != stamp.Hash {
		return errors.New("the file changed since it was last read; read it again before editing")
	}
	return nil
}

// Forget drops a path, e.g. before an edit writes a new identity.
func (s *State) Forget(path string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.files, path)
	s.mu.Unlock()
}

func stampFor(path string) (Stamp, error) {
	f, err := os.Open(path)
	if err != nil {
		return Stamp{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Stamp{}, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return Stamp{}, err
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return Stamp{Size: info.Size(), ModTime: info.ModTime(), Hash: sum}, nil
}
