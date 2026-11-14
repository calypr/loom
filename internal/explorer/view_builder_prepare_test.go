package explorer

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/publication"
)

func TestInitialProjectConfigForRecipeUsesRealOutputIDs(t *testing.T) {
	resolved, _, _ := projectConfigPublicationFixture()
	config, err := InitialProjectConfigForRecipe(resolved.Bundle)
	if err != nil {
		t.Fatalf("InitialProjectConfigForRecipe() error = %v", err)
	}
	if config.ID != resolved.Bundle.Name || config.RecipeRef.Name != resolved.Bundle.Name {
		t.Fatalf("initial config identity = %#v, want recipe-name owner and reference", config)
	}
	if len(config.Views) != len(resolved.Bundle.Outputs) {
		t.Fatalf("initial view count = %d, want %d", len(config.Views), len(resolved.Bundle.Outputs))
	}
	for i, output := range resolved.Bundle.Outputs {
		view := config.Views[i]
		if view.ID != output.Name || view.Output != output.Name || view.Table.Columns != nil {
			t.Fatalf("initial view %d = %#v, want output-backed view %q with public defaults", i, view, output.Name)
		}
	}
}

func TestPrepareViewBuilderCatalogSnapshotIsDetachedAndComplete(t *testing.T) {
	resolved, executionValue, _ := projectConfigPublicationFixture()
	snapshot, err := PrepareViewBuilderCatalogSnapshot(resolved, executionValue, "source-commit")
	if err != nil {
		t.Fatalf("PrepareViewBuilderCatalogSnapshot() error = %v", err)
	}
	if snapshot.ExplorerID != "" || len(snapshot.Config) != 0 || snapshot.ID[:8] != "catalog_" {
		t.Fatalf("catalog snapshot has a hidden owner/config: %#v", snapshot)
	}
	if snapshot.Publication.ExecutionID != executionValue.ID || snapshot.SourceGeneration != executionValue.DatasetGeneration || snapshot.RecipeDigest != resolved.StoredRecipeDigest || snapshot.ResolvedSchemaDigest != resolved.ResolvedSchemaDigest || snapshot.Dataset.SchemaDigest != executionValue.SchemaDigest {
		t.Fatalf("catalog snapshot lost exact publication identity: %#v", snapshot)
	}
	catalog, err := ViewBuilderCatalogFromRevision(snapshot)
	if err != nil {
		t.Fatalf("ViewBuilderCatalogFromRevision() error = %v", err)
	}
	if catalog.RecipeName != resolved.Bundle.Name || catalog.RevisionID != snapshot.ID || catalog.ExecutionID != executionValue.ID || catalog.Generation != executionValue.DatasetGeneration {
		t.Fatalf("catalog identity = %#v", catalog)
	}
	if len(catalog.Frames) != len(resolved.Bundle.Outputs) {
		t.Fatalf("catalog frame count = %d, want every output even though no config view exists", len(catalog.Frames))
	}
	for i, output := range resolved.Bundle.Outputs {
		if catalog.Frames[i].ID != output.Name || catalog.Frames[i].Title != output.Name {
			t.Fatalf("catalog frame %d = %#v, want actual output %q", i, catalog.Frames[i], output.Name)
		}
	}
	if got := ViewBuilderFrameID(catalog.RecipeName, catalog.Frames[0].ID); got != catalog.RecipeName+"/"+catalog.Frames[0].ID {
		t.Fatalf("qualified frame ID = %q", got)
	}
	recipeName, outputName, ok := parseViewBuilderFrameID(catalog.RecipeName + "/" + catalog.Frames[0].ID)
	if !ok || recipeName != catalog.RecipeName || outputName != catalog.Frames[0].ID {
		t.Fatalf("parse qualified frame ID = %q, %q, %t", recipeName, outputName, ok)
	}
}

func TestPrepareViewBuilderConfigClonesExactSnapshotWithoutRecipeBody(t *testing.T) {
	resolved, executionValue, _ := projectConfigPublicationFixture()
	snapshot, err := PrepareViewBuilderCatalogSnapshot(resolved, executionValue, "source-commit")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.EmittedColumns) == 0 {
		t.Fatal("fixture has no public emitted columns")
	}
	column := snapshot.EmittedColumns[0]
	config := ProjectConfig{
		ID: "file-review", Title: "File review", RecipeRef: ProjectConfigRecipeRef{Name: snapshot.Recipe.Name},
		Views: []ConfigView{{
			ID: "documents", Title: "Documents", Output: column.OutputID,
			Table: ConfigTable{Columns: []ConfigColumn{{Column: column.PublicColumn, Label: "Friendly label", Visible: true}}},
		}},
	}
	prepared, err := PrepareViewBuilderConfig(config, snapshot, "alice")
	if err != nil {
		t.Fatalf("PrepareViewBuilderConfig() error = %v", err)
	}
	revision := prepared.Revision
	if prepared.Definition.ID != config.ID || revision.ExplorerID != config.ID || revision.CreatedBy != "alice" {
		t.Fatalf("saved config identity/audit = %#v / %#v", prepared.Definition, revision)
	}
	if revision.Publication.ExecutionID != snapshot.Publication.ExecutionID || revision.SourceGeneration != snapshot.SourceGeneration || revision.RecipeDigest != snapshot.RecipeDigest || revision.ResolvedSchemaDigest != snapshot.ResolvedSchemaDigest || revision.Dataset.SchemaDigest != snapshot.Dataset.SchemaDigest {
		t.Fatalf("saved view changed publication identity: %#v", revision)
	}
	if !reflect.DeepEqual(revision.Recipe, snapshot.Recipe) || !reflect.DeepEqual(revision.EmittedColumns, snapshot.EmittedColumns) || !reflect.DeepEqual(revision.Materializations, snapshot.Materializations) || !reflect.DeepEqual(revision.Dataset, snapshot.Dataset) || !reflect.DeepEqual(revision.PublicOutputContract, snapshot.PublicOutputContract) {
		t.Fatal("saved view did not preserve the exact immutable compiler/materialization metadata")
	}
	var packet map[string]json.RawMessage
	if err := json.Unmarshal(revision.Config, &packet); err != nil {
		t.Fatalf("decode saved presentation: %v", err)
	}
	if _, ok := packet["recipe"]; ok {
		t.Fatalf("saved presentation embeds a recipe: %s", revision.Config)
	}
	runtime, err := BuildViewerProjection(&revision)
	if err != nil {
		t.Fatalf("BuildViewerProjection() error = %v", err)
	}
	if runtime == nil || len(runtime.Outputs) != 1 || runtime.Outputs[0].Table.Columns[0].Column != column.PublicColumn {
		t.Fatalf("saved view runtime = %#v", runtime)
	}
	retry, err := PrepareViewBuilderConfig(config, snapshot, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if retry.Revision.ID != revision.ID {
		t.Fatalf("same config/snapshot retry produced %q, want %q", retry.Revision.ID, revision.ID)
	}
}

func TestPrepareViewBuilderConfigRejectsRecipeAndColumnOutsideCatalog(t *testing.T) {
	resolved, executionValue, _ := projectConfigPublicationFixture()
	snapshot, err := PrepareViewBuilderCatalogSnapshot(resolved, executionValue, "source-commit")
	if err != nil {
		t.Fatal(err)
	}
	base := ProjectConfig{
		ID: "review", Title: "Review", RecipeRef: ProjectConfigRecipeRef{Name: snapshot.Recipe.Name},
		Views: []ConfigView{{ID: "documents", Output: snapshot.Recipe.Outputs[0].Name}},
	}
	wrongRecipe := base
	wrongRecipe.RecipeRef.Name = "another-recipe"
	if _, err := PrepareViewBuilderConfig(wrongRecipe, snapshot, "alice"); !errors.Is(err, ErrInvalidViewBuilder) {
		t.Fatalf("wrong recipe error = %v, want ErrInvalidViewBuilder", err)
	}
	unknownColumn := base
	unknownColumn.Views = append([]ConfigView(nil), base.Views...)
	unknownColumn.Views[0].Table.Columns = []ConfigColumn{{Column: "not-a-published-column", Visible: true}}
	if _, err := PrepareViewBuilderConfig(unknownColumn, snapshot, "alice"); !errors.Is(err, ErrInvalidViewBuilder) {
		t.Fatalf("unknown column error = %v, want ErrInvalidViewBuilder", err)
	}
}

func TestPrepareProjectConfigsForPublicationPreservesStaleChoicesAndHidesView(t *testing.T) {
	resolved, executionValue, _ := projectConfigPublicationFixture()
	base, err := PrepareProjectConfigs([]ProjectConfig{ProjectConfig{
		ID: "default", Title: "Metadata", RecipeRef: ProjectConfigRecipeRef{Name: resolved.Bundle.Name},
		Views: []ConfigView{{ID: "documents", Output: "Documents"}},
	}}, resolved, executionValue, "source-commit")
	if err != nil {
		t.Fatal(err)
	}
	var discovered string
	for _, emitted := range base[0].Revision.EmittedColumns {
		if strings.Contains(emitted.PublicColumn, "extension_tag") {
			discovered = emitted.PublicColumn
			break
		}
	}
	if discovered == "" {
		t.Fatal("fixture has no discovered extension column")
	}
	definition := ProjectConfig{
		ID: "default", Title: "Metadata", RecipeRef: ProjectConfigRecipeRef{Name: resolved.Bundle.Name},
		Views: []ConfigView{{
			ID: "documents", Title: "Documents", Output: "Documents",
			Table:        ConfigTable{Columns: []ConfigColumn{{Column: discovered, Label: "Extension tag", Visible: true}}},
			Filters:      []ConfigFilter{{Column: discovered}},
			FixedFilters: map[string][]string{discovered: {"keep"}},
		}},
	}
	resolved.Compiled.Outputs[0].OutputSchema = filterCompiledSchema(resolved.Compiled.Outputs[0].OutputSchema, "extension-tag")
	logicalColumns, err := publication.WithPublicationMetadata(execution.PublicationColumns(resolved, "Documents"))
	if err != nil {
		t.Fatal(err)
	}
	executionValue.Outputs[0].Columns = projectConfigPhysicalFixture(logicalColumns)
	executionValue.SchemaDigest = publication.FinalSchemaDigest(publication.PublicationIdentity{
		RecipeDigest: executionValue.RecipeDigest, ScopeDigest: executionValue.ScopeDigest,
		DatasetGeneration: executionValue.DatasetGeneration,
	}, []publication.OutputSchema{{Name: "Documents", Columns: logicalColumns}})

	prepared, err := PrepareProjectConfigsForPublication([]ProjectConfig{definition}, resolved, executionValue, "source-commit")
	if err != nil {
		t.Fatalf("PrepareProjectConfigsForPublication() error = %v", err)
	}
	if len(prepared) != 1 {
		t.Fatalf("publication returned %d configs, want one", len(prepared))
	}
	wantDefinitionDigest, err := definition.Digest()
	if err != nil {
		t.Fatal(err)
	}
	gotDefinitionDigest, err := prepared[0].Definition.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if gotDefinitionDigest != wantDefinitionDigest {
		t.Fatalf("publication changed saved definition digest: got %q, want %q", gotDefinitionDigest, wantDefinitionDigest)
	}
	revision := prepared[0].Revision
	if len(revision.Diagnostics) != 1 || revision.Diagnostics[0].Code != "VIEW_BINDING_UNAVAILABLE" {
		t.Fatalf("stale binding diagnostics = %#v", revision.Diagnostics)
	}
	var packet ConfigV2
	if err := json.Unmarshal(revision.Config, &packet); err != nil {
		t.Fatal(err)
	}
	if got := packet.Views[0].FixedFilters[discovered]; !reflect.DeepEqual(got, []string{"keep"}) {
		t.Fatalf("fixed filter was pruned from saved presentation: %#v", packet.Views[0].FixedFilters)
	}
	runtime, err := BuildViewerProjection(&revision)
	if err != nil {
		t.Fatal(err)
	}
	if runtime == nil || len(runtime.Outputs) != 0 {
		t.Fatalf("stale fixed-filter view must fail closed, runtime = %#v", runtime)
	}
}

func filterCompiledSchema(columns []lower.CompiledOutputColumn, omitID string) []lower.CompiledOutputColumn {
	filtered := make([]lower.CompiledOutputColumn, 0, len(columns))
	for _, column := range columns {
		if column.ID != omitID {
			filtered = append(filtered, column)
		}
	}
	return filtered
}

func TestSavedViewMetadataDoesNotMutatePublicationCatalog(t *testing.T) {
	r, e, _ := projectConfigPublicationFixture()
	snapshot, err := PrepareViewBuilderCatalogSnapshot(r, e, "commit")
	if err != nil {
		t.Fatal(err)
	}
	c := ProjectConfig{ID: "first", Title: "First", RecipeRef: ProjectConfigRecipeRef{Name: snapshot.Recipe.Name}, Views: []ConfigView{{ID: "v", Output: snapshot.Recipe.Outputs[0].Name}}}
	first, err := PrepareViewBuilderConfig(c, snapshot, "alice")
	if err != nil {
		t.Fatal(err)
	}
	first.Revision.EmittedColumns[0].Label = "mutated first view"
	c.ID = "second"
	c.Title = "Second"
	if _, err := PrepareViewBuilderConfig(c, snapshot, "bob"); err != nil {
		t.Fatalf("mutating first returned view corrupted immutable source catalog and blocked second save: %v", err)
	}
}
