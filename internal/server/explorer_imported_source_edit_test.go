package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
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
	t.Run("source label remains editable after earlier apply", func(t *testing.T) {
		params := copySourceParametersForTest(t, document, "fields")
		params.FieldGroup.Fields[0].Label = "First source label"
		first, err := resolver.Edit(ctx, "project-a", authorized, document, &authoringv2.ImportedSourceStepChange{StepID: "fields", Enabled: true, Parameters: params})
		if err != nil {
			t.Fatal(err)
		}
		params = copySourceParametersForTest(t, first, "fields")
		params.FieldGroup.Fields[0].Label = "Second source label"
		second, err := resolver.Edit(ctx, "project-a", authorized, first, &authoringv2.ImportedSourceStepChange{StepID: "fields", Enabled: true, Parameters: params})
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range second.Columns {
			if column.Column == "id" {
				if column.Label != "Second source label" {
					t.Fatalf("earlier source label became an unintended presentation override: %s", column.Label)
				}
				return
			}
		}
		t.Fatal("source field disappeared")
	})
	t.Run("atomic alias and dependent selector edit", func(t *testing.T) {
		traversal := copySourceParametersForTest(t, document, "traversals[0]")
		fields := copySourceParametersForTest(t, document, "traversals[0].fields")
		traversal.Traversal.Alias = "person"
		fields.FieldGroup.Fields[0].Expr = recipe.Expression{Select: "person.id"}
		change := authoringv2.ImportedSourceStepChange{StepID: "traversals[0]", Enabled: true, Parameters: traversal}
		before, _ := json.Marshal(document)
		if _, err := resolver.Edit(ctx, "project-a", authorized, document, &change); err == nil {
			t.Fatal("alias-only edit silently retargeted dependent selector")
		}
		updated, err := resolver.EditBatch(ctx, "project-a", authorized, document, []authoringv2.ImportedSourceStepChange{change, {StepID: "traversals[0].fields", Enabled: true, Parameters: fields}})
		if err != nil {
			t.Fatal(err)
		}
		if updated.Route.Children[0].OccurrenceID != document.Route.Children[0].OccurrenceID {
			t.Fatal("alias edit changed occurrence identity")
		}
		oldID := ""
		for _, c := range document.Columns {
			if c.Column == "patient__patient_id" {
				oldID = c.ColumnID
			}
		}
		if oldID == "" {
			t.Fatal("fixture missing child field")
		}
		found := false
		for _, c := range updated.Columns {
			if c.Column == "person__patient_id" {
				found = true
				if c.ColumnID != oldID {
					t.Fatal("atomic alias edit retargeted source column identity")
				}
			}
		}
		if !found {
			t.Fatal("atomic alias edit not reflected")
		}
		after, _ := json.Marshal(document)
		if string(before) != string(after) {
			t.Fatal("failed or proposed batch mutated saved document")
		}
		if _, err := resolver.load(ctx, "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized, updated); err != nil {
			t.Fatalf("atomic alias reload: %v", err)
		}
	})
	t.Run("typed field rename and selector preserve original identity and presentation", func(t *testing.T) {
		var params *authoringv2.ImportedSourceParameters
		for _, step := range document.ImportedSource.Steps {
			if step.ID == "fields" {
				raw, _ := json.Marshal(step.Parameters)
				if err := json.Unmarshal(raw, &params); err != nil {
					t.Fatal(err)
				}
			}
		}
		if params == nil {
			t.Fatal("missing field parameters")
		}
		edited := document
		edited.Columns = append([]authoringv2.Column(nil), document.Columns...)
		originalID := ""
		for i := range edited.Columns {
			if edited.Columns[i].Column == "id" {
				originalID = edited.Columns[i].ColumnID
				edited.Columns[i].Label = "Saved heading"
			}
		}
		if originalID == "" {
			t.Fatal("missing original field ID")
		}
		params.FieldGroup.Fields[0].Name = "specimen_key"
		params.FieldGroup.Fields[0].Expr = recipe.Expression{Select: "root.status"}
		updated, err := resolver.Edit(ctx, "project-a", authorized, edited, &authoringv2.ImportedSourceStepChange{StepID: "fields", Enabled: true, Parameters: params})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, column := range updated.Columns {
			if column.Column == "specimen_key" {
				found = true
				if column.ColumnID != originalID || column.Label != "Saved heading" || column.Source.Imported.OutputColumn != "specimen_key" {
					t.Fatalf("field identity/presentation lost: %+v", column)
				}
			}
		}
		if !found {
			t.Fatal("rename did not edit existing source field")
		}
		if !reflect.DeepEqual(updated.ImportedRecipe, document.ImportedRecipe) {
			t.Fatal("parameter edit changed immutable pin")
		}
		if len(updated.ImportedSource.FieldBindings) != 1 || updated.ImportedSource.FieldBindings[0].FieldID != "fields[0]" || updated.ImportedSource.FieldBindings[0].ColumnID != originalID {
			t.Fatal("missing exact original field binding")
		}
		if _, err := resolver.load(ctx, "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized, updated); err != nil {
			t.Fatalf("reload failed: %v", err)
		}
		raw, _ := json.Marshal(updated)
		var forged authoringv2.Document
		if err := json.Unmarshal(raw, &forged); err != nil {
			t.Fatal(err)
		}
		forged.ImportedSource.FieldBindings[0].ColumnID = "forged_column"
		if _, err := resolver.load(ctx, "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized, forged); err == nil {
			t.Fatal("forged persisted binding was trusted")
		}
	})
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

func TestPinnedFieldBindingsCoverOriginalSlotsWithoutBorrowingCollisionWinner(t *testing.T) {
	output := recipe.Output{Name: "Example", RootResourceType: "Specimen", RowGrain: "specimen", Fields: []recipe.Field{{Name: "shadowed", Expr: recipe.Expression{Select: "root.id"}}, {Name: "visible", Expr: recipe.Expression{Select: "root.status"}}}}
	columns := []lower.CompiledOutputColumn{
		{ID: "dynamic_winner", Name: "shadowed", SourceOrigin: &lower.CompiledSourceOrigin{SourceKind: "DYNAMIC_COLUMN", SourcePath: "dynamicColumns[0]"}},
		{ID: "visible_id", Name: "visible", SourceOrigin: &lower.CompiledSourceOrigin{SourceKind: "FIELD_GROUP", SourcePath: "fields"}},
	}
	got, err := pinnedFieldBindingIDs(output, columns)
	if err != nil {
		t.Fatal(err)
	}
	if got["fields[1]"] != "visible_id" || got["fields[0]"] == "" || got["fields[0]"] == "dynamic_winner" {
		t.Fatalf("wrong original field identities: %+v", got)
	}
	again, err := pinnedFieldBindingIDs(output, columns)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatal("shadowed identity is not deterministic")
	}
	if err := verifyPinnedFieldBindings([]authoringv2.ImportedSourceFieldBinding{{FieldID: "fields[0]", ColumnID: "dynamic_winner"}}, got); err == nil {
		t.Fatal("shadowed operation borrowed collision winner identity")
	}
	columns = append(columns, columns[1])
	if _, err := pinnedFieldBindingIDs(output, columns); err == nil {
		t.Fatal("ambiguous original provenance accepted")
	}
}

func copySourceParametersForTest(t *testing.T, d authoringv2.Document, id string) *authoringv2.ImportedSourceParameters {
	t.Helper()
	for _, step := range d.ImportedSource.Steps {
		if step.ID == id {
			raw, err := json.Marshal(step.Parameters)
			if err != nil {
				t.Fatal(err)
			}
			var p *authoringv2.ImportedSourceParameters
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			return p
		}
	}
	t.Fatalf("missing source step %s", id)
	return nil
}

func TestImportedSourceAtomicExpansionAliasPreservesUUIDAndFieldDependencies(t *testing.T) {
	ctx := context.Background()
	ref := recipe.Expression{Call: "reference_id", Args: []recipe.Expression{{Select: "member.entity.reference"}}}
	output := recipe.Output{Name: "GroupMember", RootResourceType: "Group", RowGrain: "expanded", Expand: &recipe.Expansion{From: recipe.Expression{Select: "root.member[]"}, As: "member"}, Identity: &recipe.Identity{Name: "id", Expr: recipe.Expression{Call: "uuid5", Args: []recipe.Expression{{Literal: json.RawMessage(`"aced-idp.org"`)}, {Select: "root.id"}, {Call: "concat", Args: []recipe.Expression{{Literal: json.RawMessage(`","`)}, ref}}}}}, Fields: []recipe.Field{{Name: "group_id", Expr: recipe.Expression{Select: "root.id"}}, {Name: "member_id", Expr: ref}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	resolver := importedRecipeSources{registry: &importedSourceRegistry{entry: exec.Entry{Bundle: bundle, Digest: digest}}, engine: &dataframeexecution.Engine{}, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: explorerScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "group", ResourceType: "Group", RowRootEligible: true}}, nil, nil, nil)
	authorized := lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	source, err := resolver.load(ctx, "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized)
	if err != nil {
		t.Fatal(err)
	}
	schemas := map[string][]recipe.StageColumn{}
	for _, o := range source.resolved.Compiled.Outputs {
		cols, err := importedSourceSchema(o.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cols {
			schemas[o.Name] = append(schemas[o.Name], c.Column)
		}
	}
	workspace, err := recipeimport.ImportWorkspace(bundle, authoringV2Catalog(snapshot, "test"), schemas)
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	expansion := copySourceParametersForTest(t, document, "expand")
	identity := copySourceParametersForTest(t, document, "identity")
	fields := copySourceParametersForTest(t, document, "fields")
	expansion.Expansion.As = "entry"
	identity.Identity.Expr.Args[2].Args[1].Args[0].Select = "entry.entity.reference"
	fields.FieldGroup.Fields[1].Expr.Args[0].Select = "entry.entity.reference"
	alone := authoringv2.ImportedSourceStepChange{StepID: "expand", Enabled: true, Parameters: expansion}
	if _, err := resolver.Edit(ctx, "project-a", authorized, document, &alone); err == nil {
		t.Fatal("expansion alias alone invalidated dependencies without rejection")
	}
	updated, err := resolver.EditBatch(ctx, "project-a", authorized, document, []authoringv2.ImportedSourceStepChange{alone, {StepID: "identity", Enabled: true, Parameters: identity}, {StepID: "fields", Enabled: true, Parameters: fields}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document.ImportedRecipe, updated.ImportedRecipe) || len(updated.Columns) != len(document.Columns) {
		t.Fatal("atomic expansion edit changed pin or dropped source fields")
	}
	for i, c := range updated.Columns {
		if c.ColumnID != document.Columns[i].ColumnID {
			t.Fatal("atomic expansion edit changed intended field identity")
		}
	}
	if _, err := resolver.load(ctx, "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized, updated); err != nil {
		t.Fatalf("atomic expansion reload: %v", err)
	}
}

func TestImportedSourceDiscoveredCollisionPreservesWinnerIdentity(t *testing.T) {
	ctx := context.Background()
	empty := ""
	output := recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", CollisionPolicy: "overwrite", Fields: []recipe.Field{{Name: "shadowed", Expr: recipe.Expression{Select: "root.id"}}}, DynamicColumns: []recipe.DynamicColumn{{Name: "identifiers", ColumnPrefix: &empty, Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, MaxColumns: 1}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	registry := &importedSourceRegistry{entry: exec.Entry{Bundle: bundle, Digest: digest}}
	engine, err := dataframeexecution.New(dataframeexecution.Config{Registry: registry, ScopeDigest: recipeScopeDigest, ResolveBundle: recipeSchemaResolver(func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		if options.Project != "project-a" || options.DatasetGeneration != "generation-a" || options.ResourceType != "Specimen" {
			t.Fatalf("discovery lost exact scope: %+v", options)
		}
		return []catalog.PopulatedField{{ResourceType: "Specimen", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"shadowed"}}}, nil
	}, nil), QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
		t.Fatal("compile-only probe unexpectedly queried runtime")
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := importedRecipeSources{registry: registry, engine: engine, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: explorerScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "specimen", ResourceType: "Specimen", RowRootEligible: true}}, nil, nil, nil)
	authorized := lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	source, err := resolver.load(ctx, "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized)
	if err != nil {
		t.Fatal(err)
	}
	schemas := map[string][]recipe.StageColumn{}
	for _, o := range source.resolved.Compiled.Outputs {
		cols, err := importedSourceSchema(o.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cols {
			schemas[o.Name] = append(schemas[o.Name], c.Column)
		}
	}
	w, err := recipeimport.ImportWorkspace(bundle, authoringV2Catalog(snapshot, "test"), schemas)
	if err != nil {
		t.Fatal(err)
	}
	document := w.Documents[0]
	oldID := ""
	for _, c := range document.Columns {
		if c.Column == "shadowed" {
			oldID = c.ColumnID
		}
	}
	if oldID == "" {
		t.Fatal("no winner")
	}
	params := copySourceParametersForTest(t, document, "fields")
	params.FieldGroup.Fields[0].Name = "revealed"
	changed, err := resolver.Edit(ctx, "project-a", authorized, document, &authoringv2.ImportedSourceStepChange{StepID: "fields", Enabled: true, Parameters: params})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changed.Columns {
		if c.Column == "shadowed" {
			if c.ColumnID != oldID {
				t.Fatalf("untouched dynamic winner identity changed: before=%s after=%s", oldID, c.ColumnID)
			}
			return
		}
	}
	t.Fatal("winner disappeared")
}

func TestGeneratedNativeKeyRebindingRejectsAmbiguousContributorsAtomically(t *testing.T) {
	before := []importedProjectionFact{{ColumnID: "old-a", Owner: "traversals[0].extensionColumns[0]", Kind: "EXTENSION_COLUMN", Key: "leaf-url", SemanticKey: "parent-a/leaf"}, {ColumnID: "old-b", Owner: "traversals[0].extensionColumns[0]", Kind: "EXTENSION_COLUMN", Key: "leaf-url", SemanticKey: "parent-b/leaf"}}
	after := []importedProjectionFact{{ColumnID: "new-a", Owner: before[0].Owner, Kind: before[0].Kind, Key: before[0].Key, SemanticKey: "parent-b/leaf"}, {ColumnID: "new-b", Owner: before[0].Owner, Kind: before[0].Kind, Key: before[0].Key, SemanticKey: "parent-a/leaf"}}
	candidate := lower.CompiledRecipeOutput{OutputSchema: []lower.CompiledOutputColumn{{ID: "new-a", Name: "renamed-b"}, {ID: "new-b", Name: "renamed-a"}}}
	if err := rebindImportedGeneratedProjectionIDs(&candidate, before, after); err != nil {
		t.Fatal(err)
	}
	if candidate.OutputSchema[0].ID != "old-b" || candidate.OutputSchema[1].ID != "old-a" {
		t.Fatal("native hierarchy identity followed mutable public names or array order")
	}
	after[1].SemanticKey = "unknown-parent/leaf"
	candidate.OutputSchema[0].ID = "new-a"
	candidate.OutputSchema[1].ID = "new-b"
	original := append([]lower.CompiledOutputColumn(nil), candidate.OutputSchema...)
	if err := rebindImportedGeneratedProjectionIDs(&candidate, before, after); err == nil || !reflect.DeepEqual(candidate.OutputSchema, original) {
		t.Fatal("ambiguous native key selected an arbitrary contributor or changed partial state")
	}
	before = before[:1]
	after[0].SemanticKey = before[0].SemanticKey
	after[1].SemanticKey = before[0].SemanticKey
	if err := rebindImportedGeneratedProjectionIDs(&candidate, before, after); err == nil || !reflect.DeepEqual(candidate.OutputSchema, original) {
		t.Fatal("distinct native outputs borrowed the same original column identity")
	}
	after = after[:1]
	after[0].Owner = "traversals[1].extensionColumns[0]"
	after[0].LeafKey = "native-parent-a-leaf"
	candidate.OutputSchema = candidate.OutputSchema[:1]
	if err := rebindImportedGeneratedProjectionIDs(&candidate, before, after); err != nil || candidate.OutputSchema[0].ID == "old-a" || candidate.OutputSchema[0].ID == "old-b" {
		t.Fatal("a distinct occurrence borrowed an unrelated original identity")
	}
}

type sourceIdentityFixture struct {
	resolver   importedRecipeSources
	authorized lifecycle.AuthorizedCapability
	snapshot   capability.Snapshot
	workspace  authoringv2.Workspace
}

func newSourceIdentityFixture(t *testing.T, output recipe.Output, fields []catalog.PopulatedField) sourceIdentityFixture {
	t.Helper()
	ctx := context.Background()
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	registry := &importedSourceRegistry{entry: exec.Entry{Bundle: bundle, Digest: digest}}
	engine, err := dataframeexecution.New(dataframeexecution.Config{Registry: registry, ScopeDigest: recipeScopeDigest, ResolveBundle: recipeSchemaResolver(func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		if options.Project != "project-a" || options.DatasetGeneration != "generation-a" || options.ResourceType != output.RootResourceType {
			t.Fatal("native identity discovery escaped its exact authorized scope")
		}
		return fields, nil
	}, nil), QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
		t.Fatal("identity compilation unexpectedly executed a query")
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := importedRecipeSources{registry: registry, engine: engine, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: explorerScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "root", ResourceType: output.RootResourceType, RowRootEligible: true}}, nil, nil, nil)
	authorized := lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	source, err := resolver.load(ctx, "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized)
	if err != nil {
		t.Fatal(err)
	}
	schemas := map[string][]recipe.StageColumn{}
	for _, o := range source.resolved.Compiled.Outputs {
		columns, err := importedSourceSchema(o.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range columns {
			schemas[o.Name] = append(schemas[o.Name], c.Column)
		}
	}
	workspace, err := recipeimport.ImportWorkspace(bundle, authoringV2Catalog(snapshot, "test"), schemas)
	if err != nil {
		t.Fatal(err)
	}
	return sourceIdentityFixture{resolver, authorized, snapshot, workspace}
}
func TestImportedSourceGeneratedNamingPreservesNativeKeys(t *testing.T) {
	cases := []struct {
		name   string
		output recipe.Output
		fields []catalog.PopulatedField
		step   string
		edit   func(*authoringv2.ImportedSourceParameters)
	}{
		{"dynamic family", recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", DynamicColumns: []recipe.DynamicColumn{{Name: "identifiers", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, MaxColumns: 1}}}, []catalog.PopulatedField{{ResourceType: "Specimen", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"system-key"}}}, "dynamicColumns[0]", func(p *authoringv2.ImportedSourceParameters) { p.Dynamic.Name = "project_identifiers" }},
		{"typed extension family", recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", ExtensionColumns: []recipe.ExtensionColumn{{Name: "typed_extensions", Source: recipe.Expression{Select: "root.extension[]"}, MaxColumns: 4}}}, []catalog.PopulatedField{{ResourceType: "Specimen", Path: "extension[].url", Kind: "scalar", ExtensionValues: []catalog.ExtensionValueObservation{{URL: "http://example.org/tag", SourcePath: "extension[]", ValuePath: "valueString", ValueType: "string"}}}}, "extensionColumns[0]", func(p *authoringv2.ImportedSourceParameters) { p.Extension.Name = "project_extensions" }},
		{"catalog naming", recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", CatalogProjections: []recipe.CatalogProjection{{Name: "populated_fields", IncludePaths: []string{"*"}, Kinds: []string{"scalar"}, Naming: recipe.ColumnNamingPath, MaxColumns: 4}}}, []catalog.PopulatedField{{ResourceType: "Specimen", Path: "type.text", Kind: "scalar"}}, "catalogProjections[0]", func(p *authoringv2.ImportedSourceParameters) {
			p.Catalog.Name = "selected_fields"
			p.Catalog.Naming = recipe.ColumnNamingPathSuffix
		}},
		{"pivot family and alias", recipe.Output{Name: "Observation", RootResourceType: "Observation", RowGrain: "resource", Pivots: []recipe.Pivot{{Name: "measurements", ColumnExpr: recipe.Expression{Select: "root.code.text"}, ValueExpr: recipe.Expression{Select: "root.valueString"}, Columns: []string{"code-a"}}}}, nil, "pivots[0]", func(p *authoringv2.ImportedSourceParameters) {
			p.Pivot.Name = "revised_measurements"
			p.Pivot.ColumnAliases = map[string]string{"code-a": "explicit_alias"}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSourceIdentityFixture(t, c.output, c.fields)
			document := f.workspace.Documents[0]
			if len(document.Columns) != 1 {
				t.Fatalf("expected exactly one native family value: %+v", document.Columns)
			}
			old := document.Columns[0]
			document.Columns[0].Label = "Independent custom heading"
			params := copySourceParametersForTest(t, document, c.step)
			c.edit(params)
			updated, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, document, &authoringv2.ImportedSourceStepChange{StepID: c.step, Enabled: true, Parameters: params})
			if err != nil {
				t.Fatal(err)
			}
			if len(updated.Columns) != 1 || updated.Columns[0].Column == old.Column || updated.Columns[0].ColumnID != old.ColumnID || updated.Columns[0].Label != "Independent custom heading" {
				t.Fatalf("native naming edit retargeted value or presentation: before=%+v after=%+v", old, updated.Columns)
			}
			if !reflect.DeepEqual(updated.ImportedRecipe, document.ImportedRecipe) {
				t.Fatal("native naming edit mutated pin")
			}
			ref := updated.ImportedRecipe
			if _, err := f.resolver.load(context.Background(), "project-a", ref.Name, ref.TranslationVersion, ref.Digest, f.authorized, updated); err != nil {
				t.Fatalf("native naming identity lost on reload: %v", err)
			}
		})
	}
}

func TestImportedSourceHiddenFieldEditPreservesBoundDownstreamWinner(t *testing.T) {
	empty := ""
	output := recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", CollisionPolicy: "overwrite", Fields: []recipe.Field{{Name: "shadowed", Expr: recipe.Expression{Select: "root.id"}}}, DynamicColumns: []recipe.DynamicColumn{{Name: "identifiers", ColumnPrefix: &empty, Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, MaxColumns: 1}}}
	f := newSourceIdentityFixture(t, output, []catalog.PopulatedField{{ResourceType: "Specimen", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"shadowed"}}})
	document := f.workspace.Documents[0]
	if len(document.Columns) != 1 {
		t.Fatal("fixture did not produce an actual shadowed field")
	}
	winner := document.Columns[0]
	document.Construction = &authoringv2.Construction{Version: 1, Steps: []authoringv2.ConstructionStep{{ID: "keep-winner", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}}, Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{ColumnID: winner.ColumnID, Operator: authoringv2.ConstructionFilterExists}}, Outputs: []authoringv2.StageColumn{{ID: winner.ColumnID, Name: winner.Column, Label: winner.Label, Type: winner.LogicalType}}}}}
	params := copySourceParametersForTest(t, document, "fields")
	params.FieldGroup.Fields[0].Label = "Updated hidden declaration"
	updated, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, document, &authoringv2.ImportedSourceStepChange{StepID: "fields", Enabled: true, Parameters: params})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Columns) != 1 || updated.Columns[0].ColumnID != winner.ColumnID || !reflect.DeepEqual(updated.Construction, document.Construction) {
		t.Fatal("hidden field edit retargeted winning value or downstream operation")
	}
	f.workspace.Documents[0] = updated
	resolved, err := f.resolver.Resolve(context.Background(), "project-a", f.workspace, f.authorized)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "bound-winner", f.workspace, f.snapshot, explorercompilation.ResolvedInputs{ImportedRecipes: resolved}); err != nil {
		t.Fatalf("unchanged winner's downstream binding no longer compiles: %v", err)
	}
}

func TestImportedSourceLegacyEnablementRetainsDiscoveredWinnerIdentity(t *testing.T) {
	empty := ""
	output := recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", CollisionPolicy: "overwrite", Fields: []recipe.Field{{Name: "shadowed", Expr: recipe.Expression{Select: "root.id"}}}, DynamicColumns: []recipe.DynamicColumn{{Name: "identifiers", ColumnPrefix: &empty, Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, MaxColumns: 1}}}
	f := newSourceIdentityFixture(t, output, []catalog.PopulatedField{{ResourceType: "Specimen", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"shadowed"}}})
	document := f.workspace.Documents[0]
	if len(document.Columns) != 1 {
		t.Fatal("fixture missing discovered winner")
	}
	old := document.Columns[0]
	updated, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, document, &authoringv2.ImportedSourceStepChange{StepID: "fields", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Columns) != 1 || updated.Columns[0].ColumnID != old.ColumnID || updated.Columns[0].Column != old.Column {
		t.Fatal("legacy enablement changed an untouched discovered winner identity")
	}
}

func TestImportedSourceNewPivotKeyRenameKeepsAuthoredIdentity(t *testing.T) {
	output := recipe.Output{Name: "Observation", RootResourceType: "Observation", RowGrain: "resource", Pivots: []recipe.Pivot{{Name: "measurements", ColumnExpr: recipe.Expression{Select: "root.code.text"}, ValueExpr: recipe.Expression{Select: "root.valueString"}, Columns: []string{"pinned-key"}}}}
	f := newSourceIdentityFixture(t, output, nil)
	document := f.workspace.Documents[0]
	firstParams := copySourceParametersForTest(t, document, "pivots[0]")
	firstParams.Pivot.Columns = append(firstParams.Pivot.Columns, "audited_score")
	first, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, document, &authoringv2.ImportedSourceStepChange{StepID: "pivots[0]", Enabled: true, Parameters: firstParams})
	if err != nil {
		t.Fatal(err)
	}
	newID := ""
	stages := []authoringv2.StageColumn{}
	for _, c := range first.Columns {
		if c.Column == "measurements__audited_score" {
			newID = c.ColumnID
		}
		stages = append(stages, authoringv2.StageColumn{ID: c.ColumnID, Name: c.Column, Label: c.Label, Type: c.LogicalType})
	}
	if newID == "" {
		t.Fatal("real first edit did not expose new native key")
	}
	first.Construction = &authoringv2.Construction{Version: 1, Steps: []authoringv2.ConstructionStep{{ID: "keep-audited-score", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}}, Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{ColumnID: newID, Operator: authoringv2.ConstructionFilterExists}}, Outputs: stages}}}
	f.workspace.Documents[0] = first
	resolved, err := f.resolver.Resolve(context.Background(), "project-a", f.workspace, f.authorized)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = explorercompilation.CompileWorkspace(context.Background(), "project-a", "newkey-before-rename", f.workspace, f.snapshot, explorercompilation.ResolvedInputs{ImportedRecipes: resolved}); err != nil {
		t.Fatalf("producer fixture before rename is invalid: %v", err)
	}
	nextParams := copySourceParametersForTest(t, first, "pivots[0]")
	nextParams.Pivot.Name = "renamed_measurements"
	second, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, first, &authoringv2.ImportedSourceStepChange{StepID: "pivots[0]", Enabled: true, Parameters: nextParams})
	if err != nil {
		t.Fatal(err)
	}
	afterID := ""
	for _, c := range second.Columns {
		if c.Column == "renamed_measurements__audited_score" {
			afterID = c.ColumnID
		}
	}
	if afterID == "" {
		t.Fatal("second rename omitted existing native key")
	}
	f.workspace.Documents[0] = second
	resolved, resolveErr := f.resolver.Resolve(context.Background(), "project-a", f.workspace, f.authorized)
	var compileErr error
	if resolveErr == nil {
		_, compileErr = explorercompilation.CompileWorkspace(context.Background(), "project-a", "newkey-after-rename", f.workspace, f.snapshot, explorercompilation.ResolvedInputs{ImportedRecipes: resolved})
	}
	if afterID != newID {
		t.Fatalf("same authored native key identity changed across valid rename: before=%s after=%s; resolve=%v compile=%v", newID, afterID, resolveErr, compileErr)
	}
	if resolveErr != nil || compileErr != nil {
		t.Fatalf("valid downstream field binding no longer compiles: %v/%v", resolveErr, compileErr)
	}
	aliasParams := copySourceParametersForTest(t, second, "pivots[0]")
	aliasParams.Pivot.ColumnAliases = map[string]string{"audited_score": "published_score"}
	third, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, second, &authoringv2.ImportedSourceStepChange{StepID: "pivots[0]", Enabled: true, Parameters: aliasParams})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, column := range third.Columns {
		if column.Column == "published_score" {
			found = true
			if column.ColumnID != newID {
				t.Fatal("explicit pivot alias changed the first-created native identity")
			}
		}
	}
	if !found {
		t.Fatal("explicit pivot alias missing")
	}
	f.workspace.Documents[0] = third
	raw, err := f.workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := authoringv2.DecodeWorkspace(raw)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err = f.resolver.Resolve(context.Background(), "project-a", reloaded, f.authorized)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "newkey-after-alias-reload", reloaded, f.snapshot, explorercompilation.ResolvedInputs{ImportedRecipes: resolved}); err != nil {
		t.Fatalf("alias/reload invalidated bound native key: %v", err)
	}

}

func TestImportedSourceNewNativeKeysRemainStableAcrossNamingAndReload(t *testing.T) {
	cases := []struct {
		name           string
		output         recipe.Output
		fields         []catalog.PopulatedField
		step           string
		create, rename func(*authoringv2.ImportedSourceParameters)
	}{
		{"dynamic", recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", DynamicColumns: []recipe.DynamicColumn{{Name: "values", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, MaxColumns: 4}}}, []catalog.PopulatedField{{ResourceType: "Specimen", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"pinned-key"}}, {ResourceType: "Specimen", Path: "extension[].url", Kind: "scalar", DistinctValues: []string{"new-key"}}}, "dynamicColumns[0]", func(p *authoringv2.ImportedSourceParameters) {
			p.Dynamic.Source = recipe.Expression{Select: "root.extension[]"}
			p.Dynamic.Key = &recipe.Expression{Select: "item.url"}
			p.Dynamic.Value = &recipe.Expression{Select: "item.valueString"}
		}, func(p *authoringv2.ImportedSourceParameters) { p.Dynamic.Name = "renamed_values" }},
		{"catalog", recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", CatalogProjections: []recipe.CatalogProjection{{Name: "fields", IncludePaths: []string{"id"}, Kinds: []string{"scalar"}, Naming: recipe.ColumnNamingPath, MaxColumns: 4}}}, []catalog.PopulatedField{{ResourceType: "Specimen", Path: "id", Kind: "scalar"}, {ResourceType: "Specimen", Path: "type.text", Kind: "scalar"}}, "catalogProjections[0]", func(p *authoringv2.ImportedSourceParameters) { p.Catalog.IncludePaths = []string{"type.text"} }, func(p *authoringv2.ImportedSourceParameters) {
			p.Catalog.Name = "renamed_fields"
			p.Catalog.Naming = recipe.ColumnNamingPathSuffix
		}},
		{"typed extension", recipe.Output{Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "file", ExtensionColumns: []recipe.ExtensionColumn{{Name: "extensions", Source: recipe.Expression{Select: "root.extension[]"}, MaxColumns: 4}}}, []catalog.PopulatedField{{ResourceType: "DocumentReference", Path: "extension[].url", Kind: "scalar", ExtensionValues: []catalog.ExtensionValueObservation{{URL: "http://example.org/pinned", SourcePath: "extension[]", ValuePath: "valueString", ValueType: "string"}}}, {ResourceType: "DocumentReference", Path: "content[].attachment.extension[].url", Kind: "scalar", ExtensionValues: []catalog.ExtensionValueObservation{{URL: "http://example.org/new-leaf", SourcePath: "content[].attachment.extension[]", ValuePath: "valueString", ValueType: "string"}, {URL: "http://example.org/second-leaf", SourcePath: "content[].attachment.extension[]", ValuePath: "valueString", ValueType: "string"}}}}, "extensionColumns[0]", func(p *authoringv2.ImportedSourceParameters) {
			p.Extension.Source = recipe.Expression{Select: "root.content[].attachment.extension[]"}
		}, func(p *authoringv2.ImportedSourceParameters) {
			p.Extension.Name = "renamed_extensions"
			prefix := "published"
			p.Extension.ColumnPrefix = &prefix
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSourceIdentityFixture(t, c.output, c.fields)
			document := f.workspace.Documents[0]
			params := copySourceParametersForTest(t, document, c.step)
			c.create(params)
			first, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, document, &authoringv2.ImportedSourceStepChange{StepID: c.step, Enabled: true, Parameters: params})
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Columns) < 1 {
				t.Fatal("new native keys missing")
			}
			for _, c := range first.Columns {
				if !strings.HasPrefix(c.ColumnID, "source_key_") {
					t.Fatalf("new native key did not receive first-creation identity: %+v", first.Columns)
				}
			}
			column := first.Columns[0]
			first.Construction = &authoringv2.Construction{Version: 1, Steps: []authoringv2.ConstructionStep{{ID: "bound-new-key", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}}, Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{ColumnID: column.ColumnID, Operator: authoringv2.ConstructionFilterExists}}, Outputs: stageColumnsForSourceIdentity(first.Columns)}}}
			compile := func(d authoringv2.Document) {
				t.Helper()
				f.workspace.Documents[0] = d
				resolved, err := f.resolver.Resolve(context.Background(), "project-a", f.workspace, f.authorized)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "new-native-key", f.workspace, f.snapshot, explorercompilation.ResolvedInputs{ImportedRecipes: resolved}); err != nil {
					t.Fatal(err)
				}
			}
			compile(first)
			params = copySourceParametersForTest(t, first, c.step)
			c.rename(params)
			second, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, first, &authoringv2.ImportedSourceStepChange{StepID: c.step, Enabled: true, Parameters: params})
			if err != nil {
				t.Fatal(err)
			}
			if len(second.Columns) != len(first.Columns) || !reflect.DeepEqual(second.Construction, first.Construction) {
				t.Fatal("naming changed native keys or bound operation")
			}
			oldNames := map[string]string{}
			for _, c := range first.Columns {
				oldNames[c.ColumnID] = c.Column
			}
			for _, c := range second.Columns {
				old, ok := oldNames[c.ColumnID]
				if !ok || old == c.Column {
					t.Fatal("naming retargeted a new native key instead of changing its public name")
				}
			}
			compile(second)
			raw, err := f.workspace.CanonicalJSON()
			if err != nil {
				t.Fatal(err)
			}
			reloaded, err := authoringv2.DecodeWorkspace(raw)
			if err != nil {
				t.Fatal(err)
			}
			compile(reloaded.Documents[0])
		})
	}
}

func TestNewNativeKeyIDsExcludeMutablePresentationAndRejectLeafAmbiguity(t *testing.T) {
	makeCandidate := func(ids ...string) *lower.CompiledRecipeOutput {
		c := &lower.CompiledRecipeOutput{Name: "Output"}
		for _, id := range ids {
			c.OutputSchema = append(c.OutputSchema, lower.CompiledOutputColumn{ID: id, Name: "mutable_" + id})
		}
		return c
	}
	base := importedProjectionFact{ColumnID: "generated-a", Owner: "traversals[0].pivots[0]", Kind: "PIVOT", Key: "raw-key", SemanticKey: "mutable-semantic-qualifier"}
	first := makeCandidate(base.ColumnID)
	if err := rebindImportedGeneratedProjectionIDs(first, nil, []importedProjectionFact{base}); err != nil {
		t.Fatal(err)
	}
	renamed := base
	renamed.ColumnID = "different-generated-id"
	renamed.SemanticKey = "different-mutable-qualifier"
	next := makeCandidate(renamed.ColumnID)
	if err := rebindImportedGeneratedProjectionIDs(next, nil, []importedProjectionFact{renamed}); err != nil || next.OutputSchema[0].ID != first.OutputSchema[0].ID {
		t.Fatal("unique native identity depends on generated name, ID or semantic qualifier")
	}
	extension := base
	extension.Kind = "EXTENSION_COLUMN"
	extension.LeafKey = "native-parent/leaf"
	extension.ColumnID = "ext-a"
	a := makeCandidate(extension.ColumnID)
	if err := rebindImportedGeneratedProjectionIDs(a, nil, []importedProjectionFact{extension}); err != nil {
		t.Fatal(err)
	}
	other := extension
	other.ColumnID = "ext-b"
	other.LeafKey = "another-parent/leaf"
	b := makeCandidate(extension.ColumnID, other.ColumnID)
	if err := rebindImportedGeneratedProjectionIDs(b, nil, []importedProjectionFact{extension, other}); err != nil || a.OutputSchema[0].ID != b.OutputSchema[0].ID || b.OutputSchema[0].ID == b.OutputSchema[1].ID {
		t.Fatal("adding a distinct native leaf changed the first leaf's identity")
	}
	other.LeafKey = extension.LeafKey
	b = makeCandidate(extension.ColumnID, other.ColumnID)
	original := append([]lower.CompiledOutputColumn(nil), b.OutputSchema...)
	if err := rebindImportedGeneratedProjectionIDs(b, nil, []importedProjectionFact{extension, other}); err == nil || !reflect.DeepEqual(original, b.OutputSchema) {
		t.Fatal("ambiguous new native leaves were assigned arbitrary or partial identities")
	}
}

func stageColumnsForSourceIdentity(columns []authoringv2.Column) []authoringv2.StageColumn {
	result := make([]authoringv2.StageColumn, 0, len(columns))
	for _, c := range columns {
		result = append(result, authoringv2.StageColumn{ID: c.ColumnID, Name: c.Column, Label: c.Label, Type: c.LogicalType})
	}
	return result
}
