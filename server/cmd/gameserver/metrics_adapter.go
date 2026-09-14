package main

import (
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/metrics"
)

// metricsObserver bridges the transport-level network.Observer callbacks into
// the Prometheus metrics registry. It exists so the network package stays free
// of a metrics dependency: network emits bounded events, this adapter records
// them.
//
// Reliable-queue depth is tracked per-connection by the network layer and
// reported as deltas of the *current* connection's queue; because only the
// writer drains it, the reported value is a point-in-time gauge sample rather
// than a true global sum. It is sufficient for observability of backpressure
// build-up and recovery (D9).
type metricsObserver struct {
	m *metrics.Metrics
}

func newMetricsObserver(m *metrics.Metrics) *metricsObserver {
	return &metricsObserver{m: m}
}

func (o *metricsObserver) OnConnectionAccepted() { o.m.ObserveConnectionAccepted() }
func (o *metricsObserver) OnConnectionClosed()   { o.m.ObserveConnectionClosed() }
func (o *metricsObserver) OnBytesReceived(n int) { o.m.ObserveBytesReceived(n) }
func (o *metricsObserver) OnBytesSent(n int)     { o.m.ObserveBytesSent(n) }
func (o *metricsObserver) OnFrameReceived()      { o.m.ObserveFrameReceived() }
func (o *metricsObserver) OnFrameSent()          { o.m.ObserveFrameSent() }
func (o *metricsObserver) OnSnapshotSent(b int)  { o.m.ObserveSnapshotSent(b) }
func (o *metricsObserver) OnReliableRejection()  { o.m.ObserveReliableRejection() }
func (o *metricsObserver) OnSnapshotDrop()       { o.m.ObserveSnapshotDrop() }
func (o *metricsObserver) OnReliableDepth(d int) { o.m.SetReliableQueueDepth(d) }

func (o *metricsObserver) OnInvalidFrame(reason string) {
	_ = o.m.ObserveInvalidFrame(metrics.FrameResult(reason))
}
