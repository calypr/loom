package compilation

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestCompileAuthoringCorrelatedLookupCarriesSelectedPair(t *testing.T) {
	document := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "out", Title: "Output"}, RootResourceType: "Observation",
		Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"},
		Columns: []authoringv2.Column{{Column: "height_cm", Label: "Shared", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceObservationComponentByCode, Lookup: &authoringv2.LookupSource{
			Binding: &fhirschema.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"},
			Key:     &fhirschema.CorrelatedKey{System: "urn:study:A", Code: "shared.code"},
		}}}},
	}
	snapshot := fixtureSnapshotForProject("project")
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "n_observation", ResourceType: "Observation", RowRootEligible: true, RowGrain: "observation"})
	result, err := Compile(context.Background(), "project", "explorer", document, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	pivots := result.Bundle.Outputs[0].Pivots
	if len(pivots) != 1 || pivots[0].Correlation == nil || pivots[0].CorrelationSystem != "urn:study:A" || pivots[0].CorrelationCode != "shared.code" {
		t.Fatalf("compiled pivots = %#v", pivots)
	}
	if pivots[0].ColumnAliases["shared.code"] != "height_cm" || !isZeroRecipeExpression(pivots[0].ColumnExpr) || !isZeroRecipeExpression(pivots[0].ValueExpr) || len(pivots[0].ValueFallbacks) != 0 {
		t.Fatalf("correlated pivot retained legacy expressions or lost alias: %#v", pivots[0])
	}
}

func TestCompileAuthoringExtensionLookupCarriesOnlyTypedAncestry(t *testing.T) {
	document := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "out", Title: "Output"}, RootResourceType: "Observation",
		Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"},
		Columns: []authoringv2.Column{{Column: "left_leaf", Label: "Left leaf", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceExtensionByURL, Lookup: &authoringv2.LookupSource{Extension: &fhirschema.ExtensionBinding{
			OwnerPath: "extension[].extension[]", URLPath: []string{"urn:parent:left", "urn:leaf"}, ValuePath: "valueString", LogicalType: "string", ChoiceArms: []string{"valueString"},
		}, ProjectionMode: "ALL"}}}},
	}
	snapshot := fixtureSnapshotForProject("project")
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "n_observation", ResourceType: "Observation", RowRootEligible: true, RowGrain: "observation"})
	result, err := Compile(context.Background(), "project", "explorer", document, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	pivots := result.Bundle.Outputs[0].Pivots
	if len(pivots) != 1 || pivots[0].ExtensionCorrelation == nil || len(pivots[0].ExtensionCorrelation.URLPath) != 2 {
		t.Fatalf("compiled extension pivots = %#v", pivots)
	}
	if pivots[0].ProjectionMode != "ALL" || !isZeroRecipeExpression(pivots[0].ColumnExpr) || !isZeroRecipeExpression(pivots[0].ValueExpr) || len(pivots[0].ValueFallbacks) != 0 {
		t.Fatalf("extension pivot retained legacy expressions or lost projection: %#v", pivots[0])
	}
}

func isZeroRecipeExpression(value recipe.Expression) bool {
	return value.Select == "" && value.Call == "" && value.Literal == nil && value.Document == nil && len(value.Args) == 0
}

func TestCapabilityConceptCandidatesReachAuthoringCatalog(t *testing.T) {
	snapshot := fixtureSnapshotForProject("project")
	snapshot.Candidates[0].ConceptCandidates = []capability.ConceptCandidate{{
		SourceResourceType: "Observation", SourcePath: "code", SourceCanonical: "Observation.code", SourceProfile: "profile:test", OwningScope: "component[]",
		ExtensionURLPath: []string{"parent", "leaf"}, KeySelector: "code.coding[]", System: "urn:study:A", Code: "shared",
		ValueSelector: "valueQuantity.value", ChoiceArm: "valueQuantity", LogicalType: "decimal", ObservedUnits: []string{"cm"},
		Completeness: "COMPLETE", Status: "SUPPORTED", Population: 2, Examples: []string{"111"}, ExamplesTruncated: true, RuleHint: "paired", RuleVersion: "v1",
	}}
	catalog := catalogFromCapability(snapshot, "explorer")
	if len(catalog.Candidates) == 0 || len(catalog.Candidates[0].ConceptCandidates) != 1 {
		t.Fatalf("catalog candidates = %#v", catalog.Candidates)
	}
	concept := catalog.Candidates[0].ConceptCandidates[0]
	if concept.System != "urn:study:A" || concept.Code != "shared" || concept.SourceCanonical != "Observation.code" || concept.SourceProfile != "profile:test" || concept.ExtensionURLPath[1] != "leaf" || concept.Examples[0] != "111" || !concept.ExamplesTruncated || concept.RuleHint != "paired" || concept.RuleVersion != "v1" {
		t.Fatalf("catalog concept candidate = %#v", concept)
	}
}
