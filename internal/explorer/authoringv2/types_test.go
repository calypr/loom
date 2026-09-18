package authoringv2

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAggregateSourceAcceptsClosedOrderedTemporalReduction(t *testing.T) {
	raw := []byte(`{"kind":"aggregate","aggregate":{"operation":"FIRST_ORDERED","path":"valueQuantity.value","temporal":{"timestampPath":"effectiveDateTime","anchorPath":"meta.lastUpdated","lowerOffsetSeconds":-86400,"upperOffsetSeconds":0,"lowerInclusive":true,"upperInclusive":true,"direction":"DESC","precision":"INSTANT","tiePolicy":"REQUIRE_UNIQUE"}}}`)
	var source ColumnSource
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if err := source.validate("source"); err != nil {
		t.Fatalf("ordered temporal source rejected: %v", err)
	}
	if source.Aggregate == nil || source.Aggregate.Temporal == nil || source.Aggregate.Temporal.AnchorPath != "meta.lastUpdated" {
		t.Fatalf("ordered temporal source = %#v", source)
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
			{ID: "patient-id", NodeID: "patient", Label: "ID", LogicalType: "string", ProjectionModes: []string{"SCALAR"}, DefaultProjectionMode: "SCALAR"},
			{ID: "encounter-id", NodeID: "encounter", Label: "ID", LogicalType: "string", ProjectionModes: []string{"SCALAR"}, DefaultProjectionMode: "SCALAR"},
		},
		RoutePolicy: RoutePolicy{Unbounded: true, AllowRepeatedEdges: true, AllowSelfLoops: true},
	}
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
	repeatedCatalog.Candidates = append(repeatedCatalog.Candidates, CatalogCandidate{ID: "patient-family", NodeID: "patient", FieldPath: "name[].family", LogicalType: "string", Repeated: true, ProjectionModes: []string{"ALL"}})
	repeated := ContributorPredicate{CandidateID: "patient-family", Operator: ContributorEquals, Value: &ContributorValue{Kind: ContributorString, String: stringPtr("Smith")}}
	if err := ValidateContributorForCatalog(document, repeatedCatalog, RootOccurrenceID, document.Columns[1].Source, repeated); err == nil || !strings.Contains(err.Error(), "requires explicit ANY") {
		t.Fatalf("repeated predicate error=%v", err)
	}
	repeated.Quantifier = ContributorAny
	if err := ValidateContributorForCatalog(document, repeatedCatalog, RootOccurrenceID, document.Columns[1].Source, repeated); err != nil {
		t.Fatalf("repeated ANY predicate rejected: %v", err)
	}
}
