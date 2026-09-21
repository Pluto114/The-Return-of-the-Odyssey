package main

import "sync"

// deferredEventSink 在首次匹配和重赛时，把首个权威 StageStarted 及后续可靠事件排在
// MatchFound 之后。Send 由房间唯一分发器调用，Release 可并发执行。
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
		// 新房间启动期间不能静默丢弃可靠事件。
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
			break // closingSink 负责关闭饱和或已失效连接
		}
	}
	s.pending = nil
	s.released = true
}
