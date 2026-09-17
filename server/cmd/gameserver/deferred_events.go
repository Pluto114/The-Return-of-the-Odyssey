package main

import "sync"

// deferredEventSink keeps the first authoritative StageStarted (and any
// following reliable events) ordered behind MatchFound during initial matching
// and team rematching.
// Send is called by the room's sole dispatcher; Release may run concurrently.
type deferredEventSink struct {
	mu         sync.Mutex
	downstream closingSink
	pending    [][]byte
	released   bool
}

const maxDeferredEvents = 128

func newDeferredEventSink(downstream closingSink) *deferredEventSink {
	return &deferredEventSink{downstream: downstream}
}

func (s *deferredEventSink) Send(frame []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return s.downstream.Send(frame)
	}
	if len(s.pending) == maxDeferredEvents {
		// Reliable events cannot be silently dropped while the new room starts.
		go s.downstream.connection.Close()
		return false
	}
	s.pending = append(s.pending, append([]byte(nil), frame...))
	return true
}

func (s *deferredEventSink) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, frame := range s.pending {
		if !s.downstream.Send(frame) {
			break // closingSink closes a saturated/closed connection
		}
	}
	s.pending = nil
	s.released = true
}
