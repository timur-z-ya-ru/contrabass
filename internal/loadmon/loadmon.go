// Package loadmon provides system load monitoring and adaptive concurrency control.
package loadmon

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Thresholds for scaling decisions.
const (
	defaultHighCPU  = 0.80 // scale down above this
	defaultLowCPU   = 0.50 // scale up below this
	defaultHighMem  = 0.85
	defaultLowMem   = 0.60
	defaultInterval = 5 * time.Second
)

// Config configures the adaptive load monitor.
type Config struct {
	Ceiling     int           // max_concurrency from workflow config (absolute max)
	Floor       int           // minimum concurrency (default 1)
	HighCPU     float64       // CPU threshold to scale down (0-1)
	LowCPU      float64       // CPU threshold to scale up (0-1)
	HighMem     float64       // memory threshold to scale down (0-1)
	LowMem      float64       // memory threshold to scale up (0-1)
	PollInterval time.Duration // how often to sample system load
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig(ceiling int) Config {
	if ceiling <= 0 {
		ceiling = 2
	}
	return Config{
		Ceiling:      ceiling,
		Floor:        1,
		HighCPU:      defaultHighCPU,
		LowCPU:       defaultLowCPU,
		HighMem:      defaultHighMem,
		LowMem:       defaultLowMem,
		PollInterval: defaultInterval,
	}
}

// Snapshot holds a point-in-time system load reading.
type Snapshot struct {
	CPULoad    float64 // 1-minute load average / num CPUs (0-1+ range)
	MemUsed    float64 // fraction of total memory in use (0-1)
	Timestamp  time.Time
}

// Monitor tracks system load and computes adaptive concurrency.
type Monitor struct {
	cfg      Config
	mu       sync.RWMutex
	current  int      // current adaptive concurrency
	snapshot Snapshot // latest reading
	stopCh   chan struct{}
	stopped  bool

	// For testing: injectable load reader
	readLoad func() Snapshot
}

// New creates a Monitor. Call Start() to begin background polling.
func New(cfg Config) *Monitor {
	return &Monitor{
		cfg:      cfg,
		current:  cfg.Ceiling,
		stopCh:   make(chan struct{}),
		readLoad: readSystemLoad,
	}
}

// Start begins background polling of system load.
func (m *Monitor) Start() {
	go m.loop()
}

// Stop terminates the background polling goroutine.
func (m *Monitor) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.stopped {
		m.stopped = true
		close(m.stopCh)
	}
}

// Concurrency returns the current adaptive concurrency limit.
func (m *Monitor) Concurrency() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// Load returns the latest system load snapshot.
func (m *Monitor) Load() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

func (m *Monitor) loop() {
	interval := m.cfg.PollInterval
	if interval <= 0 {
		interval = defaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial reading.
	m.update()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.update()
		}
	}
}

func (m *Monitor) update() {
	snap := m.readLoad()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.snapshot = snap
	m.current = m.compute(snap)
}

// compute determines the target concurrency based on load.
// Bidirectional: scales down under high load, scales up when resources are free.
func (m *Monitor) compute(snap Snapshot) int {
	cur := m.current
	ceil := m.cfg.Ceiling
	floor := m.cfg.Floor
	if floor <= 0 {
		floor = 1
	}

	highLoad := snap.CPULoad > m.cfg.HighCPU || snap.MemUsed > m.cfg.HighMem
	lowLoad := snap.CPULoad < m.cfg.LowCPU && snap.MemUsed < m.cfg.LowMem

	switch {
	case highLoad && cur > floor:
		// Scale down by 1 per cycle to avoid oscillation.
		cur--
	case lowLoad && cur < ceil:
		// Scale up by 1 per cycle.
		cur++
	}

	if cur < floor {
		cur = floor
	}
	if cur > ceil {
		cur = ceil
	}
	return cur
}

// readSystemLoad reads /proc/loadavg and memory stats.
func readSystemLoad() Snapshot {
	snap := Snapshot{Timestamp: time.Now()}
	snap.CPULoad = readCPULoad()
	snap.MemUsed = readMemUsage()
	return snap
}

// readCPULoad reads 1-minute load average from /proc/loadavg,
// normalized by number of CPUs.
func readCPULoad() float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0
	}
	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	cpus := runtime.NumCPU()
	if cpus <= 0 {
		cpus = 1
	}
	return load1 / float64(cpus)
}

// readMemUsage reads memory stats from /proc/meminfo.
// Returns fraction of memory in use (1 - available/total).
func readMemUsage() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}

	var total, available uint64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total = val
		case "MemAvailable:":
			available = val
		}
	}

	if total == 0 {
		return 0
	}
	return 1.0 - float64(available)/float64(total)
}
