package latency

import (
	"sort"
	"sync"
	"time"
)

type Recorder struct {
	mu   sync.RWMutex
	data map[string][]float64
}

func NewRecorder() *Recorder {
	return &Recorder{data: make(map[string][]float64)}
}

func (r *Recorder) Add(op string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ms := float64(d.Microseconds()) / 1000.0
	r.data[op] = append(r.data[op], ms)
	if len(r.data[op]) > 5000 {
		r.data[op] = r.data[op][len(r.data[op])-5000:]
	}
}

type Stats struct {
	Count int
	P50Ms float64
	P95Ms float64
	P99Ms float64
	MaxMs float64
}

func (r *Recorder) Snapshot() map[string]Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make(map[string]Stats, len(r.data))
	for op, values := range r.data {
		out[op] = calc(values)
	}
	return out
}

func calc(values []float64) Stats {
	if len(values) == 0 {
		return Stats{}
	}
	cpy := make([]float64, len(values))
	copy(cpy, values)
	sort.Float64s(cpy)
	return Stats{
		Count: len(cpy),
		P50Ms: q(cpy, 0.50),
		P95Ms: q(cpy, 0.95),
		P99Ms: q(cpy, 0.99),
		MaxMs: cpy[len(cpy)-1],
	}
}

func q(sorted []float64, quantile float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if quantile <= 0 {
		return sorted[0]
	}
	if quantile >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(float64(len(sorted)-1) * quantile)
	return sorted[idx]
}
