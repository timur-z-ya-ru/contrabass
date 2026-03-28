package loadmon

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompute_ScaleDown(t *testing.T) {
	cfg := DefaultConfig(5)
	m := New(cfg)
	m.current = 5

	// High CPU → scale down by 1
	result := m.compute(Snapshot{CPULoad: 0.9, MemUsed: 0.5})
	assert.Equal(t, 4, result)
}

func TestCompute_ScaleDownMem(t *testing.T) {
	cfg := DefaultConfig(5)
	m := New(cfg)
	m.current = 5

	// High memory → scale down by 1
	result := m.compute(Snapshot{CPULoad: 0.3, MemUsed: 0.9})
	assert.Equal(t, 4, result)
}

func TestCompute_ScaleUp(t *testing.T) {
	cfg := DefaultConfig(5)
	m := New(cfg)
	m.current = 2

	// Low CPU and low memory → scale up by 1
	result := m.compute(Snapshot{CPULoad: 0.3, MemUsed: 0.4})
	assert.Equal(t, 3, result)
}

func TestCompute_AtCeiling(t *testing.T) {
	cfg := DefaultConfig(5)
	m := New(cfg)
	m.current = 5

	// Low load but already at ceiling → stay
	result := m.compute(Snapshot{CPULoad: 0.2, MemUsed: 0.3})
	assert.Equal(t, 5, result)
}

func TestCompute_AtFloor(t *testing.T) {
	cfg := DefaultConfig(5)
	m := New(cfg)
	m.current = 1

	// High load but already at floor → stay at 1
	result := m.compute(Snapshot{CPULoad: 0.95, MemUsed: 0.95})
	assert.Equal(t, 1, result)
}

func TestCompute_MidRange_NoChange(t *testing.T) {
	cfg := DefaultConfig(5)
	m := New(cfg)
	m.current = 3

	// Between thresholds → no change
	result := m.compute(Snapshot{CPULoad: 0.6, MemUsed: 0.7})
	assert.Equal(t, 3, result)
}

func TestCompute_GradualScaleDown(t *testing.T) {
	cfg := DefaultConfig(5)
	m := New(cfg)
	m.current = 5

	// Simulate sustained high load → scales down 1 per cycle
	for i := 5; i > 1; i-- {
		result := m.compute(Snapshot{CPULoad: 0.9, MemUsed: 0.9})
		assert.Equal(t, i-1, result)
		m.current = result
	}
	// At floor, stays at 1
	result := m.compute(Snapshot{CPULoad: 0.9, MemUsed: 0.9})
	assert.Equal(t, 1, result)
}

func TestCompute_GradualScaleUp(t *testing.T) {
	cfg := DefaultConfig(5)
	m := New(cfg)
	m.current = 1

	// Simulate sustained low load → scales up 1 per cycle
	for i := 1; i < 5; i++ {
		result := m.compute(Snapshot{CPULoad: 0.2, MemUsed: 0.3})
		assert.Equal(t, i+1, result)
		m.current = result
	}
	// At ceiling, stays at 5
	result := m.compute(Snapshot{CPULoad: 0.2, MemUsed: 0.3})
	assert.Equal(t, 5, result)
}

func TestMonitor_StartStop(t *testing.T) {
	cfg := DefaultConfig(3)
	cfg.PollInterval = 10 * time.Millisecond
	m := New(cfg)

	// Inject a mock load reader
	m.readLoad = func() Snapshot {
		return Snapshot{CPULoad: 0.3, MemUsed: 0.4, Timestamp: time.Now()}
	}

	m.Start()
	time.Sleep(50 * time.Millisecond)

	snap := m.Load()
	require.False(t, snap.Timestamp.IsZero())
	assert.Equal(t, 3, m.Concurrency()) // low load, at ceiling

	m.Stop()
}

func TestMonitor_AdaptiveUnderLoad(t *testing.T) {
	cfg := DefaultConfig(4)
	cfg.PollInterval = 10 * time.Millisecond
	m := New(cfg)

	// Start with high load
	m.readLoad = func() Snapshot {
		return Snapshot{CPULoad: 0.95, MemUsed: 0.5, Timestamp: time.Now()}
	}

	m.Start()
	// Wait for several cycles to scale down
	time.Sleep(100 * time.Millisecond)

	conc := m.Concurrency()
	assert.Less(t, conc, 4, "should have scaled down from ceiling")

	m.Stop()
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig(0)
	assert.Equal(t, 2, cfg.Ceiling, "zero ceiling should default to 2")
	assert.Equal(t, 1, cfg.Floor)
	assert.Equal(t, defaultHighCPU, cfg.HighCPU)
	assert.Equal(t, defaultLowCPU, cfg.LowCPU)
}
