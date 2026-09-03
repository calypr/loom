package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestComparePerformanceRequiresAbsoluteFloorAndRepeatBaseConfirmation(t *testing.T) {
	base := map[string]float64{
		"generation_upload": 4,
		"explorer_publish":  10,
		"explorer_viewer":   .2,
		"graphql":           .2,
	}
	current := map[string]float64{
		"generation_upload": 9,
		"explorer_publish":  21,
		"explorer_viewer":   .5,
		"graphql":           .5,
	}
	repeatBase := map[string]float64{
		"generation_upload": 5,
		"explorer_publish":  12,
		"explorer_viewer":   .3,
		"graphql":           .3,
	}
	repeatCurrent := map[string]float64{
		"generation_upload": 11,
		"explorer_publish":  25,
		"explorer_viewer":   .7,
		"graphql":           .7,
	}
	got := ComparePerformanceRuns(base, current, repeatBase, repeatCurrent)
	if len(got) != 4 {
		t.Fatalf("observations=%d", len(got))
	}
	for _, observation := range got {
		if !observation.Suspected || !observation.RepeatSuspected || !observation.ReverseSuspected {
			t.Fatalf("observation=%#v", observation)
		}
	}
	if got[0].Name != "explorer_publish" {
		t.Fatalf("observations are not sorted: %#v", got)
	}
	if !PerformanceFailure(got) {
		t.Fatal("expected confirmed performance failure")
	}
}

func TestComparePerformanceRequiresRepeatFailureAgainstRepeatBase(t *testing.T) {
	base := map[string]float64{"generation_upload": 4}
	current := map[string]float64{"generation_upload": 9}
	repeatBase := map[string]float64{"generation_upload": 5}
	repeatCurrent := map[string]float64{"generation_upload": 9}
	got := ComparePerformanceRuns(base, current, repeatBase, repeatCurrent)
	if len(got) != 1 || !got[0].Suspected || got[0].ReverseSuspected || PerformanceFailure(got) {
		t.Fatalf("got=%#v", got)
	}
}

func TestComparePerformanceAppliesMetricFloors(t *testing.T) {
	base := map[string]float64{
		"generation_upload": 2.5,
		"explorer_publish":  2.5,
		"explorer_viewer":   .05,
		"graphql":           .05,
	}
	current := map[string]float64{
		"generation_upload": 4.9,
		"explorer_publish":  4.9,
		"explorer_viewer":   .099,
		"graphql":           .099,
	}
	for _, observation := range ComparePerformanceRuns(base, current, current, current) {
		if observation.Suspected {
			t.Fatalf("below floor should not be suspected: %#v", observation)
		}
	}
	base["generation_upload"] = 2
	current["generation_upload"] = 5
	got := ComparePerformanceRuns(base, current, nil, nil)
	var ingestion *PerformanceObservation
	for i := range got {
		if got[i].Name == "generation_upload" {
			ingestion = &got[i]
		}
	}
	if ingestion == nil || !ingestion.Suspected {
		t.Fatalf("expected exact floor to be eligible: %#v", got)
	}
}

func TestPerformanceStageSecondsSelectsContractStages(t *testing.T) {
	got := PerformanceStageSeconds([]StageReport{
		{Name: "generation_upload", Seconds: 1.2},
		{Name: "arango_counts", Seconds: 99},
		{Name: "graphql", Seconds: .4},
	})
	if len(got) != 2 || got["generation_upload"] != 1.2 || got["graphql"] != .4 {
		t.Fatalf("metrics=%#v", got)
	}
}

func TestComparePerformanceDoesNotFailWithoutBase(t *testing.T) {
	if got := ComparePerformanceRuns(nil, map[string]float64{"graphql": 8}, nil, nil); len(got) != 0 || PerformanceFailure(got) {
		t.Fatalf("got=%#v", got)
	}
}

func TestComparePerformanceReportFilesRequiresRepeatToFail(t *testing.T) {
	dir := t.TempDir()
	base := writePerformanceTestReport(t, dir, "base.json", 2, 2, .05, .05)
	current := writePerformanceTestReport(t, dir, "current.json", 5, 5, .1, .1)
	first, err := ComparePerformanceReportFiles(base, current, "", "")
	if err != nil || first.Status != "SUSPECTED" {
		t.Fatalf("first comparison = %#v, %v", first, err)
	}
	repeatBase := writePerformanceTestReport(t, dir, "repeat-base.json", 4, 4, .1, .1)
	repeatCurrent := writePerformanceTestReport(t, dir, "repeat-current.json", 5, 5, .11, .11)
	confirmed, err := ComparePerformanceReportFiles(base, current, repeatBase, repeatCurrent)
	if err != nil || confirmed.Status != "PASSED" {
		t.Fatalf("confirmed comparison = %#v, %v", confirmed, err)
	}
	output := filepath.Join(dir, "performance.json")
	if err := WritePerformanceComparison(output, confirmed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal(err)
	}
}

func writePerformanceTestReport(t *testing.T, dir, name string, ingestion, publication, viewer, graphql float64) string {
	t.Helper()
	report := Report{Status: "PASSED", Stages: []StageReport{
		{Name: "generation_upload", Seconds: ingestion},
		{Name: "explorer_publish", Seconds: publication},
		{Name: "explorer_viewer", Seconds: viewer},
		{Name: "graphql", Seconds: graphql},
	}}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
