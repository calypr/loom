package authoringv2

import (
	"encoding/json"
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
