package semantic

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestLowerConceptSelectionsPreservesIdentityAndRepeatedArrays(t *testing.T) {
	catalog := Result{ResourceType: "Patient", Concepts: []Concept{
		{ID: "patient.birth", RuleID: "future.direct.v9", Label: "Birth date", Source: SourceDescriptor{ResourceType: "Patient", Path: "birthDate"}, Output: OutputDescriptor{Mode: OutputScalar, ValueType: "date", Selection: Selection{Mode: OutputScalar, SourcePath: "Patient", ValueSelector: "birthDate"}}},
		{ID: "patient.identifiers", RuleID: "future.identifier.v2", Label: "Identifiers", Source: SourceDescriptor{ResourceType: "Patient", Path: "identifier[]", Repeated: true}, Output: OutputDescriptor{Mode: OutputScalar, ValueType: "string", Cardinality: CardinalityRepeated, Selection: Selection{Mode: OutputScalar, SourcePath: "Patient", ValueSelector: "identifier[].value"}}},
	}}
	got, err := LowerConceptSelections("Patient", []recipe.ConceptSelection{
		{ConceptID: "patient.birth", RuleID: "future.direct.v9", ColumnName: "birth_date", Label: "Research birth date"},
		{ConceptID: "patient.identifiers", RuleID: "future.identifier.v2", ColumnName: "identifiers"},
	}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Fields) != 2 || got.Fields[0].Expr.Select != "root.birthDate" {
		t.Fatalf("unexpected lowered fields: %#v", got.Fields)
	}
	if got.Fields[0].Label != "Research birth date" || got.Fields[0].ConceptID != "patient.birth" {
		t.Fatalf("authored metadata was not retained: %#v", got.Fields[0])
	}
	if got.Fields[1].ValueMode != recipe.ValueModeAll || got.Fields[1].Expr.Select != "root.identifier[].value" {
		t.Fatalf("repeated concept was not lowered as an array: %#v", got.Fields[1])
	}
}

func TestLowerConceptSelectionsRejectsStaleAndMismatchedIdentity(t *testing.T) {
	catalog := Result{ResourceType: "Patient", Concepts: []Concept{{ID: "patient.birth", RuleID: "direct.v1", Source: SourceDescriptor{ResourceType: "Patient", Path: "birthDate"}, Output: OutputDescriptor{Selection: Selection{SourcePath: "Patient", ValueSelector: "birthDate"}}}}}
	_, err := LowerConceptSelections("Patient", []recipe.ConceptSelection{{ConceptID: "patient.missing", RuleID: "direct.v1", ColumnName: "missing"}}, catalog)
	if err == nil || !strings.Contains(err.Error(), "CONCEPT_NOT_FOUND") {
		t.Fatalf("stale concept error=%v", err)
	}
	_, err = LowerConceptSelections("Patient", []recipe.ConceptSelection{{ConceptID: "patient.birth", RuleID: "old.v1", ColumnName: "birth"}}, catalog)
	if err == nil || !strings.Contains(err.Error(), "CONCEPT_RULE_MISMATCH") {
		t.Fatalf("rule mismatch error=%v", err)
	}
}

func TestLowerBundleConceptSelectionsUsesUnknownRuleMetadata(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "semantic", TranslationVersion: "v1", Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", ConceptSelections: []recipe.ConceptSelection{{ConceptID: "patient.future", RuleID: "FutureClinicalSource.rule.v8", ColumnName: "future_score"}}}}}
	catalog := map[string]Result{"Patient": {ResourceType: "Patient", Concepts: []Concept{{ID: "patient.future", RuleID: "FutureClinicalSource.rule.v8", Source: SourceDescriptor{ResourceType: "Patient", Path: "birthDate"}, Output: OutputDescriptor{Mode: "future_value_family", ValueType: "futureDecimal128", Selection: Selection{Mode: "future_value_family", SourcePath: "Patient", ValueSelector: "birthDate"}}}}}}
	lowered, err := LowerBundleConceptSelections(bundle, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(lowered.Outputs[0].Fields) != 1 || lowered.Outputs[0].Fields[0].RuleID != "FutureClinicalSource.rule.v8" {
		t.Fatalf("unknown future rule was not retained: %#v", lowered.Outputs[0].Fields)
	}
	if len(lowered.Outputs[0].ConceptSelections) != 1 {
		t.Fatalf("authored concept selection was dropped")
	}
	plan, err := BuildRecipePlanWithConcepts(bundle, recipe.RuntimeBindings{}, catalog)
	if err != nil {
		t.Fatalf("build lowered concept plan: %v", err)
	}
	if len(plan.Outputs) != 1 || len(plan.Outputs[0].ConceptColumns) != 1 || plan.Outputs[0].ConceptColumns[0].ConceptID != "patient.future" || plan.Outputs[0].ConceptColumns[0].Selector.ValueSelector != "birthDate" {
		t.Fatalf("concept audit metadata did not reach semantic plan: %#v", plan.Outputs[0].ConceptColumns)
	}
}

func TestLowerDynamicConceptUsesItemSelectorsWithoutRowExpansion(t *testing.T) {
	catalog := Result{ResourceType: "Patient", Concepts: []Concept{{ID: "patient.identifiers", RuleID: "identifier.v1", Source: SourceDescriptor{ResourceType: "Patient", Path: "identifier[]", Repeated: true}, Output: OutputDescriptor{Mode: OutputDynamicFamily, Cardinality: CardinalityRepeated, Selection: Selection{Mode: OutputDynamicFamily, ItemSource: "identifier[]", KeySelector: "system", ValueSelector: "value"}}}}}
	got, err := LowerConceptSelections("Patient", []recipe.ConceptSelection{{ConceptID: "patient.identifiers", RuleID: "identifier.v1", ColumnName: "identifier_values"}}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.DynamicColumns) != 1 || got.DynamicColumns[0].Source.Select != "root.identifier[]" || got.DynamicColumns[0].Key.Select != "item.system" || got.DynamicColumns[0].Value.Select != "item.value" {
		t.Fatalf("unexpected dynamic lowering: %#v", got.DynamicColumns)
	}
	if len(got.Fields) != 0 {
		t.Fatalf("dynamic concept unexpectedly expanded into fields")
	}
}
