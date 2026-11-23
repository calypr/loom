package importedsource

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog/fieldcatalog"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/fielddiscovery"
	"github.com/calypr/loom/internal/dataframe/recipe"
	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
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
	registry := &importedSourceRegistry{entry: recipe.Entry{Bundle: bundle, Digest: digest}}
	resolver := Sources{registry: registry, engine: &dataframeexecution.Engine{}, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "specimen", ResourceType: "Specimen", RowRootEligible: true}, {ID: "patient", ResourceType: "Patient", RowRootEligible: true}},
		[]capability.Edge{{ID: "subject", FromNodeID: "specimen", ToNodeID: "patient", Label: "subject", StorageDirection: "OUTBOUND"}}, nil, nil)
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
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
	workspace, err := recipeimport.ImportWorkspace(bundle, ws.CatalogFromCapability(snapshot, "test"), schemas)
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	t.Run("source label remains editable after earlier apply", func(t *testing.T) {
		params := copySourceParametersForTest(t, document, "fields")
		params.FieldGroup.Fields[0].Label = "First source label"
		first, err := resolver.Edit(ctx, "project-a", authorized, document, &authoringv2.ImportedSourceStepChange{StepID: "fields", Parameters: params})
		if err != nil {
			t.Fatal(err)
		}
		params = copySourceParametersForTest(t, first, "fields")
		params.FieldGroup.Fields[0].Label = "Second source label"
		second, err := resolver.Edit(ctx, "project-a", authorized, first, &authoringv2.ImportedSourceStepChange{StepID: "fields", Parameters: params})
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
		change := authoringv2.ImportedSourceStepChange{StepID: "traversals[0]", Parameters: traversal}
		before, _ := json.Marshal(document)
		if _, err := resolver.Edit(ctx, "project-a", authorized, document, &change); err == nil {
			t.Fatal("alias-only edit silently retargeted dependent selector")
		}
		updated, err := resolver.EditBatch(ctx, "project-a", authorized, document, []authoringv2.ImportedSourceStepChange{change, {StepID: "traversals[0].fields", Parameters: fields}})
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
		var params *ws.ImportedSourceParameters
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
		edited.Columns = append([]ws.Column(nil), document.Columns...)
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
		updated, err := resolver.Edit(ctx, "project-a", authorized, edited, &authoringv2.ImportedSourceStepChange{StepID: "fields", Parameters: params})
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
		var forged ws.Document
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
	updated, err := resolver.Edit(ctx, "project-a", authorized, document, &authoringv2.ImportedSourceStepChange{StepID: stepID, Remove: true})
	if err != nil {
		t.Fatal(err)
	}
	if *updated.ImportedRecipe != *document.ImportedRecipe || len(updated.Route.Children) != 0 || len(updated.Columns) >= len(document.Columns) {
		t.Fatalf("source step removal failed to change schema/route while retaining pin: %#v", updated)
	}
	if len(document.Route.Children) != 1 {
		t.Fatal("source edit mutated accepted document")
	}
	if updated.ImportedSource == nil || len(updated.ImportedSource.RemovedStepIDs) != 1 || updated.ImportedSource.RemovedStepIDs[0] != stepID {
		t.Fatalf("removed step was not recorded in the source overlay: %#v", updated.ImportedSource)
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
		t.Fatal("removed traversal still executes")
	}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ws.DecodeWorkspace(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range restored.Documents[0].ImportedSource.Steps {
		if step.ID == stepID {
			t.Fatal("removed step returned after reload")
		}
	}
	dependent := document
	var patientColumn string
	var stageColumns []ws.StageColumn
	for _, column := range document.Columns {
		if strings.Contains(column.Column, "patient") {
			patientColumn = column.ColumnID
		}
		stageColumns = append(stageColumns, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	if patientColumn == "" {
		t.Fatal("fixture has no patient column")
	}
	dependent.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "has_patient", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: patientColumn, Operator: ws.ConstructionFilterExists}}, Outputs: stageColumns,
	}}}
	workspace.Documents[0] = dependent
	resolved, err = resolver.Resolve(ctx, "project-a", workspace, authorized)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := explorercompilation.CompileWorkspace(ctx, "project-a", "test", workspace, snapshot, explorercompilation.ResolvedInputs{ImportedRecipes: resolved}); err != nil {
		t.Fatalf("dependent baseline does not compile: %v", err)
	}
	if _, err := resolver.Edit(ctx, "project-a", authorized, dependent, &authoringv2.ImportedSourceStepChange{StepID: stepID, Remove: true}); err == nil {
		t.Fatal("source removal accepted a filter that still depends on the removed patient column")
	}
	workspace.Documents[0] = document
	workspace.Documents[1], err = resolver.Edit(ctx, "project-a", authorized, workspace.Documents[1], &authoringv2.ImportedSourceStepChange{StepID: stepID, Remove: true})
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
	columns := []facade.CompiledOutputColumn{
		{ID: "dynamic_winner", Name: "shadowed", SourceOrigin: &facade.CompiledSourceOrigin{SourceKind: "DYNAMIC_COLUMN", SourcePath: "dynamicColumns[0]"}},
		{ID: "visible_id", Name: "visible", SourceOrigin: &facade.CompiledSourceOrigin{SourceKind: "FIELD_GROUP", SourcePath: "fields"}},
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
	if err := recipeimport.VerifyPinnedFieldBindings([]ws.ImportedSourceFieldBinding{{FieldID: "fields[0]", ColumnID: "dynamic_winner"}}, got); err == nil {
		t.Fatal("shadowed operation borrowed collision winner identity")
	}
	columns = append(columns, columns[1])
	if _, err := pinnedFieldBindingIDs(output, columns); err == nil {
		t.Fatal("ambiguous original provenance accepted")
	}
}

func copySourceParametersForTest(t *testing.T, d ws.Document, id string) *ws.ImportedSourceParameters {
	t.Helper()
	for _, step := range d.ImportedSource.Steps {
		if step.ID == id {
			raw, err := json.Marshal(step.Parameters)
			if err != nil {
				t.Fatal(err)
			}
			var p *ws.ImportedSourceParameters
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
	resolver := Sources{registry: &importedSourceRegistry{entry: recipe.Entry{Bundle: bundle, Digest: digest}}, engine: &dataframeexecution.Engine{}, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "group", ResourceType: "Group", RowRootEligible: true}}, nil, nil, nil)
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
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
	workspace, err := recipeimport.ImportWorkspace(bundle, ws.CatalogFromCapability(snapshot, "test"), schemas)
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
	alone := authoringv2.ImportedSourceStepChange{StepID: "expand", Parameters: expansion}
	if _, err := resolver.Edit(ctx, "project-a", authorized, document, &alone); err == nil {
		t.Fatal("expansion alias alone invalidated dependencies without rejection")
	}
	updated, err := resolver.EditBatch(ctx, "project-a", authorized, document, []authoringv2.ImportedSourceStepChange{alone, {StepID: "identity", Parameters: identity}, {StepID: "fields", Parameters: fields}})
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
	registry := &importedSourceRegistry{entry: recipe.Entry{Bundle: bundle, Digest: digest}}
	engine, err := dataframeexecution.New(dataframeexecution.Config{Registry: registry, ScopeDigest: reciperuntime.ScopeDigest, ResolveBundle: fielddiscovery.RecipeSchemaResolver(func(_ context.Context, options fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
		if options.Project != "project-a" || options.DatasetGeneration != "generation-a" || options.ResourceType != "Specimen" {
			t.Fatalf("discovery lost exact scope: %+v", options)
		}
		return []fieldcatalog.PopulatedField{{ResourceType: "Specimen", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"shadowed"}}}, nil
	}, nil), QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
		t.Fatal("compile-only probe unexpectedly queried runtime")
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolver := Sources{registry: registry, engine: engine, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "specimen", ResourceType: "Specimen", RowRootEligible: true}}, nil, nil, nil)
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
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
	w, err := recipeimport.ImportWorkspace(bundle, ws.CatalogFromCapability(snapshot, "test"), schemas)
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
	changed, err := resolver.Edit(ctx, "project-a", authorized, document, &authoringv2.ImportedSourceStepChange{StepID: "fields", Parameters: params})
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
	candidate := facade.CompiledRecipeOutput{OutputSchema: []facade.CompiledOutputColumn{{ID: "new-a", Name: "renamed-b"}, {ID: "new-b", Name: "renamed-a"}}}
	if err := rebindImportedGeneratedProjectionIDs(&candidate, before, after); err != nil {
		t.Fatal(err)
	}
	if candidate.OutputSchema[0].ID != "old-b" || candidate.OutputSchema[1].ID != "old-a" {
		t.Fatal("native hierarchy identity followed mutable public names or array order")
	}
	after[1].SemanticKey = "unknown-parent/leaf"
	candidate.OutputSchema[0].ID = "new-a"
	candidate.OutputSchema[1].ID = "new-b"
	original := append([]facade.CompiledOutputColumn(nil), candidate.OutputSchema...)
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
	resolver   Sources
	authorized ports.AuthorizedCapability
	snapshot   capability.Snapshot
	workspace  ws.Workspace
}

func newSourceIdentityFixture(t *testing.T, output recipe.Output, fields []fieldcatalog.PopulatedField) sourceIdentityFixture {
	t.Helper()
	ctx := context.Background()
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	registry := &importedSourceRegistry{entry: recipe.Entry{Bundle: bundle, Digest: digest}}
	engine, err := dataframeexecution.New(dataframeexecution.Config{Registry: registry, ScopeDigest: reciperuntime.ScopeDigest, ResolveBundle: fielddiscovery.RecipeSchemaResolver(func(_ context.Context, options fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
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
	resolver := Sources{registry: registry, engine: engine, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "root", ResourceType: output.RootResourceType, RowRootEligible: true}}, nil, nil, nil)
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
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
	workspace, err := recipeimport.ImportWorkspace(bundle, ws.CatalogFromCapability(snapshot, "test"), schemas)
	if err != nil {
		t.Fatal(err)
	}
	return sourceIdentityFixture{resolver, authorized, snapshot, workspace}
}

func TestImportedSourceGeneratedNamingPreservesNativeKeys(t *testing.T) {
	cases := []struct {
		name   string
		output recipe.Output
		fields []fieldcatalog.PopulatedField
		step   string
		edit   func(*ws.ImportedSourceParameters)
	}{
		{"dynamic family", recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", DynamicColumns: []recipe.DynamicColumn{{Name: "identifiers", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, MaxColumns: 1}}}, []fieldcatalog.PopulatedField{{ResourceType: "Specimen", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"system-key"}}}, "dynamicColumns[0]", func(p *ws.ImportedSourceParameters) { p.Dynamic.Name = "project_identifiers" }},
		{"typed extension family", recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", ExtensionColumns: []recipe.ExtensionColumn{{Name: "typed_extensions", Source: recipe.Expression{Select: "root.extension[]"}, MaxColumns: 4}}}, []fieldcatalog.PopulatedField{{ResourceType: "Specimen", Path: "extension[].url", Kind: "scalar", ExtensionValues: []fieldcatalog.ExtensionValueObservation{{URL: "http://example.org/tag", SourcePath: "extension[]", ValuePath: "valueString", ValueType: "string"}}}}, "extensionColumns[0]", func(p *ws.ImportedSourceParameters) { p.Extension.Name = "project_extensions" }},
		{"catalog naming", recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", CatalogProjections: []recipe.CatalogProjection{{Name: "populated_fields", IncludePaths: []string{"*"}, Kinds: []string{"scalar"}, Naming: recipe.ColumnNamingPath, MaxColumns: 4}}}, []fieldcatalog.PopulatedField{{ResourceType: "Specimen", Path: "type.text", Kind: "scalar"}}, "catalogProjections[0]", func(p *ws.ImportedSourceParameters) {
			p.Catalog.Name = "selected_fields"
			p.Catalog.Naming = recipe.ColumnNamingPathSuffix
		}},
		{"pivot family and alias", recipe.Output{Name: "Observation", RootResourceType: "Observation", RowGrain: "resource", Pivots: []recipe.Pivot{{Name: "measurements", ColumnExpr: recipe.Expression{Select: "root.code.text"}, ValueExpr: recipe.Expression{Select: "root.valueString"}, Columns: []string{"code-a"}}}}, nil, "pivots[0]", func(p *ws.ImportedSourceParameters) {
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
			updated, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, document, &authoringv2.ImportedSourceStepChange{StepID: c.step, Parameters: params})
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
	f := newSourceIdentityFixture(t, output, []fieldcatalog.PopulatedField{{ResourceType: "Specimen", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"shadowed"}}})
	document := f.workspace.Documents[0]
	if len(document.Columns) != 1 {
		t.Fatal("fixture did not produce an actual shadowed field")
	}
	winner := document.Columns[0]
	document.Construction = &ws.Construction{Version: 1, Steps: []ws.ConstructionStep{{ID: "keep-winner", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}}, Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: winner.ColumnID, Operator: ws.ConstructionFilterExists}}, Outputs: []ws.StageColumn{{ID: winner.ColumnID, Name: winner.Column, Label: winner.Label, Type: winner.LogicalType}}}}}
	params := copySourceParametersForTest(t, document, "fields")
	params.FieldGroup.Fields[0].Label = "Updated hidden declaration"
	updated, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, document, &authoringv2.ImportedSourceStepChange{StepID: "fields", Parameters: params})
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

func TestImportedSourceStepRemovalRetainsDiscoveredWinnerIdentity(t *testing.T) {
	empty := ""
	output := recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", CollisionPolicy: "overwrite", Fields: []recipe.Field{{Name: "shadowed", Expr: recipe.Expression{Select: "root.id"}}}, DynamicColumns: []recipe.DynamicColumn{{Name: "identifiers", ColumnPrefix: &empty, Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, MaxColumns: 1}}}
	f := newSourceIdentityFixture(t, output, []fieldcatalog.PopulatedField{{ResourceType: "Specimen", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"shadowed"}}})
	document := f.workspace.Documents[0]
	if len(document.Columns) != 1 {
		t.Fatal("fixture missing discovered winner")
	}
	old := document.Columns[0]
	updated, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, document, &authoringv2.ImportedSourceStepChange{StepID: "fields", Remove: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Columns) != 1 || updated.Columns[0].ColumnID != old.ColumnID || updated.Columns[0].Column != old.Column {
		t.Fatal("source step removal changed an untouched discovered winner identity")
	}
	for _, step := range updated.ImportedSource.Steps {
		if step.ID == "fields" {
			t.Fatal("removed source step remains in recipe")
		}
	}
}

func TestImportedSourceNewPivotKeyRenameKeepsAuthoredIdentity(t *testing.T) {
	output := recipe.Output{Name: "Observation", RootResourceType: "Observation", RowGrain: "resource", Pivots: []recipe.Pivot{{Name: "measurements", ColumnExpr: recipe.Expression{Select: "root.code.text"}, ValueExpr: recipe.Expression{Select: "root.valueString"}, Columns: []string{"pinned-key"}}}}
	f := newSourceIdentityFixture(t, output, nil)
	document := f.workspace.Documents[0]
	firstParams := copySourceParametersForTest(t, document, "pivots[0]")
	firstParams.Pivot.Columns = append(firstParams.Pivot.Columns, "audited_score")
	first, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, document, &authoringv2.ImportedSourceStepChange{StepID: "pivots[0]", Parameters: firstParams})
	if err != nil {
		t.Fatal(err)
	}
	newID := ""
	stages := []ws.StageColumn{}
	for _, c := range first.Columns {
		if c.Column == "measurements__audited_score" {
			newID = c.ColumnID
		}
		stages = append(stages, ws.StageColumn{ID: c.ColumnID, Name: c.Column, Label: c.Label, Type: c.LogicalType})
	}
	if newID == "" {
		t.Fatal("real first edit did not expose new native key")
	}
	first.Construction = &ws.Construction{Version: 1, Steps: []ws.ConstructionStep{{ID: "keep-audited-score", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}}, Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: newID, Operator: ws.ConstructionFilterExists}}, Outputs: stages}}}
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
	second, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, first, &authoringv2.ImportedSourceStepChange{StepID: "pivots[0]", Parameters: nextParams})
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
	third, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, second, &authoringv2.ImportedSourceStepChange{StepID: "pivots[0]", Parameters: aliasParams})
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
	reloaded, err := ws.DecodeWorkspace(raw)
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
		fields         []fieldcatalog.PopulatedField
		step           string
		create, rename func(*ws.ImportedSourceParameters)
	}{
		{"dynamic", recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", DynamicColumns: []recipe.DynamicColumn{{Name: "values", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, MaxColumns: 4}}}, []fieldcatalog.PopulatedField{{ResourceType: "Specimen", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"pinned-key"}}, {ResourceType: "Specimen", Path: "extension[].url", Kind: "scalar", DistinctValues: []string{"new-key"}}}, "dynamicColumns[0]", func(p *ws.ImportedSourceParameters) {
			p.Dynamic.Source = recipe.Expression{Select: "root.extension[]"}
			p.Dynamic.Key = &recipe.Expression{Select: "item.url"}
			p.Dynamic.Value = &recipe.Expression{Select: "item.valueString"}
		}, func(p *ws.ImportedSourceParameters) { p.Dynamic.Name = "renamed_values" }},
		{"catalog", recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", CatalogProjections: []recipe.CatalogProjection{{Name: "fields", IncludePaths: []string{"id"}, Kinds: []string{"scalar"}, Naming: recipe.ColumnNamingPath, MaxColumns: 4}}}, []fieldcatalog.PopulatedField{{ResourceType: "Specimen", Path: "id", Kind: "scalar"}, {ResourceType: "Specimen", Path: "type.text", Kind: "scalar"}}, "catalogProjections[0]", func(p *ws.ImportedSourceParameters) { p.Catalog.IncludePaths = []string{"type.text"} }, func(p *ws.ImportedSourceParameters) {
			p.Catalog.Name = "renamed_fields"
			p.Catalog.Naming = recipe.ColumnNamingPathSuffix
		}},
		{"typed extension", recipe.Output{Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "file", ExtensionColumns: []recipe.ExtensionColumn{{Name: "extensions", Source: recipe.Expression{Select: "root.extension[]"}, MaxColumns: 4}}}, []fieldcatalog.PopulatedField{{ResourceType: "DocumentReference", Path: "extension[].url", Kind: "scalar", ExtensionValues: []fieldcatalog.ExtensionValueObservation{{URL: "http://example.org/pinned", SourcePath: "extension[]", ValuePath: "valueString", ValueType: "string"}}}, {ResourceType: "DocumentReference", Path: "content[].attachment.extension[].url", Kind: "scalar", ExtensionValues: []fieldcatalog.ExtensionValueObservation{{URL: "http://example.org/new-leaf", SourcePath: "content[].attachment.extension[]", ValuePath: "valueString", ValueType: "string"}, {URL: "http://example.org/second-leaf", SourcePath: "content[].attachment.extension[]", ValuePath: "valueString", ValueType: "string"}}}}, "extensionColumns[0]", func(p *ws.ImportedSourceParameters) {
			p.Extension.Source = recipe.Expression{Select: "root.content[].attachment.extension[]"}
		}, func(p *ws.ImportedSourceParameters) {
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
			first, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, document, &authoringv2.ImportedSourceStepChange{StepID: c.step, Parameters: params})
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
			first.Construction = &ws.Construction{Version: 1, Steps: []ws.ConstructionStep{{ID: "bound-new-key", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}}, Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: column.ColumnID, Operator: ws.ConstructionFilterExists}}, Outputs: stageColumnsForSourceIdentity(first.Columns)}}}
			compile := func(d ws.Document) {
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
			second, err := f.resolver.Edit(context.Background(), "project-a", f.authorized, first, &authoringv2.ImportedSourceStepChange{StepID: c.step, Parameters: params})
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
			reloaded, err := ws.DecodeWorkspace(raw)
			if err != nil {
				t.Fatal(err)
			}
			compile(reloaded.Documents[0])
		})
	}
}

func TestNewNativeKeyIDsExcludeMutablePresentationAndRejectLeafAmbiguity(t *testing.T) {
	makeCandidate := func(ids ...string) *facade.CompiledRecipeOutput {
		c := &facade.CompiledRecipeOutput{Name: "Output"}
		for _, id := range ids {
			c.OutputSchema = append(c.OutputSchema, facade.CompiledOutputColumn{ID: id, Name: "mutable_" + id})
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
	original := append([]facade.CompiledOutputColumn(nil), b.OutputSchema...)
	if err := rebindImportedGeneratedProjectionIDs(b, nil, []importedProjectionFact{extension, other}); err == nil || !reflect.DeepEqual(original, b.OutputSchema) {
		t.Fatal("ambiguous new native leaves were assigned arbitrary or partial identities")
	}
}

func stageColumnsForSourceIdentity(columns []ws.Column) []ws.StageColumn {
	result := make([]ws.StageColumn, 0, len(columns))
	for _, c := range columns {
		result = append(result, ws.StageColumn{ID: c.ColumnID, Name: c.Column, Label: c.Label, Type: c.LogicalType})
	}
	return result
}

const (
	importedOutputID    = "MedicationAdministration"
	doseUnitColumnID    = "builder_dose_unit"
	medCategoryColumnID = "builder_medication_category"
)

type unifiedAuthoringFixture struct {
	bundle     recipe.Bundle
	digest     string
	resolver   Sources
	authorized ports.AuthorizedCapability
	workspace  ws.Workspace
}

func findUnifiedRecipeOutput(bundle recipe.Bundle, name string) (recipe.Output, bool) {
	for _, output := range bundle.Outputs {
		if output.Name == name {
			return output, true
		}
	}
	return recipe.Output{}, false
}

func unifiedColumnIDs(columns []ws.Column) []string {
	ids := make([]string, 0, len(columns))
	for _, column := range columns {
		ids = append(ids, column.ColumnID)
	}
	return ids
}

func newUnifiedAuthoringFixture(t *testing.T) unifiedAuthoringFixture {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "recipeimport", "testdata", "default-dataframer.json"))
	if err != nil {
		t.Fatalf("read checked-in imported recipe fixture: %v", err)
	}
	var bundle recipe.Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("decode checked-in imported recipe fixture: %v", err)
	}
	if len(bundle.Outputs) == 0 {
		t.Fatal("imported recipe fixture has no outputs")
	}
	output, ok := findUnifiedRecipeOutput(bundle, importedOutputID)
	if !ok {
		t.Fatalf("imported recipe fixture has no %q output", importedOutputID)
	}
	bundle.Outputs = []recipe.Output{output}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatalf("digest imported recipe fixture: %v", err)
	}
	registry := &importedSourceRegistry{entry: recipe.Entry{Bundle: bundle, Digest: digest}}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: registry, ScopeDigest: reciperuntime.ScopeDigest,
		ResolveBundle: fielddiscovery.RecipeSchemaResolver(func(context.Context, fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
			return nil, nil
		}, nil),
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			t.Fatal("baseline fixture unexpectedly queried live data")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("create isolated recipe resolver: %v", err)
	}
	resolver := Sources{registry: registry, engine: engine, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope)},
		capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_medication_administration", ResourceType: "MedicationAdministration", RowRootEligible: true, RowGrain: "resource"}},
		nil,
		[]capability.Candidate{{ID: "dose-unit", NodeID: "n_medication_administration", ResourceType: "MedicationAdministration", FieldPath: "dosage.dose.unit", Label: "Dose unit", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil,
	)
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	source, err := resolver.load(context.Background(), "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized)
	if err != nil {
		if sourceErr, ok := err.(*ports.Error); ok && sourceErr.Cause != nil {
			t.Fatalf("resolve checked-in imported recipe fixture: %v", sourceErr.Cause)
		}
		t.Fatalf("resolve checked-in imported recipe fixture: %v", err)
	}
	schemas := make(map[string][]recipe.StageColumn, len(source.resolved.Compiled.Outputs))
	for _, output := range source.resolved.Compiled.Outputs {
		columns, err := importedSourceSchema(output.OutputSchema)
		if err != nil {
			t.Fatalf("derive %q imported schema: %v", output.Name, err)
		}
		for _, column := range columns {
			schemas[output.Name] = append(schemas[output.Name], column.Column)
		}
	}
	workspace, err := recipeimport.ImportWorkspace(bundle, ws.CatalogFromCapability(snapshot, "test"), schemas)
	if err != nil {
		t.Fatalf("import checked-in recipe into Builder document: %v", err)
	}
	var selected *ws.Document
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == importedOutputID {
			selected = &workspace.Documents[index]
			break
		}
	}
	if selected == nil {
		t.Fatalf("imported workspace has no %q document", importedOutputID)
	}
	var selectedTab []ws.Tab
	for _, tab := range workspace.Tabs {
		if tab.OutputID == importedOutputID {
			selectedTab = append(selectedTab, tab)
		}
	}
	workspace.Documents = []ws.Document{*selected}
	workspace.Tabs = selectedTab
	if err := workspace.Validate(); err != nil {
		t.Fatalf("selected imported output workspace is invalid: %v", err)
	}
	return unifiedAuthoringFixture{bundle: bundle, digest: digest, resolver: resolver, authorized: authorized, workspace: workspace}
}

func addUnifiedAuthoringColumns(t *testing.T, document ws.Document) ws.Document {
	t.Helper()
	visible := true
	document.Columns = append(document.Columns,
		ws.Column{
			ColumnID: doseUnitColumnID, Column: "dose_unit", Label: "Dose unit", LogicalType: "string",
			OccurrenceID: ws.RootOccurrenceID,
			Source:       ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "dosage.dose.unit", ProjectionMode: "VALUE"}},
			Table:        &ws.TablePresentation{Visible: &visible},
		},
		ws.Column{
			ColumnID: medCategoryColumnID, Column: "medication_category", Label: "Medication category", LogicalType: "string",
			OccurrenceID: ws.RootOccurrenceID,
			Source: ws.ColumnSource{Kind: ws.SourceCodedValue, Lookup: &ws.LookupSource{
				Binding: &fhirbinding.CorrelatedBinding{
					OwnerPath: "category[]", KeyPath: "category[].coding[]", ValueScope: fhirbinding.CorrelatedValueScopeCoding,
					SelectionScope: fhirbinding.CorrelatedSelectionScopeExact,
					SystemPath:     "system", CodePath: "code", ValuePath: "display",
					ValueFallback: []string{"code"}, FallbackOnBlankString: true, LogicalType: "string",
				},
				Key: &fhirbinding.CorrelatedKey{System: "urn:example:medication-administration-category", Code: "inpatient"},
			}},
			Table: &ws.TablePresentation{Visible: &visible},
		},
	)
	if err := document.Validate(); err != nil {
		t.Fatalf("imported recipe plus Builder columns is invalid: %v", err)
	}
	return document
}

func TestImportedSourceEditRetainsBuilderAddedColumns(t *testing.T) {
	fixture := newUnifiedAuthoringFixture(t)
	document := addUnifiedAuthoringColumns(t, fixture.workspace.Documents[0])
	params := copySourceParametersForTest(t, document, "fields")
	if params.FieldGroup == nil || len(params.FieldGroup.Fields) == 0 {
		t.Fatal("MedicationAdministration fixture has no editable source fields")
	}
	params.FieldGroup.Fields[0].Label = "Administration identifier"
	updated, err := fixture.resolver.Edit(context.Background(), "project-a", fixture.authorized, document, &authoringv2.ImportedSourceStepChange{StepID: "fields", Parameters: params})
	if err != nil {
		t.Fatalf("edit imported recipe source: %v", err)
	}
	if !reflect.DeepEqual(updated.ImportedRecipe, document.ImportedRecipe) {
		t.Fatal("source edit changed the immutable imported recipe pin")
	}
	wantColumns := append([]ws.Column(nil), document.Columns...)
	wantColumns[0].Label = params.FieldGroup.Fields[0].Label
	if !reflect.DeepEqual(updated.Columns, wantColumns) {
		t.Fatalf("source edit changed the authored column sequence or dropped Builder additions: got ids %v, want ids %v", unifiedColumnIDs(updated.Columns), unifiedColumnIDs(wantColumns))
	}

	workspace := fixture.workspace
	workspace.Documents = []ws.Document{updated}
	resolved, err := fixture.resolver.Resolve(context.Background(), "project-a", workspace, fixture.authorized)
	if err != nil {
		t.Fatalf("resolve edited imported recipe: %v", err)
	}
	compiled, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "test", workspace, fixture.authorized.Snapshot, explorercompilation.ResolvedInputs{ImportedRecipes: resolved})
	if err != nil {
		t.Fatalf("compile imported recipe after source edit: %v", err)
	}
	if len(compiled.Bundle.Outputs) != 1 || len(compiled.Presentations) != 1 || len(compiled.OutputContracts) != 1 {
		t.Fatalf("edited compile has incomplete output metadata: %#v", compiled)
	}
	output := compiled.Bundle.Outputs[0]
	var doseUnit *recipe.Field
	for index := range output.Fields {
		if output.Fields[index].ColumnID == doseUnitColumnID {
			doseUnit = &output.Fields[index]
			break
		}
	}
	if doseUnit == nil || doseUnit.Name != "dose_unit" || doseUnit.Label != "Dose unit" || doseUnit.FieldRef != "dosage.dose.unit" {
		t.Fatalf("compiled direct Builder projection lost its identity or meaning: %#v", doseUnit)
	}
	var medicationCategory *recipe.Pivot
	for index := range output.Pivots {
		if output.Pivots[index].ColumnAliases["inpatient"] == "medication_category" {
			medicationCategory = &output.Pivots[index]
			break
		}
	}
	if medicationCategory == nil || medicationCategory.Correlation == nil || medicationCategory.CorrelationCode != "inpatient" {
		t.Fatalf("compiled coded-value Builder projection lost its exact category binding: %#v", medicationCategory)
	}
	presentation := compiled.Presentations[0].Columns
	columnOrder := map[string]int{}
	for _, column := range presentation {
		columnOrder[column.PublicColumn] = column.Order
	}
	doseOrder, dosePresent := columnOrder["dose_unit"]
	categoryOrder, categoryPresent := columnOrder["medication_category"]
	if !dosePresent || !categoryPresent || doseOrder >= categoryOrder {
		t.Fatalf("compiled presentation lost authored column order: dose=%d present=%v category=%d present=%v", doseOrder, dosePresent, categoryOrder, categoryPresent)
	}
	for _, expected := range []string{"dose_unit", "medication_category"} {
		found := false
		for _, column := range compiled.OutputContracts[0].Columns {
			if column.Column == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("compiled output contract omitted authored column %q", expected)
		}
	}
}

func importedSchemaFromDocument(document ws.Document) []recipe.StageColumn {
	var schema []recipe.StageColumn
	for _, column := range document.Columns {
		if !isImportedRecipeColumn(column) {
			continue
		}
		schema = append(schema, recipe.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	return schema
}

func rebuildSourceOutput(t *testing.T, fixture unifiedAuthoringFixture) recipe.Output {
	t.Helper()
	output, ok := findUnifiedRecipeOutput(fixture.bundle, importedOutputID)
	if !ok {
		t.Fatalf("fixture has no %q output", importedOutputID)
	}
	return output
}

func rebuildSourceCatalog(fixture unifiedAuthoringFixture) ws.CatalogSnapshot {
	return ws.CatalogFromCapability(fixture.authorized.Snapshot, "")
}

func TestRebuildImportedDocumentPreservesAuthoredColumnsAcrossSchemaChanges(t *testing.T) {
	fixture := newUnifiedAuthoringFixture(t)
	document := addUnifiedAuthoringColumns(t, fixture.workspace.Documents[0])
	pinned := importedSchemaFromDocument(document)
	if len(pinned) < 4 {
		t.Fatalf("fixture has only %d pinned columns", len(pinned))
	}
	authoredDirect, authoredCoded := document.Columns[len(document.Columns)-2], document.Columns[len(document.Columns)-1]
	visible, tableOrder, filterOrder, chartOrder := false, 7, 3, 2
	authoredDirect.Table = &ws.TablePresentation{Visible: &visible, Order: &tableOrder, Pinned: true, CellRenderer: "fileActions"}
	authoredDirect.Filter = &ws.FilterPresentation{Label: "Dose filter", Order: &filterOrder}
	authoredDirect.Chart = &ws.ChartPresentation{Type: "bar", Title: "Dose units", Order: &chartOrder}
	authoredDirect.LogicalType = "string"
	authoredCoded.Table = &ws.TablePresentation{Visible: &visible, Pinned: true}
	authoredCoded.Label = "Authored medication category"
	document.Columns = []ws.Column{
		document.Columns[0], authoredDirect, document.Columns[1], document.Columns[2],
		authoredCoded, document.Columns[3], document.Columns[4],
	}
	if err := document.Validate(); err != nil {
		t.Fatalf("interleaved source and authored columns are invalid: %v", err)
	}
	wantAuthored := []ws.Column{authoredDirect, authoredCoded}

	schema := []recipe.StageColumn{
		{ID: pinned[0].ID, Name: "renamed_pinned_column", Label: "Renamed source field", Type: "string"},
		{ID: "new_pinned_column_id", Name: "new_pinned_column", Label: "New pinned field", Type: "boolean"},
		pinned[1], pinned[3], pinned[4],
	}
	updated, err := rebuildImportedDocument(document, rebuildSourceOutput(t, fixture), schema, rebuildSourceCatalog(fixture))
	if err != nil {
		t.Fatalf("rebuild changed imported schema: %v", err)
	}
	gotIDs := unifiedColumnIDs(updated.Columns)
	wantIDs := []string{pinned[0].ID, authoredDirect.ColumnID, "new_pinned_column_id", pinned[1].ID, authoredCoded.ColumnID, pinned[3].ID, pinned[4].ID}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("merged column order = %v, want %v", gotIDs, wantIDs)
	}
	var gotAuthored []ws.Column
	for _, column := range updated.Columns {
		if column.ColumnID == authoredDirect.ColumnID || column.ColumnID == authoredCoded.ColumnID {
			gotAuthored = append(gotAuthored, column)
		}
	}
	if !reflect.DeepEqual(gotAuthored, wantAuthored) {
		t.Fatalf("authored column metadata changed during schema refresh:\n got: %#v\nwant: %#v", gotAuthored, wantAuthored)
	}
	if got := updated.Columns[0]; got.Column != "renamed_pinned_column" || got.Label != "Renamed source field" || got.Source.Imported == nil || got.Source.Imported.OutputColumn != got.Column {
		t.Fatalf("stable pinned column did not refresh its schema identity: %#v", got)
	}
	for _, column := range updated.Columns {
		if column.ColumnID == pinned[2].ID {
			t.Fatalf("removed pinned schema column %q remained in the candidate", pinned[2].ID)
		}
	}
}

func TestRebuildImportedDocumentRejectsAuthoredIdentityAndNameCollisions(t *testing.T) {
	t.Run("stable id collision", func(t *testing.T) {
		fixture := newUnifiedAuthoringFixture(t)
		document := addUnifiedAuthoringColumns(t, fixture.workspace.Documents[0])
		document.Columns[len(document.Columns)-2].ColumnID = "new_pinned_column_id"
		inputColumns := append([]ws.Column(nil), document.Columns...)
		schema := append(importedSchemaFromDocument(document), recipe.StageColumn{ID: "new_pinned_column_id", Name: "new_pinned", Label: "New pinned"})
		updated, err := rebuildImportedDocument(document, rebuildSourceOutput(t, fixture), schema, rebuildSourceCatalog(fixture))
		if err == nil || !strings.Contains(err.Error(), "collides with an authored column") {
			t.Fatalf("stable ID collision returned document=%#v err=%v", updated, err)
		}
		if !reflect.DeepEqual(document.Columns, inputColumns) {
			t.Fatal("rejected source rebuild mutated the caller's document")
		}
	})

	t.Run("public name collision", func(t *testing.T) {
		fixture := newUnifiedAuthoringFixture(t)
		document := addUnifiedAuthoringColumns(t, fixture.workspace.Documents[0])
		schema := append(importedSchemaFromDocument(document), recipe.StageColumn{ID: "new_pinned_name_collision", Name: "dose_unit", Label: "Conflicting source name"})
		updated, err := rebuildImportedDocument(document, rebuildSourceOutput(t, fixture), schema, rebuildSourceCatalog(fixture))
		if err == nil || !strings.Contains(err.Error(), "duplicate column") {
			t.Fatalf("public name collision returned document=%#v err=%v", updated, err)
		}
	})
}

func TestRebuildImportedDocumentRejectsRemovedAuthoredDependencies(t *testing.T) {
	t.Run("removed route", func(t *testing.T) {
		fixture := newUnifiedAuthoringFixture(t)
		document := addUnifiedAuthoringColumns(t, fixture.workspace.Documents[0])
		document.Route.Children = []ws.RouteNode{{OccurrenceID: "pinned_child", ResourceType: "Patient", Relationship: "subject"}}
		document.Columns[len(document.Columns)-2].OccurrenceID = "pinned_child"
		if err := document.Validate(); err != nil {
			t.Fatalf("route dependency fixture is invalid: %v", err)
		}
		output := rebuildSourceOutput(t, fixture)
		output.Traversals = nil
		updated, err := rebuildImportedDocument(document, output, importedSchemaFromDocument(document), rebuildSourceCatalog(fixture))
		if err == nil || !strings.Contains(err.Error(), "unknown occurrence") {
			t.Fatalf("removed route returned document=%#v err=%v", updated, err)
		}
	})

	t.Run("removed fixed filter output", func(t *testing.T) {
		fixture := newUnifiedAuthoringFixture(t)
		document := fixture.workspace.Documents[0]
		if len(document.Columns) == 0 {
			t.Fatal("fixture has no imported columns")
		}
		removed := document.Columns[0]
		document.FixedFilters = []ws.FixedFilter{{Column: removed.Column, Values: []string{"active"}}}
		if err := document.Validate(); err != nil {
			t.Fatalf("fixed filter dependency fixture is invalid: %v", err)
		}
		schema := importedSchemaFromDocument(document)[1:]
		updated, err := rebuildImportedDocument(document, rebuildSourceOutput(t, fixture), schema, rebuildSourceCatalog(fixture))
		if err == nil || !strings.Contains(err.Error(), "fixedFilters") {
			t.Fatalf("removed fixed-filter column returned document=%#v err=%v", updated, err)
		}
	})

	t.Run("removed construction input", func(t *testing.T) {
		fixture := newUnifiedAuthoringFixture(t)
		document := addUnifiedAuthoringColumns(t, fixture.workspace.Documents[0])
		input := document.Columns[0]
		outputs := make([]ws.StageColumn, len(document.Columns))
		for index, column := range document.Columns {
			outputs[index] = ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType}
		}
		document.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
			ID: "retain_source", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
				ColumnID: input.ColumnID, Operator: ws.ConstructionFilterExists,
			}},
			Outputs: outputs,
		}}}
		if err := document.Validate(); err != nil {
			t.Fatalf("construction dependency fixture is invalid: %v", err)
		}
		schema := importedSchemaFromDocument(document)[1:]
		updated, err := rebuildImportedDocument(document, rebuildSourceOutput(t, fixture), schema, rebuildSourceCatalog(fixture))
		if err == nil || !strings.Contains(err.Error(), "construction") {
			t.Fatalf("removed construction input returned document=%#v err=%v", updated, err)
		}
	})
}
