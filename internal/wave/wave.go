package wave

// Phase represents a major milestone containing multiple waves.
type Phase struct {
	Name      string
	Milestone string
	Epic      string
	Waves     []Wave
}

// Pipeline is the complete state of the wave pipeline.
type Pipeline struct {
	Phases      []Phase
	ActivePhase int
	ActiveWave  int
	DAG         *DAG
	Config      *WaveConfig
}
