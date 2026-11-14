package explorer

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	recipeexec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataset"
)

func TestDecodeProjectConfigsIsStrictAndDigestIsStable(t *testing.T) {
	valid := []byte(`{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":[{"id":"documents","title":"Documents","recipeRef":{"name":"metadata"},"views":[{"id":"documents","output":"Documents","table":{"columns":[]}}]}]}`)
	catalog, err := DecodeProjectConfigs(valid)
	if err != nil {
		t.Fatalf("DecodeProjectConfigs() error = %v", err)
	}
	first, err := catalog.Configs[0].Digest()
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	catalog.Configs[0].Views[0].Table.Columns = nil
	second, err := catalog.Configs[0].Digest()
	if err != nil {
		t.Fatalf("Digest() for nil columns error = %v", err)
	}
	if first != second {
		t.Fatalf("nil and empty columns changed digest: %q != %q", first, second)
	}

	for name, raw := range map[string]string{
		"unknown field":          `{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":[],"extra":true}`,
		"duplicate key":          `{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":[],"kind":"ProjectConfigs"}`,
		"trailing value":         `{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":[]} {}`,
		"null configs":           `{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":null}`,
		"inline recipe rejected": `{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":[{"id":"documents","title":"Documents","recipe":{"name":"metadata"},"views":[{"id":"documents","output":"Documents"}]}]}`,
		"unsupported chart":      `{"apiVersion":"loom.calypr.org/project-configs/v1","kind":"ProjectConfigs","configs":[{"id":"documents","title":"Documents","recipeRef":{"name":"metadata"},"views":[{"id":"documents","output":"Documents","charts":[{"column":"status","type":"scatter"}]}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeProjectConfigs([]byte(raw)); err == nil {
				t.Fatal("DecodeProjectConfigs() succeeded, want error")
			}
		})
	}
}

func TestPrepareProjectConfigsSharesOneExactPublication(t *testing.T) {
	resolved, executionValue, at := projectConfigPublicationFixture()
	configs := []ProjectConfig{
		{
			ID: "default", Title: "Metadata", RecipeRef: ProjectConfigRecipeRef{Name: resolved.Bundle.Name},
			Views: []ConfigView{{
				ID: "documents", Title: "Documents", Output: "Documents",
				Table:   ConfigTable{Columns: []ConfigColumn{{Column: "document_reference_id", Visible: true}, {Column: "document_reference_status", Visible: true}}},
				Filters: []ConfigFilter{{Column: "document_reference_status"}},
				Charts:  []ConfigChart{{Column: "document_reference_status", Type: "pie", Title: "Statuses"}},
				Actions: []ConfigAction{{Type: "export", Title: "Export", Output: "Documents", Columns: []string{"document_reference_id", "document_reference_extension_tag"}}},
			}},
			SharedFilters: map[string][]SharedFilter{"status": {{Output: "Documents", Column: "document_reference_status"}}},
			FileActions:   FileActions{Extensions: map[string][]string{"pdf": {"download"}}, Actions: map[string]string{"download": "download"}},
		},
		{
			ID: "files", Title: "Files", RecipeRef: ProjectConfigRecipeRef{Name: resolved.Bundle.Name},
			Views: []ConfigView{{
				ID: "files", Title: "File labels", Output: "Documents",
				Table:  ConfigTable{Columns: []ConfigColumn{{Column: "document_reference_extension_tag", Visible: true}}},
				Charts: []ConfigChart{{Column: "document_reference_extension_tag", Type: "pie"}},
			}},
		},
		{ID: "unrelated", Title: "Other recipe", RecipeRef: ProjectConfigRecipeRef{Name: "other-recipe"}, Views: []ConfigView{{ID: "other", Output: "Other"}}},
	}

	prepared, err := PrepareProjectConfigs(configs, resolved, executionValue, "source-commit")
	if err != nil {
		t.Fatalf("PrepareProjectConfigs() error = %v", err)
	}
	if len(prepared) != 2 {
		t.Fatalf("prepared config count = %d, want 2 matching recipe refs", len(prepared))
	}
	if prepared[0].Definition.ID != "default" || prepared[1].Definition.ID != "files" {
		t.Fatalf("prepared order/IDs = %q, %q", prepared[0].Definition.ID, prepared[1].Definition.ID)
	}
	for _, publicationValue := range prepared {
		revision := publicationValue.Revision
		if revision.Publication.ExecutionID != executionValue.ID || revision.Dataset.Generation != executionValue.DatasetGeneration || revision.RecipeDigest != resolved.StoredRecipeDigest || revision.ResolvedSchemaDigest != resolved.ResolvedSchemaDigest || revision.Dataset.SchemaDigest != executionValue.SchemaDigest {
			t.Fatalf("revision is not pinned to shared publication: %#v", revision)
		}
		if !revision.CreatedAt.Equal(executionValue.CreatedAt) || revision.ReadyAt == nil || !revision.ReadyAt.Equal(at) || !revision.Publication.UpdatedAt.Equal(at) {
			t.Fatalf("revision timestamps were not copied from publication: %#v", revision)
		}
		var configPacket map[string]json.RawMessage
		if err := json.Unmarshal(revision.Config, &configPacket); err != nil {
			t.Fatalf("decode revision config: %v", err)
		}
		if _, ok := configPacket["recipe"]; ok {
			t.Fatalf("revision ConfigV2 embeds recipe instead of referencing Revision.Recipe: %s", revision.Config)
		}
		if revision.Recipe.Name != resolved.Bundle.Name || revision.Recipe.TranslationVersion != resolved.Bundle.TranslationVersion {
			t.Fatalf("revision recipe snapshot is not the resolved recipe: %#v", revision.Recipe)
		}
		for _, materialization := range revision.Materializations {
			if materialization.MaterializationID != executionValue.ID {
				t.Fatalf("materialization %q points at %q, want %q", materialization.OutputID, materialization.MaterializationID, executionValue.ID)
			}
			for _, column := range materialization.Columns {
				if column.LoomOwned || column.Name == "auth_resource_path" || strings.HasPrefix(column.Name, "__loom_") {
					t.Fatalf("internal physical metadata leaked into materialization: %#v", column)
				}
			}
		}
		for _, output := range revision.Dataset.Outputs {
			for _, column := range output.Columns {
				if column.LoomOwned || column.Name == "auth_resource_path" || strings.HasPrefix(column.Name, "__loom_") {
					t.Fatalf("internal physical metadata leaked into dataset: %#v", column)
				}
			}
		}
		runtime, err := BuildViewerProjection(&revision)
		if err != nil {
			t.Fatalf("BuildViewerProjection(%q) error = %v", publicationValue.Definition.ID, err)
		}
		if runtime == nil || len(runtime.Outputs) != 1 || runtime.Outputs[0].Materialization == nil || runtime.Outputs[0].Materialization.MaterializationID != executionValue.ID {
			t.Fatalf("runtime for %q does not use shared materialization: %#v", publicationValue.Definition.ID, runtime)
		}
	}
	if prepared[0].Revision.ID == prepared[1].Revision.ID {
		t.Fatalf("different config definitions share revision ID %q", prepared[0].Revision.ID)
	}
	metadataRuntime, err := BuildViewerProjection(&prepared[0].Revision)
	if err != nil {
		t.Fatal(err)
	}
	filesRuntime, err := BuildViewerProjection(&prepared[1].Revision)
	if err != nil {
		t.Fatal(err)
	}
	visibleColumns := make([]string, 0)
	for _, column := range metadataRuntime.Outputs[0].Table.Columns {
		if column.Visible {
			visibleColumns = append(visibleColumns, column.Column)
		}
	}
	if !reflect.DeepEqual(visibleColumns, []string{"document_reference_id", "document_reference_status"}) {
		t.Fatalf("default visible table columns = %#v, bindings=%#v", visibleColumns, metadataRuntime.Outputs[0].Table.Columns)
	}
	filesVisible := make([]string, 0)
	for _, column := range filesRuntime.Outputs[0].Table.Columns {
		if column.Visible {
			filesVisible = append(filesVisible, column.Column)
		}
	}
	if len(metadataRuntime.Outputs[0].Charts) != 1 || metadataRuntime.Outputs[0].Charts[0].Type != "pie" || !reflect.DeepEqual(filesVisible, []string{"document_reference_extension_tag"}) {
		t.Fatalf("distinct presentation not retained: metadata=%#v files=%#v", metadataRuntime.Outputs[0], filesRuntime.Outputs[0])
	}
}

func TestPrepareProjectConfigsRejectsStalePublicationAndInvalidBindings(t *testing.T) {
	resolved, executionValue, _ := projectConfigPublicationFixture()
	valid := ProjectConfig{ID: "files", Title: "Files", RecipeRef: ProjectConfigRecipeRef{Name: resolved.Bundle.Name}, Views: []ConfigView{{ID: "files", Output: "Documents", Table: ConfigTable{Columns: []ConfigColumn{{Column: "document_reference_id", Visible: true}}}}}}

	tests := map[string]func(*execution.Resolved, *publication.BundleExecution, *ProjectConfig){
		"project mismatch": func(_ *execution.Resolved, value *publication.BundleExecution, _ *ProjectConfig) {
			value.Project = "other-project"
		},
		"generation mismatch": func(_ *execution.Resolved, value *publication.BundleExecution, _ *ProjectConfig) {
			value.DatasetGeneration = "other-generation"
		},
		"recipe digest mismatch": func(_ *execution.Resolved, value *publication.BundleExecution, _ *ProjectConfig) {
			value.RecipeDigest = "other-recipe"
		},
		"schema mismatch": func(_ *execution.Resolved, value *publication.BundleExecution, _ *ProjectConfig) {
			value.SchemaDigest = "other-schema"
		},
		"scope mismatch": func(_ *execution.Resolved, value *publication.BundleExecution, _ *ProjectConfig) {
			value.ScopeDigest = "other-scope"
		},
		"queryable selector mismatch": func(_ *execution.Resolved, value *publication.BundleExecution, _ *ProjectConfig) {
			value.Outputs[0].Selector.Output = "Other"
		},
		"unknown column": func(_ *execution.Resolved, _ *publication.BundleExecution, config *ProjectConfig) {
			config.Views[0].Table.Columns[0].Column = "missing"
		},
		"internal column": func(_ *execution.Resolved, _ *publication.BundleExecution, config *ProjectConfig) {
			config.Views[0].Table.Columns[0].Column = "__loom_row_id"
		},
		"internal filter": func(_ *execution.Resolved, _ *publication.BundleExecution, config *ProjectConfig) {
			config.Views[0].Filters = []ConfigFilter{{Column: "__loom_row_id"}}
		},
		"non-scalar chart": func(_ *execution.Resolved, _ *publication.BundleExecution, config *ProjectConfig) {
			config.Views[0].Charts = []ConfigChart{{Column: "document_reference_payload", Type: "pie"}}
		},
		"unknown action column": func(_ *execution.Resolved, _ *publication.BundleExecution, config *ProjectConfig) {
			config.Views[0].Actions = []ConfigAction{{Type: "export", Title: "Export", Columns: []string{"missing"}}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			r, e, config := cloneProjectConfigPublicationFixture(resolved, executionValue, valid)
			mutate(&r, &e, &config)
			if _, err := PrepareProjectConfigs([]ProjectConfig{config}, r, e, "source-commit"); err == nil {
				t.Fatal("PrepareProjectConfigs() succeeded, want error")
			}
		})
	}
}

func TestPrepareProjectConfigsIsDeterministicAndRequiresPublishedExecution(t *testing.T) {
	resolved, executionValue, _ := projectConfigPublicationFixture()
	config := ProjectConfig{ID: "files", Title: "Files", RecipeRef: ProjectConfigRecipeRef{Name: resolved.Bundle.Name}, Views: []ConfigView{{ID: "files", Output: "Documents"}}}
	first, err := PrepareProjectConfigs([]ProjectConfig{config}, resolved, executionValue, "source-commit")
	if err != nil {
		t.Fatal(err)
	}
	second, err := PrepareProjectConfigs([]ProjectConfig{config}, resolved, executionValue, "source-commit")
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Revision.ID != second[0].Revision.ID || !first[0].Revision.CreatedAt.Equal(second[0].Revision.CreatedAt) {
		t.Fatalf("same publication produced unstable revision: %#v != %#v", first[0].Revision, second[0].Revision)
	}

	bad := executionValue
	bad.State = publication.BundleFailed
	if _, err := PrepareProjectConfigs([]ProjectConfig{config}, resolved, bad, "source-commit"); err == nil {
		t.Fatal("failed execution was accepted")
	}
	if _, err := PrepareProjectConfigs([]ProjectConfig{config}, resolved, executionValue, ""); err == nil {
		t.Fatal("empty source commit was accepted")
	}
	unmatched, err := PrepareProjectConfigs([]ProjectConfig{{ID: "other", Title: "Other", RecipeRef: ProjectConfigRecipeRef{Name: "other"}, Views: []ConfigView{{ID: "v", Output: "Other"}}}}, resolved, executionValue, "source-commit")
	if err != nil || len(unmatched) != 0 {
		t.Fatalf("unmatched recipe config = %#v, %v; want empty success", unmatched, err)
	}
	empty, err := PrepareProjectConfigs(nil, resolved, executionValue, "source-commit")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty config set = %#v, %v; want empty success", empty, err)
	}
	unmatchedInvalid, err := PrepareProjectConfigs([]ProjectConfig{{
		ID: "other", Title: "Other", RecipeRef: ProjectConfigRecipeRef{Name: "other"},
		Views: []ConfigView{{ID: "v", Output: "Other", Charts: []ConfigChart{{Column: "missing", Type: "unsupported"}}}},
	}}, resolved, executionValue, "source-commit")
	if err != nil || len(unmatchedInvalid) != 0 {
		t.Fatalf("unmatched config affected data publication: %#v, %v", unmatchedInvalid, err)
	}
}

func TestPrepareProjectConfigsAllowsOnlyOmittedDiscoveredPublicationColumns(t *testing.T) {
	resolved, published, _ := projectConfigPublicationFixture()
	logical, err := publication.WithPublicationMetadata(execution.PublicationColumns(resolved, "Documents"))
	if err != nil {
		t.Fatal(err)
	}
	retained := make([]publication.LogicalColumn, 0, len(logical))
	physical := make([]publication.PhysicalColumn, 0, len(published.Outputs[0].Columns))
	for _, column := range logical {
		if column.Name == "document_reference_extension_tag" && column.Provenance == publication.ColumnDiscovered {
			continue
		}
		retained = append(retained, column)
	}
	for _, column := range published.Outputs[0].Columns {
		if column.Name != "document_reference_extension_tag" {
			physical = append(physical, column)
		}
	}
	published.Outputs[0].Columns = physical
	published.SchemaDigest = publication.FinalSchemaDigest(publication.PublicationIdentity{
		RecipeDigest: published.RecipeDigest, ScopeDigest: published.ScopeDigest,
		DatasetGeneration: published.DatasetGeneration,
	}, []publication.OutputSchema{{Name: "Documents", Columns: retained}})
	config := ProjectConfig{ID: "files", Title: "Files", RecipeRef: ProjectConfigRecipeRef{Name: resolved.Bundle.Name}, Views: []ConfigView{{ID: "files", Output: "Documents"}}}
	prepared, err := PrepareProjectConfigs([]ProjectConfig{config}, resolved, published, "source-commit")
	if err != nil {
		t.Fatalf("unpopulated discovered column rejected exact publication: %v", err)
	}
	for _, column := range prepared[0].Revision.Dataset.Outputs[0].Columns {
		if column.Name == "document_reference_extension_tag" {
			t.Fatal("unretained discovered column leaked into Viewer wire metadata")
		}
	}
	config.Views[0].Charts = []ConfigChart{{Column: "document_reference_extension_tag", Type: "pie"}}
	if _, err := PrepareProjectConfigs([]ProjectConfig{config}, resolved, published, "source-commit"); err == nil {
		t.Fatal("config referenced an unretained discovered column")
	}

	explicitMissing := published
	explicitMissing.Outputs = append([]publication.BundleOutputRecord(nil), published.Outputs...)
	explicitColumns := make([]publication.PhysicalColumn, 0, len(published.Outputs[0].Columns))
	for _, column := range published.Outputs[0].Columns {
		if column.Name != "document_reference_id" {
			explicitColumns = append(explicitColumns, column)
		}
	}
	explicitMissing.Outputs[0].Columns = explicitColumns
	if _, err := PrepareProjectConfigs([]ProjectConfig{ProjectConfig{ID: "files", Title: "Files", RecipeRef: ProjectConfigRecipeRef{Name: resolved.Bundle.Name}, Views: []ConfigView{{ID: "files", Output: "Documents"}}}}, resolved, explicitMissing, "source-commit"); err == nil {
		t.Fatal("missing explicit compiler column was accepted")
	}
}

func TestPrepareProjectConfigsUsesFinalEngineResolvedSchemaDigest(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "engine-resolved-recipe",
		TranslationVersion:  "engine-v1",
		Outputs: []recipe.Output{{
			Name: "Documents", RootResourceType: "DocumentReference", RowGrain: "file",
			Fields: []recipe.Field{{Name: "id", Label: "Document ID", Expr: recipe.Expression{Select: "root.id"}}},
		}},
	}
	bindings := recipe.RuntimeBindings{
		Project: "HTAN_INT-BForePC", DatasetGeneration: "generation-engine",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"HTAN_INT-BForePC"},
	}
	engine, err := execution.New(execution.Config{
		Registry:    projectConfigEmptyRecipeReader{},
		QueryRows:   func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
		ScopeDigest: func(recipe.RuntimeBindings) string { return "engine-scope-digest" },
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.ResolveBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatalf("Engine.ResolveBundle() error = %v", err)
	}
	if resolved.Semantic.ResolvedSchemaDigest != resolved.ResolvedSchemaDigest {
		t.Fatalf("final resolved schema digest = %q, semantic = %q", resolved.ResolvedSchemaDigest, resolved.Semantic.ResolvedSchemaDigest)
	}
	if resolved.Compiled.ResolvedSchemaDigest == resolved.ResolvedSchemaDigest {
		t.Fatalf("fixture no longer exercises Engine's pre-final compiled digest: %#v", resolved.Compiled)
	}

	created := time.Date(2026, time.November, 11, 10, 0, 0, 0, time.UTC)
	publishedAt := created.Add(time.Minute)
	verifiedAt := publishedAt.Add(-time.Second)
	compiledOutput := resolved.Compiled.Outputs[0]
	logicalColumns, err := publication.WithPublicationMetadata(execution.PublicationColumns(resolved, compiledOutput.Name))
	if err != nil {
		t.Fatal(err)
	}
	physicalColumns := projectConfigPhysicalFixture(logicalColumns)
	finalSchemaDigest := publication.FinalSchemaDigest(publication.PublicationIdentity{
		RecipeDigest: resolved.StoredRecipeDigest, ScopeDigest: resolved.Semantic.ScopeDigest,
		DatasetGeneration: bindings.DatasetGeneration,
	}, []publication.OutputSchema{{Name: compiledOutput.Name, Columns: logicalColumns}})
	selector := dataset.DataframeSelector{Recipe: resolved.Bundle.Name, TranslationVersion: resolved.Bundle.TranslationVersion, Output: compiledOutput.Name}
	published := publication.BundleExecution{
		ID: "engine-execution", Key: "engine-execution-key",
		BundleIdentity: publication.BundleIdentity{
			Name: resolved.Bundle.Name, TranslationVersion: resolved.Bundle.TranslationVersion,
			Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
			RecipeDigest: resolved.StoredRecipeDigest, SchemaDigest: finalSchemaDigest,
			ScopeDigest: resolved.Semantic.ScopeDigest, EngineVersion: "engine-v1",
			AuthScopeMode: string(bindings.AuthScopeMode), AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...),
		},
		State: publication.BundlePublished, CreatedAt: created, PublishedAt: &publishedAt,
		Outputs: []publication.BundleOutputRecord{{
			Name: compiledOutput.Name, PhysicalTable: "published_documents", Selector: selector,
			Columns: physicalColumns, State: publication.BundlePublished, VerifiedAt: &verifiedAt,
		}},
	}
	prepared, err := PrepareProjectConfigs([]ProjectConfig{{
		ID: "engine-config", Title: "Engine config", RecipeRef: ProjectConfigRecipeRef{Name: bundle.Name},
		Views: []ConfigView{{ID: "documents", Output: compiledOutput.Name}},
	}}, resolved, published, "source-commit")
	if err != nil {
		t.Fatalf("PrepareProjectConfigs() rejected exact Engine publication: %v", err)
	}
	if len(prepared) != 1 || prepared[0].Revision.ResolvedSchemaDigest != resolved.ResolvedSchemaDigest || prepared[0].Revision.Dataset.SchemaDigest != published.SchemaDigest || prepared[0].Revision.Dataset.SchemaDigest == resolved.ResolvedSchemaDigest {
		t.Fatalf("resolved and published schema digests were conflated: revision=%#v execution=%q resolved=%q", prepared, published.SchemaDigest, resolved.ResolvedSchemaDigest)
	}
}

type projectConfigEmptyRecipeReader struct{}

func (projectConfigEmptyRecipeReader) LoadRecipe(context.Context, string) (recipeexec.Entry, error) {
	return recipeexec.Entry{}, errors.New("recipe registry must not be used by ResolveBundle")
}

func (projectConfigEmptyRecipeReader) LoadRecipeVersion(context.Context, string, string) (recipeexec.Entry, error) {
	return recipeexec.Entry{}, errors.New("recipe registry must not be used by ResolveBundle")
}

func projectConfigPublicationFixture() (execution.Resolved, publication.BundleExecution, time.Time) {
	created := time.Date(2026, time.November, 11, 10, 0, 0, 0, time.UTC)
	published := created.Add(3 * time.Minute)
	verified := published.Add(-time.Second)
	bundle := recipe.Bundle{
		RecipeSchemaVersion: 1,
		Name:                "calypr-meta-default",
		TranslationVersion:  "v1",
		Outputs: []recipe.Output{{
			Name: "Documents", RootResourceType: "DocumentReference", RowGrain: "file",
			RootColumnNaming: recipe.RootColumnNamingPrefixed,
			Fields:           []recipe.Field{{Name: "id", Label: "Document ID", Expr: recipe.Expression{Select: "root.id"}}},
		}},
	}
	digest, err := bundle.Digest()
	if err != nil {
		panic(err)
	}
	resolved := execution.Resolved{
		Bundle: bundle, StoredRecipeDigest: digest, ResolvedSchemaDigest: "schema-v1",
		Semantic: semantic.ResolvedRecipePlan{
			ResolvedSchemaDigest: "schema-v1", ScopeDigest: "scope-v1", SourceGeneration: "generation-v1",
			SemanticPlan: semantic.RecipePlan{Bindings: recipe.RuntimeBindings{
				Project: "HTAN_INT-BForePC", DatasetGeneration: "generation-v1",
				AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"HTAN_INT-BForePC"},
			}},
		},
		Compiled: lower.CompiledRecipe{
			RecipeDigest: digest, TranslationVersion: bundle.TranslationVersion,
			ResolvedSchemaDigest: "compiler-schema-v0", ScopeDigest: "scope-v1", SourceGeneration: "generation-v1",
			Outputs: []lower.CompiledRecipeOutput{{
				Name: "Documents", RootResourceType: "DocumentReference", RootColumnNaming: recipe.RootColumnNamingPrefixed,
				OutputSchema: []lower.CompiledOutputColumn{
					{ID: "document-id", Name: "id", Label: "Document ID", Kind: "string", Cardinality: "required_one"},
					{ID: "status", Name: "status", Label: "Document status", Kind: "code", Cardinality: "optional_one", Nullable: true},
					{ID: "extension-tag", Name: "extension_tag", Label: "Extension tag", SemanticPath: "DocumentReference.extension.tag", Kind: "string", Cardinality: "optional_one", Nullable: true, Discovered: true},
					{ID: "payload", Name: "payload", Label: "Payload", Kind: "object", Cardinality: "optional_one", Nullable: true},
					{ID: "__loom_row_id", Name: "__loom_row_id", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true},
				},
			}},
		},
	}
	logicalColumns, err := publication.WithPublicationMetadata(execution.PublicationColumns(resolved, "Documents"))
	if err != nil {
		panic(err)
	}
	physicalColumns := projectConfigPhysicalFixture(logicalColumns)
	finalSchemaDigest := publication.FinalSchemaDigest(publication.PublicationIdentity{
		RecipeDigest: digest, ScopeDigest: "scope-v1", DatasetGeneration: "generation-v1",
	}, []publication.OutputSchema{{Name: "Documents", Columns: logicalColumns}})
	selector := dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: "Documents"}
	executionValue := publication.BundleExecution{
		ID: "execution-v1", Key: "key-v1",
		BundleIdentity: publication.BundleIdentity{
			Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Project: "HTAN_INT-BForePC",
			DatasetGeneration: "generation-v1", RecipeDigest: digest, SchemaDigest: finalSchemaDigest,
			ScopeDigest: "scope-v1", EngineVersion: "engine-v1", AuthScopeMode: string(authscope.ReadScopeRestricted),
			AuthResourcePaths: []string{"HTAN_INT-BForePC"},
		},
		State: publication.BundlePublished, CreatedAt: created, PublishedAt: &published,
		Outputs: []publication.BundleOutputRecord{{
			Name: "Documents", PhysicalTable: "published_documents", Selector: selector,
			Columns: physicalColumns, State: publication.BundlePublished, VerifiedAt: &verified,
		}},
	}
	return resolved, executionValue, published
}

func projectConfigPhysicalFixture(columns []publication.LogicalColumn) []publication.PhysicalColumn {
	physical := make([]publication.PhysicalColumn, 0, len(columns))
	for _, column := range columns {
		loomOwned := column.LoomOwned || column.IsIdentity || strings.HasPrefix(column.Name, "__loom_") || column.Name == "auth_resource_path" || column.Name == "project_id"
		physical = append(physical, publication.PhysicalColumn{
			ID: column.ID, Name: column.Name, SemanticPath: column.SemanticPath,
			ClickHouse: "String", LogicalType: column.Kind, Nullable: column.Nullable,
			Repeated: column.Repeated, Provenance: column.Provenance, LoomOwned: loomOwned,
		})
	}
	return physical
}

func cloneProjectConfigPublicationFixture(resolved execution.Resolved, executionValue publication.BundleExecution, config ProjectConfig) (execution.Resolved, publication.BundleExecution, ProjectConfig) {
	resolved.Compiled.Outputs = append([]lower.CompiledRecipeOutput(nil), resolved.Compiled.Outputs...)
	resolved.Compiled.Outputs[0].OutputSchema = append([]lower.CompiledOutputColumn(nil), resolved.Compiled.Outputs[0].OutputSchema...)
	executionValue.Outputs = append([]publication.BundleOutputRecord(nil), executionValue.Outputs...)
	executionValue.Outputs[0].Columns = append([]publication.PhysicalColumn(nil), executionValue.Outputs[0].Columns...)
	config.Views = append([]ConfigView(nil), config.Views...)
	if len(config.Views) > 0 {
		config.Views[0].Table.Columns = append([]ConfigColumn(nil), config.Views[0].Table.Columns...)
		config.Views[0].Filters = append([]ConfigFilter(nil), config.Views[0].Filters...)
		config.Views[0].Charts = append([]ConfigChart(nil), config.Views[0].Charts...)
		config.Views[0].Actions = append([]ConfigAction(nil), config.Views[0].Actions...)
	}
	return resolved, executionValue, config
}

type projectConfigPublicationTarget struct {
	schemas []publication.OutputSchema
	digest  string
}

func (*projectConfigPublicationTarget) SupportsObjectValues() bool { return true }

func (target *projectConfigPublicationTarget) Begin(_ context.Context, _ publication.PublicationIdentity, schemas []publication.OutputSchema) (publication.Transaction, error) {
	target.schemas = schemas
	return target, nil
}

func (*projectConfigPublicationTarget) WriteBatch(context.Context, string, []map[string]any) error {
	return nil
}

func (target *projectConfigPublicationTarget) FinalizeSchema(_ context.Context, schemas []publication.OutputSchema) error {
	target.schemas = schemas
	return nil
}

func (target *projectConfigPublicationTarget) SetFinalSchemaDigest(digest string) error {
	target.digest = digest
	return nil
}

func (*projectConfigPublicationTarget) SetQualityReports(context.Context, []publication.QualityReport) error {
	return nil
}

func (*projectConfigPublicationTarget) Commit(context.Context) ([]publication.PublishedOutput, error) {
	return nil, nil
}

func (*projectConfigPublicationTarget) Abort(context.Context, error) error { return nil }

func (*projectConfigPublicationTarget) Idempotent() bool { return false }

func (*projectConfigPublicationTarget) ExistingPublishedOutputs() []publication.PublishedOutput {
	return nil
}

func (*projectConfigPublicationTarget) ExistingQualityReports() []publication.QualityReport {
	return nil
}

func TestPrepareProjectConfigsDetachesNestedPresentationAfterRealPublication(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "project-config-alias", TranslationVersion: "v1",
		Outputs: []recipe.Output{{
			Name: "Documents", RootResourceType: "DocumentReference", RowGrain: "file",
			Fields: []recipe.Field{{Name: "id", Label: "Document ID", Expr: recipe.Expression{Select: "root.id"}}},
		}},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a", AuthScopeMode: authscope.ReadScopeUnrestricted}
	engine, err := execution.New(execution.Config{
		Registry:    projectConfigEmptyRecipeReader{},
		QueryRows:   func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
		ScopeDigest: func(recipe.RuntimeBindings) string { return "scope-a" },
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.ResolveBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	target := &projectConfigPublicationTarget{}
	identity := publication.PublicationIdentity{
		Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Project: bindings.Project,
		DatasetGeneration: bindings.DatasetGeneration, RecipeDigest: resolved.StoredRecipeDigest,
		ScopeDigest: resolved.Semantic.ScopeDigest, AuthScopeMode: string(bindings.AuthScopeMode),
		AuthResourcePaths: bindings.AuthResourcePaths,
	}
	if _, err := publication.Publish(context.Background(), target, identity, []publication.OutputStream{{
		Name: "Documents", Columns: execution.PublicationColumns(resolved, "Documents"),
		Stream: func(context.Context, func(map[string]any) error) error { return nil },
	}}, publication.Limits{}); err != nil {
		t.Fatalf("publication.Publish() error = %v", err)
	}
	created := time.Date(2026, time.November, 11, 10, 0, 0, 0, time.UTC)
	publishedAt := created.Add(time.Minute)
	verifiedAt := publishedAt.Add(-time.Second)
	executionValue := publication.BundleExecution{
		ID: "alias-execution", Key: "alias-execution-key",
		BundleIdentity: publication.BundleIdentity{
			Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Project: bindings.Project,
			DatasetGeneration: bindings.DatasetGeneration, RecipeDigest: resolved.StoredRecipeDigest,
			SchemaDigest: target.digest, ScopeDigest: resolved.Semantic.ScopeDigest,
			EngineVersion: "loom-recipe-v2", AuthScopeMode: string(bindings.AuthScopeMode),
		},
		State: publication.BundlePublished, CreatedAt: created, PublishedAt: &publishedAt,
		Outputs: []publication.BundleOutputRecord{{
			Name: "Documents", PhysicalTable: "published_documents",
			Selector: dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: "Documents"},
			Columns:  projectConfigPhysicalFixture(target.schemas[0].Columns),
			State:    publication.BundlePublished, VerifiedAt: &verifiedAt,
		}},
	}
	definition := ProjectConfig{
		ID: "alias-config", Title: "Alias config", RecipeRef: ProjectConfigRecipeRef{Name: bundle.Name},
		Views: []ConfigView{{
			ID: "documents", Output: "Documents",
			FixedFilters: map[string][]string{"document_reference_id": {"original"}},
			Actions: []ConfigAction{{
				Type: "export", Title: "Export", Columns: []string{"document_reference_id"},
				ExportHeaders: map[string]string{"document_reference_id": "Original ID"},
			}},
		}},
		FileActions: FileActions{Extensions: map[string][]string{"pdf": {"download"}}, Actions: map[string]string{"download": "Original download"}},
	}
	prepared, err := PrepareProjectConfigs([]ProjectConfig{definition}, resolved, executionValue, "source-commit")
	if err != nil {
		t.Fatalf("PrepareProjectConfigs() error = %v", err)
	}
	definition.Views[0].FixedFilters["document_reference_id"][0] = "mutated"
	definition.Views[0].Actions[0].Columns[0] = "mutated"
	definition.Views[0].Actions[0].ExportHeaders["document_reference_id"] = "Mutated ID"
	definition.FileActions.Extensions["pdf"][0] = "mutated"
	definition.FileActions.Actions["download"] = "Mutated download"
	if got := prepared[0].Definition.Views[0].FixedFilters["document_reference_id"][0]; got != "original" {
		t.Fatalf("prepared definition aliases caller map value: got %q, want original", got)
	}
	if prepared[0].Definition.Views[0].Actions[0].Columns[0] != "document_reference_id" || prepared[0].Definition.Views[0].Actions[0].ExportHeaders["document_reference_id"] != "Original ID" || prepared[0].Definition.FileActions.Extensions["pdf"][0] != "download" || prepared[0].Definition.FileActions.Actions["download"] != "Original download" {
		t.Fatalf("prepared nested action/file-action data aliases caller: %#v", prepared[0].Definition)
	}
	if !strings.Contains(string(prepared[0].Revision.Config), `"original"`) || strings.Contains(string(prepared[0].Revision.Config), `"mutated"`) || !strings.Contains(string(prepared[0].Revision.Config), `"Original ID"`) {
		t.Fatalf("revision presentation changed with caller mutation: %s", prepared[0].Revision.Config)
	}
}
