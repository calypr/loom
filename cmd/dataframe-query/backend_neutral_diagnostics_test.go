package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBackendNeutralDiagnosticsAndPlanIdentity proves the CLI reads the
// agreed backend-neutral diagnostics schema: it prints the source-query
// interval under the neutral source_query label, takes it from the new
// sourceQueryMs field when present (including a modern PostgreSQL server's
// explicit arangoQueryMs=0), and falls back to the legacy arangoQueryMs only
// for older servers that predate sourceQueryMs. It also pins that the
// checked-in example queries select the neutral field, because the CLI's
// measurement comes from whatever the request asks the server to project.
func TestBackendNeutralDiagnosticsAndPlanIdentity(t *testing.T) {
	t.Run("agreed schema drives the neutral label", func(t *testing.T) {
		paths := writeRequestFiles(t)
		var stdout, stderr bytes.Buffer
		body := `{"data":{"runFhirDataframe":{"rowCount":2,"rows":[{"id":"1"},{"id":"2"}],"diagnostics":{"inputResolutionMs":1.5,"requestPreparationMs":2.5,"compilationMs":3.5,"sourceQueryMs":42.5,"arangoQueryMs":0,"rowMaterializationMs":4.5,"resultAssemblyMs":0.5,"totalMs":60,"plan":{"traversalSets":1,"sharedTraversalCount":0,"requiredMatchReuseCount":0,"scopedSharingCandidateGroups":0,"scopedSharingCandidateSets":0,"potentialSharingOpportunityGroups":0,"potentialSharingOpportunitySets":0,"richSourceReuse":[]}}}}}`
		if err := run(append(paths, "-print-response=false"), clientForResponses(200, body), &stdout, &stderr); err != nil {
			t.Fatalf("run: %v (stderr=%s)", err, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "source_query=42.500") {
			t.Fatalf("stdout missing the neutral source_query reading of sourceQueryMs:\n%s", out)
		}
		if strings.Contains(out, "arango_query=") {
			t.Fatalf("stdout still prints the AQL-specific stage label:\n%s", out)
		}
	})

	t.Run("modern postgres zero does not mask the neutral field", func(t *testing.T) {
		// A PostgreSQL server reports arangoQueryMs as an explicit zero; the
		// neutral reading must come from sourceQueryMs, never from the zero
		// legacy projection.
		diagnostics := dataframeDiagnostics{SourceQueryMs: ptr(7.25), ArangoQueryMs: ptr(0)}
		if got := diagnostics.sourceQueryMs(); got != 7.25 {
			t.Fatalf("sourceQueryMs() = %v, want 7.25 from the neutral field", got)
		}
	})

	t.Run("legacy server falls back to arangoQueryMs", func(t *testing.T) {
		paths := writeRequestFiles(t)
		var stdout, stderr bytes.Buffer
		body := `{"data":{"runFhirDataframe":{"rowCount":1,"rows":[{"id":"1"}],"diagnostics":{"inputResolutionMs":1.5,"requestPreparationMs":2.5,"compilationMs":3.5,"arangoQueryMs":17.25,"rowMaterializationMs":4.5,"resultAssemblyMs":0.5,"totalMs":60,"plan":{"traversalSets":0,"sharedTraversalCount":0,"requiredMatchReuseCount":0,"scopedSharingCandidateGroups":0,"scopedSharingCandidateSets":0,"potentialSharingOpportunityGroups":0,"potentialSharingOpportunitySets":0,"richSourceReuse":[]}}}}}`
		if err := run(append(paths, "-print-response=false"), clientForResponses(200, body), &stdout, &stderr); err != nil {
			t.Fatalf("run: %v (stderr=%s)", err, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "source_query=17.250") {
			t.Fatalf("legacy server without sourceQueryMs must still report the cursor interval under the neutral label:\n%s", out)
		}
	})

	t.Run("example queries select the neutral field", func(t *testing.T) {
		for _, name := range []string{"meta_patient_dataframe.graphql", "meta_gdc_case_matrix.graphql"} {
			raw, err := os.ReadFile(filepath.Join("..", "..", "examples", name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			selection := string(raw)
			if !strings.Contains(selection, "sourceQueryMs") {
				t.Fatalf("%s does not select the agreed sourceQueryMs field", name)
			}
			if !strings.Contains(selection, "arangoQueryMs") {
				t.Fatalf("%s must keep selecting the required legacy arangoQueryMs field for compatibility", name)
			}
		}
	})
}

func ptr(value float64) *float64 { return &value }
