package server

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/recipeimport"
)

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
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: explorerScopeDigest(scope)}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "document", ResourceType: "DocumentReference", RowRootEligible: true}}, nil, nil, nil)
	authorized := lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	resolver := importedRecipeSources{registry: &importedSourceRegistry{entry: exec.Entry{Bundle: bundle, Digest: digest}}, engine: &dataframeexecution.Engine{}, defaultName: bundle.Name}
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
	workspace, err := recipeimport.ImportWorkspace(bundle, authoringV2Catalog(snapshot, "DocumentReference"), schemas)
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
