package benchmark

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
)

// Measurement method (bound before any number is reported):
//
//   - warmup iterations run first and are recorded but excluded from
//     statistics;
//   - measured iterations interleave backends round-robin per round so clock
//     and host drift hit both arms alike;
//   - every sample keeps its raw wall duration, outcome, cache label, and
//     error identity; failed and timed-out samples remain in raw results and
//     are excluded from statistics, never silently dropped;
//   - headline robustness is median + variance; p50/p95 are reported with a
//     declared quantile method;
//   - unavailable metrics stay unavailable, never zero.

// CacheLabel is the honest cache-state label attached to every sample.
// OS/page-cache state is never claimed cold without evidence; process-cold
// is only available where the harness owns the process restart.
type CacheLabel string

const (
	CacheWarm        CacheLabel = "warm"         // warmed steady state
	CacheProcessCold CacheLabel = "process-cold" // owning server process restarted before this sample
	CacheServiceWarm CacheLabel = "service-warm" // external service, no restart ownership (never claimed cold)
)

// Outcome classifies one execution attempt.
type Outcome string

const (
	OutcomePassed  Outcome = "passed"
	OutcomeFailed  Outcome = "failed"
	OutcomeTimeout Outcome = "timeout"
	OutcomeError   Outcome = "error"
)

// Sample is one recorded attempt. Duration is wall time through Loom as
// observed by the client; Phase attributes the dominant measured phase when
// the route separates it.
type Sample struct {
	WorkloadID string     `json:"workloadId"`
	Backend    string     `json:"backend"`
	Round      int        `json:"round"`
	Iteration  int        `json:"iteration"`
	Warmup     bool       `json:"warmup"`
	Outcome    Outcome    `json:"outcome"`
	Millis     float64    `json:"millis"`
	RowsSeen   int        `json:"rowsSeen"`
	Truncated  bool       `json:"truncated"`
	Cache      CacheLabel `json:"cache"`
	StartedAt  time.Time  `json:"startedAt"`
	ErrorCode  string     `json:"errorCode,omitempty"`
}

// RunConfig binds the repeatability policy. Zero values are refused, not
// defaulted silently, so a run's method is always explicit in its report.
type RunConfig struct {
	Warmup      int // discarded warmup iterations per workload x backend
	Iterations  int // measured iterations per workload x backend
	Concurrency int // simultaneous requests in the throughput phase
	Rounds      int // interleaved rounds; Iterations must split evenly
}

func (c RunConfig) Validate() error {
	if c.Warmup < 0 {
		return fmt.Errorf("benchmark: warmup must be >= 0")
	}
	if c.Iterations < 1 {
		return fmt.Errorf("benchmark: iterations must be >= 1")
	}
	if c.Concurrency < 1 {
		return fmt.Errorf("benchmark: concurrency must be >= 1")
	}
	if c.Rounds < 1 {
		return fmt.Errorf("benchmark: rounds must be >= 1")
	}
	if c.Iterations%c.Rounds != 0 {
		return fmt.Errorf("benchmark: iterations %d must split evenly across rounds %d", c.Iterations, c.Rounds)
	}
	return nil
}

// Summary is the statistics block for one workload x backend x cache label.
// Missing halves are unavailable, never zero: Count==0 means no samples.
type Summary struct {
	Count          int     `json:"count"`
	FailedCount    int     `json:"failedCount"`
	MedianMillis   float64 `json:"medianMillis"`
	MeanMillis     float64 `json:"meanMillis"`
	VarianceMs     float64 `json:"varianceMillis"`
	StdDevMs       float64 `json:"stdDevMillis"`
	MinMillis      float64 `json:"minMillis"`
	MaxMillis      float64 `json:"maxMillis"`
	P50Millis      float64 `json:"p50Millis"`
	P95Millis      float64 `json:"p95Millis"`
	QuantileMethod string  `json:"quantileMethod"`
	// CoefficientOfVariation is stddev/mean for the passed samples; the
	// stability control compares it against StabilityCVLimit.
	CoefficientOfVariation float64 `json:"coefficientOfVariation"`
	// ThroughputPerSecond is concurrency/windowSeconds for the throughput
	// phase only; 0 with ThroughputMeasured=false means unavailable.
	ThroughputPerSecond float64 `json:"throughputPerSecond"`
	ThroughputMeasured  bool    `json:"throughputMeasured"`
}

// Summarize computes statistics over the passed, non-warmup samples for one
// (workload, backend, cache) cell. Failed samples are counted separately and
// excluded from the distributions; their presence in raw results is enforced
// elsewhere.
func Summarize(samples []Sample) Summary {
	summary := Summary{QuantileMethod: "nearest-rank-on-sorted-passed-durations"}
	var passed []float64
	for _, s := range samples {
		if s.Warmup {
			continue
		}
		summary.Count++
		if s.Outcome != OutcomePassed {
			summary.FailedCount++
			continue
		}
		passed = append(passed, s.Millis)
	}
	if len(passed) == 0 {
		return summary
	}
	sort.Float64s(passed)
	var total, totalSq float64
	for _, ms := range passed {
		total += ms
		totalSq += ms * ms
	}
	n := float64(len(passed))
	summary.MeanMillis = total / n
	summary.MedianMillis = percentile(passed, 0.5)
	summary.P50Millis = summary.MedianMillis
	summary.P95Millis = percentile(passed, 0.95)
	summary.MinMillis = passed[0]
	summary.MaxMillis = passed[len(passed)-1]
	variance := totalSq/n - summary.MeanMillis*summary.MeanMillis
	if variance < 0 {
		variance = 0
	}
	summary.VarianceMs = variance
	summary.StdDevMs = math.Sqrt(variance)
	if summary.MeanMillis > 0 {
		summary.CoefficientOfVariation = summary.StdDevMs / summary.MeanMillis
	}
	return summary
}

// percentile uses nearest-rank on the sorted slice: rank = ceil(q*n), 1-based.
// The method is declared in Summary.QuantileMethod rather than assumed.
func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(q * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// StabilityCVLimit is the ceiling for the stability control: a workload arm
// whose coefficient of variation exceeds it marks the environment unstable
// and its ratios are excluded from performance claims.
const StabilityCVLimit = 0.35

// SpeedRatio compares two medians. It is computed only when both sides are
// valid (see gates.go); the caller must not present it otherwise.
type SpeedRatio struct {
	ArangoOverPostgres float64 `json:"arangoOverPostgres"`
	PostgresOverArango float64 `json:"postgresOverArango"`
}

func NewSpeedRatio(arangoMedian, postgresMedian float64) (SpeedRatio, error) {
	if arangoMedian <= 0 || postgresMedian <= 0 {
		return SpeedRatio{}, fmt.Errorf("benchmark: speed ratio needs positive medians, got arango=%s postgres=%s",
			strconv.FormatFloat(arangoMedian, 'g', -1, 64), strconv.FormatFloat(postgresMedian, 'g', -1, 64))
	}
	return SpeedRatio{
		ArangoOverPostgres: arangoMedian / postgresMedian,
		PostgresOverArango: postgresMedian / arangoMedian,
	}, nil
}

// Clock is injectable so accounting tests never sleep.
type Clock interface {
	Now() time.Time
	Since(time.Time) time.Duration
}

type monotonicClock struct{}

func (monotonicClock) Now() time.Time                  { return time.Now() }
func (monotonicClock) Since(t time.Time) time.Duration { return time.Since(t) }

// NewMonotonicClock returns the production clock (time.Now carries a
// monotonic reading; durations use Since, never wall subtraction).
func NewMonotonicClock() Clock { return monotonicClock{} }

// ElapsedMillis measures one attempt with the injected clock.
func ElapsedMillis(clock Clock, started time.Time) float64 {
	return float64(clock.Since(started).Microseconds()) / 1000.0
}

// ThroughputWindow summarizes a sustained concurrency window: window seconds
// are measured, not declared, and only passed requests count toward
// throughput.
func ThroughputSummary(samples []Sample, window time.Duration) Summary {
	summary := Summarize(samples)
	var passed int
	for _, s := range samples {
		if !s.Warmup && s.Outcome == OutcomePassed {
			passed++
		}
	}
	seconds := window.Seconds()
	if passed > 0 && seconds > 0 {
		summary.ThroughputPerSecond = float64(passed) / seconds
		summary.ThroughputMeasured = true
	}
	return summary
}
