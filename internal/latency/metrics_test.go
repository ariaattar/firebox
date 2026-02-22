package latency

import (
	"testing"
	"time"
)

func TestRecorderSnapshot(t *testing.T) {
	r := NewRecorder()
	r.Add("run", 10*time.Millisecond)
	r.Add("run", 20*time.Millisecond)
	r.Add("run", 30*time.Millisecond)
	r.Add("run", 40*time.Millisecond)
	r.Add("run", 50*time.Millisecond)

	got := r.Snapshot()["run"]
	if got.Count != 5 {
		t.Fatalf("Count = %d, want 5", got.Count)
	}
	if got.P50Ms < 20 || got.P50Ms > 30 {
		t.Fatalf("P50Ms = %.2f, want ~20-30", got.P50Ms)
	}
	if got.P95Ms < 40 {
		t.Fatalf("P95Ms = %.2f, want >= 40", got.P95Ms)
	}
	if got.MaxMs != 50 {
		t.Fatalf("MaxMs = %.2f, want 50", got.MaxMs)
	}
}
