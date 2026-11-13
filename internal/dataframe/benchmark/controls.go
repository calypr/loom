package benchmark

import (
	"context"
	"fmt"
	"sort"
)

// Control-run computations. These turn raw samples into the ControlRuns
// block; the verifier independently re-checks their bounds, so a run that
// skips a control cannot forge one.

// passedWarm returns the samples that carry timing meaning: warm cache,
// measured (not warmup), successful.
func passedWarm(samples []Sample) []Sample {
	var out []Sample
	for _, s := range samples {
		if s.Warmup || s.Cache != CacheWarm || s.Outcome != OutcomePassed {
			continue
		}
		out = append(out, s)
	}
	return out
}

func cellSamples(samples []Sample, workload, backend string) []Sample {
	var out []Sample
	for _, s := range samples {
		if s.WorkloadID == workload && s.Backend == backend {
			out = append(out, s)
		}
	}
	return out
}

// ControlUnstableCells returns "workload/backend" cells whose passed-warm
// coefficient of variation exceeds the ceiling. The driver excludes these
// cells from claims; with fewer than 3 samples a cell cannot demonstrate
// stability and is reported unstable (never silently trusted).
func ControlUnstableCells(samples []Sample, limit float64) []string {
	type cell struct{ workload, backend string }
	seen := map[cell]bool{}
	var cells []cell
	for _, s := range passedWarm(samples) {
		c := cell{s.WorkloadID, s.Backend}
		if !seen[c] {
			seen[c] = true
			cells = append(cells, c)
		}
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].workload != cells[j].workload {
			return cells[i].workload < cells[j].workload
		}
		return cells[i].backend < cells[j].backend
	})
	var unstable []string
	for _, c := range cells {
		sum := Summarize(cellSamples(samples, c.workload, c.backend))
		if sum.Count < 3 || sum.CoefficientOfVariation > limit {
			unstable = append(unstable, c.workload+"/"+c.backend)
		}
	}
	return unstable
}

// ControlSelfRatio computes the backend-vs-itself sanity ratio across
// halves of the iteration sequence: for every cell, medians of the first
// and second half of passed-warm samples ordered by (round, iteration);
// the reported value is the WORST deviation across cells (always >= 1).
// ok=false when any evaluated cell cannot be split (fewer than 4 samples)
// or no cells exist: a missing self-ratio control cannot pass the verifier.
func ControlSelfRatio(samples []Sample, backend string) (float64, bool) {
	type cell struct{ workload, backend string }
	seen := map[cell]bool{}
	var cells []cell
	for _, s := range passedWarm(samples) {
		if s.Backend != backend {
			continue
		}
		c := cell{s.WorkloadID, backend}
		if !seen[c] {
			seen[c] = true
			cells = append(cells, c)
		}
	}
	if len(cells) == 0 {
		return 0, false
	}
	worst := 1.0
	for _, c := range cells {
		cellSamplesSorted := cellSamples(samples, c.workload, c.backend)
		passed := passedWarm(cellSamplesSorted)
		if len(passed) < 4 {
			return 0, false
		}
		sort.SliceStable(passed, func(i, j int) bool {
			if passed[i].Round != passed[j].Round {
				return passed[i].Round < passed[j].Round
			}
			return passed[i].Iteration < passed[j].Iteration
		})
		mid := len(passed) / 2
		a := Summarize(passed[:mid]).MedianMillis
		b := Summarize(passed[mid:]).MedianMillis
		if a <= 0 || b <= 0 {
			return 0, false
		}
		ratio := a / b
		if ratio < 1 {
			ratio = 1 / ratio
		}
		if ratio > worst {
			worst = ratio
		}
	}
	return worst, true
}

// ControlEmptyFloorMillis turns empty-result floor samples into the control
// value (median of passed warm samples); ok=false without evidence.
func ControlEmptyFloorMillis(samples []Sample) (float64, bool) {
	passed := passedWarm(samples)
	if len(passed) == 0 {
		return 0, false
	}
	var ms []float64
	for _, s := range passed {
		ms = append(ms, s.Millis)
	}
	sort.Float64s(ms)
	return percentile(ms, 0.5), true
}

// MeasureEmptyFloor times an identical request whose expectation is ZERO
// rows (an impossible fixed filter). Any rows back is a protocol error: the
// floor request stopped being empty, so the control would be meaningless.
func (a *Arm) MeasureEmptyFloor(ctx context.Context, clock Clock, token, project, explorer, receiptID, outputID string, iterations int) (float64, error) {
	if iterations < 3 {
		return 0, fmt.Errorf("benchmark: empty-floor control needs >= 3 iterations")
	}
	var ms []float64
	for i := 0; i < iterations; i++ {
		started := clock.Now()
		result, err := a.ExecuteOutput(ctx, token, project, explorer, receiptID, outputID, 1)
		elapsed := ElapsedMillis(clock, started)
		if err != nil {
			return 0, fmt.Errorf("benchmark: empty-floor request failed: %w", err)
		}
		if result.RowCount != 0 {
			return 0, fmt.Errorf("benchmark: empty-floor request returned %d rows; the control request is not empty", result.RowCount)
		}
		ms = append(ms, elapsed)
	}
	sort.Float64s(ms)
	return percentile(ms, 0.5), nil
}
