package importedsource

import (
	"context"
	"encoding/json"
	"errors"
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
)

func TestImportedRelationshipAddMatchRemoveRoundTripPreservesPin(t *testing.T) {
	ctx := context.Background()
	output := recipe.Output{Name: "Specimen", RootResourceType: "Specimen", RowGrain: "specimen", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	registry := &importedSourceRegistry{entry: recipe.Entry{Bundle: bundle, Digest: digest}}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: registry, ScopeDigest: reciperuntime.ScopeDigest,
		ResolveBundle: fielddiscovery.RecipeSchemaResolver(func(_ context.Context, options fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
			if options.ResourceType != "Patient" {
				return nil, nil
			}
			return []fieldcatalog.PopulatedField{{ResourceType: "Patient", Path: "identifier[].system", Kind: "scalar", DistinctValues: []string{"fixture-system"}}}, nil
		}, nil),
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return errors.New("unexpected query during source editing")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver := Sources{registry: registry, engine: engine, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "specimen", ResourceType: "Specimen", RowRootEligible: true}, {ID: "patient", ResourceType: "Patient", RowRootEligible: true}}, []capability.Edge{{ID: "subject", FromNodeID: "specimen", ToNodeID: "patient", Label: "subject_Patient", StorageDirection: "OUTBOUND"}}, []capability.Candidate{{ID: "patient-id", NodeID: "patient", ResourceType: "Patient", FieldPath: "id", Label: "Patient ID", LogicalType: "string", Cardinality: "required_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}}}, nil)
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	workspace, err := recipeimport.ImportWorkspace(bundle, ws.CatalogFromCapability(snapshot, "test"), map[string][]recipe.StageColumn{"Specimen": {{ID: "specimen-id", Name: "id", Label: "ID", Type: "string"}}})
	if err != nil {
		t.Fatal(err)
	}
	original := workspace.Documents[0]
	before, _ := json.Marshal(original)
	added, err := resolver.EditRelationship(ctx, "project-a", authorized, original, authoringv2.ImportedRelationshipChange{Action: "ADD", ParentOccurrenceID: ws.RootOccurrenceID, EdgeID: "subject", MatchMode: ws.RouteMatchOptional})
	if err != nil {
		t.Fatal(err)
	}
	if *added.ImportedRecipe != *original.ImportedRecipe || len(added.Route.Children) != 1 || added.Rows.Kind != ws.RowDefinitionRecords {
		t.Fatal("add changed pin or row identity")
	}
	relation := added.ImportedSource.Relationships.Additions[0]
	if !strings.HasPrefix(relation.OccurrenceID, "project_") || relation.Fields[0].ValueMode != "ALL" {
		t.Fatal("addition missing stable identity or explicit collection policy")
	}
	t.Run("identifier rule on Graph Connect occurrence", func(t *testing.T) {
		key, value := recipe.Expression{Select: "item.system"}, recipe.Expression{Select: "item.value"}
		params := &ws.ImportedSourceParameters{Dynamic: &ws.ImportedDynamicParameters{
			Name: "patient_identifiers", Source: recipe.Expression{Select: "identifier[]"}, Key: &key, Value: &value,
		}}
		created, err := resolver.CreateSourceRule(ctx, "project-a", authorized, added, ws.SourceRuleCreate{
			Kind: "DYNAMIC_COLUMN", OccurrenceID: relation.OccurrenceID, Parameters: params,
		})
		if err != nil {
			t.Fatal(err)
		}
		effective, err := recipeimport.ApplySourcePlan(output, created.ImportedSource)
		if err != nil {
			t.Fatal(err)
		}
		if len(effective.DynamicColumns) != 0 || len(effective.Traversals) != 1 || len(effective.Traversals[0].DynamicColumns) != 1 ||
			effective.Traversals[0].DynamicColumns[0].Source.Select != relation.OccurrenceID+".identifier[]" {
			t.Fatalf("rule escaped its Graph Connect traversal: %#v", effective.Traversals)
		}
		if len(created.Columns) != len(added.Columns)+1 {
			t.Fatalf("identifier rule did not generate a column: %#v", created.Columns)
		}
		hydrated, err := recipeimport.HydrateSourcePlan(output, created.ImportedSource)
		if err != nil {
			t.Fatal(err)
		}
		step := hydrated.Steps[len(hydrated.Steps)-1]
		if step.OccurrenceID != relation.OccurrenceID || step.ParentID != "relationships."+relation.OccurrenceID {
			t.Fatalf("rule step is not nested under its occurrence: %#v", step)
		}
		beforeIDs := map[string]bool{}
		for _, column := range added.Columns {
			beforeIDs[column.ColumnID] = true
		}
		var generatedID, generatedName string
		for _, column := range created.Columns {
			if !beforeIDs[column.ColumnID] {
				generatedID, generatedName = column.ColumnID, column.Column
			}
		}
		prefix := "external"
		editedParams := copySourceParametersForTest(t, created, step.ID)
		editedParams.Dynamic.ColumnPrefix = &prefix
		edited, err := resolver.Edit(ctx, "project-a", authorized, created, &authoringv2.ImportedSourceStepChange{
			StepID: step.ID, Parameters: editedParams,
		})
		if err != nil {
			t.Fatal(err)
		}
		retained := false
		for _, column := range edited.Columns {
			retained = retained || (column.ColumnID == generatedID && column.Column != generatedName)
		}
		if generatedID == "" || !retained {
			t.Fatal("renaming the generated column changed its ID")
		}
		_, err = resolver.EditRelationship(ctx, "project-a", authorized, created, authoringv2.ImportedRelationshipChange{Action: "REMOVE", OccurrenceID: relation.OccurrenceID})
		var unavailable *ws.UnavailableSourceRuleOccurrence
		if !errors.As(err, &unavailable) || unavailable.OccurrenceID != relation.OccurrenceID {
			t.Fatalf("removed rule occurrence error = %v", err)
		}
	})
	workspace.Documents[0] = added
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := ws.DecodeWorkspace(raw)
	if err != nil {
		t.Fatal(err)
	}
	required, err := resolver.EditRelationship(ctx, "project-a", authorized, reloaded.Documents[0], authoringv2.ImportedRelationshipChange{Action: "MATCH", OccurrenceID: relation.OccurrenceID, MatchMode: ws.RouteMatchRequired})
	if err != nil {
		t.Fatal(err)
	}
	if required.Route.Children[0].OccurrenceID != relation.OccurrenceID || required.Route.Children[0].MatchMode != ws.RouteMatchRequired {
		t.Fatal("match edit retargeted occurrence")
	}
	effective, err := recipeimport.ApplySourcePlan(output, required.ImportedSource)
	if err != nil {
		t.Fatal(err)
	}
	if len(effective.Traversals) != 1 || effective.Traversals[0].MatchMode != recipe.MatchRequired || effective.Traversals[0].Fields[0].ValueMode != recipe.ValueModeAll {
		t.Fatalf("native recipe lost requested policy: %#v", effective.Traversals)
	}
	removed, err := resolver.EditRelationship(ctx, "project-a", authorized, required, authoringv2.ImportedRelationshipChange{Action: "REMOVE", OccurrenceID: relation.OccurrenceID})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Route.Children) != 0 || *removed.ImportedRecipe != *original.ImportedRecipe {
		t.Fatal("removal lost pin or retained traversal")
	}
	after, _ := json.Marshal(original)
	if string(before) != string(after) {
		t.Fatal("relationship edit mutated original document")
	}
	if actual, _ := registry.entry.Bundle.Digest(); actual != digest {
		t.Fatal("shared immutable recipe changed")
	}
	for _, change := range []authoringv2.ImportedRelationshipChange{{Action: "ADD", ParentOccurrenceID: "missing", EdgeID: "subject", MatchMode: ws.RouteMatchOptional}, {Action: "ADD", ParentOccurrenceID: ws.RootOccurrenceID, EdgeID: "unauthorized", MatchMode: ws.RouteMatchOptional}, {Action: "MATCH", OccurrenceID: ws.RootOccurrenceID, MatchMode: ws.RouteMatchRequired}} {
		if _, err := resolver.EditRelationship(ctx, "project-a", authorized, original, change); err == nil {
			t.Fatalf("accepted unauthorized edit: %#v", change)
		}
	}
}

func TestImportedSourceSchemaPreservesCompilerFacts(t *testing.T) {
	columns := []facade.CompiledOutputColumn{
		{ID: "internal", Name: "__loom_row_id", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true},
		{ID: "identifier", Name: "identifier", Label: "Participant", Kind: "string", Cardinality: "optional_one", Nullable: true, SemanticPath: "root.identifier"},
		{ID: "codes", Name: "codes", Kind: "string", Cardinality: "many"},
	}
	actual, err := importedSourceSchema(columns)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != 2 {
		t.Fatalf("got %d public columns", len(actual))
	}
	if actual[0].Column.ID != "identifier" || actual[0].Column.Label != "Participant" || actual[0].Shape != "scalar" || actual[0].SemanticPath != "root.identifier" || !actual[0].Column.Nullable {
		t.Fatalf("compiler facts changed: %+v", actual[0])
	}
	if actual[1].Shape != "array" || actual[1].Cardinality != "many" || actual[1].Column.Label != "codes" {
		t.Fatalf("repeated source metadata: %+v", actual[1])
	}
	columns[1].Cardinality = "unknown"
	if _, err := importedSourceSchema(columns); err == nil {
		t.Fatal("unproven cardinality accepted")
	}
}

type importedSourceRegistry struct {
	entry recipe.Entry
	reads int
}

func (r *importedSourceRegistry) LoadRecipe(context.Context, string) (recipe.Entry, error) {
	r.reads++
	return r.entry, nil
}

func (r *importedSourceRegistry) LoadRecipeVersion(context.Context, string, string) (recipe.Entry, error) {
	r.reads++
	return r.entry, nil
}

func TestImportedSourceResolutionRejectsChangedPinsAndForeignScopes(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "resource", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}}}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	registry := &importedSourceRegistry{entry: recipe.Entry{Bundle: bundle, Digest: digest}}
	resolver := Sources{registry: registry, engine: &dataframeexecution.Engine{}, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	authorized := ports.AuthorizedCapability{Snapshot: capability.Snapshot{Identity: capability.SnapshotIdentity{Project: "HTAN_INT/BForePC", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope)}}, Scope: scope}
	if _, err := resolver.load(context.Background(), "HTAN_INT/other", bundle.Name, bundle.TranslationVersion, digest, authorized); err == nil {
		t.Fatal("foreign project accepted")
	}
	if registry.reads != 0 {
		t.Fatal("foreign scope reached registry")
	}
	if _, err := resolver.load(context.Background(), "HTAN_INT/BForePC", "unapproved", bundle.TranslationVersion, digest, authorized); err == nil {
		t.Fatal("unapproved source accepted")
	}
	if registry.reads != 0 {
		t.Fatal("unapproved source reached registry")
	}
	if _, err := resolver.load(context.Background(), "HTAN_INT/BForePC", bundle.Name, bundle.TranslationVersion, "stale", authorized); err == nil {
		t.Fatal("changed source pin accepted")
	}
	if registry.reads != 1 {
		t.Fatalf("reads = %d", registry.reads)
	}
	registry.entry.Digest = "corrupt"
	if _, err := resolver.load(context.Background(), "HTAN_INT/BForePC", bundle.Name, bundle.TranslationVersion, digest, authorized); err == nil {
		t.Fatal("corrupt registry digest accepted")
	}
}

func TestBindImportedSourceIDsPreservesExistingAndSeparatesOutputs(t *testing.T) {
	schema := []facade.CompiledOutputColumn{{Name: "identifier", SemanticPath: "root.identifier"}, {ID: "existing", Name: "age"}, {Name: "__loom_row_id", Internal: true, Identity: true}}
	first := bindImportedSourceIDs("Patient", schema)
	again := bindImportedSourceIDs("Patient", schema)
	other := bindImportedSourceIDs("Specimen", schema)
	if first[0].ID == "" || first[0].ID != again[0].ID || first[0].ID == other[0].ID {
		t.Fatal("imported identities must be stable and output scoped")
	}
	if first[1].ID != "existing" || first[2].ID != "" || schema[0].ID != "" {
		t.Fatal("binding changed existing identities, hidden columns, or the input schema")
	}
}

func TestSavedExtensionIdentitiesSurviveCorrelatedCompiler(t *testing.T) {
	ctx := context.Background()
	prefix := ""
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{{
		Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "resource",
		ExtensionColumns: []recipe.ExtensionColumn{
			{Name: "empty_family", Source: recipe.Expression{Select: "root.extension[]"}, MaxColumns: 10, Columns: []recipe.ExtensionColumnMapping{}},
			{Name: "attachment", ColumnPrefix: &prefix, Source: recipe.Expression{Select: "root.content[].attachment.extension[]"}, MaxColumns: 10, Columns: []recipe.ExtensionColumnMapping{
				{Name: "sha256", URL: "https://example.org/sha256", SourcePath: "content[].attachment.extension[]", ValuePath: "valueString", ValueType: "string"},
				{Name: "source_path", URL: "https://example.org/source_path", SourcePath: "content[].attachment.extension[]", ValuePath: "valueString", ValueType: "string"},
			}},
		},
	}}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "document", ResourceType: "DocumentReference", RowRootEligible: true}}, nil, nil, nil)
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	resolver := Sources{registry: &importedSourceRegistry{entry: recipe.Entry{Bundle: bundle, Digest: digest}}, engine: &dataframeexecution.Engine{}, defaultName: bundle.Name}
	source, err := resolver.load(ctx, "project-a", bundle.Name, bundle.TranslationVersion, digest, authorized)
	if err != nil {
		t.Fatal(err)
	}

	expected := map[string]string{"sha256": "import_0f168b510db1a4a305688b94", "source_path": "import_7732e25ef1f51e1498d5710e"}
	schemas := map[string][]recipe.StageColumn{}
	for _, output := range source.resolved.Compiled.Outputs {
		columns, err := importedSourceSchema(output.OutputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range columns {
			if column.Column.ID != expected[column.Column.Name] {
				t.Fatalf("%s identity changed: got %s want %s", column.Column.Name, column.Column.ID, expected[column.Column.Name])
			}
			schemas[output.Name] = append(schemas[output.Name], column.Column)
		}
	}
	workspace, err := recipeimport.ImportWorkspace(bundle, ws.CatalogFromCapability(snapshot, "DocumentReference"), schemas)
	if err != nil {
		t.Fatal(err)
	}
	for i := range workspace.Documents[0].Columns {
		workspace.Documents[0].Columns[i].ColumnID = expected[workspace.Documents[0].Columns[i].Column]
	}
	network, err := resolver.Network(ctx, "project-a", authorized, workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(network.ProjectedFields) != 2 {
		t.Fatalf("fields=%+v", network.ProjectedFields)
	}
	for _, field := range network.ProjectedFields {
		if field.Status != "EXACT" || field.Removal != "DISABLE_PINNED_OUTPUT" || field.SourceStepID != "extensionColumns[1]" {
			t.Fatalf("binding lost: %+v", field)
		}
	}
	resolved, err := resolver.Resolve(ctx, "project-a", workspace, authorized)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = explorercompilation.CompileWorkspace(ctx, "project-a", "test", workspace, snapshot, explorercompilation.ResolvedInputs{ImportedRecipes: resolved}); err != nil {
		t.Fatal(err)
	}
}
