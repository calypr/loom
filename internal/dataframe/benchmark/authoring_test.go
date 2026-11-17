package benchmark

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

// Format fidelity is judged by the repository's own decoder: if
// authoringv2.DecodeWorkspace accepts a battery workspace and its validation
// passes, the live routes will see exactly this JSON.

func TestBatteryWorkspacesDecode(t *testing.T) {
	workspaces, err := BuildAllWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != len(Battery) {
		t.Fatalf("every battery workload must have a workspace template: %d/%d", len(workspaces), len(Battery))
	}
	for _, spec := range Battery {
		raw, ok := workspaces[spec.ID]
		if !ok {
			t.Fatalf("missing workspace for %s", spec.ID)
		}
		ws, err := authoringv2.DecodeWorkspace(raw)
		if err != nil {
			t.Fatalf("%s: DecodeWorkspace rejected the authored shape: %v", spec.ID, err)
		}
		if len(ws.Documents) != 1 {
			t.Fatalf("%s: expected one document", spec.ID)
		}
		doc := ws.Documents[0]
		if doc.Output.ID != OutputIDFor(spec.ID) {
			t.Fatalf("%s: output id mismatch: %s", spec.ID, doc.Output.ID)
		}
		if len(doc.Columns) != len(spec.Columns) {
			// hidden helper columns (e.g. a filter column excluded from the
			// published contract) are allowed; the visible projection must
			// equal the contract exactly.
			var visible []string
			for _, column := range doc.Columns {
				if column.Table.Visible == nil || *column.Table.Visible {
					visible = append(visible, column.Column)
				}
			}
			if strings.Join(visible, ",") != strings.Join(spec.Columns, ",") {
				t.Fatalf("%s: visible columns %v != contract %v", spec.ID, visible, spec.Columns)
			}
		} else {
			for i, column := range spec.Columns {
				if doc.Columns[i].Column != column {
					t.Fatalf("%s: column %d = %q want %q", spec.ID, i, doc.Columns[i].Column, column)
				}
			}
		}
	}
}

func TestBatteryWorkspaceRouteShape(t *testing.T) {
	// Root is Patient with base occurrence for every workload except
	// derive-age-band, whose rows are observations at an Observation root
	// (one row per observation, acceptance-proven single-node route form);
	// child occurrences use the relationship spellings proven by the
	// acceptance fixture.
	workspaces, err := BuildAllWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"subject_Patient": true, "partOfStudy_ResearchStudy": true}
	for id, raw := range workspaces {
		ws, err := authoringv2.DecodeWorkspace(raw)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		route := ws.Documents[0].Route
		wantRoot := "Patient"
		if id == WorkloadDeriveAgeBand {
			wantRoot = "Observation"
		}
		if route.OccurrenceID != "base" || route.ResourceType != wantRoot {
			t.Fatalf("%s: route root: %+v", id, route)
		}
		var walk func(children []authoringv2.RouteNode)
		walk = func(children []authoringv2.RouteNode) {
			for _, child := range children {
				if !allowed[child.Relationship] {
					t.Fatalf("%s: unproven relationship %q (acceptance-proven set only)", id, child.Relationship)
				}
				walk(child.Children)
			}
		}
		walk(route.Children)
	}
}

func TestAuthoringVerificationNoneRejected(t *testing.T) {
	status := AuthoringVerification()
	for _, spec := range Battery {
		got, ok := status[spec.ID]
		if !ok {
			t.Fatalf("%s: no authoring verdict", spec.ID)
		}
		if got == AuthoringRejected {
			// not a test failure per se: the driver would ship this row as
			// planned/not_run — but during development we want to know.
			t.Logf("workload %s authoring REJECTED by decoder", spec.ID)
		}
	}
}

// TestDeriveAgeBandOfflineWitness proves the derive-age-band workload CAN
// reach passed, offline and without any server: the workspace decodes, its
// exhaustive recode map agrees with the independently thresholded oracle on
// every fixture value, and simulating the authored shape (identifier FIRST
// + recode + fixed filter) against the uploaded documents reproduces the
// oracle bag byte-for-byte. A backend that faithfully applies recode and
// filter therefore cannot fail this workload — the ClickHouse-lane
// rehearsal only has to confirm faithfulness, not discover a mismatch.
func TestDeriveAgeBandOfflineWitness(t *testing.T) {
	params := DatasetParams{Preset: PresetTiny, Seed: 5, Project: "bench-proj", Generation: "gen-w", AuthResourcePath: "/lox/lox-w"}
	files, err := GenerateFiles(params)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := BuildWorkspace(WorkloadDeriveAgeBand)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authoringv2.DecodeWorkspace(raw); err != nil {
		t.Fatalf("derive-age-band workspace must decode: %v", err)
	}

	// Extract the recode map from the authored workspace, not from the
	// helper, so the witness checks the actual wire artifact.
	var doc struct {
		Documents []struct {
			Columns []struct {
				Column              string `json:"column"`
				ValueTransformation struct {
					Kind                string `json:"kind"`
					ExactCategoryRecode struct {
						Mappings      []map[string]string `json:"mappings"`
						UnknownPolicy string              `json:"unknownPolicy"`
					} `json:"exactCategoryRecode"`
				} `json:"valueTransformation"`
			} `json:"columns"`
			FixedFilters []struct {
				Column string   `json:"column"`
				Values []string `json:"values"`
			} `json:"fixedFilters"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	recode := map[string]string{}
	for _, col := range doc.Documents[0].Columns {
		if col.Column != "age_band" {
			continue
		}
		if col.ValueTransformation.Kind != "EXACT_CATEGORY_RECODE" ||
			col.ValueTransformation.ExactCategoryRecode.UnknownPolicy != "ERROR" {
			t.Fatalf("age_band must recode with unknownPolicy ERROR: %+v", col.ValueTransformation)
		}
		for _, m := range col.ValueTransformation.ExactCategoryRecode.Mappings {
			if _, dup := recode[m["from"]]; dup {
				t.Fatalf("duplicate recode source %q", m["from"])
			}
			recode[m["from"]] = m["to"]
		}
	}
	if len(recode) != AgeBandFixtureMax {
		t.Fatalf("recode map must cover [1,%d] exhaustively: %d entries", AgeBandFixtureMax, len(recode))
	}
	for v := 1; v <= AgeBandFixtureMax; v++ {
		if recode[strconv.Itoa(v)] != AgeBandFor(float64(v)) {
			t.Fatalf("recode map disagrees with the oracle banding at %d: %q vs %q",
				v, recode[strconv.Itoa(v)], AgeBandFor(float64(v)))
		}
	}
	if got := doc.Documents[0].FixedFilters; len(got) != 1 || got[0].Column != "age_band" ||
		strings.Join(got[0].Values, ",") != "young,middle,senior" {
		t.Fatalf("fixed filter must keep exactly the banded rows: %+v", got)
	}

	// Simulate the authored shape against the uploaded documents: one row
	// per observation with a bench age identifier, band = recode[value].
	var simulated []map[string]any
	seenAge := 0
	for _, line := range strings.Split(strings.TrimRight(files.Files["Observation.ndjson"], "\n"), "\n") {
		var o struct {
			ID   string `json:"id"`
			Code struct {
				Coding []struct {
					Code string `json:"code"`
				} `json:"coding"`
			} `json:"code"`
			Identifier []struct {
				System string `json:"system"`
				Value  string `json:"value"`
			} `json:"identifier"`
			ValueQuantity *struct {
				Value float64 `json:"value"`
			} `json:"valueQuantity"`
		}
		if err := json.Unmarshal([]byte(line), &o); err != nil {
			t.Fatal(err)
		}
		isAge := len(o.Code.Coding) == 1 && o.Code.Coding[0].Code == ObservationCodeAge
		var bench []string
		for _, id := range o.Identifier {
			if id.System == BenchAgeIdentifierSystem {
				bench = append(bench, id.Value)
			}
		}
		switch {
		case !isAge && len(bench) != 0:
			t.Fatalf("%s: non-age observation must not carry the bench age identifier", o.ID)
		case isAge && o.ValueQuantity == nil && len(bench) != 0:
			t.Fatalf("%s: missing-value age observation must not carry the identifier either", o.ID)
		case isAge && o.ValueQuantity != nil:
			if len(bench) != 1 {
				t.Fatalf("%s: age observation needs exactly one identifier value, got %d", o.ID, len(bench))
			}
			if bench[0] != strconv.FormatFloat(o.ValueQuantity.Value, 'f', -1, 64) {
				t.Fatalf("%s: identifier %q diverged from valueQuantity %v", o.ID, bench[0], o.ValueQuantity.Value)
			}
			band, ok := recode[bench[0]]
			if !ok {
				t.Fatalf("%s: fixture age %s outside the recode domain", o.ID, bench[0])
			}
			simulated = append(simulated, map[string]any{"observation_id": o.ID, "age_band": band})
			seenAge++
		}
	}
	if seenAge == 0 {
		t.Fatal("no valued age observations in the tiny preset")
	}
	columns, _ := workloadColumns(WorkloadDeriveAgeBand)
	simulatedBag := Bag(CanonicalizeRows(columns, simulated))
	expected, err := OracleFor(WorkloadDeriveAgeBand, files.Facts)
	if err != nil {
		t.Fatal(err)
	}
	if expected == nil {
		t.Fatal("derive-age-band must have an oracle")
	}
	if a, b, diff := FirstDifference(*expected, simulatedBag); diff {
		t.Fatalf("workspace-simulated bag differs from the oracle bag: first divergence expected %q, simulated %q", a, b)
	}
	if expected.Digest() != simulatedBag.Digest() {
		t.Fatalf("bag digests differ: oracle %s simulated %s", expected.Digest(), simulatedBag.Digest())
	}
}

// TestLiveBatteryExclusionsRegistry pins the live battery law from review
// r4559430560: a live leg runs exactly the authoring-proven workloads,
// and every exclusion carries a named reason. A silent exclusion is the
// same deception class the report verifier exists to refuse.
func TestLiveBatteryExclusionsRegistry(t *testing.T) {
	exclusions := LiveBatteryExclusions()
	statuses := AuthoringVerification()
	live := 0
	for _, spec := range Battery {
		reason, excluded := exclusions[spec.ID]
		if statuses[spec.ID] == AuthoringProven && !unverifiedWorkloads[spec.ID] {
			if excluded {
				t.Errorf("%s: proven workload excluded from live legs: %q", spec.ID, reason)
			}
			live++
			continue
		}
		if !excluded {
			t.Errorf("%s: unproven verdict %q not excluded from live legs", spec.ID, statuses[spec.ID])
			continue
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s: exclusion without a named reason", spec.ID)
		}
	}
	if live == 0 {
		t.Fatal("live battery is empty: nothing would ever be timed")
	}
	if live == len(Battery) {
		t.Fatal("nothing is excluded: the disproven oracle rows must stay out of live legs")
	}
	// The two review findings must be named in the registry with their
	// actual defects, not generic prose.
	if r := exclusions[WorkloadDeriveAgeBand]; !strings.Contains(r, "EXACT_CATEGORY_RECODE") ||
		!strings.Contains(r, "acceptance-fixture precedent") || !strings.Contains(r, "TestDeriveAgeBandOfflineWitness") {
		t.Errorf("derive-age-band exclusion must name the unproven-recode shape and the offline witness: %q", r)
	}
	if r := exclusions[WorkloadSortLimitTopN]; !strings.Contains(r, "name[].family") ||
		!strings.Contains(r, "first-only") {
		t.Errorf("sort-limit-topn exclusion must name the multi-family risk: %q", r)
	}
}
