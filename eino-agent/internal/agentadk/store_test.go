package agentadk

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(filepath.Join(dir, "ckpt"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, found, err := store.Get(ctx, "missing"); err != nil || found {
		t.Fatalf("missing: found=%v err=%v", found, err)
	}
	if err := store.Set(ctx, "run/1", []byte("state")); err != nil {
		t.Fatal(err)
	}
	data, found, err := store.Get(ctx, "run/1")
	if err != nil || !found || string(data) != "state" {
		t.Fatalf("get: %q found=%v err=%v", data, found, err)
	}
	// IDs are hashed: the slash must not create a nested path.
	if _, err := os.Stat(filepath.Join(dir, "ckpt", "run")); !os.IsNotExist(err) {
		t.Fatalf("checkpoint id created a subdirectory: %v", err)
	}
	if err := store.Delete(ctx, "run/1"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Get(ctx, "run/1"); err != nil || found {
		t.Fatalf("after delete: found=%v err=%v", found, err)
	}
}
