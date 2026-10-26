package input

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestReadLineThenEOF(t *testing.T) {
	src := New(strings.NewReader("hello\n"))
	defer src.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	line, err := src.ReadLine(ctx)
	if err != nil || line != "hello" {
		t.Fatalf("line = %q, err = %v", line, err)
	}
	if _, err := src.ReadLine(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("second read err = %v, want io.EOF", err)
	}
}

func TestCloseUnblocksRead(t *testing.T) {
	src := New(strings.NewReader(""))
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := src.ReadLine(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}
