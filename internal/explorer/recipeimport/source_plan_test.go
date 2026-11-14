package recipeimport

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestSourcePlanDescribesNativeOutputStagesWithStableOwnerPaths(t *testing.T) {
	bundle := defaultSourcePlanFixture(t)
	var documentReference recipe.Output
	var groupMember recipe.Output
	for _, output := range bundle.Outputs {
		switch output.Name {
		case "DocumentReference":
			documentReference = output
		case "GroupMember":
			groupMember = output
		}
	}
	if documentReference.Name == "" || groupMember.Name == "" {
		t.Fatal("default source fixture is missing expected outputs")
	}

	plan := SourcePlan(documentReference)
	if err := plan.Validate(); err != nil {
		t.Fatalf("generated source plan is invalid: %v", err)
	}
	wantKinds := []string{
		SourcePlanFieldGroup,
		SourcePlanDynamicColumn,
		SourcePlanExtensionColumn,
		SourcePlanTraversal,
		SourcePlanCatalogProjection,
		SourcePlanPivot,
	}
	for _, kind := range wantKinds {
		if !sourcePlanHasKind(plan, kind) {
			t.Errorf("source plan has no %s step", kind)
		}
	}
	fieldGroup, ok := sourcePlanStep(plan, "fields")
	if !ok || !strings.Contains(fieldGroup.Summary, "id, identifier, status") {
		t.Fatalf("field group summary does not expose configured field names: %#v, found=%t", fieldGroup, ok)
	}
	patientTraversal, ok := sourcePlanStep(plan, "traversals[0].traversals[0]")
	if !ok || patientTraversal.Kind != SourcePlanTraversal || !strings.Contains(patientTraversal.Title, "Patient") || patientTraversal.ParentID != "traversals[0]" {
		t.Fatalf("nested Patient traversal = %#v, found=%t", patientTraversal, ok)
	}
	patientCatalog, ok := sourcePlanStep(plan, "traversals[0].traversals[0].catalogProjections[0]")
	if !ok || patientCatalog.ParentID != patientTraversal.ID {
		t.Fatalf("nested catalog projection = %#v, found=%t", patientCatalog, ok)
	}
	groupPlan := SourcePlan(groupMember)
	expansion, ok := sourcePlanStep(groupPlan, "expand")
	if !ok || expansion.Kind != SourcePlanExpansion || !expansion.Editable || !expansion.Enabled {
		t.Fatalf("expansion step must expose editable source settings: %#v, found=%t", expansion, ok)
	}
	identity, ok := sourcePlanStep(groupPlan, "identity")
	if !ok || identity.Kind != SourcePlanIdentity || !identity.Editable || !identity.Enabled {
		t.Fatalf("identity step must expose editable source settings: %#v, found=%t", identity, ok)
	}
}

func TestApplySourcePlanAppliesNestedEnablementAndBoundedOverridesWithoutMutatingSource(t *testing.T) {
	bundle := defaultSourcePlanFixture(t)
	output := outputNamed(t, bundle, "DocumentReference")
	original, err := cloneOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	plan := SourcePlan(output)
	setSourcePlanEnabled(t, &plan, "traversals[0].traversals[0].catalogProjections[0]", false)
	setSourcePlanEnabled(t, &plan, "traversals[0].traversals[0].dynamicColumns[0]", false)
	setSourcePlanEnabled(t, &plan, "traversals[0].traversals[0]", false)
	setSourcePlanEnabled(t, &plan, "fields", false)

	maxStep, ok := sourcePlanStep(plan, "dynamicColumns[0]")
	if !ok || maxStep.MaxColumns == nil || maxStep.ValueMode == "" {
		t.Fatalf("root dynamic column did not advertise its native settings: %#v, found=%t", maxStep, ok)
	}
	newMaxColumns := *maxStep.MaxColumns / 2
	if newMaxColumns < 1 {
		newMaxColumns = 1
	}
	if err := updateSourcePlanStep(&plan, maxStep.ID, func(step *authoringv2.ImportedSourceStep) {
		step.MaxColumns = &newMaxColumns
		step.ValueMode = "ALL"
	}); err != nil {
		t.Fatal(err)
	}
	beforeStepCount := len(plan.Steps)

	got, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatalf("ApplySourcePlan: %v", err)
	}
	if len(plan.Steps) != beforeStepCount {
		t.Fatal("applying the source plan removed disabled steps from the durable plan")
	}
	if len(got.Fields) != 0 {
		t.Fatalf("disabled root field group remains: %#v", got.Fields)
	}
	if got.DynamicColumns[0].MaxColumns != newMaxColumns || got.DynamicColumns[0].ValueMode != recipe.ValueModeAll {
		t.Fatalf("root dynamic override = max %d, mode %q", got.DynamicColumns[0].MaxColumns, got.DynamicColumns[0].ValueMode)
	}
	if len(got.Traversals) != len(output.Traversals) {
		t.Fatalf("outer traversals = %d, want %d", len(got.Traversals), len(output.Traversals))
	}
	if got.Traversals[0].Name != "subject_Specimen" {
		t.Fatalf("first retained traversal = %q", got.Traversals[0].Name)
	}
	if len(got.Traversals[0].DynamicColumns) != 2 || len(got.Traversals[0].CatalogProjections) != 1 {
		t.Fatalf("nested projections were not disabled: dynamic=%d catalog=%d", len(got.Traversals[0].DynamicColumns), len(got.Traversals[0].CatalogProjections))
	}
	if len(got.Traversals[0].Traversals) != 0 {
		t.Fatalf("disabled Patient traversal remains executable: %#v", got.Traversals[0].Traversals)
	}
	if !reflect.DeepEqual(output, original) {
		t.Fatal("ApplySourcePlan mutated the pinned source output")
	}

	setSourcePlanEnabled(t, &plan, "traversals[0].traversals[0]", true)
	reenabled, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatalf("re-enable disabled traversal: %v", err)
	}
	if len(reenabled.Traversals[0].Traversals) != 1 {
		t.Fatalf("re-enabled Patient traversal count = %d, want 1", len(reenabled.Traversals[0].Traversals))
	}
	if len(reenabled.Traversals[0].Traversals[0].DynamicColumns) != 1 || len(reenabled.Traversals[0].Traversals[0].CatalogProjections) != 0 {
		t.Fatalf("re-enabled traversal lost its disabled child settings: %#v", reenabled.Traversals[0].Traversals[0])
	}
}

func TestApplySourcePlanRejectsStepsAndOverridesOutsidePinnedSource(t *testing.T) {
	bundle := defaultSourcePlanFixture(t)
	output := outputNamed(t, bundle, "DocumentReference")
	base := SourcePlan(output)

	tests := []struct {
		name   string
		mutate func(*testing.T, *authoringv2.ImportedSourcePlan)
	}{
		{
			name: "forged step id",
			mutate: func(_ *testing.T, plan *authoringv2.ImportedSourcePlan) {
				plan.Steps[0].ID = "forged"
			},
		},
		{
			name: "forged title",
			mutate: func(_ *testing.T, plan *authoringv2.ImportedSourcePlan) {
				plan.Steps[0].Title = "Invented recipe step"
			},
		},
		{
			name: "missing step",
			mutate: func(_ *testing.T, plan *authoringv2.ImportedSourcePlan) {
				plan.Steps = plan.Steps[:len(plan.Steps)-1]
			},
		},
		{
			name: "maximum raised above pinned limit",
			mutate: func(t *testing.T, plan *authoringv2.ImportedSourcePlan) {
				step, _ := sourcePlanStep(*plan, "dynamicColumns[0]")
				value := *step.MaxColumns + 1
				if err := updateSourcePlanStep(plan, step.ID, func(step *authoringv2.ImportedSourceStep) { step.MaxColumns = &value }); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "settings added to field group",
			mutate: func(t *testing.T, plan *authoringv2.ImportedSourcePlan) {
				value := 1
				if err := updateSourcePlanStep(plan, "fields", func(step *authoringv2.ImportedSourceStep) { step.MaxColumns = &value }); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "invalid value mode",
			mutate: func(t *testing.T, plan *authoringv2.ImportedSourcePlan) {
				if err := updateSourcePlanStep(plan, "dynamicColumns[0]", func(step *authoringv2.ImportedSourceStep) { step.ValueMode = "ZIP" }); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := cloneSourcePlan(base)
			test.mutate(t, &plan)
			if _, err := ApplySourcePlan(output, &plan); err == nil {
				t.Fatal("ApplySourcePlan accepted a source plan outside the pinned source")
			}
		})
	}

	groupMember := outputNamed(t, bundle, "GroupMember")
	readOnlyPlan := SourcePlan(groupMember)
	setSourcePlanEnabled(t, &readOnlyPlan, "expand", false)
	if _, err := ApplySourcePlan(groupMember, &readOnlyPlan); err == nil {
		t.Fatal("ApplySourcePlan accepted disabling a read-only expansion step")
	}
}

func TestImportedSourcePlanValidationChecksHierarchyAndDocumentOwnership(t *testing.T) {
	plan := authoringv2.ImportedSourcePlan{Steps: []authoringv2.ImportedSourceStep{
		{ID: "traversals[0]", Kind: SourcePlanTraversal, Title: "Traversal", Summary: "A relationship.", Enabled: true, Editable: true},
		{ID: "traversals[0].fields", ParentID: "traversals[0]", Kind: SourcePlanFieldGroup, Title: "Fields", Summary: "Source fields.", Enabled: true, Editable: true},
	}}
	if err := plan.Validate(); err != nil {
		t.Fatalf("valid source plan: %v", err)
	}
	plan.Steps[1].ParentID = "missing"
	if err := plan.Validate(); err == nil {
		t.Fatal("source plan with a missing parent was accepted")
	}

	document := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "source", Title: "Source"},
		RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Rows: authoringv2.RecordsRowDefinition(), ImportedSource: &authoringv2.ImportedSourcePlan{},
	}
	if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "requires an importedRecipe") {
		t.Fatalf("source plan without pin validation error = %v", err)
	}
}

func TestImportWorkspacePersistsGeneratedSourcePlan(t *testing.T) {
	bundle := defaultSourcePlanFixture(t)
	schemas := make(map[string][]recipe.StageColumn, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		schemas[output.Name] = []recipe.StageColumn{{ID: "stable_" + output.Name, Name: "id", Type: "string"}}
	}
	workspace, err := ImportWorkspace(bundle, catalogForBundle(bundle), schemas)
	if err != nil {
		t.Fatalf("ImportWorkspace: %v", err)
	}
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := authoringv2.DecodeWorkspace(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Documents) != len(bundle.Outputs) {
		t.Fatalf("documents = %d, want %d", len(restored.Documents), len(bundle.Outputs))
	}
	for index, document := range restored.Documents {
		want := SourcePlan(bundle.Outputs[index])
		if document.ImportedSource == nil || !reflect.DeepEqual(*document.ImportedSource, want) {
			t.Fatalf("document %q source plan = %#v, want %#v", document.Output.ID, document.ImportedSource, want)
		}
	}
}

func defaultSourcePlanFixture(t *testing.T) recipe.Bundle {
	t.Helper()
	raw, err := os.ReadFile("testdata/default-dataframer.json")
	if err != nil {
		t.Fatal(err)
	}
	var bundle recipe.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("decode source-plan fixture: %v", err)
	}
	return bundle
}

func outputNamed(t *testing.T, bundle recipe.Bundle, name string) recipe.Output {
	t.Helper()
	for _, output := range bundle.Outputs {
		if output.Name == name {
			return output
		}
	}
	t.Fatalf("output %q not found", name)
	return recipe.Output{}
}

func sourcePlanHasKind(plan authoringv2.ImportedSourcePlan, kind string) bool {
	for _, step := range plan.Steps {
		if step.Kind == kind {
			return true
		}
	}
	return false
}

func sourcePlanStep(plan authoringv2.ImportedSourcePlan, id string) (authoringv2.ImportedSourceStep, bool) {
	for _, step := range plan.Steps {
		if step.ID == id {
			return step, true
		}
	}
	return authoringv2.ImportedSourceStep{}, false
}

func setSourcePlanEnabled(t *testing.T, plan *authoringv2.ImportedSourcePlan, id string, enabled bool) {
	t.Helper()
	if err := updateSourcePlanStep(plan, id, func(step *authoringv2.ImportedSourceStep) { step.Enabled = enabled }); err != nil {
		t.Fatal(err)
	}
}

func updateSourcePlanStep(plan *authoringv2.ImportedSourcePlan, id string, update func(*authoringv2.ImportedSourceStep)) error {
	for index := range plan.Steps {
		if plan.Steps[index].ID == id {
			update(&plan.Steps[index])
			return nil
		}
	}
	return fmt.Errorf("source-plan step %q not found", id)
}

func cloneSourcePlan(plan authoringv2.ImportedSourcePlan) authoringv2.ImportedSourcePlan {
	steps := make([]authoringv2.ImportedSourceStep, len(plan.Steps))
	copy(steps, plan.Steps)
	for index := range steps {
		if steps[index].MaxColumns != nil {
			value := *steps[index].MaxColumns
			steps[index].MaxColumns = &value
		}
	}
	return authoringv2.ImportedSourcePlan{Steps: steps}
}
