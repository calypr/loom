package recipeimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/dataframe/recipe"
	recipeschema "github.com/calypr/loom/internal/dataframe/recipe/schema"
	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	ws "github.com/calypr/loom/internal/explorer/workspace"
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

func TestDefaultSourceParameterCoverageAndAtomicEdits(t *testing.T) {
	bundle := defaultSourcePlanFixture(t)
	for _, out := range bundle.Outputs {
		t.Run(out.Name, func(t *testing.T) {
			before, _ := json.Marshal(out)
			plan := SourcePlan(out)
			for _, step := range plan.Steps {
				if step.Parameters == nil {
					t.Fatalf("missing settings for %s", step.ID)
				}
				if err := step.Parameters.Validate(""); err != nil {
					t.Fatalf("%s: %v", step.ID, err)
				}
			}
			applied, err := ApplySourcePlan(out, &plan)
			if err != nil {
				t.Fatal(err)
			}

			legacy := cloneSourcePlan(plan)
			for i := range legacy.Steps {
				legacy.Steps[i].Parameters = nil
			}
			want, err := ApplySourcePlan(out, &legacy)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(applied, want) {
				t.Fatal("hydrated effective parameters changed source semantics")
			}
			after, _ := json.Marshal(out)
			if string(before) != string(after) {
				t.Fatal("source parameter projection mutated pin")
			}
		})
	}
	cases := []struct {
		name, output, step string
		mutate             func(*ws.ImportedSourceParameters)
		check              func(recipe.Output) bool
	}{
		{"output", "ResearchSubject", "output", func(p *ws.ImportedSourceParameters) { p.Output.CollisionPolicy = "error" }, func(o recipe.Output) bool { return o.CollisionPolicy == "error" }},
		{"field", "ResearchSubject", "fields", func(p *ws.ImportedSourceParameters) {
			p.FieldGroup.Fields[1].Name = "subject_identifier"
			p.FieldGroup.Fields[1].Expr.Args[0].Select = "root.identifier[].system"
		}, func(o recipe.Output) bool {
			return o.Fields[1].Name == "subject_identifier" && o.Fields[1].Expr.Args[0].Select == "root.identifier[].system"
		}},
		{"catalog", "ResearchSubject", "catalogProjections[0]", func(p *ws.ImportedSourceParameters) {
			p.Catalog.IncludePaths = []string{"status"}
			p.Catalog.Naming = recipe.ColumnNamingPathSuffix
			p.Catalog.Outputs = []ws.ImportedCatalogOutput{{FieldPath: "status", Enabled: true, Name: "study_status"}}
		}, func(o recipe.Output) bool {
			projection := o.CatalogProjections[0]
			return reflect.DeepEqual(projection.IncludePaths, []string{"status"}) && projection.Naming == recipe.ColumnNamingPathSuffix && reflect.DeepEqual(projection.OutputOverrides, []recipe.CatalogProjectionOutputOverride{{FieldPath: "status", Enabled: true, Name: "study_status"}})
		}},
		{"dynamic", "DocumentReference", "dynamicColumns[0]", func(p *ws.ImportedSourceParameters) {
			p.Dynamic.Key = &recipe.Expression{Select: "item.system"}
			prefix := "ident_"
			p.Dynamic.ColumnPrefix = &prefix
			p.Dynamic.Outputs = []ws.ImportedDynamicOutput{{Key: "urn:disabled", Enabled: false}}
		}, func(o recipe.Output) bool {
			return o.DynamicColumns[0].Key.Select == "item.system" && *o.DynamicColumns[0].ColumnPrefix == "ident_" && reflect.DeepEqual(o.DynamicColumns[0].OutputOverrides, []recipe.DynamicColumnOutputOverride{{Key: "urn:disabled", Enabled: false}})
		}},
		{"extension", "DocumentReference", "extensionColumns[1]", func(p *ws.ImportedSourceParameters) {
			p.Extension.Source.Select = "root.extension[]"
			p.Extension.Outputs = []ws.ImportedExtensionOutput{{Key: recipe.ExtensionColumnOutputKey(recipe.ExtensionColumnMapping{URL: "https://example.org/value", URLPath: []string{"https://example.org/parent"}, SourcePath: "extension[].extension[]"}), Enabled: false}}
		}, func(o recipe.Output) bool {
			return o.ExtensionColumns[1].Source.Select == "root.extension[]" && len(o.ExtensionColumns[1].OutputOverrides) == 1 && !o.ExtensionColumns[1].OutputOverrides[0].Enabled
		}},
		{"pivot", "ResearchSubject", "traversals[0].traversals[0].pivots[0]", func(p *ws.ImportedSourceParameters) {
			p.Pivot.Discovery.Path = "component[].code"
			p.Pivot.Outputs = []ws.ImportedPivotOutput{{Key: "http://loinc.org|8480-6", Enabled: false}}
		}, func(o recipe.Output) bool {
			pivot := o.Traversals[0].Traversals[0].Pivots[0]
			return pivot.Discovery.Path == "component[].code" && reflect.DeepEqual(pivot.OutputOverrides, []recipe.PivotOutputOverride{{Key: "http://loinc.org|8480-6", Enabled: false}})
		}},
		{"traversal", "ResearchSubject", "traversals[0]", func(p *ws.ImportedSourceParameters) { p.Traversal.Alias = "person" }, func(o recipe.Output) bool { return o.Traversals[0].Alias == "person" }},
		{"expansion", "GroupMember", "expand", func(p *ws.ImportedSourceParameters) {
			p.Expansion.EmptyPolicy = recipe.ExpansionPreserveParent
		}, func(o recipe.Output) bool { return o.Expand.EmptyPolicy == recipe.ExpansionPreserveParent }},
		{"identity", "GroupMember", "identity", func(p *ws.ImportedSourceParameters) {
			p.Identity.Expr.Args[0].Literal = json.RawMessage(`"project-namespace"`)
		}, func(o recipe.Output) bool { return string(o.Identity.Expr.Args[0].Literal) == `"project-namespace"` }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := outputNamed(t, bundle, c.output)
			before, _ := json.Marshal(out)
			plan := SourcePlan(out)
			for i := range plan.Steps {
				if plan.Steps[i].ID == c.step {
					c.mutate(plan.Steps[i].Parameters)
				}
			}
			got, err := ApplySourcePlan(out, &plan)
			if err != nil {
				t.Fatal(err)
			}
			if !c.check(got) {
				t.Fatal("parameter edit did not update intended operation")
			}
			after, _ := json.Marshal(out)
			if string(before) != string(after) {
				t.Fatal("edit mutated pin")
			}
			again, err := HydrateSourcePlan(out, &plan)
			if err != nil {
				t.Fatal(err)
			}
			got2, err := ApplySourcePlan(out, &again)
			if err != nil || !reflect.DeepEqual(got, got2) {
				t.Fatalf("overlay reload lost exact settings: %v", err)
			}
		})
	}
}

func TestSourceParametersRetainLegacyPathsAndRejectFieldRetargeting(t *testing.T) {
	out := outputNamed(t, defaultSourcePlanFixture(t), "ResearchSubject")
	p := SourcePlan(out)

	p.Steps = p.Steps[1:]
	for i := range p.Steps {
		p.Steps[i].Parameters = nil
	}
	hydrated, err := HydrateSourcePlan(out, &p)
	if err != nil {
		t.Fatal(err)
	}
	if hydrated.Steps[0].ID != "output" || hydrated.Steps[1].ID != "fields" {
		t.Fatal("legacy source identities were renumbered")
	}
	for i := range hydrated.Steps {
		if hydrated.Steps[i].ID == "fields" {
			f := hydrated.Steps[i].Parameters.FieldGroup.Fields
			f[0], f[1] = f[1], f[0]
		}
	}
	if _, err := ApplySourcePlan(out, &hydrated); err == nil {
		t.Fatal("field reorder changed intended occurrence without rejection")
	}
}

func TestRenamedSourceFamilyUsesEffectiveNameWithoutChangingOriginalStepID(t *testing.T) {
	plan := SourcePlan(outputNamed(t, defaultSourcePlanFixture(t), "DocumentReference"))
	var step ws.ImportedSourceStep
	for _, s := range plan.Steps {
		if s.ID == "dynamicColumns[0]" {
			step = s
		}
	}
	if step.Parameters == nil || step.Parameters.Dynamic == nil {
		t.Fatal("fixture missing original source family")
	}
	originalID, originalKey := step.ID, step.SourceKey
	step.Parameters.Dynamic.Name = "project_identifier_keys"
	if effectiveSourceKey(step) != "project_identifier_keys" || step.ID != originalID || step.SourceKey != originalKey {
		t.Fatal("mutable family name changed original operation identity")
	}
}

func TestCatalogOutputOverrideDisableReloadAndReenableByFieldPath(t *testing.T) {
	output := recipe.Output{
		Name: "Patient", RootResourceType: "Patient", RootOccurrenceID: "base", RowGrain: "resource",
		CatalogProjections: []recipe.CatalogProjection{{Name: "populated_fields", IncludePaths: []string{"*"}, MaxColumns: 4}},
	}
	plan := SourcePlan(output)
	if err := updateSourcePlanStep(&plan, "catalogProjections[0]", func(step *ws.ImportedSourceStep) {
		step.Parameters.Catalog.Outputs = []ws.ImportedCatalogOutput{{FieldPath: "gender", Enabled: false}}
	}); err != nil {
		t.Fatal(err)
	}
	disabled, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled.CatalogProjections[0].OutputOverrides) != 1 || disabled.CatalogProjections[0].OutputOverrides[0].Enabled {
		t.Fatalf("disable was not applied: %#v", disabled.CatalogProjections[0].OutputOverrides)
	}
	reloaded, err := HydrateSourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	step, ok := sourcePlanStep(reloaded, "catalogProjections[0]")
	if !ok || len(step.Parameters.Catalog.Outputs) != 1 || step.Parameters.Catalog.Outputs[0].FieldPath != "gender" || step.Parameters.Catalog.Outputs[0].Enabled {
		t.Fatalf("disabled output identity was lost on reload: %+v", step)
	}
	if err := updateSourcePlanStep(&reloaded, "catalogProjections[0]", func(step *ws.ImportedSourceStep) {
		step.Parameters.Catalog.Outputs[0].Enabled = true
		step.Parameters.Catalog.Outputs[0].Name = "patient_gender"
	}); err != nil {
		t.Fatal(err)
	}
	reenabled, err := ApplySourcePlan(output, &reloaded)
	if err != nil {
		t.Fatal(err)
	}
	want := recipe.CatalogProjectionOutputOverride{FieldPath: "gender", Enabled: true, Name: "patient_gender"}
	if !reflect.DeepEqual(reenabled.CatalogProjections[0].OutputOverrides, []recipe.CatalogProjectionOutputOverride{want}) {
		t.Fatalf("re-enabled output changed identity: %#v", reenabled.CatalogProjections[0].OutputOverrides)
	}
}

func TestDiscoveredPivotOutputOverridePersistsThroughReloadAndReenable(t *testing.T) {
	output := recipe.Output{
		Name: "Observation", RootResourceType: "Observation", RootOccurrenceID: "base", RowGrain: "resource",
		Pivots: []recipe.Pivot{{Name: "observation_values", Discovery: &recipe.PivotDiscovery{Family: "observation_code_value", MaxColumns: 8}}},
	}
	plan := SourcePlan(output)
	if err := updateSourcePlanStep(&plan, "pivots[0]", func(step *ws.ImportedSourceStep) {
		step.Parameters.Pivot.Outputs = []ws.ImportedPivotOutput{{Key: "http://loinc.org|8480-6", Enabled: false, Name: stringPointer("systolic")}}
	}); err != nil {
		t.Fatal(err)
	}
	disabled, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled.Pivots[0].OutputOverrides) != 1 || disabled.Pivots[0].OutputOverrides[0].Enabled || disabled.Pivots[0].OutputOverrides[0].Name == nil || *disabled.Pivots[0].OutputOverrides[0].Name != "systolic" {
		t.Fatalf("pivot output disable = %#v", disabled.Pivots[0].OutputOverrides)
	}
	reloaded, err := HydrateSourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	step, ok := sourcePlanStep(reloaded, "pivots[0]")
	if !ok || len(step.Parameters.Pivot.Outputs) != 1 || step.Parameters.Pivot.Outputs[0].Key != "http://loinc.org|8480-6" || step.Parameters.Pivot.Outputs[0].Enabled || step.Parameters.Pivot.Outputs[0].Name == nil || *step.Parameters.Pivot.Outputs[0].Name != "systolic" {
		t.Fatalf("disabled pivot output identity was lost on reload: %+v", step)
	}
	if err := updateSourcePlanStep(&reloaded, "pivots[0]", func(step *ws.ImportedSourceStep) {
		step.Parameters.Pivot.Outputs[0].Enabled = true
	}); err != nil {
		t.Fatal(err)
	}
	reenabled, err := ApplySourcePlan(output, &reloaded)
	if err != nil {
		t.Fatal(err)
	}
	got := reenabled.Pivots[0].OutputOverrides[0]
	if got.Key != "http://loinc.org|8480-6" || !got.Enabled || got.Name == nil || *got.Name != "systolic" {
		t.Fatalf("re-enabled raw key or alias changed: %#v", got)
	}
}

func stringPointer(value string) *string { return &value }

type catalogRuleDiscovery struct {
	fields []recipeschema.FieldCandidate
}

func (d catalogRuleDiscovery) Fields(_ context.Context, _ recipeschema.Scope, resourceType string) ([]recipeschema.FieldCandidate, error) {
	var result []recipeschema.FieldCandidate
	for _, field := range d.fields {
		if field.ResourceType == resourceType {
			result = append(result, field)
		}
	}
	return result, nil
}

func TestCatalogRuleAppliesAndRediscoversIncludedFields(t *testing.T) {
	output := recipe.Output{
		Name: "patients", RootResourceType: "Patient", RowGrain: "resource",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
	}
	plan := SourcePlan(output)
	rule := ws.SourceRuleCreate{
		Kind: SourcePlanCatalogProjection,
		Parameters: &ws.ImportedSourceParameters{Catalog: &ws.ImportedCatalogParameters{
			Name: "demographic_fields", IncludePaths: []string{"birthDate", "gender", "identifier[].value", "identifier[].system"},
			ExcludePaths: []string{"gender"}, Kinds: []string{"scalar"}, Naming: recipe.ColumnNamingPath,
		}},
	}
	plan, err := EditPlan(output, plan, ws.CatalogSnapshot{}, PlanEdit{RuleCreate: &rule}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(effective.CatalogProjections) != 1 || effective.CatalogProjections[0].Name != "demographic_fields" ||
		effective.CatalogProjections[0].MaxColumns != ws.MaxImportedRuleColumns {
		t.Fatalf("applied catalog rule = %#v", effective.CatalogProjections)
	}

	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "catalog-rule-test", TranslationVersion: "1", Outputs: []recipe.Output{effective}}
	discovery := catalogRuleDiscovery{fields: []recipeschema.FieldCandidate{
		{ResourceType: "Patient", Path: "birthDate", Kind: "scalar"},
		{ResourceType: "Patient", Path: "gender", Kind: "scalar"},
		{ResourceType: "Patient", Path: "identifier[].value", Kind: "scalar"},
		{ResourceType: "Patient", Path: "identifier[].system", Kind: "scalar"},
	}}
	resolved, err := recipeschema.Resolve(context.Background(), bundle, recipeschema.Scope{Project: "fixture", DatasetGeneration: "fixture-v1"}, discovery)
	if err != nil {
		t.Fatal(err)
	}
	fields := resolved.Bundle.Outputs[0].Fields
	if len(fields) != 4 {
		t.Fatalf("resolved fields = %#v, want id plus three included fields", fields)
	}
	byPath := map[string]recipe.Field{}
	for _, field := range fields {
		byPath[field.FieldRef] = field
	}
	if _, exists := byPath["gender"]; exists {
		t.Fatal("excluded gender path was rediscovered")
	}
	for _, path := range []string{"birthDate", "identifier[].value", "identifier[].system"} {
		field, exists := byPath[path]
		if !exists || field.SourceKey != "demographic_fields" || field.Expr.Select == "" {
			t.Fatalf("included field %q was not generated by its catalog rule: %#v", path, field)
		}
	}
}

func TestNewCatalogRuleKeepsSeparateColumnOwnership(t *testing.T) {
	output := recipe.Output{Name: "documents", RootResourceType: "DocumentReference", RowGrain: "resource",
		CatalogProjections: []recipe.CatalogProjection{{Name: "catalog_fields", IncludePaths: []string{"status"}, MaxColumns: 10}},
	}
	rule := ws.SourceRuleCreate{Kind: SourcePlanCatalogProjection,
		Parameters: &ws.ImportedSourceParameters{Catalog: &ws.ImportedCatalogParameters{
			Name: "catalog_fields", IncludePaths: []string{"description"}, Naming: recipe.ColumnNamingPath,
		}},
	}
	plan, err := EditPlan(output, SourcePlan(output), ws.CatalogSnapshot{}, PlanEdit{RuleCreate: &rule}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	network := authoringnetwork.AuthoringNetwork{
		Occurrences: []authoringnetwork.NetworkOccurrence{{OccurrenceID: "base"}},
		Bindings:    []authoringnetwork.NetworkColumnBinding{{ColumnID: "status"}, {ColumnID: "description"}},
	}
	columns := []facade.CompiledOutputColumn{
		{ID: "status", SourceOrigin: &facade.CompiledSourceOrigin{OccurrenceID: "base", SourceKind: SourcePlanCatalogProjection, SourceKey: effective.CatalogProjections[0].Name}},
		{ID: "description", SourceOrigin: &facade.CompiledSourceOrigin{OccurrenceID: "base", SourceKind: SourcePlanCatalogProjection, SourceKey: effective.CatalogProjections[1].Name}},
	}
	bound, err := BindCompiledNetwork(network, columns, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Bindings[0].SourceStepID != "catalogProjections[0]" || bound.Bindings[1].SourceStepID != plan.RuleAdditions[0].StepID {
		t.Fatalf("catalog column owners = %#v", bound.Bindings)
	}
}

func TestSourceRuleDatatypeValidation(t *testing.T) {
	system := recipe.Expression{Call: "last_segment", Args: []recipe.Expression{{Select: "item.system"}}}
	display, sibling := recipe.Expression{Select: "item.display"}, recipe.Expression{Select: "root.code.coding[].display"}
	category := func(value *recipe.Expression) *ws.ImportedSourceParameters {
		return &ws.ImportedSourceParameters{Dynamic: &ws.ImportedDynamicParameters{
			Name: "category", Source: recipe.Expression{Select: "category[].coding[]"}, Key: &system, Value: value,
		}}
	}
	extension := func(path string) *ws.ImportedSourceParameters {
		return &ws.ImportedSourceParameters{Extension: &ws.ImportedExtensionParameters{
			Name: "extensions", Source: recipe.Expression{Select: path},
		}}
	}
	catalog := func(excluded string) *ws.ImportedSourceParameters {
		return &ws.ImportedSourceParameters{Catalog: &ws.ImportedCatalogParameters{
			Name: "fields", IncludePaths: []string{"identifier[].value"}, ExcludePaths: []string{excluded},
		}}
	}
	badNaming := catalog("identifier[].system")
	badNaming.Catalog.Naming = "unsupported"

	for _, tc := range []struct {
		name, kind, resourceType string
		params                   *ws.ImportedSourceParameters
		valid                    bool
	}{
		{"category coding", SourcePlanDynamicColumn, "Observation", category(&display), true},
		{"resource without category", SourcePlanDynamicColumn, "Patient", category(&display), false},
		{"category sibling value", SourcePlanDynamicColumn, "Observation", category(&sibling), false},
		{"nested extension", SourcePlanExtensionColumn, "Patient", extension("extension[].extension[]"), true},
		{"non-extension path", SourcePlanExtensionColumn, "Patient", extension("identifier[]"), false},
		{"catalog paths", SourcePlanCatalogProjection, "Patient", catalog("identifier[].system"), true},
		{"unknown exclusion", SourcePlanCatalogProjection, "Patient", catalog("unavailable"), false},
		{"unknown naming policy", SourcePlanCatalogProjection, "Patient", badNaming, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRuleAddition(tc.kind, tc.resourceType, tc.params)
			if (err == nil) != tc.valid {
				t.Fatalf("valid = %v, received error %v", tc.valid, err)
			}
		})
	}
}

func TestSourceRuleColumnLimitIsBounded(t *testing.T) {
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "resource"}
	limit := 4
	create := ws.SourceRuleCreate{
		Kind: SourcePlanCatalogProjection, MaxColumns: &limit,
		Parameters: &ws.ImportedSourceParameters{Catalog: &ws.ImportedCatalogParameters{
			Name: "fields", IncludePaths: []string{"gender"},
		}},
	}
	plan, err := EditPlan(output, SourcePlan(output), ws.CatalogSnapshot{}, PlanEdit{RuleCreate: &create}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if effective.CatalogProjections[0].MaxColumns != limit {
		t.Fatalf("compiled limit = %d, want %d", effective.CatalogProjections[0].MaxColumns, limit)
	}
	create.MaxColumns = nil
	withDefault, err := EditPlan(output, SourcePlan(output), ws.CatalogSnapshot{}, PlanEdit{RuleCreate: &create}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	step := withDefault.Steps[len(withDefault.Steps)-1]
	if step.MaxColumns == nil || *step.MaxColumns != ws.MaxImportedRuleColumns {
		t.Fatalf("default limit is not available when editing: %+v", step)
	}
	excess := ws.MaxImportedRuleColumns + 1
	change := authoringv2.ImportedSourceStepChange{StepID: plan.RuleAdditions[0].StepID, MaxColumns: &excess}
	if _, err := EditPlan(output, plan, ws.CatalogSnapshot{}, PlanEdit{StepChanges: []authoringv2.ImportedSourceStepChange{change}}, PlanEditInput{}); err == nil {
		t.Fatal("accepted a column limit above the rule maximum")
	}
}

func TestSourcePlanRejects129thRule(t *testing.T) {
	plan := ws.ImportedSourcePlan{RuleAdditions: make([]ws.ImportedSourceRuleAddition, 129)}
	if err := plan.Validate(); err == nil || err.Error() != "source plan has more than 128 rule additions" {
		t.Fatalf("expected the rule addition cap, got %v", err)
	}
}

func TestUnchangedOpaqueSourceExpressionsSurviveUnrelatedEdits(t *testing.T) {
	cases := []struct {
		name       string
		expression recipe.Expression
	}{
		{name: "document reference", expression: recipe.Expression{Document: &recipe.DocumentRef{Context: "root"}}},
		{name: "unsupported function", expression: recipe.Expression{Call: "legacy_lookup", Args: []recipe.Expression{{Select: "root.id"}}}},
		{name: "complex literal", expression: recipe.Expression{Literal: []byte(`{"legacy":true}`)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pinned := recipe.Output{
				RootResourceType: "Patient",
				Fields:           []recipe.Field{{Name: "legacy_value", Label: "Legacy", Expr: tc.expression}},
			}
			plan := SourcePlan(pinned)
			params := plan.Steps[1].Parameters
			params.FieldGroup.Fields[0].Label = "Current label"
			change := authoringv2.ImportedSourceStepChange{StepID: "fields", Parameters: params}
			raw, err := json.Marshal(change)
			if err != nil {
				t.Fatal(err)
			}
			var decoded authoringv2.ImportedSourceStepChange
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unrelated parameter edit failed request validation: %v", err)
			}
			plan.Steps[1].Parameters = decoded.Parameters

			got, err := ApplySourcePlan(pinned, &plan)
			if err != nil {
				t.Fatalf("unrelated field edit rejected unchanged opaque expression: %v", err)
			}
			if !reflect.DeepEqual(got.Fields[0].Expr, tc.expression) {
				t.Fatalf("opaque expression changed: got %#v, want %#v", got.Fields[0].Expr, tc.expression)
			}

			changed := SourcePlan(pinned)
			changed.Steps[1].Parameters.FieldGroup.Fields[0].Expr = recipe.Expression{Select: "root.id"}
			if _, err := ApplySourcePlan(pinned, &changed); err == nil || !strings.Contains(err.Error(), "cannot be changed") {
				t.Fatalf("editing an opaque expression was not rejected at the pin boundary: %v", err)
			}
		})
	}
}

func TestSourceParameterEditsCannotIntroduceOpaqueExpressions(t *testing.T) {
	unsupported := []recipe.Expression{
		{Document: &recipe.DocumentRef{Context: "root"}},
		{Call: "legacy_lookup", Args: []recipe.Expression{{Select: "root.id"}}},
		{Literal: []byte(`{"legacy":true}`)},
	}
	for _, expression := range unsupported {
		plan := SourcePlan(recipe.Output{
			RootResourceType: "Patient",
			Fields:           []recipe.Field{{Name: "record_id", Expr: recipe.Expression{Select: "root.id"}}},
		})
		plan.Steps[1].Parameters.FieldGroup.Fields[0].Expr = expression
		if _, err := ApplySourcePlan(recipe.Output{
			RootResourceType: "Patient",
			Fields:           []recipe.Field{{Name: "record_id", Expr: recipe.Expression{Select: "root.id"}}},
		}, &plan); err == nil {
			t.Fatalf("new opaque expression %#v was accepted", expression)
		}
	}
}

func TestDynamicRuleSelectorsStayWithinTheirRepeatedSourceItem(t *testing.T) {
	identifier := func(source, key, value recipe.Expression) *ws.ImportedDynamicParameters {
		return &ws.ImportedDynamicParameters{Name: "values", Source: source, Key: &key, Value: &value}
	}
	system := recipe.Expression{Select: "item.system"}
	lastSystem := recipe.Expression{Call: "last_segment", Args: []recipe.Expression{system}}
	value := recipe.Expression{Select: "item.value"}
	display := recipe.Expression{Select: "item.display"}

	for _, test := range []struct {
		name      string
		resource  string
		rule      *ws.ImportedDynamicParameters
		wantValid bool
	}{
		{"identifier system and value", "Patient", identifier(recipe.Expression{Select: "identifier[]"}, system, value), true},
		{"category coding system and display", "Observation", identifier(recipe.Expression{Select: "category[].coding[]"}, lastSystem, display), true},
		{"value from sibling array", "Observation", identifier(recipe.Expression{Select: "category[].coding[]"}, lastSystem, recipe.Expression{Select: "root.code.coding[].display"}), false},
		{"key from another repeated item", "Observation", identifier(recipe.Expression{Select: "category[].coding[]"}, recipe.Expression{Select: "item.extension[].url"}, display), false},
		{"key call around external selector", "Observation", identifier(recipe.Expression{Select: "category[].coding[]"}, recipe.Expression{Call: "last_segment", Args: []recipe.Expression{{Select: "root.code.coding[].system"}}}, display), false},
		{"value from another document", "Observation", identifier(recipe.Expression{Select: "category[].coding[]"}, lastSystem, recipe.Expression{Document: &recipe.DocumentRef{Context: "another"}}), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			parameters := &ws.ImportedSourceParameters{Dynamic: test.rule}
			err := validateRuleAddition(SourcePlanDynamicColumn, test.resource, parameters)
			if (err == nil) != test.wantValid {
				t.Fatalf("validation error = %v, want valid %v", err, test.wantValid)
			}
		})
	}
}

func TestCategoryCodingPresetAppliesToItsTraversal(t *testing.T) {
	output := recipe.Output{
		Name: "patients", RootResourceType: "Patient", RowGrain: "resource",
		Traversals: []recipe.Traversal{{Name: "observations", Alias: "observation", ToResourceType: "Observation", OccurrenceID: "observations"}},
	}
	params := &ws.ImportedSourceParameters{Dynamic: &ws.ImportedDynamicParameters{
		Name: "category_codings", Source: recipe.Expression{Select: "category[].coding[]"},
		Key:   &recipe.Expression{Call: "last_segment", Args: []recipe.Expression{{Select: "item.system"}}},
		Value: &recipe.Expression{Select: "item.display"},
	}}
	input := ws.SourceRuleCreate{Kind: SourcePlanDynamicColumn, OccurrenceID: "observations", Parameters: params}
	plan, err := EditPlan(output, SourcePlan(output), ws.CatalogSnapshot{}, PlanEdit{RuleCreate: &input}, PlanEditInput{})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := ApplySourcePlan(output, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(effective.DynamicColumns) != 0 || len(effective.Traversals[0].DynamicColumns) != 1 {
		t.Fatalf("unexpected category rule scope: %#v", effective)
	}
	rule := effective.Traversals[0].DynamicColumns[0]
	if rule.Source.Select != "observation.category[].coding[]" || rule.Key.Args[0].Select != "item.system" || rule.Value.Select != "item.display" {
		t.Fatalf("category rule lost its paired selectors: %#v", rule)
	}
}

func TestCompiledNetworkUsesAuthoringValuePolicyVocabulary(t *testing.T) {
	plan := ws.ImportedSourcePlan{Steps: []ws.ImportedSourceStep{{ID: "fields", Kind: SourcePlanFieldGroup, Title: "Fields", Summary: "Pinned fields", Editable: true}}}
	for _, tc := range []struct{ compiled, authored string }{{"scalar", "VALUE"}, {"first", "FIRST"}, {"array", "ALL"}, {"distinct_array", "DISTINCT"}, {"AUTO", "AUTO"}, {"ALL", "ALL"}} {
		t.Run(tc.compiled, func(t *testing.T) {
			network := authoringnetwork.AuthoringNetwork{Occurrences: []authoringnetwork.NetworkOccurrence{{OccurrenceID: "base", ResourceType: "Patient", Enabled: true}}, Bindings: []authoringnetwork.NetworkColumnBinding{{ColumnID: "id", Status: "UNKNOWN"}}}
			got, err := BindCompiledNetwork(network, []facade.CompiledOutputColumn{{ID: "id", Name: "id", SourceOrigin: &facade.CompiledSourceOrigin{OccurrenceID: "base", SourcePath: "fields", SourceKind: SourcePlanFieldGroup, ValueMode: tc.compiled}}}, &plan)
			if err != nil {
				t.Fatal(err)
			}
			if got.Bindings[0].ValueMode != tc.authored {
				t.Fatalf("compiled shape %q leaked as value policy %q; want authoring policy %q", tc.compiled, got.Bindings[0].ValueMode, tc.authored)
			}
		})
	}
}
