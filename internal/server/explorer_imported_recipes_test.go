package server

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func TestImportedSourceSchemaPreservesCompilerFacts(t *testing.T) {
	columns := []lower.CompiledOutputColumn{
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
	entry exec.Entry
	reads int
}

func (r *importedSourceRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	r.reads++
	return r.entry, nil
}
func (r *importedSourceRegistry) LoadRecipeVersion(context.Context, string, string) (exec.Entry, error) {
	r.reads++
	return r.entry, nil
}

func TestImportedSourceResolutionRejectsChangedPinsAndForeignScopes(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "resource", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}}}}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	registry := &importedSourceRegistry{entry: exec.Entry{Bundle: bundle, Digest: digest}}
	resolver := importedRecipeSources{registry: registry, engine: &dataframeexecution.Engine{}, defaultName: bundle.Name}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	authorized := lifecycle.AuthorizedCapability{Snapshot: capability.Snapshot{Identity: capability.SnapshotIdentity{Project: "HTAN_INT/BForePC", AuthorizationScopeDigest: explorerScopeDigest(scope)}}, Scope: scope}
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
	schema := []lower.CompiledOutputColumn{{Name: "identifier", SemanticPath: "root.identifier"}, {ID: "existing", Name: "age"}, {Name: "__loom_row_id", Internal: true, Identity: true}}
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
