package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
)

func TestAggregateSourceAcceptsContributorWindowsAndKeepsOrderingFirstOrderedOnly(t *testing.T) {
	window := `"contributorWindow":{"timestampPath":"effectiveDateTime","anchorPath":"meta.lastUpdated","lowerOffsetSeconds":-86400,"upperOffsetSeconds":0,"lowerInclusive":true,"upperInclusive":false,"precision":"INSTANT"}`
	for _, operation := range []string{"COUNT", "EXISTS", "MIN", "MAX", "MEAN", "SUM"} {
		t.Run(operation, func(t *testing.T) {
			path := ""
			if operation != "COUNT" && operation != "EXISTS" {
				path = `"path":"valueQuantity.value",`
			}
			raw := `{"kind":"aggregate","aggregate":{"operation":"` + operation + `",` + path + window + `}}`
			var source ColumnSource
			if err := json.Unmarshal([]byte(raw), &source); err != nil {
				t.Fatal(err)
			}
			if err := source.validate("source"); err != nil {
				t.Fatalf("windowed %s source rejected: %v", operation, err)
			}
			if source.Aggregate == nil || source.Aggregate.ContributorWindow == nil || source.Aggregate.Ordering != nil {
				t.Fatalf("windowed source = %#v", source)
			}
		})
	}
	for _, operation := range []string{"COUNT_DISTINCT", "DISTINCT_VALUES", "CONTAINS_ALL", "REQUIRE_ONE", "COLLECT"} {
		t.Run(operation+"_rejects_window", func(t *testing.T) {
			raw := `{"kind":"aggregate","aggregate":{"operation":"` + operation + `","path":"status",` + window + `}}`
			var source ColumnSource
			if err := json.Unmarshal([]byte(raw), &source); err != nil {
				t.Fatal(err)
			}
			if err := source.validate("source"); err == nil || !strings.Contains(err.Error(), "contributorWindow is not supported") {
				t.Fatalf("windowed %s source error = %v, want unsupported operation", operation, err)
			}
		})
	}

	ordered := `{"kind":"aggregate","aggregate":{"operation":"FIRST_ORDERED","path":"valueQuantity.value",` + window + `,"ordering":{"timestampPath":"effectiveDateTime","direction":"DESC","tiePolicy":"REQUIRE_UNIQUE"}}}`
	var source ColumnSource
	if err := json.Unmarshal([]byte(ordered), &source); err != nil {
		t.Fatal(err)
	}
	if err := source.validate("source"); err != nil {
		t.Fatalf("ordered contributor window rejected: %v", err)
	}
	if source.Aggregate == nil || source.Aggregate.ContributorWindow == nil || source.Aggregate.Ordering == nil || source.Aggregate.Ordering.TimestampPath != "effectiveDateTime" {
		t.Fatalf("ordered contributor window = %#v", source)
	}
	for _, operation := range []string{"COUNT", "SUM"} {
		t.Run(operation+"_rejects_ordering", func(t *testing.T) {
			raw := `{"kind":"aggregate","aggregate":{"operation":"` + operation + `","path":"valueQuantity.value","ordering":{"timestampPath":"effectiveDateTime","direction":"DESC","tiePolicy":"RESOURCE_KEY"}}}`
			var source ColumnSource
			if err := json.Unmarshal([]byte(raw), &source); err != nil {
				t.Fatal(err)
			}
			if err := source.validate("source"); err == nil || !strings.Contains(err.Error(), "ordering is only valid for FIRST_ORDERED") {
				t.Fatalf("%s ordering error = %v, want FIRST_ORDERED-only error", operation, err)
			}
		})
	}

	var legacy ColumnSource
	if err := json.Unmarshal([]byte(`{"kind":"aggregate","aggregate":{"operation":"FIRST_ORDERED","path":"valueQuantity.value","temporal":{}}}`), &legacy); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("legacy temporal field was accepted on writable source JSON: %v", err)
	}
}

func TestAggregateSourceRejectsLegacyWhereOnCurrentWire(t *testing.T) {
	var source ColumnSource
	err := json.Unmarshal([]byte(`{"kind":"aggregate","aggregate":{"operation":"COUNT","where":{"path":"status","equals":"final"}}}`), &source)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("legacy aggregate where was writable: %v", err)
	}
}

func TestRetiredCodedLookupKindsAreNotWritable(t *testing.T) {
	for _, kind := range []string{"codingBySystem", "observationComponentByCode"} {
		t.Run(kind, func(t *testing.T) {
			raw := `{"kind":"` + kind + `","lookup":{"binding":{"keyPath":"code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","logicalType":"decimal"},"key":{"system":"urn:study","code":"height"}}}`
			var source ColumnSource
			if err := json.Unmarshal([]byte(raw), &source); err == nil || !strings.Contains(err.Error(), `unsupported source kind "`+kind+`"`) {
				t.Fatalf("retired source was accepted by the current JSON decoder: %v", err)
			}
		})
	}
}

func TestAggregateUnitNormalizationAcceptsPresetAndRejectsTechnicalFields(t *testing.T) {
	var source ColumnSource
	if err := json.Unmarshal([]byte(`{"kind":"aggregate","aggregate":{"operation":"MIN","path":"valueQuantity.value","unitNormalization":{"policyId":"to-centimeters","version":"1"}}}`), &source); err != nil {
		t.Fatal(err)
	}
	if err := source.validate("source"); err != nil {
		t.Fatalf("preset normalization rejected: %v", err)
	}
	if source.Aggregate == nil || source.Aggregate.UnitNormalization == nil || source.Aggregate.UnitNormalization.PolicyID != "to-centimeters" {
		t.Fatalf("normalized source = %#v", source)
	}
	var countSource ColumnSource
	if err := json.Unmarshal([]byte(`{"kind":"aggregate","aggregate":{"operation":"COUNT","path":"valueQuantity.value","unitNormalization":{"policyId":"to-centimeters","version":"1"}}}`), &countSource); err != nil {
		t.Fatal(err)
	}
	if err := countSource.validate("source"); err == nil || !strings.Contains(err.Error(), "not supported for COUNT") {
		t.Fatalf("count normalization error = %v", err)
	}
	if err := json.Unmarshal([]byte(`{"kind":"aggregate","aggregate":{"operation":"MIN","path":"valueQuantity.value","unitNormalization":{"systemPath":"valueQuantity.system","codePath":"valueQuantity.code","target":{"system":"http://unitsofmeasure.org","code":"cm"},"rules":[]}}}`), &ColumnSource{}); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("technical unit fields were accepted: %v", err)
	}
}

func testCatalog() CatalogSnapshot {
	nodes := []CatalogNode{
		{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
		{ID: "encounter", ResourceType: "Encounter", RowRootEligible: true},
	}
	edges := []CatalogEdge{
		{ID: "patient-encounter", FromNodeID: "patient", ToNodeID: "encounter", Label: "encounters"},
		{ID: "encounter-self", FromNodeID: "encounter", ToNodeID: "encounter", Label: "revisits"},
	}
	return CatalogSnapshot{
		APIVersion: APIVersion, Kind: CatalogKind, Project: "p", ExplorerID: "e",
		SourceGeneration: "g", AuthorizationScopeDigest: "scope", SnapshotToken: "sha256:snapshot",
		Complete: true, Nodes: nodes, Edges: edges,
		Candidates: []CatalogCandidate{
			{ID: "patient-id", NodeID: "patient", FieldPath: "id", Cardinality: "optional_one", Label: "ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: testFieldConstructionChoice("sha256:snapshot", "patient-id", "patient", "Patient", "id", "optional_one", capability.ProjectionScalar)},
			{ID: "encounter-id", NodeID: "encounter", FieldPath: "id", Cardinality: "optional_one", Label: "ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: testFieldConstructionChoice("sha256:snapshot", "encounter-id", "encounter", "Encounter", "id", "optional_one", capability.ProjectionScalar)},
		},
		RoutePolicy: RoutePolicy{Unbounded: true, AllowRepeatedEdges: true, AllowSelfLoops: true},
	}
}

func TestCatalogDigestCanonicalizesConceptCandidateOrder(t *testing.T) {
	first := testCatalog()
	first.Candidates[0].ConceptCandidates = []ConceptCandidate{
		{SourceResourceType: "Patient", SourcePath: "z", ExtensionURLPath: []string{"z"}, ObservedUnits: []string{"unit-z"}, Examples: []string{"example-z"}, Completeness: "COMPLETE", Status: "OBSERVED"},
		{SourceResourceType: "Patient", SourcePath: "a", ExtensionURLPath: []string{"a"}, ObservedUnits: []string{"unit-a"}, Examples: []string{"example-a"}, Completeness: "COMPLETE", Status: "OBSERVED"},
	}
	second := first
	second.Candidates = append([]CatalogCandidate(nil), first.Candidates...)
	second.Candidates[0].ConceptCandidates = append([]ConceptCandidate(nil), first.Candidates[0].ConceptCandidates...)
	second.Candidates[0].ConceptCandidates[0], second.Candidates[0].ConceptCandidates[1] = second.Candidates[0].ConceptCandidates[1], second.Candidates[0].ConceptCandidates[0]
	firstDigest, err := first.Digest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("catalog digest changed with concept candidate order: %s != %s", firstDigest, secondDigest)
	}
	canonical, err := first.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	first.Candidates[0].ConceptCandidates[0].ExtensionURLPath[0] = "mutated"
	afterMutation, err := first.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) == string(afterMutation) {
		t.Fatal("canonical catalog JSON shared nested concept-candidate slices with its source")
	}
}

func testFieldConstructionChoice(snapshotToken, candidateID, nodeID, resourceType, path, cardinality string, projection capability.ProjectionMode) *capability.ConstructionChoice {
	choice, err := capability.NewFieldConstructionChoice(snapshotToken, capability.Candidate{
		ID: candidateID, NodeID: nodeID, ResourceType: resourceType, FieldPath: path,
		Cardinality: cardinality, LogicalType: "string", ProjectionModes: []capability.ProjectionMode{projection},
	})
	if err != nil {
		panic(err)
	}
	return &choice
}

func TestEmptyBuilderStateAndBackendLeakage(t *testing.T) {
	state := BuilderState{APIVersion: APIVersion, Kind: StateKind, Catalog: testCatalog()}
	if err := state.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := state.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"rowNodeId", "selector", "aql", "physicalCollection", "backend"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("backend field leaked: %q in %s", forbidden, raw)
		}
	}
}

func TestCatalogRejectsSelectableCandidateWithoutConstructionChoice(t *testing.T) {
	catalog := testCatalog()
	catalog.Candidates[0].ConstructionChoice = nil
	if err := catalog.Validate(); err == nil || !strings.Contains(err.Error(), "constructionChoice is required") {
		t.Fatalf("catalog validation error=%v, want missing constructionChoice", err)
	}
}

func TestContributorPredicateValidationUsesCatalogCardinalityAndKind(t *testing.T) {
	document := workspaceDocument("patients")
	document.Columns = append(document.Columns, Column{Column: "count", Label: "Count", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceAggregate, Aggregate: &AggregateSource{Operation: "COUNT"}}})
	catalog := commandCatalog()
	validString := ContributorPredicate{CandidateID: "patient-id", Operator: ContributorEquals, Value: &ContributorValue{Kind: ContributorString, String: stringPtr("active")}}
	if err := ValidateContributorForCatalog(document, catalog, RootOccurrenceID, document.Columns[1].Source, validString); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		predicate ContributorPredicate
		want      string
	}{
		{"exists value", ContributorPredicate{CandidateID: "patient-id", Operator: ContributorExists, Value: validString.Value}, "does not accept value"},
		{"equals missing value", ContributorPredicate{CandidateID: "patient-id", Operator: ContributorEquals}, "requires value"},
		{"scalar any", ContributorPredicate{CandidateID: "patient-id", Operator: ContributorExists, Quantifier: ContributorAny}, "scalar contributor candidate"},
		{"wrong value kind", ContributorPredicate{CandidateID: "patient-id", Operator: ContributorEquals, Value: &ContributorValue{Kind: ContributorValueCode, Code: &ContributorCode{Code: "active"}}}, "requires STRING"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateContributorForCatalog(document, catalog, RootOccurrenceID, document.Columns[1].Source, tc.predicate); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want substring %q", err, tc.want)
			}
		})
	}
	repeatedCatalog := catalog
	repeatedCatalog.Candidates = append(repeatedCatalog.Candidates, CatalogCandidate{ID: "patient-family", NodeID: "patient", FieldPath: "name[].family", Cardinality: "many", LogicalType: "string", Repeated: true, ProjectionModes: []string{"ALL"}, ConstructionChoice: testFieldConstructionChoice("sha256:snapshot", "patient-family", "patient", "Patient", "name[].family", "many", capability.ProjectionArray)})
	repeated := ContributorPredicate{CandidateID: "patient-family", Operator: ContributorEquals, Value: &ContributorValue{Kind: ContributorString, String: stringPtr("Smith")}}
	if err := ValidateContributorForCatalog(document, repeatedCatalog, RootOccurrenceID, document.Columns[1].Source, repeated); err == nil || !strings.Contains(err.Error(), "requires explicit ANY") {
		t.Fatalf("repeated predicate error=%v", err)
	}
	repeated.Quantifier = ContributorAny
	if err := ValidateContributorForCatalog(document, repeatedCatalog, RootOccurrenceID, document.Columns[1].Source, repeated); err != nil {
		t.Fatalf("repeated ANY predicate rejected: %v", err)
	}
}

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

func TestCatalogAggregateBatchMatchesIndividualChoices(t *testing.T) {
	base := CatalogSnapshot{
		Nodes: []CatalogNode{{ID: "observation", ResourceType: "Observation"}, {ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "report", ResourceType: "DiagnosticReport", RowRootEligible: true}},
		Candidates: []CatalogCandidate{
			{ID: "obs-id", NodeID: "observation", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one"},
			{ID: "patient-birth", NodeID: "patient", FieldPath: "birthDate", LogicalType: "date_time", Cardinality: "optional_one"},
			{ID: "obs-effective", NodeID: "observation", FieldPath: "effectiveDateTime", LogicalType: "date_time", Cardinality: "optional_one"},
			{ID: "obs-updated", NodeID: "observation", FieldPath: "meta.lastUpdated", LogicalType: "date_time", Cardinality: "optional_one"},
			{ID: "report-issued", NodeID: "report", FieldPath: "issued", LogicalType: "date_time", Cardinality: "optional_one"},
			{ID: "patient-updated", NodeID: "patient", FieldPath: "meta.lastUpdated", LogicalType: "date_time", Cardinality: "optional_one"},
			{ID: "obs-value", NodeID: "observation", FieldPath: "valueQuantity.value", LogicalType: "decimal", Cardinality: "optional_one", ConceptCandidates: []ConceptCandidate{{ValueSelector: "valueQuantity.value", ObservedUnits: []string{"kg", "cm", "furlong"}}}},
			{ID: "repeated", NodeID: "observation", FieldPath: "effectiveDateTime", LogicalType: "date_time", Cardinality: "many", Repeated: true},
			{ID: "missing-node", NodeID: "absent", FieldPath: "id", LogicalType: "string"},
		},
	}
	cases := map[string]CatalogSnapshot{"mixed": base, "empty": {}}
	noRoot := base
	noRoot.Nodes = append([]CatalogNode(nil), base.Nodes...)
	for i := range noRoot.Nodes {
		noRoot.Nodes[i].RowRootEligible = false
	}
	cases["no-root-anchors"] = noRoot
	duplicates := base
	duplicates.Candidates = append(append([]CatalogCandidate(nil), base.Candidates...), CatalogCandidate{ID: "obs-id", NodeID: "patient", LogicalType: "decimal", FieldPath: "id"})
	duplicates.Nodes = append(append([]CatalogNode(nil), base.Nodes...), CatalogNode{ID: "observation", ResourceType: "DiagnosticReport", RowRootEligible: true})
	cases["duplicate-identifiers"] = duplicates
	noTimestamps := base
	noTimestamps.Candidates = []CatalogCandidate{base.Candidates[0], base.Candidates[6], base.Candidates[8]}
	cases["no-timestamps"] = noTimestamps
	for name, catalog := range cases {
		t.Run(name, func(t *testing.T) {
			before, _ := json.Marshal(catalog)
			got := AggregateTransformationCapabilitiesForCatalogCandidates(catalog)
			if len(got) != len(catalog.Candidates) {
				t.Fatalf("results=%d candidates=%d", len(got), len(catalog.Candidates))
			}
			for i, candidate := range catalog.Candidates {
				want := AggregateTransformationCapabilitiesForCatalog(catalog, candidate.ID)
				if !reflect.DeepEqual(got[i], want) {
					t.Fatalf("candidate %s: batch=%#v individual=%#v", candidate.ID, got[i], want)
				}
			}
			after, _ := json.Marshal(catalog)
			if string(after) != string(before) {
				t.Fatal("batch mutated catalog")
			}
		})
	}
}

func TestCatalogAggregateBatchResultsDoNotShareMutableChoices(t *testing.T) {
	catalog := CatalogSnapshot{Nodes: []CatalogNode{{ID: "obs", ResourceType: "Observation", RowRootEligible: true}}, Candidates: []CatalogCandidate{
		{ID: "one", NodeID: "obs", FieldPath: "effectiveDateTime", LogicalType: "date_time", Cardinality: "optional_one"},
		{ID: "two", NodeID: "obs", FieldPath: "meta.lastUpdated", LogicalType: "date_time", Cardinality: "optional_one"},
	}}
	got := AggregateTransformationCapabilitiesForCatalogCandidates(catalog)
	want := AggregateTransformationCapabilitiesForCatalog(catalog, "two")
	got[0].Temporal.TimestampFields[0].Label = "changed"
	got[0].Temporal.AnchorFields[0].Label = "changed"
	got[0].UnitNormalization.Presets[0].Reason = "changed"
	if !reflect.DeepEqual(got[1], want) {
		t.Fatal("mutating one result changed another candidate's choices")
	}
}

func syntheticWideTransformationCatalog(size int) CatalogSnapshot {
	catalog := CatalogSnapshot{Nodes: []CatalogNode{{ID: "obs", ResourceType: "Observation"}, {ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "report", ResourceType: "DiagnosticReport", RowRootEligible: true}}}
	for i := 0; i < size; i++ {
		candidate := CatalogCandidate{ID: fmt.Sprintf("candidate-%05d", i), NodeID: "obs", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one"}
		switch i % 16 {
		case 0:
			candidate.FieldPath = "effectiveDateTime"
			candidate.LogicalType = "date_time"
		case 1:
			candidate.NodeID = "patient"
			candidate.FieldPath = "meta.lastUpdated"
			candidate.LogicalType = "date_time"
		case 2:
			candidate.NodeID = "report"
			candidate.FieldPath = "issued"
			candidate.LogicalType = "date_time"
		case 3:
			candidate.FieldPath = "valueQuantity.value"
			candidate.LogicalType = "decimal"
			candidate.ConceptCandidates = []ConceptCandidate{{ValueSelector: "valueQuantity.value", ObservedUnits: []string{"kg"}}}
		}
		catalog.Candidates = append(catalog.Candidates, candidate)
	}
	return catalog
}

var aggregateTransformationBenchmarkSink []AggregateTransformationCapabilities

func BenchmarkSyntheticWideCatalogAggregateTransformations(b *testing.B) {
	for _, size := range []int{256, 1024} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			catalog := syntheticWideTransformationCatalog(size)
			b.Run("individual", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					results := make([]AggregateTransformationCapabilities, len(catalog.Candidates))
					for j, candidate := range catalog.Candidates {
						results[j] = AggregateTransformationCapabilitiesForCatalog(catalog, candidate.ID)
					}
					aggregateTransformationBenchmarkSink = results
				}
			})
			b.Run("batch", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					aggregateTransformationBenchmarkSink = AggregateTransformationCapabilitiesForCatalogCandidates(catalog)
				}
			})
		})
	}
}

func TestStoredSourcePlanMigrationVerifiesLegacyDigestBeforeRemovingRules(t *testing.T) {
	document := workspaceDocument("patients")
	document.ImportedRecipe = &ImportedRecipeRef{Name: "pinned", TranslationVersion: "v1", Digest: strings.Repeat("a", 64), Output: "patients"}
	document.ImportedSource = &ImportedSourcePlan{Steps: []ImportedSourceStep{
		{ID: "output", Kind: "ROOT", Title: "Output", Summary: "Output"},
		{ID: "dynamicColumns[0]", Kind: "DYNAMIC_COLUMNS", Title: "Identifiers", Summary: "Identifiers", Editable: true},
	}}
	workspace := Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, SemanticsVersion: CurrentSemanticsVersion,
		Explorer: ExplorerMetadata{Title: "Legacy source"}, Documents: []Document{document},
		Tabs: []Tab{{ID: "patients", OutputID: "patients", Title: "Patients", Visible: true}},
	}
	current, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bytes.Replace(current, []byte(`"summary":"Output","editable":false`), []byte(`"summary":"Output","enabled":true,"editable":false`), 1)
	raw = bytes.Replace(raw, []byte(`"summary":"Identifiers","editable":true`), []byte(`"summary":"Identifiers","enabled":false,"editable":true`), 1)
	oldDigest := workspaceDigest(raw)

	converted, changed, err := ConvertStoredSourcePlan(raw, oldDigest)
	if err != nil || !changed {
		t.Fatalf("convert legacy source plan: changed=%v, err=%v", changed, err)
	}
	plan := converted.Documents[0].ImportedSource
	if len(plan.Steps) != 1 || plan.Steps[0].ID != "output" || !slices.Equal(plan.RemovedStepIDs, []string{"dynamicColumns[0]"}) {
		t.Fatalf("unexpected converted source plan: %+v", plan)
	}
	if _, _, err := ConvertStoredSourcePlan(bytes.Replace(raw, []byte(`"enabled":false`), []byte(`"enabled":true`), 1), oldDigest); err == nil {
		t.Fatal("migration accepted a draft that no longer matched the stored digest")
	}
	migration, err := MigrateStoredWorkspace(raw, CatalogSnapshot{})
	if err != nil || !slices.Contains(migration.AcceptedOriginalDigests, oldDigest) || bytes.Contains(migration.CanonicalJSON, []byte(`"enabled"`)) {
		t.Fatalf("legacy source not verified and converted by workspace migration: %v", err)
	}
	if _, changed, err := ConvertStoredSourcePlan(migration.CanonicalJSON, migration.MigratedCanonicalDigest); err != nil || changed {
		t.Fatalf("second conversion: changed=%v, err=%v", changed, err)
	}
}

func TestMigrateStoredWorkspaceRecognizesMixedNilStepDigest(t *testing.T) {
	w := constructionWorkspace(workspaceDocument("a"))
	w.Documents = append(w.Documents, workspaceDocument("b"))
	w.Tabs = append(w.Tabs, Tab{ID: "b", Title: "B", OutputID: "b", Order: 1, Visible: true})
	for i := range w.Documents {
		d, err := migrateStoredDocumentFixture(w.Documents[i])
		if err != nil {
			t.Fatal(err)
		}
		w.Documents[i] = d
		w.Documents[i].Construction.Steps = []ConstructionStep{}
	}
	canonical, err := w.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.LastIndex(canonical, []byte(`"steps":[]`))
	if at < 0 {
		t.Fatal(string(canonical))
	}
	raw := append([]byte(nil), canonical[:at]...)
	raw = append(raw, []byte(`"steps":null`)...)
	raw = append(raw, canonical[at+len(`"steps":[]`):]...)
	sum := sha256.Sum256(raw)
	want := "sha256:" + hex.EncodeToString(sum[:])
	migrated, err := MigrateStoredWorkspace(raw, CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(migrated.AcceptedOriginalDigests, want) {
		t.Fatalf("original mixed-null canonical digest %s absent from accepted %v; nil lists=%d", want, migrated.AcceptedOriginalDigests, migrated.OriginalNilConstructionStepLists)
	}
}
