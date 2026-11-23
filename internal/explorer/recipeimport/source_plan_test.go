package recipeimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

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
	if !ok || expansion.Kind != SourcePlanExpansion || !expansion.Editable {
		t.Fatalf("expansion step must expose editable source settings: %#v, found=%t", expansion, ok)
	}
	identity, ok := sourcePlanStep(groupPlan, "identity")
	if !ok || identity.Kind != SourcePlanIdentity || !identity.Editable {
		t.Fatalf("identity step must expose editable source settings: %#v, found=%t", identity, ok)
	}
}

func TestImportedIdentifierRuleAdditionIsValidatedAppliedAndHydrated(t *testing.T) {
	output := recipe.Output{Name: "patient", RootResourceType: "Patient", RowGrain: "resource"}
	key := recipe.Expression{Call: "last_segment", Args: []recipe.Expression{{Select: "item.system"}}}
	value := recipe.Expression{Select: "item.value"}
	parameters := &ws.ImportedSourceParameters{Dynamic: &ws.ImportedDynamicParameters{
		Name: "identifiers", Source: recipe.Expression{Select: "identifier[]"}, Key: &key, Value: &value,
		Outputs: []ws.ImportedDynamicOutput{{Key: "excluded", Enabled: false}},
	}}
	addition := ws.ImportedSourceRuleAddition{Kind: "DYNAMIC_COLUMN", StepID: "ruleAdditions[0]", Parameters: parameters}
	plan := SourcePlan(output)
	plan.RuleAdditions = []ws.ImportedSourceRuleAddition{addition}
	plan.Steps = append(plan.Steps, ruleAdditionStep(addition, ""))

	effective, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatalf("apply generated Identifier addition: %v", err)
	}
	if len(effective.DynamicColumns) != 1 || effective.DynamicColumns[0].Name != "identifiers" || effective.DynamicColumns[0].Source.Select != "root.identifier[]" {
		t.Fatalf("effective dynamic columns = %#v", effective.DynamicColumns)
	}
	if overrides := effective.DynamicColumns[0].OutputOverrides; len(overrides) != 1 || overrides[0].Key != "excluded" || overrides[0].Enabled {
		t.Fatalf("identifier rule lost output exclusion: %#v", overrides)
	}
	hydrated, err := HydrateSourcePlan(output, &plan)
	if err != nil {
		t.Fatalf("hydrate generated Identifier addition: %v", err)
	}
	if len(hydrated.RuleAdditions) != 1 || len(hydrated.Steps) != len(plan.Steps) || hydrated.Steps[len(hydrated.Steps)-1].ID != addition.StepID {
		t.Fatalf("hydrated source plan lost editable addition: %#v", hydrated)
	}

	unsupported := output
	unsupported.RootResourceType = "NotAResource"
	if _, err := ApplySourcePlan(unsupported, &plan); err == nil {
		t.Fatal("accepted an Identifier addition without generated FHIR resource metadata")
	}

	parameters.Dynamic.Value = &key
	if _, err := ApplySourcePlan(output, &plan); err == nil {
		t.Fatal("accepted an Identifier addition with an unpaired value selector")
	}
}

func TestRootRuleWithQualifiedSelectorReloadsAndSavesRelative(t *testing.T) {
	output := recipe.Output{Name: "patient", RootResourceType: "Patient", RowGrain: "resource"}
	plan := SourcePlan(output)
	key, value := recipe.Expression{Select: "item.system"}, recipe.Expression{Select: "item.value"}
	params := &ws.ImportedSourceParameters{Dynamic: &ws.ImportedDynamicParameters{
		Name: "identifiers", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &key, Value: &value,
	}}
	addition := ws.ImportedSourceRuleAddition{StepID: "ruleAdditions[0]", Kind: "DYNAMIC_COLUMN", Parameters: params}
	plan.RuleAdditions = append(plan.RuleAdditions, addition)
	plan.Steps = append(plan.Steps, ruleAdditionStep(addition, ""))
	hydrated, err := HydrateSourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	change := authoringv2.ImportedSourceStepChange{StepID: addition.StepID, Parameters: params}
	updated, err := EditPlan(output, hydrated, ws.CatalogSnapshot{}, PlanEdit{StepChanges: []authoringv2.ImportedSourceStepChange{change}}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	if updated.RuleAdditions[0].Parameters.Dynamic.Source.Select != "identifier[]" || params.Dynamic.Source.Select != "root.identifier[]" {
		t.Fatal("saving the edit must store the relative selector without changing the input")
	}
}

func TestEditPlanDeletesRuleWithoutReusingItsIdentity(t *testing.T) {
	output := recipe.Output{Name: "patient", RootResourceType: "Patient", RowGrain: "resource"}
	original := SourcePlan(output)
	catalog := ws.CatalogSnapshot{}
	key, value := recipe.Expression{Select: "item.system"}, recipe.Expression{Select: "item.value"}
	rule := ws.SourceRuleCreate{Kind: "DYNAMIC_COLUMN", Parameters: &ws.ImportedSourceParameters{
		Dynamic: &ws.ImportedDynamicParameters{Name: "identifiers", Source: recipe.Expression{Select: "identifier[]"}, Key: &key, Value: &value},
	}}
	created, err := EditPlan(output, original, catalog, PlanEdit{RuleCreate: &rule}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(original.RuleAdditions) != 0 || len(created.RuleAdditions) != 1 {
		t.Fatal("creation mutated the input or lost the new rule")
	}
	id := created.RuleAdditions[0].StepID
	other := rule
	other.Parameters = &ws.ImportedSourceParameters{
		Dynamic: &ws.ImportedDynamicParameters{Name: "other_identifiers", Source: recipe.Expression{Select: "identifier[]"}, Key: &key, Value: &value},
	}
	createdBoth, err := EditPlan(output, created, catalog, PlanEdit{RuleCreate: &other}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	survivor := createdBoth.RuleAdditions[1].StepID
	change := authoringv2.ImportedSourceStepChange{StepID: id, Remove: true}
	edited, err := EditPlan(output, createdBoth, catalog, PlanEdit{StepChanges: []authoringv2.ImportedSourceStepChange{change}}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(createdBoth.RuleAdditions) != 2 || len(edited.RuleAdditions) != 1 || edited.RuleAdditions[0].StepID != survivor || len(edited.Steps) != len(original.Steps)+1 {
		t.Fatal("removal mutated its input, left the removed rule, or changed the surviving rule identity")
	}
	effective, err := ApplySourcePlan(output, &edited)
	if err != nil || len(effective.DynamicColumns) != 1 || effective.DynamicColumns[0].Name != "other_identifiers" {
		t.Fatalf("removal changed unrelated recipe output: %#v, %v", effective.DynamicColumns, err)
	}
	recreated, err := EditPlan(output, edited, catalog, PlanEdit{RuleCreate: &rule}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recreated.RuleAdditions) != 2 || recreated.RuleAdditions[1].StepID == id {
		t.Fatalf("removed rule identity was reused: %#v", recreated.RuleAdditions)
	}
	if _, err := EditPlan(output, original, catalog, PlanEdit{StepChanges: []authoringv2.ImportedSourceStepChange{change}, RuleCreate: &rule}, PlanEditInput{}); err == nil {
		t.Fatal("accepted more than one edit intent")
	}
}

func TestEditPlanDeletesPinnedRuleFromSavedAndEffectiveRecipes(t *testing.T) {
	output := outputNamed(t, defaultSourcePlanFixture(t), "DocumentReference")
	plan := SourcePlan(output)
	removedID := "dynamicColumns[0]"
	survivorID := "dynamicColumns[1]"
	edited, err := EditPlan(output, plan, ws.CatalogSnapshot{}, PlanEdit{
		StepChanges: []authoringv2.ImportedSourceStepChange{{StepID: removedID, Remove: true}},
	}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := HydrateSourcePlan(output, &edited)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := sourcePlanStep(reloaded, removedID); found {
		t.Fatal("removed imported rule reappeared after hydration")
	}
	if _, found := sourcePlanStep(reloaded, survivorID); !found {
		t.Fatal("surviving imported rule lost its step identity")
	}
	baseline, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := ApplySourcePlan(output, &reloaded)
	if err != nil || !reflect.DeepEqual(effective.DynamicColumns, baseline.DynamicColumns[1:]) {
		t.Fatalf("effective rules after removal = %#v, %v", effective.DynamicColumns, err)
	}
}

func TestApplySourcePlanRejectsDisabledRuleOccurrence(t *testing.T) {
	output := recipe.Output{Name: "subjects", RootResourceType: "ResearchSubject", RowGrain: "resource",
		Traversals: []recipe.Traversal{{Name: "subject", ToResourceType: "Patient", OccurrenceID: "patient"}},
	}
	plan := SourcePlan(output)
	key, value := recipe.Expression{Select: "item.system"}, recipe.Expression{Select: "item.value"}
	rule := ws.SourceRuleCreate{Kind: "DYNAMIC_COLUMN", OccurrenceID: "patient", Parameters: &ws.ImportedSourceParameters{
		Dynamic: &ws.ImportedDynamicParameters{Name: "identifiers", Source: recipe.Expression{Select: "identifier[]"}, Key: &key, Value: &value},
	}}
	created, err := EditPlan(output, plan, ws.CatalogSnapshot{}, PlanEdit{RuleCreate: &rule}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := ApplySourcePlan(output, &created)
	if err != nil {
		t.Fatal(err)
	}
	if got := effective.Traversals[0].DynamicColumns[0].Source.Select; got != "subject.identifier[]" {
		t.Fatalf("name fallback produced %q", got)
	}
	change := authoringv2.ImportedSourceStepChange{StepID: "traversals[0]", Remove: true}
	_, err = EditPlan(output, created, ws.CatalogSnapshot{}, PlanEdit{StepChanges: []authoringv2.ImportedSourceStepChange{change}}, PlanEditInput{})
	var unavailable *ws.UnavailableSourceRuleOccurrence
	if !errors.As(err, &unavailable) || unavailable.OccurrenceID != "patient" {
		t.Fatalf("disabled occurrence error = %v", err)
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
	removePinnedTestStep(t, &plan, "traversals[0].traversals[0].catalogProjections[0]")
	removePinnedTestStep(t, &plan, "traversals[0].traversals[0].dynamicColumns[0]")
	removePinnedTestStep(t, &plan, "traversals[0].traversals[0]")
	removePinnedTestStep(t, &plan, "fields")

	maxStep, ok := sourcePlanStep(plan, "dynamicColumns[0]")
	if !ok || maxStep.MaxColumns == nil || maxStep.ValueMode == "" {
		t.Fatalf("root dynamic column did not advertise its native settings: %#v, found=%t", maxStep, ok)
	}
	newMaxColumns := *maxStep.MaxColumns / 2
	if newMaxColumns < 1 {
		newMaxColumns = 1
	}
	if err := updateSourcePlanStep(&plan, maxStep.ID, func(step *ws.ImportedSourceStep) {
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

	hydrated, err := HydrateSourcePlan(output, &plan)
	if err != nil || !reflect.DeepEqual(hydrated.RemovedStepIDs, plan.RemovedStepIDs) {
		t.Fatalf("removed steps did not survive hydration: %#v %v", hydrated.RemovedStepIDs, err)
	}
}

func TestApplySourcePlanRejectsStepsAndOverridesOutsidePinnedSource(t *testing.T) {
	bundle := defaultSourcePlanFixture(t)
	output := outputNamed(t, bundle, "DocumentReference")
	base := SourcePlan(output)

	tests := []struct {
		name   string
		mutate func(*testing.T, *ws.ImportedSourcePlan)
	}{
		{
			name: "forged step id",
			mutate: func(_ *testing.T, plan *ws.ImportedSourcePlan) {
				plan.Steps[0].ID = "forged"
			},
		},
		{
			name: "forged title",
			mutate: func(_ *testing.T, plan *ws.ImportedSourcePlan) {
				plan.Steps[0].Title = "Invented recipe step"
			},
		},
		{
			name: "missing step",
			mutate: func(_ *testing.T, plan *ws.ImportedSourcePlan) {
				plan.Steps = plan.Steps[:len(plan.Steps)-1]
			},
		},
		{
			name: "maximum raised above pinned limit",
			mutate: func(t *testing.T, plan *ws.ImportedSourcePlan) {
				step, _ := sourcePlanStep(*plan, "dynamicColumns[0]")
				value := *step.MaxColumns + 1
				if err := updateSourcePlanStep(plan, step.ID, func(step *ws.ImportedSourceStep) { step.MaxColumns = &value }); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "settings added to field group",
			mutate: func(t *testing.T, plan *ws.ImportedSourcePlan) {
				value := 1
				if err := updateSourcePlanStep(plan, "fields", func(step *ws.ImportedSourceStep) { step.MaxColumns = &value }); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "invalid value mode",
			mutate: func(t *testing.T, plan *ws.ImportedSourcePlan) {
				if err := updateSourcePlanStep(plan, "dynamicColumns[0]", func(step *ws.ImportedSourceStep) { step.ValueMode = "ZIP" }); err != nil {
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
	readOnlyPlan.RemovedStepIDs = []string{"expand"}
	if _, err := ApplySourcePlan(groupMember, &readOnlyPlan); err == nil {
		t.Fatal("ApplySourcePlan accepted disabling a read-only expansion step")
	}
}

func TestImportedSourcePlanValidationChecksHierarchyAndDocumentOwnership(t *testing.T) {
	plan := ws.ImportedSourcePlan{Steps: []ws.ImportedSourceStep{
		{ID: "traversals[0]", Kind: SourcePlanTraversal, Title: "Traversal", Summary: "A relationship.", Editable: true},
		{ID: "traversals[0].fields", ParentID: "traversals[0]", Kind: SourcePlanFieldGroup, Title: "Fields", Summary: "Source fields.", Editable: true},
	}}
	if err := plan.Validate(); err != nil {
		t.Fatalf("valid source plan: %v", err)
	}
	plan.Steps[1].ParentID = "missing"
	if err := plan.Validate(); err == nil {
		t.Fatal("source plan with a missing parent was accepted")
	}

	document := ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: "source", Title: "Source"},
		RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
		Rows: ws.RecordsRowDefinition(), ImportedSource: &ws.ImportedSourcePlan{},
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
	restored, err := ws.DecodeWorkspace(canonical)
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

func sourcePlanHasKind(plan ws.ImportedSourcePlan, kind string) bool {
	for _, step := range plan.Steps {
		if step.Kind == kind {
			return true
		}
	}
	return false
}

func sourcePlanStep(plan ws.ImportedSourcePlan, id string) (ws.ImportedSourceStep, bool) {
	for _, step := range plan.Steps {
		if step.ID == id {
			return step, true
		}
	}
	return ws.ImportedSourceStep{}, false
}

func removePinnedTestStep(t *testing.T, plan *ws.ImportedSourcePlan, id string) {
	t.Helper()
	retained, err := retainedPinnedSteps(plan.Steps, []string{id})
	if err != nil {
		t.Fatal(err)
	}
	plan.Steps = retained
	plan.RemovedStepIDs = append(plan.RemovedStepIDs, id)
}

func updateSourcePlanStep(plan *ws.ImportedSourcePlan, id string, update func(*ws.ImportedSourceStep)) error {
	for index := range plan.Steps {
		if plan.Steps[index].ID == id {
			update(&plan.Steps[index])
			return nil
		}
	}
	return fmt.Errorf("source-plan step %q not found", id)
}

func cloneSourcePlan(plan ws.ImportedSourcePlan) ws.ImportedSourcePlan {
	steps := make([]ws.ImportedSourceStep, len(plan.Steps))
	copy(steps, plan.Steps)
	for index := range steps {
		if steps[index].MaxColumns != nil {
			value := *steps[index].MaxColumns
			steps[index].MaxColumns = &value
		}
	}
	return ws.ImportedSourcePlan{Steps: steps}
}
