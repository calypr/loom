package server

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/recipeimport"
)

func TestImportedSourceEditRebuildsRealSchemaAndPreservesPin(t *testing.T) {
	ctx := context.Background()
	output := recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields:     []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{Name: "subject_Patient", ToResourceType: "Patient", Alias: "patient", Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "patient.id"}}}}}}
	other := output
	other.Name = "OtherSpecimen"
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output, other}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	registry := &importedSourceRegistry{entry: exec.Entry{Bundle: bundle, Digest: digest}}
	resolver := importedRecipeSources{registry: registry, engine: &dataframeexecution.Engine{}, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: explorerScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "specimen", ResourceType: "Specimen", RowRootEligible: true}, {ID: "patient", ResourceType: "Patient", RowRootEligible: true}},
		[]capability.Edge{{ID: "subject", FromNodeID: "specimen", ToNodeID: "patient", Label: "subject", StorageDirection: "OUTBOUND"}}, nil, nil)
	authorized := lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	source, err := resolver.load(ctx, "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized)
	if err != nil {
		t.Fatal(err)
	}
	schemas := map[string][]recipe.StageColumn{}
	for _, compiled := range source.resolved.Compiled.Outputs {
		columns, err := importedSourceSchema(compiled.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range columns {
			schemas[compiled.Name] = append(schemas[compiled.Name], column.Column)
		}
	}
	workspace, err := recipeimport.ImportWorkspace(bundle, authoringV2Catalog(snapshot, "test"), schemas)
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	if len(document.Columns) < 2 {
		t.Fatalf("expected root and patient columns: %#v", document.Columns)
	}
	var stepID string
	for _, step := range document.ImportedSource.Steps {
		if step.Editable && strings.Contains(step.Title, "Patient") && strings.Contains(strings.ToLower(step.Kind), "travers") {
			stepID = step.ID
			break
		}
	}
	if stepID == "" {
		t.Fatalf("missing editable traversal: %#v", document.ImportedSource)
	}
	updated, err := resolver.Edit(ctx, "project-a", authorized, document, &authoringv2.ImportedSourceStepChange{StepID: stepID, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if *updated.ImportedRecipe != *document.ImportedRecipe || len(updated.Route.Children) != 0 || len(updated.Columns) >= len(document.Columns) {
		t.Fatalf("source edit failed to change schema/route while retaining pin: %#v", updated)
	}
	if len(document.Route.Children) != 1 {
		t.Fatal("source edit mutated accepted document")
	}
	workspace.Documents[0] = updated
	resolved, err := resolver.Resolve(ctx, "project-a", workspace, authorized)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := explorercompilation.CompileWorkspace(ctx, "project-a", "test", workspace, snapshot, explorercompilation.ResolvedInputs{ImportedRecipes: resolved})
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Bundle.Outputs[0].Traversals) != 0 {
		t.Fatal("disabled traversal still executes")
	}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := authoringv2.DecodeWorkspace(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range restored.Documents[0].ImportedSource.Steps {
		if step.ID == stepID && step.Enabled {
			t.Fatal("disabled state lost on reload")
		}
	}
	back, err := resolver.Edit(ctx, "project-a", authorized, restored.Documents[0], &authoringv2.ImportedSourceStepChange{StepID: stepID, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Columns) != len(document.Columns) || len(back.Route.Children) != 1 {
		t.Fatal("reenabling did not restore source")
	}
	dependent := document
	var patientColumn string
	var stageColumns []authoringv2.StageColumn
	for _, column := range document.Columns {
		if strings.Contains(column.Column, "patient") {
			patientColumn = column.ColumnID
		}
		stageColumns = append(stageColumns, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	if patientColumn == "" {
		t.Fatal("fixture has no patient column")
	}
	dependent.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "has_patient", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{ColumnID: patientColumn, Operator: authoringv2.ConstructionFilterExists}}, Outputs: stageColumns,
	}}}
	workspace.Documents[0] = dependent
	resolved, err = resolver.Resolve(ctx, "project-a", workspace, authorized)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := explorercompilation.CompileWorkspace(ctx, "project-a", "test", workspace, snapshot, explorercompilation.ResolvedInputs{ImportedRecipes: resolved}); err != nil {
		t.Fatalf("dependent baseline does not compile: %v", err)
	}
	broken, editErr := resolver.Edit(ctx, "project-a", authorized, dependent, &authoringv2.ImportedSourceStepChange{StepID: stepID, Enabled: false})
	if editErr == nil {
		workspace.Documents[0] = broken
		resolved, err = resolver.Resolve(ctx, "project-a", workspace, authorized)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := explorercompilation.CompileWorkspace(ctx, "project-a", "test", workspace, snapshot, explorercompilation.ResolvedInputs{ImportedRecipes: resolved}); err == nil {
			t.Fatal("source removal accepted a filter that still depends on the removed patient column")
		}
	}
	workspace.Documents[0] = document
	workspace.Documents[1], err = resolver.Edit(ctx, "project-a", authorized, workspace.Documents[1], &authoringv2.ImportedSourceStepChange{StepID: stepID, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err = resolver.Resolve(ctx, "project-a", workspace, authorized)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range resolved {
		for _, effective := range input.ResolvedBundle.Outputs {
			if effective.Name == "Specimen" && len(effective.Traversals) != 1 {
				t.Fatal("editing the second output changed the first output")
			}
			if effective.Name == "OtherSpecimen" && len(effective.Traversals) != 0 {
				t.Fatal("shared bundle cache ignored the second output's source settings")
			}
		}
	}
	if actual, _ := registry.entry.Bundle.Digest(); actual != digest {
		t.Fatal("shared default recipe was mutated")
	}
}
