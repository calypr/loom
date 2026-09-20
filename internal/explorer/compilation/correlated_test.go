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
	document := authoringv2.Document{Rows: authoringv2.RecordsRowDefinition(),
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "out", Title: "Output"}, RootResourceType: "Observation",
		Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"},
		Columns: []authoringv2.Column{{Column: "height_cm", Label: "Shared", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceCodedValue, Lookup: &authoringv2.LookupSource{
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

func TestCompileAuthoringOwnerRecordsCarriesLosslessBinding(t *testing.T) {
	binding := fhirschema.CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
		SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value",
		ChoiceArms: []string{"valueQuantity"}, LogicalType: "decimal", UnitPath: "valueQuantity.unit",
	}
	document := authoringv2.Document{Rows: authoringv2.RecordsRowDefinition(),
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "out", Title: "Output"}, RootResourceType: "Observation",
		Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"},
		Columns: []authoringv2.Column{{
			Column: "height_records", Label: "Height records", LogicalType: "object", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceOwnerRecords, OwnerRecords: &authoringv2.OwnerRecordsSource{
				Binding: binding, Key: fhirschema.CorrelatedKey{System: "http://loinc.org", Code: "8302-2"},
			}},
		}},
	}
	snapshot := fixtureSnapshotForProject("project")
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "n_observation", ResourceType: "Observation", RowRootEligible: true, RowGrain: "observation"})
	result, err := Compile(context.Background(), "project", "explorer", document, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	records := result.Bundle.Outputs[0].OwnerRecords
	if len(records) != 1 || records[0].Name != "height_records" || records[0].Binding.OwnerPath != "component[]" || records[0].Key.Code != "8302-2" {
		t.Fatalf("compiled owner records = %#v", records)
	}
	if len(result.EmittedColumns) != 1 || result.EmittedColumns[0].Shape != "record_list" || !result.EmittedColumns[0].Lossless || result.EmittedColumns[0].LogicalType != "object" || result.EmittedColumns[0].ProjectionMode != "OWNER_RECORDS" {
		t.Fatalf("owner-record emitted column = %#v", result.EmittedColumns)
	}
	if got := result.OutputContract.Columns[0]; got.Shape != "record_list" || got.LogicalType != "object" || !got.Lossless {
		t.Fatalf("owner-record output contract = %#v", got)
	}
}

func TestCompileCodedValueForMedicationRequestNestedOwner(t *testing.T) {
	const (
		ownerPath = "dosageInstruction[].doseAndRate[]"
		keyPath   = "dosageInstruction[].doseAndRate[].type.coding[]"
		valuePath = "doseQuantity.value"
	)
	document := authoringv2.Document{Rows: authoringv2.RecordsRowDefinition(),
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "medications", Title: "Medication requests"}, RootResourceType: "MedicationRequest",
		Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "MedicationRequest"},
		Columns: []authoringv2.Column{{Column: "daily_dose", Label: "Daily dose", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceCodedValue, Lookup: &authoringv2.LookupSource{
			Binding: &fhirschema.CorrelatedBinding{OwnerPath: ownerPath, KeyPath: keyPath, SystemPath: "system", CodePath: "code", ValuePath: valuePath, ChoiceArms: []string{"doseQuantity"}, LogicalType: "decimal"},
			Key:     &fhirschema.CorrelatedKey{System: "http://example.org/dose-types", Code: "daily-dose"},
		}}}},
	}
	snapshot := fixtureSnapshotForProject("project")
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "n_medication_request", ResourceType: "MedicationRequest", RowRootEligible: true, RowGrain: "resource"})
	result, err := Compile(context.Background(), "project", "explorer", document, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	pivots := result.Bundle.Outputs[0].Pivots
	if len(pivots) != 1 || pivots[0].Correlation == nil {
		t.Fatalf("compiled MedicationRequest pivots = %#v", pivots)
	}
	binding := pivots[0].Correlation
	if binding.OwnerPath != ownerPath || binding.KeyPath != keyPath || binding.ValuePath != valuePath || len(binding.ChoiceArms) != 1 || binding.ChoiceArms[0] != "doseQuantity" ||
		pivots[0].CorrelationSystem != "http://example.org/dose-types" || pivots[0].CorrelationCode != "daily-dose" {
		t.Fatalf("compiled MedicationRequest binding/key = %#v / %q / %q", binding, pivots[0].CorrelationSystem, pivots[0].CorrelationCode)
	}
	if len(result.EmittedColumns) != 1 || result.EmittedColumns[0].SourcePath != ownerPath+"."+valuePath {
		t.Fatalf("emitted MedicationRequest source path = %#v", result.EmittedColumns)
	}
}

func TestCompileAuthoringExtensionLookupCarriesOnlyTypedAncestry(t *testing.T) {
	document := authoringv2.Document{Rows: authoringv2.RecordsRowDefinition(),
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

func TestCompileAuthoringIdentifierBindingLowersToSystemKeyedDynamicColumn(t *testing.T) {
	document := authoringv2.Document{Rows: authoringv2.RecordsRowDefinition(),
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "out", Title: "Output"}, RootResourceType: "Patient",
		Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Columns: []authoringv2.Column{{Column: "case_id", Label: "Case ID", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceIdentifierBySystem, Lookup: &authoringv2.LookupSource{
			Identifier: &fhirschema.IdentifierBinding{OwnerPath: "identifier[]", SystemPath: "system", ValuePath: "value", SystemURI: "urn:study:case-id", LogicalType: "string"},
		}}}},
	}
	snapshot := fixtureSnapshotForProject("project")
	result, err := Compile(context.Background(), "project", "explorer", document, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	dynamics := result.Bundle.Outputs[0].DynamicColumns
	if len(dynamics) != 1 {
		t.Fatalf("compiled dynamic columns = %#v", dynamics)
	}
	dynamic := dynamics[0]
	if dynamic.Source.Select != "root.identifier[]" || dynamic.Key == nil || dynamic.Key.Select != "item.system" || dynamic.Value == nil || dynamic.Value.Select != "item.value" || dynamic.ColumnSourceKeys["case_id"] != "urn:study:case-id" {
		t.Fatalf("lowered identifier dynamic column = %#v", dynamic)
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
