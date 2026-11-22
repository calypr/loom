package viewbuilder

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/projectconfig"
)

type viewBuilderServiceStore struct {
	explorer.Store
	explorer.ViewBuilderRepository
	settings    explorer.ViewBuilderSettings
	owners      []explorer.Explorer
	revisions   map[string]explorer.Revision
	writes      int
	lastProject string
}

func (s *viewBuilderServiceStore) List(_ context.Context, project string) ([]explorer.Explorer, error) {
	s.lastProject = project
	return s.owners, nil
}
func (s *viewBuilderServiceStore) GetRevision(_ context.Context, id string) (*explorer.Revision, error) {
	value, ok := s.revisions[id]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	return &value, nil
}
func (s *viewBuilderServiceStore) GetViewBuilderSettings(context.Context, string) (explorer.ViewBuilderSettings, error) {
	return s.settings, nil
}
func (s *viewBuilderServiceStore) SaveViewBuilderConfig(_ context.Context, request explorer.ViewBuilderMutation) error {
	s.writes++
	definition := request.Publication.Definition
	revision := request.Publication.Revision
	revision.Status = explorer.RevisionActive
	s.revisions[revision.ID] = revision
	s.owners = []explorer.Explorer{{Project: request.Project, ExplorerID: definition.ID, ProjectConfig: &definition, ActiveRevisionID: revision.ID}}
	return nil
}

func TestViewBuilderServiceGETDoesNotInitializeProject(t *testing.T) {
	store := &viewBuilderServiceStore{}
	explorers, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	service := New(explorers)
	state, err := service.ViewBuilderState(context.Background(), "HTAN_INT/BForePC")
	if err != nil {
		t.Fatal(err)
	}
	if store.writes != 0 || len(state.Configs) != 0 || len(state.Catalogs) != 0 || state.Settings.Version != 0 {
		t.Fatalf("GET initialized project: %+v writes=%d", state, store.writes)
	}
	if store.lastProject != "HTAN_INT-BForePC" || state.Settings.Project != "HTAN_INT/BForePC" {
		t.Fatalf("project boundary storage=%q public=%q", store.lastProject, state.Settings.Project)
	}
	if state.Configs == nil || state.Catalogs == nil || state.Settings.Catalogs == nil {
		t.Fatal("GET returned null collections")
	}
}

func TestViewBuilderServiceSavesPresentationAgainstExactCatalog(t *testing.T) {
	resolved, published, _ := projectConfigPublicationFixture()
	snapshot, err := projectconfig.PrepareViewBuilderCatalogSnapshot(resolved, published, "commit-a")
	if err != nil {
		t.Fatal(err)
	}
	store := &viewBuilderServiceStore{
		settings:  explorer.ViewBuilderSettings{Version: 1, Catalogs: map[string]string{resolved.Bundle.Name: snapshot.ID}},
		revisions: map[string]explorer.Revision{snapshot.ID: snapshot},
	}
	explorers, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	service := New(explorers)
	config := explorer.ProjectConfig{ID: "custom", Title: "Custom", RecipeRef: explorer.ProjectConfigRecipeRef{Name: resolved.Bundle.Name}, Views: []explorer.ConfigView{{ID: "documents", Title: "Documents", Output: "Documents", Table: explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: "document_reference_id", Visible: true, Label: "My ID"}}}}}}
	request := explorer.ViewBuilderSaveRequest{CatalogRevisionID: snapshot.ID, Config: config}
	state, err := service.SaveViewBuilderConfig(context.Background(), "HTAN_INT/BForePC", config.ID, request, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if store.writes != 1 || len(state.Configs) != 1 || state.Configs[0].Config.Title != "Custom" {
		t.Fatalf("save=%+v writes=%d", state, store.writes)
	}
	revision := store.revisions[state.Configs[0].RevisionID]
	if revision.Publication.ExecutionID != published.ID || revision.SourceGeneration != published.DatasetGeneration || revision.CreatedBy != "editor" {
		t.Fatalf("view save changed execution or lost actor: %+v", revision.Publication)
	}
	request.CatalogRevisionID = "stale-catalog"
	_, err = service.SaveViewBuilderConfig(context.Background(), "HTAN_INT/BForePC", config.ID, request, "editor")
	if !errors.Is(err, explorer.ErrDraftConflict) || store.writes != 1 {
		t.Fatalf("stale catalog err=%v writes=%d", err, store.writes)
	}
	request.CatalogRevisionID = snapshot.ID
	_, err = service.SaveViewBuilderConfig(context.Background(), "different/project", config.ID, request, "editor")
	if !errors.Is(err, explorer.ErrInvalidViewBuilder) || store.writes != 1 {
		t.Fatalf("foreign project err=%v writes=%d", err, store.writes)
	}
}

func (s *viewBuilderServiceStore) Get(_ context.Context, project, id string) (*explorer.Explorer, error) {
	for _, owner := range s.owners {
		if owner.Project == project && owner.ExplorerID == id {
			return &owner, nil
		}
	}
	return nil, explorer.ErrNotFound
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
