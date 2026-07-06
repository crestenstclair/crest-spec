package mcp

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

type toolMetric struct {
	Calls   atomic.Int64
	Errors  atomic.Int64
	TotalNs atomic.Int64
	MinNs   atomic.Int64
	MaxNs   atomic.Int64
}

type ToolMetricSnapshot struct {
	Calls  int64   `json:"calls"`
	Errors int64   `json:"errors"`
	AvgMs  float64 `json:"avg_ms"`
	MinMs  float64 `json:"min_ms"`
	MaxMs  float64 `json:"max_ms"`
}

type MetricsSnapshot struct {
	UptimeSeconds float64                       `json:"uptime_seconds"`
	TotalCalls    int64                         `json:"total_calls"`
	TotalErrors   int64                         `json:"total_errors"`
	Tools         map[string]ToolMetricSnapshot `json:"tools"`
}

type Metrics struct {
	mu        sync.RWMutex
	tools     map[string]*toolMetric
	startTime time.Time
}

func NewMetrics() *Metrics {
	return &Metrics{
		tools:     make(map[string]*toolMetric),
		startTime: time.Now(),
	}
}

func (m *Metrics) getOrCreate(tool string) *toolMetric {
	m.mu.RLock()
	tm, ok := m.tools[tool]
	m.mu.RUnlock()
	if ok {
		return tm
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if tm, ok = m.tools[tool]; ok {
		return tm
	}

	tm = &toolMetric{}
	tm.MinNs.Store(math.MaxInt64)
	tm.MaxNs.Store(0)
	m.tools[tool] = tm
	return tm
}

func (m *Metrics) Record(tool string, elapsed time.Duration, err error) {
	tm := m.getOrCreate(tool)
	ns := elapsed.Nanoseconds()

	tm.Calls.Add(1)
	if err != nil {
		tm.Errors.Add(1)
	}
	tm.TotalNs.Add(ns)

	for {
		old := tm.MinNs.Load()
		if ns >= old || tm.MinNs.CompareAndSwap(old, ns) {
			break
		}
	}

	for {
		old := tm.MaxNs.Load()
		if ns <= old || tm.MaxNs.CompareAndSwap(old, ns) {
			break
		}
	}
}

func (m *Metrics) Snapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap := MetricsSnapshot{
		UptimeSeconds: time.Since(m.startTime).Seconds(),
		Tools:         make(map[string]ToolMetricSnapshot, len(m.tools)),
	}

	for name, tm := range m.tools {
		calls := tm.Calls.Load()
		errs := tm.Errors.Load()
		totalNs := tm.TotalNs.Load()
		minNs := tm.MinNs.Load()
		maxNs := tm.MaxNs.Load()

		snap.TotalCalls += calls
		snap.TotalErrors += errs

		ts := ToolMetricSnapshot{
			Calls:  calls,
			Errors: errs,
		}

		if calls > 0 {
			ts.AvgMs = float64(totalNs) / float64(calls) / 1e6
			ts.MinMs = float64(minNs) / 1e6
			ts.MaxMs = float64(maxNs) / 1e6
		}

		snap.Tools[name] = ts
	}

	return snap
}
