package acceptance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	IngestionFloorSeconds   = 5.0
	PublicationFloorSeconds = 5.0
	ProbeFloorSeconds       = 0.1
)

// PerformanceStageNames is deliberately kept in the same order as the
// acceptance contract. These are end-to-end stages, so adding a new timed
// stage here is an explicit contract change rather than an accidental report
// field becoming a gate.
var PerformanceStageNames = []string{
	"generation_upload",
	"explorer_publish",
	"explorer_viewer",
	"graphql",
}

type PerformanceObservation struct {
	Name                 string  `json:"name"`
	BaseSeconds          float64 `json:"base_seconds"`
	CurrentSeconds       float64 `json:"current_seconds"`
	AbsoluteFloor        float64 `json:"absolute_floor"`
	Ratio                float64 `json:"ratio"`
	Suspected            bool    `json:"suspected"`
	RepeatBaseSeconds    float64 `json:"repeat_base_seconds,omitempty"`
	RepeatCurrentSeconds float64 `json:"repeat_current_seconds,omitempty"`
	RepeatRatio          float64 `json:"repeat_ratio,omitempty"`
	RepeatSuspected      bool    `json:"repeat_suspected,omitempty"`
	// ReverseSuspected is retained as a wire-compatible name for consumers of
	// the initial comparator. It is always the result of comparing the repeat
	// current run against the repeat base run when ComparePerformanceRuns is
	// used.
	ReverseSuspected bool `json:"reverse_suspected"`
}

// PerformanceStageSeconds extracts only the four stages covered by the
// performance contract. A missing stage remains absent, allowing callers to
// report a malformed/unsupported base explicitly instead of treating zero as
// a very fast run.
func PerformanceStageSeconds(stages []StageReport) map[string]float64 {
	allowed := make(map[string]struct{}, len(PerformanceStageNames))
	for _, name := range PerformanceStageNames {
		allowed[name] = struct{}{}
	}
	result := make(map[string]float64, len(allowed))
	for _, stage := range stages {
		if _, ok := allowed[stage.Name]; ok && stage.Seconds >= 0 {
			result[stage.Name] = stage.Seconds
		}
	}
	return result
}

func performanceFloor(name string) (float64, bool) {
	switch name {
	case "generation_upload", "ingestion":
		return IngestionFloorSeconds, true
	case "explorer_publish", "publication":
		return PublicationFloorSeconds, true
	case "explorer_viewer", "graphql", "probe":
		return ProbeFloorSeconds, true
	default:
		return 0, false
	}
}

// ComparePerformanceRuns applies the two-part regression gate. The first
// pair is the initial (ordered) base/current run. If that pair is suspected,
// callers run both variants again in the opposite order with fresh databases
// and pass that pair as repeatBase/repeatCurrent. Confirmation is therefore
// always against the repeat's base, not a value observed before the repeat.
func ComparePerformanceRuns(base, current, repeatBase, repeatCurrent map[string]float64) []PerformanceObservation {
	names := map[string]bool{}
	for _, name := range PerformanceStageNames {
		if _, ok := base[name]; ok {
			names[name] = true
		}
		if _, ok := current[name]; ok {
			names[name] = true
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	result := make([]PerformanceObservation, 0, len(ordered))
	for _, name := range ordered {
		floor, ok := performanceFloor(name)
		if !ok {
			continue
		}
		b, cok := base[name]
		c, pok := current[name]
		if !cok || !pok || b <= 0 || c < 0 {
			continue
		}
		observation := PerformanceObservation{Name: name, BaseSeconds: b, CurrentSeconds: c, AbsoluteFloor: floor, Ratio: c / b, Suspected: c >= 2*b && c >= floor}
		if repeatBaseSeconds, ok := repeatBase[name]; ok {
			if repeatCurrentSeconds, ok := repeatCurrent[name]; ok && repeatBaseSeconds > 0 && repeatCurrentSeconds >= 0 {
				observation.RepeatBaseSeconds = repeatBaseSeconds
				observation.RepeatCurrentSeconds = repeatCurrentSeconds
				observation.RepeatRatio = repeatCurrentSeconds / repeatBaseSeconds
				observation.RepeatSuspected = repeatCurrentSeconds >= 2*repeatBaseSeconds && repeatCurrentSeconds >= floor
				observation.ReverseSuspected = observation.RepeatSuspected
			}
		}
		result = append(result, observation)
	}
	return result
}

// ComparePerformance is retained for callers that already have a reverse
// current observation but no reverse base. New callers should use
// ComparePerformanceRuns so confirmation is based on the repeat's base.
func ComparePerformance(base, current, reverse map[string]float64) []PerformanceObservation {
	return ComparePerformanceRuns(base, current, base, reverse)
}

func PerformanceFailure(observations []PerformanceObservation) bool {
	for _, observation := range observations {
		if observation.Suspected && observation.ReverseSuspected {
			return true
		}
	}
	return false
}

func PerformanceSuspicion(observations []PerformanceObservation) bool {
	for _, observation := range observations {
		if observation.Suspected {
			return true
		}
	}
	return false
}

type PerformanceComparisonReport struct {
	Status        string                   `json:"status"`
	BaseReport    string                   `json:"base_report"`
	CurrentReport string                   `json:"current_report"`
	Observations  []PerformanceObservation `json:"observations"`
}

func ComparePerformanceReportFiles(basePath, currentPath, repeatBasePath, repeatCurrentPath string) (PerformanceComparisonReport, error) {
	base, err := performanceMetricsFromReport(basePath)
	if err != nil {
		return PerformanceComparisonReport{}, fmt.Errorf("base performance report: %w", err)
	}
	current, err := performanceMetricsFromReport(currentPath)
	if err != nil {
		return PerformanceComparisonReport{}, fmt.Errorf("current performance report: %w", err)
	}
	if (strings.TrimSpace(repeatBasePath) == "") != (strings.TrimSpace(repeatCurrentPath) == "") {
		return PerformanceComparisonReport{}, errors.New("repeat base and current reports must be provided together")
	}
	var repeatBase, repeatCurrent map[string]float64
	if repeatBasePath != "" {
		repeatBase, err = performanceMetricsFromReport(repeatBasePath)
		if err != nil {
			return PerformanceComparisonReport{}, fmt.Errorf("repeat base performance report: %w", err)
		}
		repeatCurrent, err = performanceMetricsFromReport(repeatCurrentPath)
		if err != nil {
			return PerformanceComparisonReport{}, fmt.Errorf("repeat current performance report: %w", err)
		}
	}
	observations := ComparePerformanceRuns(base, current, repeatBase, repeatCurrent)
	status := "PASSED"
	if repeatBasePath == "" && PerformanceSuspicion(observations) {
		status = "SUSPECTED"
	}
	if repeatBasePath != "" && PerformanceFailure(observations) {
		status = "FAILED"
	}
	return PerformanceComparisonReport{Status: status, BaseReport: basePath, CurrentReport: currentPath, Observations: observations}, nil
}

func performanceMetricsFromReport(path string) (map[string]float64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var report Report
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, err
	}
	if report.Status != "PASSED" {
		return nil, fmt.Errorf("acceptance status is %q", report.Status)
	}
	metrics := PerformanceStageSeconds(report.Stages)
	for _, name := range PerformanceStageNames {
		if _, ok := metrics[name]; !ok {
			return nil, fmt.Errorf("required stage %q is missing", name)
		}
	}
	return metrics, nil
}

func WritePerformanceComparison(path string, report PerformanceComparisonReport) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("performance output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
