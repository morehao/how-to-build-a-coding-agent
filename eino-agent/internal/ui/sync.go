package ui

import (
	"io"
	"sync"
)

// SyncWriter serializes writes to an io.Writer. UI events and approval prompts
// can originate from different goroutines (especially under ADK), so terminal
// writes must not interleave or race.
type SyncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewSyncWriter wraps w. A nil writer becomes io.Discard.
func NewSyncWriter(w io.Writer) *SyncWriter {
	if w == nil {
		w = io.Discard
	}
	return &SyncWriter{w: w}
}

// Write implements io.Writer.
func (s *SyncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
