package filestate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadThenEditThenExternalChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New()
	if err := s.Check(path); err == nil {
		t.Fatal("expected unread error")
	}
	if err := s.MarkRead(path); err != nil {
		t.Fatal(err)
	}
	if err := s.Check(path); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Check(path); err == nil {
		t.Fatal("expected changed error")
	}
}
