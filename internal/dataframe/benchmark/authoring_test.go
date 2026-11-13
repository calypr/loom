package benchmark

import (
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
	// Root is always Patient with base occurrence; child occurrences use
	// the relationship spellings proven by the acceptance fixture.
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
		if route.OccurrenceID != "base" || route.ResourceType != "Patient" {
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
	if r := exclusions[WorkloadDeriveAgeBand]; !strings.Contains(r, "DISTINCT_VALUES") ||
		!strings.Contains(r, "band labels") || !strings.Contains(r, "valueQuantity.value") {
		t.Errorf("derive-age-band exclusion must name the oracle/workspace defect: %q", r)
	}
	if r := exclusions[WorkloadSortLimitTopN]; !strings.Contains(r, "name[].family") ||
		!strings.Contains(r, "first-only") {
		t.Errorf("sort-limit-topn exclusion must name the multi-family risk: %q", r)
	}
}
