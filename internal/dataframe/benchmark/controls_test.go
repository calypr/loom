package benchmark

import (
	"context"
	"strings"
	"testing"
	"time"
)

func timedSamples(backend, workload string, base float64, n int) []Sample {
	var out []Sample
	for i := 0; i < n; i++ {
		s := sample(backend, workload, OutcomePassed, base+float64(i%2)*0.2, false)
		s.Round = i / 2
		s.Iteration = i % 2
		out = append(out, s)
	}
	return out
}

func TestControlUnstableCells(t *testing.T) {
	stable := timedSamples(BackendPostgres, WorkloadFilterActiveGender, 10, 8)
	if got := ControlUnstableCells(stable, StabilityCVLimit); len(got) != 0 {
		t.Fatalf("tight samples must be stable, got %v", got)
	}
	// high-variance cell
	var wild []Sample
	for i := 0; i < 8; i++ {
		ms := 5.0
		if i%2 == 0 {
			ms = 500.0
		}
		s := sample(BackendArango, WorkloadGroupCountByPatient, OutcomePassed, ms, false)
		s.Round, s.Iteration = i/2, i%2
		wild = append(wild, s)
	}
	got := ControlUnstableCells(append(append([]Sample{}, stable...), wild...), StabilityCVLimit)
	if len(got) != 1 || got[0] != WorkloadGroupCountByPatient+"/"+BackendArango {
		t.Fatalf("expected only the wild cell unstable, got %v", got)
	}
	// too few samples: unstable, never silently trusted
	thin := timedSamples(BackendArango, WorkloadDistinctFamily, 10, 2)
	got = ControlUnstableCells(thin, StabilityCVLimit)
	if len(got) != 1 {
		t.Fatalf("2-sample cell must be reported unstable, got %v", got)
	}
	// warmup and failures are excluded from the timing set
	filtered := append(timedSamples(BackendPostgres, WorkloadDistinctFamily, 10, 8),
		sample(BackendPostgres, WorkloadDistinctFamily, OutcomePassed, 9000, true),
		sample(BackendPostgres, WorkloadDistinctFamily, OutcomeTimeout, 9000, false))
	if got := ControlUnstableCells(filtered, StabilityCVLimit); len(got) != 0 {
		t.Fatalf("warmup/failed samples must not poison stability, got %v", got)
	}
}

func TestControlSelfRatio(t *testing.T) {
	samples := append(
		timedSamples(BackendPostgres, WorkloadFilterActiveGender, 10, 8),
		timedSamples(BackendPostgres, WorkloadGroupCountByPatient, 20, 8)...,
	)
	value, ok := ControlSelfRatio(samples, BackendPostgres)
	if !ok || value < 1 || value > 1.05 {
		t.Fatalf("self-ratio on stable halves must sit at 1±0.05, got %v ok=%v", value, ok)
	}
	// first half fast, second half slow: worst deviation must show up
	var skewed []Sample
	for i := 0; i < 8; i++ {
		ms := 10.0
		if i >= 4 {
			ms = 30.0
		}
		s := sample(BackendArango, WorkloadFilterActiveGender, OutcomePassed, ms, false)
		s.Round, s.Iteration = i/2, i%2
		skewed = append(skewed, s)
	}
	value, ok = ControlSelfRatio(skewed, BackendArango)
	if !ok || value < 2.5 {
		t.Fatalf("skewed halves must yield a large self-ratio, got %v ok=%v", value, ok)
	}
	if _, ok := ControlSelfRatio(timedSamples(BackendArango, WorkloadFilterActiveGender, 10, 2), BackendArango); ok {
		t.Fatal("2-sample cell cannot establish a self-ratio")
	}
	if _, ok := ControlSelfRatio(nil, BackendArango); ok {
		t.Fatal("no cells must not report a self-ratio")
	}
}

func TestControlEmptyFloor(t *testing.T) {
	f := newFakeArm(BackendPostgres)
	defer f.close()
	arm := f.arm(BackendPostgres)
	f.rows["empty-out"] = []map[string]any{} // a real server returns [], not null
	median, err := arm.MeasureEmptyFloor(context.Background(), NewMonotonicClock(), "Bearer t", "proj", "bench", "receipt", "empty-out", 5)
	if err != nil {
		t.Fatal(err)
	}
	if median <= 0 {
		t.Fatalf("floor median must be positive, got %v", median)
	}
	if _, err := arm.MeasureEmptyFloor(context.Background(), NewMonotonicClock(), "Bearer t", "proj", "bench", "receipt", "empty-out", 2); err == nil {
		t.Fatal("fewer than 3 floor iterations must be refused")
	}
	// a floor request that returns rows is a protocol error
	f.rows["empty-out"] = []map[string]any{{"patient_id": "P1"}}
	if _, err := arm.MeasureEmptyFloor(context.Background(), NewMonotonicClock(), "Bearer t", "proj", "bench", "receipt", "empty-out", 5); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("non-empty floor request must fail closed, got %v", err)
	}
	_ = time.Now
}
