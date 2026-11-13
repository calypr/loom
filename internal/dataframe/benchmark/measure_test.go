package benchmark

import (
	"math"
	"testing"
	"time"
)

func sample(backend, workload string, outcome Outcome, ms float64, warmup bool) Sample {
	return Sample{WorkloadID: workload, Backend: backend, Outcome: outcome, Millis: ms, Warmup: warmup, Cache: CacheWarm, Round: 1, Iteration: 1}
}

func TestSummarizeExcludesWarmupAndFailed(t *testing.T) {
	samples := []Sample{
		sample(BackendArango, "w", OutcomePassed, 100, true), // warmup: excluded
		sample(BackendArango, "w", OutcomePassed, 10, false),
		sample(BackendArango, "w", OutcomePassed, 20, false),
		sample(BackendArango, "w", OutcomePassed, 30, false),
		sample(BackendArango, "w", OutcomeTimeout, 9999, false), // excluded from stats, counted
	}
	s := Summarize(samples)
	if s.Count != 4 {
		t.Fatalf("count must cover non-warmup samples including failures: %d", s.Count)
	}
	if s.FailedCount != 1 {
		t.Fatalf("failed count: %d", s.FailedCount)
	}
	if s.MedianMillis != 20 || s.MinMillis != 10 || s.MaxMillis != 30 {
		t.Fatalf("warmup or failed leaked into stats: %+v", s)
	}
	if s.MeanMillis != 20 {
		t.Fatalf("mean: %v", s.MeanMillis)
	}
}

func TestSummarizeQuantilesNearestRank(t *testing.T) {
	var samples []Sample
	for i := 1; i <= 20; i++ {
		samples = append(samples, sample(BackendArango, "w", OutcomePassed, float64(i), false))
	}
	s := Summarize(samples)
	// nearest-rank: p50 rank=ceil(.5*20)=10 -> 10; p95 rank=ceil(.95*20)=19 -> 19.
	if s.P50Millis != 10 || s.P95Millis != 19 {
		t.Fatalf("quantiles wrong under nearest-rank: %+v", s)
	}
	if s.QuantileMethod != "nearest-rank-on-sorted-passed-durations" {
		t.Fatal("quantile method must be declared")
	}
}

func TestSummarizeUnavailableNotZero(t *testing.T) {
	// A cell whose samples all failed: distributions are UNAVAILABLE. The
	// sentinel is Count>0,FailedCount==Count plus ThroughputMeasured=false;
	// a never-run cell has Count==0. Neither may masquerade as a real 0ms.
	s := Summarize([]Sample{sample(BackendArango, "w", OutcomeError, 5, false)})
	if s.Count != 1 || s.FailedCount != 1 {
		t.Fatalf("all-failed cell shape: %+v", s)
	}
	if s.ThroughputMeasured {
		t.Fatal("throughput must stay unmeasured without passed samples")
	}
	empty := Summarize(nil)
	if empty.Count != 0 {
		t.Fatalf("never-run cell must have Count==0: %+v", empty)
	}
}

func TestSummarizeVarianceAndCV(t *testing.T) {
	samples := []Sample{}
	for _, ms := range []float64{10, 10, 10, 10} {
		samples = append(samples, sample(BackendArango, "w", OutcomePassed, ms, false))
	}
	s := Summarize(samples)
	if s.VarianceMs != 0 || s.StdDevMs != 0 || s.CoefficientOfVariation != 0 {
		t.Fatalf("identical samples must have zero variance: %+v", s)
	}
	s2 := Summarize([]Sample{
		sample(BackendArango, "w", OutcomePassed, 10, false),
		sample(BackendArango, "w", OutcomePassed, 30, false),
	})
	// mean 20, variance 100, stddev 10, CV 0.5
	if math.Abs(s2.VarianceMs-100) > 1e-9 || math.Abs(s2.StdDevMs-10) > 1e-9 || math.Abs(s2.CoefficientOfVariation-0.5) > 1e-9 {
		t.Fatalf("variance/CV wrong: %+v", s2)
	}
}

func TestRunConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  RunConfig
		ok   bool
	}{
		{"zero", RunConfig{}, false},
		{"uneven rounds", RunConfig{Warmup: 1, Iterations: 7, Concurrency: 1, Rounds: 2}, false},
		{"negative warmup", RunConfig{Warmup: -1, Iterations: 4, Concurrency: 1, Rounds: 2}, false},
		{"ok", RunConfig{Warmup: 2, Iterations: 8, Concurrency: 4, Rounds: 4}, true},
	}
	for _, tc := range cases {
		err := tc.cfg.Validate()
		if tc.ok && err != nil {
			t.Fatalf("%s: unexpected %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%s: expected rejection", tc.name)
		}
	}
}

func TestSpeedRatioRejectsNonPositive(t *testing.T) {
	if _, err := NewSpeedRatio(0, 10); err == nil {
		t.Fatal("zero median must be refused, not reported as ratio 0")
	}
	if _, err := NewSpeedRatio(10, -1); err == nil {
		t.Fatal("negative median must be refused")
	}
	r, err := NewSpeedRatio(40, 10)
	if err != nil || r.ArangoOverPostgres != 4 || r.PostgresOverArango != 0.25 {
		t.Fatalf("ratio wrong: %+v %v", r, err)
	}
}

func TestThroughputSummaryUsesMeasuredWindowAndPassedOnly(t *testing.T) {
	samples := []Sample{
		sample(BackendArango, "w", OutcomePassed, 50, false),
		sample(BackendArango, "w", OutcomePassed, 60, false),
		sample(BackendArango, "w", OutcomeError, 10, false),
		sample(BackendArango, "w", OutcomePassed, 40, true), // warmup excluded
	}
	s := ThroughputSummary(samples, 2*time.Second)
	if !s.ThroughputMeasured {
		t.Fatal("measured window with passes must mark throughput measured")
	}
	if s.ThroughputPerSecond != 1.0 { // 2 passed / 2s
		t.Fatalf("throughput counts only passed non-warmup: %v", s.ThroughputPerSecond)
	}
	if z := ThroughputSummary(samples, 0); z.ThroughputMeasured {
		t.Fatal("zero window is not a measurement")
	}
}

func TestElapsedMillisUsesMonotonicSince(t *testing.T) {
	fake := fakeClock{start: time.Unix(0, 0), now: time.Unix(0, 0)}
	elapsed := ElapsedMillis(&fake, fake.now)
	if elapsed != 0 {
		t.Fatal("same instant must measure zero")
	}
	fake.now = fake.start.Add(1500 * time.Microsecond)
	if got := ElapsedMillis(&fake, fake.start); got != 1.5 {
		t.Fatalf("expected 1.5ms, got %v", got)
	}
	if NewMonotonicClock() == nil {
		t.Fatal("production clock must exist")
	}
}

type fakeClock struct{ start, now time.Time }

func (c *fakeClock) Now() time.Time                  { return c.now }
func (c *fakeClock) Since(t time.Time) time.Duration { return c.now.Sub(t) }
