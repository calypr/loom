package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestMigrateStoredWorkspaceLegacyContributorIsDetachedAndIdempotent(t *testing.T) {
	raw := []byte(`{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","semanticsVersion":3,"explorer":{"title":"Patients"},"documents":[{"kind":"` + Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"columns":[{"column":"patient_count","label":"Patients","occurrenceId":"base","source":{"kind":"aggregate","aggregate":{"operation":"COUNT","where":{"path":"id","equals":""}}}},{"column":"patient_birth_date","label":"Birth date","occurrenceId":"base","source":{"kind":"field","field":{"path":"birthDate","projectionMode":"VALUE"}}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`)
	catalog := commandCatalog()
	result, err := MigrateStoredWorkspace(raw, catalog)
	if err != nil {
		t.Fatal(err)
	}
	var column Column
	var retainedCurrentSource bool
	for _, migratedColumn := range result.Workspace.Documents[0].Columns {
		if migratedColumn.Column == "patient_count" {
			column = migratedColumn
		}
		if migratedColumn.Column == "patient_birth_date" && migratedColumn.Source.Field != nil && migratedColumn.Source.Field.Path == "birthDate" {
			retainedCurrentSource = true
		}
	}
	if !retainedCurrentSource {
		t.Fatal("mixed migration dropped or changed current source")
	}
	if result.Workspace.SemanticsVersion != CurrentSemanticsVersion || column.Contributor == nil || column.Contributor.Operator != ContributorExists || (column.Source.Aggregate != nil && column.Source.Aggregate.Where != nil) {
		t.Fatalf("migrated workspace = %#v", result.Workspace)
	}
	if !result.DigestChanged || result.MigratedCanonicalDigest == "" || len(result.AcceptedOriginalDigests) == 0 {
		t.Fatalf("digest evidence = %#v", result)
	}
	for _, oldDigest := range result.AcceptedOriginalDigests {
		if oldDigest == result.MigratedCanonicalDigest {
			t.Fatalf("old receipt digest %q remained current after semantic migration", oldDigest)
		}
	}
	firstBytes := append([]byte(nil), result.CanonicalJSON...)
	result.Workspace.Explorer.Title = "changed returned value"
	if !bytes.Equal(firstBytes, result.CanonicalJSON) {
		t.Fatal("returned workspace aliases canonical byte buffer")
	}
	if !bytes.Contains(raw, []byte(`"where":{"path":"id","equals":""}`)) {
		t.Fatal("migration modified input bytes")
	}

	again, err := MigrateStoredWorkspace(firstBytes, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, again.CanonicalJSON) || again.DigestChanged {
		t.Fatalf("rerun changed migrated workspace: changed=%v\nfirst=%s\nagain=%s", again.DigestChanged, firstBytes, again.CanonicalJSON)
	}
}

func TestMigrateStoredWorkspaceDecodesFlatCodedAndTemporalLegacySources(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want func(Workspace) bool
	}{
		{
			name: "flat source",
			raw:  `{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","semanticsVersion":0,"explorer":{"title":"Patients"},"documents":[{"kind":"` + Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"columns":[{"column":"patient_id","label":"Patient ID","occurrenceId":"base","source":{"kind":"field","fieldPath":"id","projectionMode":"VALUE"}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`,
			want: func(w Workspace) bool {
				return w.Documents[0].Columns[0].Source.Field != nil && w.Documents[0].Columns[0].Source.Field.Path == "id"
			},
		},
		{
			name: "retired coded kind",
			raw:  `{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","semanticsVersion":` + fmt.Sprint(CurrentSemanticsVersion) + `,"explorer":{"title":"Observation"},"documents":[{"kind":"` + Kind + `","output":{"id":"observations","title":"Observations"},"rootResourceType":"Observation","route":{"occurrenceId":"base","resourceType":"Observation"},"rows":{"kind":"RECORDS","records":{}},"columns":[{"column":"height","label":"Height","occurrenceId":"base","source":{"kind":"codingBySystem","lookup":{"binding":{"ownerPath":"","keyPath":"code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","choiceArms":["valueQuantity"],"logicalType":"decimal"},"key":{"system":"urn:study","code":"height"},"projectionMode":"VALUE"}}}]}],"tabs":[{"id":"observations","title":"Observations","outputId":"observations","order":0,"visible":true}]}`,
			want: func(w Workspace) bool {
				return w.Documents[0].Columns[0].Source.Kind == SourceCodedValue && w.Documents[0].Columns[0].Source.Lookup != nil
			},
		},
		{
			name: "temporal reduction",
			raw:  `{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","semanticsVersion":` + fmt.Sprint(aggregateTemporalPolicySemanticsVersion-1) + `,"explorer":{"title":"Observations"},"documents":[{"kind":"` + Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient","children":[{"occurrenceId":"observations","resourceType":"Observation","relationship":"subject_Patient"}]},"rows":{"kind":"RECORDS","records":{}},"columns":[{"column":"latest","label":"Latest","occurrenceId":"observations","source":{"kind":"aggregate","aggregate":{"operation":"FIRST_ORDERED","path":"valueQuantity.value","temporal":{"timestampPath":"effectiveDateTime","anchorPath":"meta.lastUpdated","lowerOffsetSeconds":-86400,"upperOffsetSeconds":0,"lowerInclusive":true,"upperInclusive":false,"direction":"DESC","precision":"INSTANT","tiePolicy":"RESOURCE_KEY"}}}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","order":0,"visible":true}]}`,
			want: func(w Workspace) bool {
				a := w.Documents[0].Columns[0].Source.Aggregate
				return a != nil && a.ContributorWindow != nil && a.Ordering != nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := MigrateStoredWorkspace([]byte(test.raw), CatalogSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			if !test.want(result.Workspace) || result.Workspace.SemanticsVersion != CurrentSemanticsVersion {
				t.Fatalf("migrated workspace did not preserve/upgrade source: %#v", result.Workspace.Documents[0].Columns[0].Source)
			}
			if _, err := MigrateStoredWorkspace(result.CanonicalJSON, CatalogSnapshot{}); err != nil {
				t.Fatalf("migrated wire did not decode again: %v", err)
			}
		})
	}
}

func TestMigrateStoredWorkspaceReportsLegacyNilStepReceiptDigest(t *testing.T) {
	workspace := constructionWorkspace(workspaceDocument("patients"))
	document, err := migrateStoredDocumentFixture(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0] = document
	workspace.Documents[0].Construction.Steps = nil
	raw, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"steps":[]`), []byte(`"steps":null`), 1)
	if !strings.Contains(string(raw), `"steps":null`) {
		t.Fatalf("test input has no nil step list: %s", raw)
	}
	legacyDigest, err := workspace.LegacyNilConstructionStepsDigest()
	if err != nil {
		t.Fatal(err)
	}
	result, err := MigrateStoredWorkspace(raw, CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if result.OriginalNilConstructionStepLists != 1 || result.OriginalLegacyNilStepsDigest != legacyDigest || !ContainsString(result.AcceptedOriginalDigests, legacyDigest) {
		t.Fatalf("legacy receipt digest relation missing: %#v want %q", result, legacyDigest)
	}
	if result.Workspace.Documents[0].Construction == nil || result.Workspace.Documents[0].Construction.Steps == nil {
		t.Fatalf("migration did not normalize empty construction steps: %#v", result.Workspace.Documents[0].Construction)
	}
	if !result.DigestChanged || result.OriginalLegacyNilStepsDigest == result.MigratedCanonicalDigest {
		t.Fatalf("nil-steps receipt was not invalidated: old=%q new=%q changed=%v", result.OriginalLegacyNilStepsDigest, result.MigratedCanonicalDigest, result.DigestChanged)
	}
}

func TestMigrateStoredWorkspaceRejectsAmbiguousPinnedCatalog(t *testing.T) {
	raw := []byte(`{"apiVersion":"` + APIVersion + `","kind":"` + WorkspaceKind + `","semanticsVersion":3,"explorer":{"title":"Patients"},"documents":[{"kind":"` + Kind + `","output":{"id":"patients","title":"Patients"},"rootResourceType":"Patient","route":{"occurrenceId":"base","resourceType":"Patient"},"columns":[{"column":"patient_count","label":"Patients","occurrenceId":"base","source":{"kind":"aggregate","aggregate":{"operation":"COUNT","where":{"path":"id","equals":"x"}}}}]}],"tabs":[{"id":"patients","title":"Patients","outputId":"patients","visible":true}]}`)
	catalog := commandCatalog()
	duplicate := catalog.Candidates[0]
	duplicate.ID = "same-path-other-id"
	catalog.Candidates = append(catalog.Candidates, duplicate)
	if _, err := MigrateStoredWorkspace(raw, catalog); err == nil || !strings.Contains(err.Error(), "matches multiple catalog candidates") {
		t.Fatalf("ambiguous migration error = %v", err)
	}
}
