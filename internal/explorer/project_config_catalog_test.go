package explorer

import (
	"encoding/json"
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

func TestPrepareProjectConfigCatalogSnapshotIsDetachedAndComplete(t *testing.T) {
	resolved, executionValue, _ := projectConfigPublicationFixture()
	snapshot, err := PrepareProjectConfigCatalogSnapshot(resolved, executionValue, "source-commit")
	if err != nil {
		t.Fatalf("PrepareProjectConfigCatalogSnapshot() error = %v", err)
	}
	if snapshot.ExplorerID != "" || len(snapshot.Config) != 0 || snapshot.ID[:8] != "catalog_" {
		t.Fatalf("catalog snapshot has a hidden owner/config: %#v", snapshot)
	}
	if snapshot.Publication.ExecutionID != executionValue.ID || snapshot.SourceGeneration != executionValue.DatasetGeneration || snapshot.RecipeDigest != resolved.StoredRecipeDigest || snapshot.ResolvedSchemaDigest != resolved.ResolvedSchemaDigest || snapshot.Dataset.SchemaDigest != executionValue.SchemaDigest {
		t.Fatalf("catalog snapshot lost exact publication identity: %#v", snapshot)
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
