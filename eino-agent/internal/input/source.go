// Package input provides a single-owner line source for the REPL and the
// approval gate. Both must read from the same channel; otherwise two readers
// race for the next line typed on the terminal.
package input

import (
	"bufio"
	"context"
	"io"
	"sync"
)

// Result is one line or a terminal read error.
type Result struct {
	Line string
	Err  error
}

// LineReader is the narrow interface the approval gate needs.
type LineReader interface {
	ReadLine(ctx context.Context) (string, error)
}

// Source reads lines from r in one goroutine and hands them to callers one at
// a time. It is safe for concurrent callers, but the REPL and approval gate
// deliberately consume it sequentially.
type Source struct {
	ch        chan Result
	done      chan struct{}
	closeOnce sync.Once
}

// New starts a scanner goroutine over r. The goroutine exits when r reaches
// EOF, when it hits a read error, or when Close is called.
func New(r io.Reader) *Source {
	s := &Source{
		ch:   make(chan Result),
		done: make(chan struct{}),
	}
	go s.scan(r)
	return s
}

func (s *Source) scan(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	defer close(s.ch)
	for scanner.Scan() {
		select {
		case s.ch <- Result{Line: scanner.Text()}:
		case <-s.done:
			return
		}
	}
	if err := scanner.Err(); err != nil {
		select {
		case s.ch <- Result{Err: err}:
		case <-s.done:
		}
	}
}

// ReadLine returns the next line, ctx.Err() when the context is canceled, or
// io.EOF at end of input.
func (s *Source) ReadLine(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res, ok := <-s.ch:
		if !ok {
			return "", io.EOF
		}
		if res.Err != nil {
			return "", res.Err
		}
		return res.Line, nil
	}
}

// Close releases the scanner goroutine. It does not close the underlying
// reader (stdin must stay open for the process lifetime).
func (s *Source) Close() error {
	s.closeOnce.Do(func() { close(s.done) })
	return nil
}
