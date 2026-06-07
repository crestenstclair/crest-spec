package mcp

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetrics_Record_UpdatesCounters(t *testing.T) {
	m := NewMetrics()

	m.Record("run_prompt", 100*time.Millisecond, nil)
	m.Record("run_prompt", 200*time.Millisecond, nil)
	m.Record("run_prompt", 300*time.Millisecond, errors.New("fail"))

	snap := m.Snapshot()
	require.Contains(t, snap.Tools, "run_prompt")

	ts := snap.Tools["run_prompt"]
	assert.Equal(t, int64(3), ts.Calls)
	assert.Equal(t, int64(1), ts.Errors)
	assert.InDelta(t, 200.0, ts.AvgMs, 1.0)
	assert.InDelta(t, 100.0, ts.MinMs, 1.0)
	assert.InDelta(t, 300.0, ts.MaxMs, 1.0)
}

func TestMetrics_Snapshot_Totals(t *testing.T) {
	m := NewMetrics()

	m.Record("tool_a", 10*time.Millisecond, nil)
	m.Record("tool_b", 20*time.Millisecond, errors.New("err"))

	snap := m.Snapshot()
	assert.Equal(t, int64(2), snap.TotalCalls)
	assert.Equal(t, int64(1), snap.TotalErrors)
	assert.Greater(t, snap.UptimeSeconds, 0.0)
}

func TestMetrics_Snapshot_ZeroCalls(t *testing.T) {
	m := NewMetrics()

	snap := m.Snapshot()
	assert.Equal(t, int64(0), snap.TotalCalls)
	assert.Equal(t, int64(0), snap.TotalErrors)
	assert.Empty(t, snap.Tools)
}

func TestMetrics_ConcurrentRecord(t *testing.T) {
	m := NewMetrics()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%10 == 0 {
				err = errors.New("err")
			}
			m.Record("concurrent_tool", time.Duration(i)*time.Millisecond, err)
		}(i)
	}
	wg.Wait()

	snap := m.Snapshot()
	assert.Equal(t, int64(100), snap.Tools["concurrent_tool"].Calls)
	assert.Equal(t, int64(10), snap.Tools["concurrent_tool"].Errors)
}

func TestMetrics_MultipleTools(t *testing.T) {
	m := NewMetrics()

	m.Record("tool_a", 10*time.Millisecond, nil)
	m.Record("tool_b", 20*time.Millisecond, nil)
	m.Record("tool_c", 30*time.Millisecond, nil)

	snap := m.Snapshot()
	assert.Len(t, snap.Tools, 3)
	assert.Contains(t, snap.Tools, "tool_a")
	assert.Contains(t, snap.Tools, "tool_b")
	assert.Contains(t, snap.Tools, "tool_c")
}
