package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	publicationdata "github.com/calypr/loom/internal/dataframe/publication"
	publicationarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/arango/db"
	"github.com/calypr/loom/internal/explorer/arango/publicationdb"
	"github.com/calypr/loom/internal/explorer/arango/selectiondb"
	explorerconfig "github.com/calypr/loom/internal/explorer/config"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/projectconfig"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/explorer/viewer"
	"github.com/calypr/loom/internal/projectid"
	projectstore "github.com/calypr/loom/internal/store/arango"
	store "github.com/calypr/loom/internal/store/arango"
	storepkg "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestPublishProjectConfigsAtomicBatchRetryAndRetirementAgainstArango(t *testing.T) {
	ctx, client, explorers, generations := projectConfigPublicationDB(t)
	project := "project-config-batch"
	bundle := projectConfigTestBundle("recipe-a", "translation-1")

	first := projectConfigPublishRequest(t, ctx, client, generations, project, "generation-1", "execution-1", bundle,
		"commit-1", 0,
		projectConfigPublication(t, project, "default", "recipe-a", "Default", "revision-1-default", "generation-1", "execution-1", bundle),
		projectConfigPublication(t, project, "files", "recipe-a", "Files", "revision-1-files", "generation-1", "execution-1", bundle),
	)
	originalConfigJSON := string(first.Configs[0].Revision.Config)
	originalContractJSON := string(first.Configs[0].Revision.PublicOutputContract)
	active, err := explorers.PublishProjectConfigs(ctx, first)
	if err != nil {
		t.Fatalf("publish first config set: %v", err)
	}
	if active.Revision != 1 || active.Release.Generation != "generation-1" {
		t.Fatalf("first active release = %+v, want generation-1 revision 1", active)
	}
	if string(first.Configs[0].Revision.Config) != originalConfigJSON || string(first.Configs[0].Revision.PublicOutputContract) != originalContractJSON {
		t.Fatal("publish changed the caller's revision JSON while canonicalizing its storage copy")
	}
	assertProjectConfigActive(t, ctx, explorers, project, "default", "revision-1-default")
	assertProjectConfigActive(t, ctx, explorers, project, "files", "revision-1-files")

	retried, err := explorers.PublishProjectConfigs(ctx, first)
	if err != nil {
		t.Fatalf("retry exact active config publication: %v", err)
	}
	if retried.Revision != 1 || retried.Release.ID != active.Release.ID {
		t.Fatalf("retry release = %+v, want unchanged revision/id from %+v", retried, active)
	}

	otherDefinition := projectConfigTestDefinition("other-recipe", "recipe-b", "Other recipe")
	otherDigest, err := otherDefinition.Digest()
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for _, owner := range []explorer.Explorer{
		{Project: project, ExplorerID: "other-recipe", Title: otherDefinition.Title, ManagementMode: viewer.ManagementRepository, ProjectConfig: &otherDefinition, DraftVersion: 1, DraftDigest: otherDigest, UpdatedAt: createdAt},
		{Project: project, ExplorerID: "interactive", Title: "Builder owner", ManagementMode: viewer.ManagementInteractive, DraftVersion: 1, UpdatedAt: createdAt},
	} {
		if _, err := explorers.Create(ctx, owner); err != nil {
			t.Fatalf("seed preserved owner %q: %v", owner.ExplorerID, err)
		}
	}

	second := projectConfigPublishRequest(t, ctx, client, generations, project, "generation-2", "execution-2", bundle,
		"commit-2", active.Revision,
		projectConfigPublication(t, project, "default", "recipe-a", "Default v2", "revision-2-default", "generation-2", "execution-2", bundle),
	)
	active, err = explorers.PublishProjectConfigs(ctx, second)
	if err != nil {
		t.Fatalf("publish reduced config set: %v", err)
	}
	if active.Revision != 2 || active.Release.Generation != "generation-2" {
		t.Fatalf("second active release = %+v, want generation-2 revision 2", active)
	}
	assertProjectConfigActive(t, ctx, explorers, project, "default", "revision-2-default")
	files, err := explorers.Get(ctx, project, "files")
	if err != nil {
		t.Fatal(err)
	}
	if files.ActiveRevisionID != "" || files.ProjectConfig == nil || files.ProjectConfig.RecipeRef.Name != "recipe-a" {
		t.Fatalf("removed same-recipe owner = %+v, want retained definition with no active revision", files)
	}
	filesRevision, err := explorers.GetRevision(ctx, "revision-1-files")
	if err != nil || filesRevision.Status != explorer.RevisionSuperseded {
		t.Fatalf("removed config revision = %+v, %v; want superseded history", filesRevision, err)
	}
	other, err := explorers.Get(ctx, project, "other-recipe")
	if err != nil || other.ProjectConfig == nil || other.ProjectConfig.RecipeRef.Name != "recipe-b" || other.ActiveRevisionID != "" {
		t.Fatalf("other-recipe owner = %+v, %v; want preserved", other, err)
	}
	interactive, err := explorers.Get(ctx, project, "interactive")
	if err != nil || interactive.ManagementMode != viewer.ManagementInteractive || interactive.ProjectConfig != nil {
		t.Fatalf("interactive owner = %+v, %v; want preserved", interactive, err)
	}
}

func TestPublishProjectConfigsRejectsCollisionsImmutableConflictAndReleaseCASAgainstArango(t *testing.T) {
	ctx, client, explorers, generations := projectConfigPublicationDB(t)
	project := "project-config-guards"
	bundle := projectConfigTestBundle("recipe-a", "translation-1")
	request := projectConfigPublishRequest(t, ctx, client, generations, project, "generation-1", "execution-1", bundle,
		"commit-1", 0,
		projectConfigPublication(t, project, "taken", "recipe-a", "Taken", "revision-taken", "generation-1", "execution-1", bundle),
	)
	if _, err := explorers.Create(ctx, explorer.Explorer{Project: project, ExplorerID: "taken", Title: "Interactive", ManagementMode: viewer.ManagementInteractive, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := explorers.PublishProjectConfigs(ctx, request); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("interactive ID collision error = %v, want ErrDraftConflict", err)
	}
	assertNoProjectConfigRelease(t, ctx, generations, project)

	legacyProject := "project-config-legacy"
	legacyRequest := projectConfigPublishRequest(t, ctx, client, generations, legacyProject, "generation-1", "execution-legacy", bundle,
		"commit-legacy", 0,
		projectConfigPublication(t, legacyProject, "default", "recipe-a", "Default", "revision-legacy", "generation-1", "execution-legacy", bundle),
	)
	if _, err := explorers.Create(ctx, explorer.Explorer{Project: legacyProject, ExplorerID: "default", Title: "Legacy repository", ManagementMode: viewer.ManagementRepository, DraftVersion: 1, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := explorers.PublishProjectConfigs(ctx, legacyRequest); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("legacy repository owner collision error = %v, want ErrDraftConflict", err)
	}
	assertNoProjectConfigRelease(t, ctx, generations, legacyProject)

	immutableProject := "project-config-immutable"
	immutableRequest := projectConfigPublishRequest(t, ctx, client, generations, immutableProject, "generation-1", "execution-immutable", bundle,
		"commit-immutable", 0,
		projectConfigPublication(t, immutableProject, "default", "recipe-a", "Default", "revision-immutable", "generation-1", "execution-immutable", bundle),
	)
	conflicting := immutableRequest.Configs[0].Revision
	conflicting.Config = json.RawMessage(strings.Replace(string(conflicting.Config), `"column":"extension_code"`, `"column":"changed_column"`, 1))
	conflicting.PublicOutputContract = json.RawMessage(strings.Replace(string(conflicting.PublicOutputContract), `"Extension code"`, `"Changed label"`, 1))
	if string(conflicting.Config) == string(immutableRequest.Configs[0].Revision.Config) ||
		string(conflicting.PublicOutputContract) == string(immutableRequest.Configs[0].Revision.PublicOutputContract) {
		t.Fatal("immutable conflict fixture did not change both JSON payloads")
	}
	if _, err := explorers.InsertRevision(ctx, conflicting); err != nil {
		t.Fatalf("seed immutable revision key: %v", err)
	}
	if _, err := explorers.PublishProjectConfigs(ctx, immutableRequest); !errors.Is(err, explorer.ErrImmutableRevision) {
		t.Fatalf("immutable revision collision error = %v, want ErrImmutableRevision", err)
	}
	assertNoProjectConfigRelease(t, ctx, generations, immutableProject)

	casProject := "project-config-cas"
	casRequest := projectConfigPublishRequest(t, ctx, client, generations, casProject, "generation-1", "execution-cas", bundle,
		"commit-cas", 77,
		projectConfigPublication(t, casProject, "default", "recipe-a", "Default", "revision-cas", "generation-1", "execution-cas", bundle),
	)
	if _, err := explorers.PublishProjectConfigs(ctx, casRequest); !errors.Is(err, dataset.ErrReleaseActivationConflict) {
		t.Fatalf("wrong release CAS error = %v, want ErrReleaseActivationConflict", err)
	}
	assertNoProjectConfigRelease(t, ctx, generations, casProject)
	if _, err := explorers.Get(ctx, casProject, "default"); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("failed release CAS left project config owner: err=%v", err)
	}
	if _, err := explorers.GetRevision(ctx, "revision-cas"); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("failed release CAS left project config revision: err=%v", err)
	}
}

func TestPublishProjectConfigsReactivatesSupersededSameExecutionRevisionAgainstArango(t *testing.T) {
	ctx, client, explorers, generations := projectConfigPublicationDB(t)
	project := "project-config-reactivation"
	bundle := projectConfigTestBundle("recipe-a", "translation-1")
	first := projectConfigPublishRequest(t, ctx, client, generations, project, "generation-1", "execution-1", bundle,
		"commit-1", 0,
		projectConfigPublication(t, project, "default", "recipe-a", "Default A", "revision-a-default", "generation-1", "execution-1", bundle),
		projectConfigPublication(t, project, "files", "recipe-a", "Files A", "revision-a-files", "generation-1", "execution-1", bundle),
	)
	firstActive, err := explorers.PublishProjectConfigs(ctx, first)
	if err != nil {
		t.Fatalf("publish A config set: %v", err)
	}

	definitionB := projectConfigPublication(t, project, "default", "recipe-a", "Default B", "revision-b-default", "generation-1", "execution-1", bundle)
	second := first
	second.Configs = []explorer.ProjectConfigPublication{definitionB}
	second.ExpectedReleaseRevision = firstActive.Revision
	secondActive, err := explorers.PublishProjectConfigs(ctx, second)
	if err != nil {
		t.Fatalf("publish B config set on same execution: %v", err)
	}
	if secondActive.Revision != firstActive.Revision || secondActive.Release.ID != firstActive.Release.ID {
		t.Fatalf("same-execution B publication changed release: first=%+v second=%+v", firstActive, secondActive)
	}
	assertProjectConfigActive(t, ctx, explorers, project, "default", "revision-b-default")
	assertProjectConfigSuperseded(t, ctx, explorers, "revision-a-default")
	assertProjectConfigSuperseded(t, ctx, explorers, "revision-a-files")

	third := first
	third.ExpectedReleaseRevision = secondActive.Revision
	thirdActive, err := explorers.PublishProjectConfigs(ctx, third)
	if err != nil {
		t.Fatalf("reactivate exact A revisions on same execution: %v", err)
	}
	if thirdActive.Revision != firstActive.Revision || thirdActive.Release.ID != firstActive.Release.ID {
		t.Fatalf("same-execution A reactivation changed release: first=%+v third=%+v", firstActive, thirdActive)
	}
	assertProjectConfigActive(t, ctx, explorers, project, "default", "revision-a-default")
	assertProjectConfigActive(t, ctx, explorers, project, "files", "revision-a-files")
	assertProjectConfigSuperseded(t, ctx, explorers, "revision-b-default")
}

func projectConfigPublicationDB(t *testing.T) (context.Context, *projectstore.Client, *Store, *datasetarango.Store) {
	t.Helper()
	endpoint := os.Getenv("LOOM_TEST_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to run project config publication against a disposable database")
	}
	database := "loom_test_project_configs_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	client, err := projectstore.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		request, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
		if err != nil {
			t.Error(err)
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Error(err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("private project config database cleanup status=%d", response.StatusCode)
		}
	})
	collections := append([]projectstore.CollectionSpec{}, BootstrapSpec().Collections...)
	collections = append(collections, datasetarango.BootstrapSpec().Collections...)
	collections = append(collections, publicationarango.BootstrapSpec().Collections...)
	if err := client.Bootstrap(ctx, projectstore.BootstrapSpec{Collections: collections}); err != nil {
		t.Fatal(err)
	}
	explorers, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	generations, err := datasetarango.New(client)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, client, explorers, generations
}

func projectConfigTestBundle(name, translation string) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                name, TranslationVersion: translation,
		Outputs: []recipe.Output{{Name: "output", RootResourceType: "Patient", RowGrain: "resource"}},
	}
}

func projectConfigTestDefinition(id, recipeName, title string) explorerconfig.ProjectConfig {
	return explorerconfig.ProjectConfig{
		ID: id, Title: title, RecipeRef: explorerconfig.ProjectConfigRecipeRef{Name: recipeName},
		Views: []explorerconfig.ConfigView{{
			ID: "view", Title: title, Output: "output",
			Table:   explorerconfig.ConfigTable{Columns: []explorerconfig.ConfigColumn{{Column: "extension_code", Visible: true}}},
			Filters: []explorerconfig.ConfigFilter{{Column: "extension_code"}},
			Charts:  []explorerconfig.ConfigChart{{Column: "extension_code", Type: "pie", Title: "Extension codes"}},
		}},
	}
}

func projectConfigPublication(t *testing.T, project, id, recipeName, title, revisionID, generation, executionID string, bundle recipe.Bundle) explorer.ProjectConfigPublication {
	t.Helper()
	config := projectConfigTestDefinition(id, recipeName, title)
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	resolvedBundle := bundle
	resolvedBundle.Outputs = append([]recipe.Output(nil), bundle.Outputs...)
	resolvedBundle.Outputs[0].RootOccurrenceID = "resolved-recipe-snapshot"
	resolvedBundle.Outputs[0].DynamicColumns = []recipe.DynamicColumn{{
		Name: "extension_catalog", Source: recipe.Expression{Select: "root.extension[]"}, Columns: []string{"extension_code"},
		ColumnTypes: map[string]string{"extension_code": "string"}, MaxColumns: 1,
	}}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	readyAt := now
	physical := publicationdata.PhysicalColumn{Name: "extension_code", ClickHouse: "Nullable(String)", LogicalType: "string", Nullable: true}
	selector := dataset.DataframeSelector{Recipe: recipeName, TranslationVersion: bundle.TranslationVersion, Output: "output"}
	viewsJSON, err := json.Marshal(config.Views)
	if err != nil {
		t.Fatal(err)
	}
	explorerJSON, err := json.Marshal(explorerconfig.ConfigExplorer{ID: id, Title: title, Management: "repository"})
	if err != nil {
		t.Fatal(err)
	}
	configJSON := json.RawMessage(fmt.Sprintf(`{ "views": %s, "project": %q, "kind": "ExplorerConfig", "explorer": %s, "apiVersion": %q }`, viewsJSON, project, explorerJSON, explorerconfig.ConfigV2APIVersion))
	contractJSON := json.RawMessage(`{
  "outputs": [{
    "columns": [{
      "column": "extension_code",
      "label": "Extension code",
      "logicalType": "string",
      "cardinality": "optional_one",
      "nullable": true,
      "shape": "scalar",
      "lossless": true,
      "mlReady": false,
      "filterable": true,
      "chartable": true
    }],
    "outputId": "output",
    "rootResourceType": "Patient",
    "rowGrain": "resource",
    "lossless": true,
    "mlReady": false
  }]
}`)
	return explorer.ProjectConfigPublication{
		Definition: config,
		Revision: explorer.Revision{
			ID: revisionID, Project: projectid.Legacy(project), ExplorerID: id,
			Recipe: resolvedBundle, RecipeDigest: digest, ResolvedSchemaDigest: strings.Repeat("d", 64),
			Config: configJSON, PublicOutputContract: contractJSON,
			EmittedColumns: []viewer.EmittedColumn{{
				EmissionID: "dynamic:extension_code", OutputID: "output", PublicColumn: "extension_code",
				Label: "Extension code", LogicalType: "string", Cardinality: "optional_one",
				Nullable: true, Shape: "scalar", Lossless: true, Filterable: true, Chartable: true,
			}},
			SourceGeneration: generation, Dataset: viewer.DatasetMetadata{
				Generation: generation, SchemaDigest: strings.Repeat("c", 64),
				Outputs: []viewer.DatasetOutput{{Name: "output", State: "PUBLISHED", Queryable: true, Selector: &selector, Columns: []publicationdata.PhysicalColumn{physical}}},
			},
			Materializations: []viewer.Materialization{{OutputID: "output", Output: "output", MaterializationID: executionID, Selector: &selector, Columns: []publicationdata.PhysicalColumn{physical}}},
			QualityReports:   []publicationdata.QualityReport{projectConfigTestQuality(project, generation, executionID)},
			Publication:      viewer.PublicationMetadata{State: "READY", Generation: generation, ExecutionID: executionID, UpdatedAt: now},
			Status:           explorer.RevisionReady, CreatedBy: "test", CreatedAt: now, ReadyAt: &readyAt,
		},
	}
}

func projectConfigTestQuality(project, generation, executionID string) publicationdata.QualityReport {
	return publicationdata.QualityReport{
		ID: "quality-" + executionID, ReceiptID: "receipt-" + executionID,
		Project: project, DatasetGeneration: generation, ScopeDigest: "scope",
		Output: "output", PolicyVersion: publicationdata.DefaultQualityPolicyVersion,
		Completeness: publicationdata.QualityComplete, Verdict: publicationdata.QualityPassed,
		RowCount: 1, Columns: []publicationdata.ColumnQuality{{Column: "extension_code", Present: 1}},
		KeyIntegrity: publicationdata.KeyIntegrity{Distinct: 1},
	}
}

func projectConfigPublishRequest(t *testing.T, ctx context.Context, client *projectstore.Client, generations *datasetarango.Store, project, generation, executionID string, bundle recipe.Bundle, commit string, expectedRevision int64, configs ...explorer.ProjectConfigPublication) explorer.ProjectConfigPublishRequest {
	t.Helper()
	now := time.Now().UTC()
	if err := projectConfigStageGeneration(ctx, generations, project, generation); err != nil {
		t.Fatal(err)
	}
	execution := projectConfigSeedExecution(t, ctx, client, project, generation, executionID, bundle, now)
	selector := execution.Selector("output")
	service := dataset.ReleaseService{
		Manifests: generations, Releases: generations,
		Verifier: projectConfigTestVerifier{execution: execution},
		Required: []dataset.DataframeSelector{selector},
	}
	release, err := service.Create(ctx, dataset.ActivationRequest{Project: project, Generation: generation, GitCommit: commit})
	if err != nil {
		t.Fatalf("create release candidate: %v", err)
	}
	return explorer.ProjectConfigPublishRequest{RecipeName: bundle.Name, Configs: configs, Release: release, ExpectedReleaseRevision: expectedRevision}
}

func projectConfigStageGeneration(ctx context.Context, generations *datasetarango.Store, project, generation string) error {
	schema, err := dataset.NewSchemaSnapshot("test-schema", "R4", strings.Repeat("b", 64), []string{"Patient"})
	if err != nil {
		return err
	}
	manifest, err := dataset.NewManifest(dataset.Ref{Project: project, Generation: generation}, schema)
	if err != nil {
		return err
	}
	manifest, err = generations.CreateManifest(ctx, manifest)
	if err != nil {
		return err
	}
	_, err = generations.TransitionManifest(ctx, manifest, dataset.StateStaged)
	return err
}

func projectConfigSeedExecution(t *testing.T, ctx context.Context, client *projectstore.Client, project, generation, id string, bundle recipe.Bundle, at time.Time) publicationdata.BundleExecution {
	t.Helper()
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	verifiedAt := at
	execution := publicationdata.BundleExecution{
		ID:  id,
		Key: "bundle-key-" + id,
		BundleIdentity: publicationdata.BundleIdentity{
			Name: bundle.Name, TranslationVersion: bundle.TranslationVersion,
			Project: project, DatasetGeneration: generation,
			RecipeDigest: digest, SchemaDigest: strings.Repeat("c", 64), ReceiptID: "receipt-" + id,
			ScopeDigest: "scope", EngineVersion: "test",
		},
		State: publicationdata.BundlePublished, CreatedAt: at, UpdatedAt: at, PublishedAt: &verifiedAt,
		QualityReports: []publicationdata.QualityReport{projectConfigTestQuality(project, generation, id)},
		Outputs: []publicationdata.BundleOutputRecord{{
			Name: "output", PhysicalTable: "published_output_" + id,
			Selector: dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: "output"},
			State:    publicationdata.BundlePublished, VerifiedAt: &verifiedAt,
		}},
	}
	doc, err := db.Document(execution, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	var inserted bool
	err = client.QueryRows(ctx, `INSERT @doc INTO @@c RETURN NEW`, 1, map[string]any{
		"@c": publicationarango.BundleExecutionsCollection, "doc": doc,
	}, func(map[string]any) error { inserted = true; return nil })
	if err != nil || !inserted {
		t.Fatalf("seed published execution: inserted=%v err=%v", inserted, err)
	}
	return execution
}

type projectConfigTestVerifier struct {
	execution publicationdata.BundleExecution
}

func (v projectConfigTestVerifier) VerifyPublication(_ context.Context, project, generation string, selector dataset.DataframeSelector) (dataset.PublicationVerification, error) {
	for _, output := range v.execution.Outputs {
		if output.Selector != selector || projectid.Canonical(project) != projectid.Canonical(v.execution.Project) || generation != v.execution.DatasetGeneration {
			continue
		}
		return dataset.PublicationVerification{Selector: selector, ExecutionID: v.execution.ID, Generation: generation, State: string(publicationdata.BundlePublished), Queryable: output.Queryable(), VerifiedAt: *output.VerifiedAt, PhysicalTable: output.PhysicalTable}, nil
	}
	return dataset.PublicationVerification{}, dataset.ErrPublicationRecordMissing
}

func assertProjectConfigActive(t *testing.T, ctx context.Context, store *Store, project, id, revisionID string) {
	t.Helper()
	owner, err := store.Get(ctx, project, id)
	if err != nil {
		t.Fatalf("load config owner %q: %v", id, err)
	}
	if owner.ManagementMode != viewer.ManagementRepository || owner.ProjectConfig == nil || owner.ActiveRevisionID != revisionID {
		t.Fatalf("config owner %q = %+v, want active revision %q", id, owner, revisionID)
	}
	revision, err := store.GetRevision(ctx, revisionID)
	if err != nil || revision.Status != explorer.RevisionActive || revision.Publication.ExecutionID == "" {
		t.Fatalf("config revision %q = %+v, %v; want active immutable revision", revisionID, revision, err)
	}
}

func assertProjectConfigSuperseded(t *testing.T, ctx context.Context, store *Store, revisionID string) {
	t.Helper()
	revision, err := store.GetRevision(ctx, revisionID)
	if err != nil || revision.Status != explorer.RevisionSuperseded {
		t.Fatalf("config revision %q = %+v, %v; want superseded", revisionID, revision, err)
	}
}

func assertNoProjectConfigRelease(t *testing.T, ctx context.Context, generations *datasetarango.Store, project string) {
	t.Helper()
	if _, err := generations.ReadActiveRelease(ctx, project); !errors.Is(err, dataset.ErrNoActiveRelease) {
		t.Fatalf("active release after rejected batch = %v, want no active release", err)
	}
}

const selectionCleanupDatabasePrefix = "loom_exv_"

type selectionCleanupDocument struct {
	exists                bool
	state                 string
	complete              bool
	hasWriterExpiresAt    bool
	writerExpiresAtMillis int64
	createdAt             string
	memberCount           int64
}

func selectionCleanupOpen(t *testing.T, budget time.Duration) (context.Context, *store.Client, *Store) {
	t.Helper()
	endpoint := os.Getenv("LOOM_TEST_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set owned LOOM_TEST_ARANGO_URL")
	}
	name := selectionCleanupDatabasePrefix + "cleanup_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(name, selectionCleanupDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", name, selectionCleanupDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	client, err := store.Open(ctx, endpoint, name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Logf("SELECTION_CLEANUP_DATABASE=%s", name)
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		req, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(name), nil)
		if err != nil {
			t.Errorf("build cleanup request for %s: %v", name, err)
			return
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("cleanup %s: %v", name, err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("cleanup %s status=%d", name, response.StatusCode)
			return
		}
		t.Logf("SELECTION_CLEANUP_DATABASE_REMOVED=%s", name)
	})
	if err := client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap %s: %v", name, err)
	}
	persistence, err := New(client)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return ctx, client, persistence
}

func selectionCleanupInspect(t *testing.T, ctx context.Context, client *store.Client, selectionID string) selectionCleanupDocument {
	t.Helper()
	info := selectionCleanupDocument{}
	if err := client.QueryRows(ctx, `LET d = DOCUMENT(@@headers, @key)
RETURN {missing: d == null, state: d == null ? null : d.state, complete: d != null AND d.complete == true, hasWriterExpiresAt: d != null && HAS(d, "writerExpiresAt"), writerExpiresAt: d == null ? null : d.writerExpiresAt, createdAt: d == null ? null : d.createdAt}`, 1, map[string]any{"@headers": db.SelectionsCollection, "key": selectionID}, func(row map[string]any) error {
		missing, _ := row["missing"].(bool)
		info.exists = !missing
		info.state, _ = row["state"].(string)
		info.complete, _ = row["complete"].(bool)
		info.hasWriterExpiresAt, _ = row["hasWriterExpiresAt"].(bool)
		info.createdAt, _ = row["createdAt"].(string)
		switch number := row["writerExpiresAt"].(type) {
		case float64:
			info.writerExpiresAtMillis = int64(number)
		case int64:
			info.writerExpiresAtMillis = number
		}
		return nil
	}); err != nil {
		t.Fatalf("inspect header %s: %v", selectionID, err)
	}
	if !info.exists {
		return info
	}
	if err := client.QueryRows(ctx, `RETURN {count: LENGTH(FOR m IN @@members FILTER m.selectionId == @key RETURN 1)}`, 1, map[string]any{"@members": db.SelectionMembersCollection, "key": selectionID}, func(row map[string]any) error {
		switch number := row["count"].(type) {
		case float64:
			info.memberCount = int64(number)
		case int64:
			info.memberCount = number
		}
		return nil
	}); err != nil {
		t.Fatalf("inspect members %s: %v", selectionID, err)
	}
	return info
}

func selectionCleanupExpireLease(t *testing.T, ctx context.Context, client *store.Client, selectionID string, lapsedFor time.Duration) int64 {
	t.Helper()
	expired := time.Now().UTC().Add(-selectiondb.SelectionWriterLease - lapsedFor).UnixMilli()
	if err := client.ExecuteAQL(ctx, `FOR d IN @@c FILTER d._key == @key UPDATE d WITH {writerExpiresAt: @writerExpiresAt} IN @@c RETURN NEW._key`, map[string]any{"@c": db.SelectionsCollection, "key": selectionID, "writerExpiresAt": expired}); err != nil {
		t.Fatalf("rewind writerExpiresAt for %s: %v", selectionID, err)
	}
	t.Logf("SELECTION_CLEANUP_LEASE_REWOUND selection=%s writerExpiresAtMillis=%d (= now - %s - %s) lease=%s", selectionID, expired, selectiondb.SelectionWriterLease, lapsedFor, selectiondb.SelectionWriterLease)
	return expired
}

func selectionCleanupAssertNoOrphans(t *testing.T, ctx context.Context, client *store.Client, label string) {
	t.Helper()
	var orphans int64
	if err := client.QueryRows(ctx, `LET ids = (FOR m IN @@members RETURN DISTINCT m.selectionId)
RETURN {count: LENGTH(FOR id IN ids FILTER DOCUMENT(@@headers, id) == null RETURN id)}`, 1, map[string]any{"@members": db.SelectionMembersCollection, "@headers": db.SelectionsCollection}, func(row map[string]any) error {
		switch number := row["count"].(type) {
		case float64:
			orphans = int64(number)
		case int64:
			orphans = number
		}
		return nil
	}); err != nil {
		t.Fatalf("orphan scan after %s: %v", label, err)
	}
	t.Logf("SELECTION_CLEANUP_ORPHAN_MEMBERS=%d after=%s", orphans, label)
	if orphans != 0 {
		t.Errorf("torn or non-transactional reap after %s: %d member documents without a header", label, orphans)
	}
}

func selectionCleanupHeader(project, selectionID, idempotencyKey string, createdAt time.Time) explorerselection.SelectionRevision {
	return explorerselection.SelectionRevision{
		ID: selectionID, Project: project, Generation: "generation-a", ResourceType: "DocumentReference",
		Rule:           explorerselection.SelectionRule{Kind: explorerselection.SelectionRuleExplicit},
		Source:         explorerselection.SelectionSource{Kind: explorerselection.SelectionSourceExplicit},
		ScopeDigest:    "cleanup-scope",
		RuleDigest:     "cleanup-rule",
		IdempotencyKey: idempotencyKey,
		CreatedAt:      createdAt,
	}
}

func selectionCleanupMembers(project, generation, resourceType, prefix string, count int) []explorerselection.SelectionMember {
	members := make([]explorerselection.SelectionMember, count)
	for i := range members {
		members[i] = explorerselection.SelectionMember{Ref: explorerselection.ResourceRef{
			Project: project, Generation: generation, ResourceType: resourceType,
			ID: fmt.Sprintf("%s-%03d", prefix, i),
		}}
	}
	return members
}

func TestSelectionCleanupStagingReapsLeaseLapsedWriterMidDigest(t *testing.T) {
	ctx, client, persistence := selectionCleanupOpen(t, 300*time.Second)
	const project = "loom_exv_cleanup_mid_digest"

	crashedID := "selection-cleanup-crashed-" + uuid.NewString()
	crashedToken := uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, crashedID, uuid.NewString(), time.Now().UTC()), crashedToken); err != nil {
		t.Fatalf("crashed creator begin: %v", err)
	}
	if _, err := persistence.AppendSelectionMembers(ctx, crashedID, crashedToken, selectionCleanupMembers(project, "generation-a", "DocumentReference", "crashed", 3)); err != nil {
		t.Fatalf("crashed creator append: %v", err)
	}
	selectionCleanupExpireLease(t, ctx, client, crashedID, time.Minute)
	foreignID := "selection-cleanup-foreign-" + uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, foreignID, uuid.NewString(), time.Now().UTC()), uuid.NewString()); err != nil {
		t.Fatalf("foreign begin after crashed creator: %v", err)
	}
	crashed := selectionCleanupInspect(t, ctx, client, crashedID)
	t.Logf("SELECTION_CLEANUP_CRASHED exists=%t state=%q memberCount=%d", crashed.exists, crashed.state, crashed.memberCount)
	if crashed.exists || crashed.memberCount != 0 {
		t.Fatalf("crashed staging creator was not reaped: %#v", crashed)
	}
	selectionCleanupAssertNoOrphans(t, ctx, client, "crashed-reap")

	liveID := "selection-cleanup-live-" + uuid.NewString()
	liveToken := uuid.NewString()
	liveMembers := selectionCleanupMembers(project, "generation-a", "DocumentReference", "live", 3)
	if _, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, liveID, uuid.NewString(), time.Now().UTC()), liveToken); err != nil {
		t.Fatalf("live writer begin: %v", err)
	}
	if inserted, err := persistence.AppendSelectionMembers(ctx, liveID, liveToken, liveMembers); err != nil || len(inserted) != 3 {
		t.Fatalf("live writer append inserted=%d err=%v", len(inserted), err)
	}

	liveDigest, liveCount, _, err := persistence.DigestSelectionMembers(ctx, project, liveID)
	if err != nil || liveCount != 3 {
		t.Fatalf("live writer digest count=%d err=%v", liveCount, err)
	}
	if _, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, foreignID+"-b", uuid.NewString(), time.Now().UTC()), uuid.NewString()); err != nil {
		t.Fatalf("foreign begin while live writer mid-digest: %v", err)
	}
	afterForeign := selectionCleanupInspect(t, ctx, client, liveID)
	t.Logf("SELECTION_CLEANUP_LIVE_WRITER afterForeign exists=%t state=%q complete=%t hasWriterExpiresAt=%t writerExpiresAtMillis=%d memberCount=%d",
		afterForeign.exists, afterForeign.state, afterForeign.complete, afterForeign.hasWriterExpiresAt, afterForeign.writerExpiresAtMillis, afterForeign.memberCount)
	if !afterForeign.exists || afterForeign.state != selectiondb.SelectionStagingState || afterForeign.memberCount != 3 {
		t.Fatalf("active writer inside the lease was touched: %#v", afterForeign)
	}
	liveCompleted, err := persistence.CompleteSelection(ctx, liveID, liveToken, liveDigest, liveCount, 0, time.Now().UTC())
	if err != nil || liveCompleted == nil || !liveCompleted.Complete {
		t.Fatalf("live writer complete after foreign begin = %#v err=%v", liveCompleted, err)
	}
	t.Logf("SELECTION_CLEANUP_LIVE_WRITER_COMPLETED digest=%s memberCount=%d", liveCompleted.MembershipDigest, liveCompleted.MemberCount)
	selectionCleanupAssertNoOrphans(t, ctx, client, "live-writer")

	idleID := "selection-cleanup-idle-" + uuid.NewString()
	idleToken := uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, idleID, uuid.NewString(), time.Now().UTC()), idleToken); err != nil {
		t.Fatalf("idle writer begin: %v", err)
	}
	if inserted, err := persistence.AppendSelectionMembers(ctx, idleID, idleToken, selectionCleanupMembers(project, "generation-a", "DocumentReference", "idle", 3)); err != nil || len(inserted) != 3 {
		t.Fatalf("idle writer append inserted=%d err=%v", len(inserted), err)
	}
	rewound := selectionCleanupExpireLease(t, ctx, client, idleID, time.Minute)
	idleDigest, idleCount, _, err := persistence.DigestSelectionMembers(ctx, project, idleID)
	if err != nil || idleCount != 3 {
		t.Fatalf("idle writer digest count=%d err=%v", idleCount, err)
	}
	secondSessionID := "selection-cleanup-second-" + uuid.NewString()
	secondStarted, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, secondSessionID, uuid.NewString(), time.Now().UTC()), uuid.NewString())
	if err != nil {
		t.Fatalf("second session begin: %v", err)
	}
	victim := selectionCleanupInspect(t, ctx, client, idleID)
	t.Logf("SELECTION_CLEANUP_H3A victimHeader exists=%t state=%q complete=%t hasWriterExpiresAt=%t writerExpiresAtMillis=%d(rewoundTo=%d nowMillis=%d) memberResidue=%d secondSessionComplete=%t",
		victim.exists, victim.state, victim.complete, victim.hasWriterExpiresAt, victim.writerExpiresAtMillis, rewound, time.Now().UTC().UnixMilli(), victim.memberCount, secondStarted.Complete)
	completeOutcome, completeErr := persistence.CompleteSelection(ctx, idleID, idleToken, idleDigest, idleCount, 0, time.Now().UTC())
	if completeErr != nil {
		t.Logf("SELECTION_CLEANUP_H3A_COMPLETE_OUTCOME error=%v notFound=%t incomplete=%t conflict=%t",
			completeErr, errors.Is(completeErr, explorerselection.ErrSelectionNotFound), errors.Is(completeErr, explorerselection.ErrSelectionIncomplete), errors.Is(completeErr, explorerselection.ErrSelectionConflict))
	} else {
		t.Logf("SELECTION_CLEANUP_H3A_COMPLETE_OUTCOME ok complete=%t memberCount=%d digest=%s", completeOutcome.Complete, completeOutcome.MemberCount, completeOutcome.MembershipDigest)
	}
	afterComplete := selectionCleanupInspect(t, ctx, client, idleID)
	t.Logf("SELECTION_CLEANUP_H3A_AFTER_COMPLETE exists=%t state=%q complete=%t memberResidue=%d", afterComplete.exists, afterComplete.state, afterComplete.complete, afterComplete.memberCount)
	if victim.exists != (afterComplete.memberCount > 0) && !victim.exists && afterComplete.memberCount > 0 {
		t.Errorf("header removed but staged members survived: header=%#v members=%d", victim, afterComplete.memberCount)
	}
	if victim.exists && afterComplete.memberCount == 0 {
		t.Errorf("header kept but staged members removed: header=%#v", victim)
	}
	selectionCleanupAssertNoOrphans(t, ctx, client, "lease-lapsed-mid-digest")
	if _, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, "selection-cleanup-tail-"+uuid.NewString(), uuid.NewString(), time.Now().UTC()), uuid.NewString()); err != nil {
		t.Fatalf("tail begin: %v", err)
	}
	selectionCleanupAssertNoOrphans(t, ctx, client, "tail-begin")
}

func TestSelectionCleanupStagingDestroysCompletedStagingHeader(t *testing.T) {
	ctx, client, persistence := selectionCleanupOpen(t, 240*time.Second)
	const project = "loom_exv_cleanup_completed"

	rejected := selectionCleanupHeader(project, "selection-cleanup-reject-"+uuid.NewString(), uuid.NewString(), time.Now().UTC())
	rejected.Complete = true
	rejected.MembershipDigest = strings.Repeat("a", 64)
	rejected.MemberCount = 1
	now := time.Now().UTC()
	rejected.CompletedAt = &now
	_, err := persistence.BeginSelection(ctx, rejected, uuid.NewString())
	t.Logf("SELECTION_CLEANUP_H3B_BEGIN_COMPLETE_ERROR=%v", err)
	if err == nil {
		t.Errorf("BeginSelection accepted a complete revision; state=STAGING with complete=true is directly reachable")
	}

	fullID := "selection-cleanup-full-" + uuid.NewString()
	fullToken := uuid.NewString()
	fullMembers := selectionCleanupMembers(project, "generation-a", "DocumentReference", "full", 4)
	if _, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, fullID, uuid.NewString(), time.Now().UTC()), fullToken); err != nil {
		t.Fatalf("full lifecycle begin: %v", err)
	}
	if _, err := persistence.AppendSelectionMembers(ctx, fullID, fullToken, fullMembers); err != nil {
		t.Fatalf("full lifecycle append: %v", err)
	}
	fullDigest, fullCount, fullBytes, err := persistence.DigestSelectionMembers(ctx, project, fullID)
	if err != nil || fullCount != 4 {
		t.Fatalf("full lifecycle digest count=%d err=%v", fullCount, err)
	}
	if _, err := persistence.CompleteSelection(ctx, fullID, fullToken, fullDigest, fullCount, fullBytes, time.Now().UTC()); err != nil {
		t.Fatalf("full lifecycle complete: %v", err)
	}
	var stagingComplete int64
	if err := client.QueryRows(ctx, `RETURN {count: LENGTH(FOR d IN @@c FILTER d.state == @state AND d.complete == true RETURN d._key)}`, 1, map[string]any{"@c": db.SelectionsCollection, "state": selectiondb.SelectionStagingState}, func(row map[string]any) error {
		switch number := row["count"].(type) {
		case float64:
			stagingComplete = int64(number)
		case int64:
			stagingComplete = number
		}
		return nil
	}); err != nil {
		t.Fatalf("scan staging+complete: %v", err)
	}
	t.Logf("SELECTION_CLEANUP_H3B_PRODUCTION_STAGING_COMPLETE_DOCUMENTS=%d", stagingComplete)
	if stagingComplete != 0 {
		t.Errorf("production lifecycle produced state=STAGING with complete=true")
	}
	tamperedHeader := selectionCleanupInspect(t, ctx, client, fullID)
	t.Logf("SELECTION_CLEANUP_H3B_PRODUCTION_COMPLETED_HEADER exists=%t state=%q complete=%t memberCount=%d", tamperedHeader.exists, tamperedHeader.state, tamperedHeader.complete, tamperedHeader.memberCount)
	if err := persistence.CleanupSelectionStaging(ctx, time.Now().UTC().Add(time.Hour), 100); err != nil {
		t.Fatalf("cleanup with a future horizon: %v", err)
	}
	survivor := selectionCleanupInspect(t, ctx, client, fullID)
	t.Logf("SELECTION_CLEANUP_H3B_CLEANUP_WITH_FUTURE_HORIZON exists=%t memberCount=%d", survivor.exists, survivor.memberCount)
	if !survivor.exists || survivor.memberCount != 4 {
		t.Errorf("completed selection destroyed by cleanup: %#v", survivor)
	}

	const guardedID = "selection-cleanup-guarded"
	guardedToken := "guarded-writer-token"
	completedAt := time.Now().UTC().Add(-3 * time.Hour)
	guardedDoc := map[string]any{
		"_key":             guardedID,
		"id":               guardedID,
		"project":          project,
		"generation":       "generation-a",
		"resourceType":     "DocumentReference",
		"rule":             map[string]any{"kind": string(explorerselection.SelectionRuleExplicit)},
		"source":           map[string]any{"kind": string(explorerselection.SelectionSourceExplicit)},
		"scopeDigest":      "cleanup-scope",
		"ruleDigest":       "cleanup-rule",
		"membershipDigest": strings.Repeat("b", 64),
		"memberCount":      2,
		"memberBytes":      96,
		"complete":         true,
		"idempotencyKey":   "cleanup-guarded",
		"createdAt":        time.Now().UTC().Add(-4 * time.Hour).Format(time.RFC3339Nano),
		"completedAt":      completedAt.Format(time.RFC3339Nano),
		"state":            selectiondb.SelectionStagingState,
		"writerToken":      guardedToken,
		"writerExpiresAt":  time.Now().UTC().Add(-2 * time.Hour).UnixMilli(),
	}
	memberDocs := make([]map[string]any, 0, 2)
	for _, member := range selectionCleanupMembers(project, "generation-a", "DocumentReference", "guarded", 2) {
		memberKey := selectiondb.SelectionMemberKey(guardedID, member.Ref)
		memberDocs = append(memberDocs, map[string]any{
			"_key": memberKey, "selectionId": guardedID,
			"ref":       map[string]any{"project": member.Ref.Project, "generation": member.Ref.Generation, "resourceType": member.Ref.ResourceType, "id": member.Ref.ID},
			"memberKey": memberKey,
			"project":   member.Ref.Project, "generation": member.Ref.Generation, "resourceType": member.Ref.ResourceType, "id": member.Ref.ID,
		})
	}
	if err := client.ExecuteAQL(ctx, `INSERT @doc INTO @@headers`, map[string]any{"@headers": db.SelectionsCollection, "doc": guardedDoc}); err != nil {
		t.Fatalf("seed guarded header: %v", err)
	}
	if err := client.ExecuteAQL(ctx, `FOR doc IN @docs INSERT doc INTO @@members`, map[string]any{"@members": db.SelectionMembersCollection, "docs": memberDocs}); err != nil {
		t.Fatalf("seed guarded members: %v", err)
	}
	seeded := selectionCleanupInspect(t, ctx, client, guardedID)
	t.Logf("SELECTION_CLEANUP_H3B_SEEDED exists=%t state=%q complete=%t memberCount=%d", seeded.exists, seeded.state, seeded.complete, seeded.memberCount)
	if !seeded.exists || !seeded.complete || seeded.state != selectiondb.SelectionStagingState || seeded.memberCount != 2 {
		t.Fatalf("guarded fixture not seeded: %#v", seeded)
	}

	abortErr := persistence.AbortSelection(ctx, guardedID, guardedToken)
	t.Logf("SELECTION_CLEANUP_H3B_ABORT error=%v", abortErr)
	afterAbort := selectionCleanupInspect(t, ctx, client, guardedID)
	t.Logf("SELECTION_CLEANUP_H3B_ABORT_THEN exists=%t memberCount=%d", afterAbort.exists, afterAbort.memberCount)
	if !afterAbort.exists || afterAbort.memberCount != 2 {
		t.Errorf("AbortSelection destroyed a complete=true document despite its guard: %#v", afterAbort)
	}
	_, completeErr := persistence.CompleteSelection(ctx, guardedID, guardedToken, strings.Repeat("b", 64), 2, 96, time.Now().UTC())
	t.Logf("SELECTION_CLEANUP_H3B_COMPLETE error=%v incomplete=%t conflict=%t", completeErr, errors.Is(completeErr, explorerselection.ErrSelectionIncomplete), errors.Is(completeErr, explorerselection.ErrSelectionConflict))
	afterComplete := selectionCleanupInspect(t, ctx, client, guardedID)
	if !afterComplete.exists || afterComplete.memberCount != 2 {
		t.Errorf("CompleteSelection destroyed a complete=true document despite its guard: %#v", afterComplete)
	}

	if err := persistence.CleanupSelectionStaging(ctx, time.Now().UTC(), 100); err != nil {
		t.Fatalf("cleanup guarded: %v", err)
	}
	afterCleanup := selectionCleanupInspect(t, ctx, client, guardedID)
	t.Logf("SELECTION_CLEANUP_H3B_CLEANUP_THEN exists=%t state=%q complete=%t memberResidue=%d", afterCleanup.exists, afterCleanup.state, afterCleanup.complete, afterCleanup.memberCount)
	if afterCleanup.exists || afterCleanup.memberCount != 0 {
		t.Logf("SELECTION_CLEANUP_H3B_CLEANUP_PRESERVED_COMPLETED_STAGING exists=%t memberResidue=%d", afterCleanup.exists, afterCleanup.memberCount)
	} else {
		t.Logf("SELECTION_CLEANUP_H3B_CLEANUP_DESTROYED_COMPLETED_STAGING=true")
	}
	selectionCleanupAssertNoOrphans(t, ctx, client, "guarded-cleanup")
}

func TestSelectionCleanupStagingLeaseGapAtScale(t *testing.T) {
	ctx, _, persistence := selectionCleanupOpen(t, 25*time.Minute)
	size := int(lifecycle.DefaultSelectionMaxRows)
	if override := strings.TrimSpace(os.Getenv("LOOM_EXV_SCALE_ROWS")); override != "" {
		parsed, err := strconv.Atoi(override)
		if err != nil || parsed <= 0 {
			t.Fatalf("LOOM_EXV_SCALE_ROWS=%q: %v", override, err)
		}
		size = parsed
	}
	selectionCleanupScaleRun(t, ctx, persistence, "loom_exv_cleanup_scale", "generation-a", "DocumentReference", size)
}

func selectionCleanupScaleRun(t *testing.T, ctx context.Context, persistence *Store, project, generation, resourceType string, size int) {
	t.Helper()
	selectionID := "selection-cleanup-scale-" + uuid.NewString()
	writerToken := uuid.NewString()
	header := selectionCleanupHeader(project, selectionID, uuid.NewString(), time.Now().UTC())
	header.Generation = generation
	header.ResourceType = resourceType
	if _, err := persistence.BeginSelection(ctx, header, writerToken); err != nil {
		t.Fatalf("scale begin: %v", err)
	}
	members := make([]explorerselection.SelectionMember, size)
	for i := range members {
		members[i] = explorerselection.SelectionMember{Ref: explorerselection.ResourceRef{Project: project, Generation: generation, ResourceType: resourceType, ID: fmt.Sprintf("scale-%07d", i)}}
	}
	appendStart := time.Now()
	batches := 0
	for offset := 0; offset < len(members); offset += 256 {
		end := offset + 256
		if end > len(members) {
			end = len(members)
		}
		if _, err := persistence.AppendSelectionMembers(ctx, selectionID, writerToken, members[offset:end]); err != nil {
			t.Fatalf("scale append offset=%d: %v", offset, err)
		}
		batches++
	}
	appendElapsed := time.Since(appendStart)

	unrenewedStart := time.Now()
	digest, count, bytes, err := persistence.DigestSelectionMembers(ctx, project, selectionID)
	if err != nil {
		t.Fatalf("scale digest: %v", err)
	}
	digestElapsed := time.Since(unrenewedStart)
	if count != int64(size) {
		t.Fatalf("scale digest count=%d want=%d", count, size)
	}
	if want := explorerselection.MembershipDigest(members); digest != want {
		t.Fatalf("scale digest = %s want %s", digest, want)
	}
	if _, err := persistence.CompleteSelection(ctx, selectionID, writerToken, digest, count, bytes, time.Now().UTC()); err != nil {
		t.Fatalf("scale complete: %v", err)
	}
	unrenewedElapsed := time.Since(unrenewedStart)
	t.Logf("SELECTION_CLEANUP_SCALE rows=%d batches=%d appendElapsed=%s digestElapsed=%s unRenewedGapToComplete=%s lease=%s lifecycleSelectionBudget=%s lifecycleMaxRows=%d",
		size, batches, appendElapsed.Round(time.Millisecond), digestElapsed.Round(time.Millisecond), unrenewedElapsed.Round(time.Millisecond), selectiondb.SelectionWriterLease, lifecycle.DefaultSelectionMaxDuration, lifecycle.DefaultSelectionMaxRows)
	if unrenewedElapsed >= selectiondb.SelectionWriterLease {
		t.Errorf("un-renewed digest+complete gap %s reached the %s writer lease", unrenewedElapsed, selectiondb.SelectionWriterLease)
	}
}

func TestViewBuilderCatalogSaveDefaultsDeleteAndSourceRebindAgainstArango(t *testing.T) {
	ctx, client, explorers, generations := projectConfigPublicationDB(t)
	project := "view-builder-storage"
	bundle := projectConfigTestBundle("recipe-a", "translation-1")
	legacy := projectConfigPublishRequest(t, ctx, client, generations, "view-builder-adopt", "legacy-generation", "legacy-execution", bundle,
		"legacy-commit", 0,
		projectConfigPublication(t, "view-builder-adopt", "default", "recipe-a", "Legacy default", "legacy-revision", "legacy-generation", "legacy-execution", bundle),
	)
	if _, err := explorers.PublishProjectConfigs(ctx, legacy); err != nil {
		t.Fatalf("seed pre-View-Builder project: %v", err)
	}
	legacySettings, err := explorers.GetViewBuilderSettings(ctx, "view-builder-adopt")
	if err != nil || legacySettings.Version != 0 || len(legacySettings.Catalogs) != 0 {
		t.Fatalf("read of unadopted project settings = %+v, %v; want read-only version 0", legacySettings, err)
	}
	if err := explorers.AdoptViewBuilderCatalogs(ctx, "view-builder-adopt"); err != nil {
		t.Fatalf("adopt existing published revision: %v", err)
	}
	legacySettings, err = explorers.GetViewBuilderSettings(ctx, "view-builder-adopt")
	if err != nil || legacySettings.Version != 1 || legacySettings.DefaultConfigID != "default" || legacySettings.DefaultFrameID != "recipe-a/output" {
		t.Fatalf("adopted project settings = %+v, %v", legacySettings, err)
	}
	legacySnapshot, err := explorers.GetRevision(ctx, legacySettings.Catalogs["recipe-a"])
	if err != nil || legacySnapshot.ExplorerID != "" || legacySnapshot.Status != explorer.RevisionReady || len(legacySnapshot.Config) != 0 || legacySnapshot.Publication.ExecutionID != "legacy-execution" {
		t.Fatalf("adopted metadata-only snapshot = %+v, %v", legacySnapshot, err)
	}
	if err := explorers.AdoptViewBuilderCatalogs(ctx, "view-builder-adopt"); err != nil {
		t.Fatalf("repeat existing-project adoption: %v", err)
	}
	repeatedLegacySettings, err := explorers.GetViewBuilderSettings(ctx, "view-builder-adopt")
	if err != nil || repeatedLegacySettings.Version != legacySettings.Version || repeatedLegacySettings.Catalogs["recipe-a"] != legacySettings.Catalogs["recipe-a"] {
		t.Fatalf("repeat adoption changed settings: %+v, %v", repeatedLegacySettings, err)
	}

	first := projectConfigPublishRequest(t, ctx, client, generations, project, "generation-1", "execution-1", bundle,
		"commit-1", 0,
		projectConfigPublication(t, project, "default", "recipe-a", "Default", "revision-1-default", "generation-1", "execution-1", bundle),
		projectConfigPublication(t, project, "files", "recipe-a", "Files", "revision-1-files", "generation-1", "execution-1", bundle),
	)
	first.ExpectedConfigRevisionIDs = map[string]string{"default": "", "files": ""}
	first.CatalogSnapshot = viewBuilderTestCatalogSnapshot(t, first.Configs[0])
	active, err := explorers.PublishProjectConfigs(ctx, first)
	if err != nil {
		t.Fatalf("publish source with catalog snapshot: %v", err)
	}
	if active.Revision != 1 {
		t.Fatalf("first release revision = %d, want 1", active.Revision)
	}

	settings, err := explorers.GetViewBuilderSettings(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Version != 1 || settings.Catalogs["recipe-a"] != first.CatalogSnapshot.ID {
		t.Fatalf("published catalog settings = %+v", settings)
	}
	catalogSnapshot, err := explorers.GetRevision(ctx, settings.Catalogs["recipe-a"])
	if err != nil || catalogSnapshot.ExplorerID != "" || catalogSnapshot.Status != explorer.RevisionReady || len(catalogSnapshot.Config) != 0 {
		t.Fatalf("stored metadata-only catalog snapshot = %+v, %v", catalogSnapshot, err)
	}

	if _, err := explorers.PublishProjectConfigs(ctx, first); err != nil {
		t.Fatalf("retry exact source publication after lost acknowledgement: %v", err)
	}
	retriedSettings, err := explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || retriedSettings.Version != settings.Version || retriedSettings.Catalogs["recipe-a"] != settings.Catalogs["recipe-a"] {
		t.Fatalf("source retry changed settings: %+v, %v", retriedSettings, err)
	}

	if err := explorers.SaveViewBuilderDefaults(ctx, project, viewer.ViewBuilderDefaultsRequest{
		ExpectedVersion: settings.Version, DefaultConfigID: "default", DefaultFrameID: viewer.ViewBuilderFrameID("recipe-a", "output"),
	}); err != nil {
		t.Fatalf("save explicit project default: %v", err)
	}
	settings, err = explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || settings.Version != 2 || settings.DefaultConfigID != "default" || settings.DefaultFrameID != "recipe-a/output" {
		t.Fatalf("saved defaults = %+v, %v", settings, err)
	}

	updatedDefinition := projectConfigTestDefinition("default", "recipe-a", "Edited Default")
	prepared, err := projectconfig.PrepareViewBuilderConfig(updatedDefinition, *catalogSnapshot, "editor@example.test")
	if err != nil {
		t.Fatalf("prepare view edit against exact catalog snapshot: %v", err)
	}
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: "revision-1-default", CatalogRevisionID: settings.Catalogs["recipe-a"],
		Publication: prepared, Actor: "editor@example.test",
	}); err != nil {
		t.Fatalf("save view edit: %v", err)
	}
	differentDefinition := projectConfigTestDefinition("default", "recipe-a", "A different title")
	alternate, err := projectconfig.PrepareViewBuilderConfig(differentDefinition, *catalogSnapshot, "editor@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: prepared.Revision.ID, CatalogRevisionID: settings.Catalogs["recipe-a"],
		Publication: alternate, Actor: "editor@example.test",
	}); err != nil {
		t.Fatalf("save alternate title: %v", err)
	}
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: alternate.Revision.ID, CatalogRevisionID: settings.Catalogs["recipe-a"],
		Publication: prepared, Actor: "editor@example.test",
	}); err != nil {
		t.Fatalf("restore prior valid view revision after A-B-A: %v", err)
	}
	beforeNoop, err := explorers.GetRevision(ctx, prepared.Revision.ID)
	if err != nil || beforeNoop.ActivatedAt == nil {
		t.Fatalf("read reactivated view revision before exact save retry: %+v, %v", beforeNoop, err)
	}
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: prepared.Revision.ID, CatalogRevisionID: settings.Catalogs["recipe-a"],
		Publication: prepared, Actor: "editor@example.test",
	}); err != nil {
		t.Fatalf("retry the now-active exact view save: %v", err)
	}
	afterNoop, err := explorers.GetRevision(ctx, prepared.Revision.ID)
	if err != nil || afterNoop.ActivatedAt == nil || !afterNoop.ActivatedAt.Equal(*beforeNoop.ActivatedAt) {
		t.Fatalf("exact save retry changed active revision metadata: before=%+v after=%+v err=%v", beforeNoop.ActivatedAt, afterNoop.ActivatedAt, err)
	}
	settingsAfterEdit, err := explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || settingsAfterEdit.Version != settings.Version || settingsAfterEdit.DefaultConfigID != "default" || settingsAfterEdit.DefaultFrameID != "recipe-a/output" {
		t.Fatalf("view save changed project defaults/catalog version: %+v, %v", settingsAfterEdit, err)
	}
	owner, err := explorers.Get(ctx, project, "default")
	if err != nil || owner.ActiveRevisionID != prepared.Revision.ID || owner.ProjectConfig.Title != "Edited Default" {
		t.Fatalf("saved view owner = %+v, %v", owner, err)
	}

	filesDefinition := projectConfigTestDefinition("files", "recipe-a", "Edited Files")
	filesPrepared, err := projectconfig.PrepareViewBuilderConfig(filesDefinition, *catalogSnapshot, "editor@example.test")
	if err != nil {
		t.Fatalf("prepare second view edit: %v", err)
	}
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: "revision-1-files", CatalogRevisionID: settings.Catalogs["recipe-a"],
		Publication: filesPrepared, Actor: "editor@example.test",
	}); err != nil {
		t.Fatalf("save second view edit: %v", err)
	}
	changedImmutable := filesPrepared
	changedImmutable.Revision.PublicOutputContract = []byte(`{"outputs":[]}`)
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: filesPrepared.Revision.ID, CatalogRevisionID: settings.Catalogs["recipe-a"],
		Publication: changedImmutable, Actor: "editor@example.test",
	}); !errors.Is(err, explorer.ErrImmutableRevision) {
		t.Fatalf("changed immutable revision error = %v, want ErrImmutableRevision", err)
	}

	badDefinition := updatedDefinition
	badDefinition.Views[0].Output = "removed-output"
	badPublication := prepared
	badPublication.Definition = badDefinition
	badPublication.Revision.IntentDigest, err = badDefinition.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: prepared.Revision.ID, CatalogRevisionID: settings.Catalogs["recipe-a"], Publication: badPublication,
	}); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("removing the current default frame error = %v, want ErrDraftConflict", err)
	}
	if err := explorers.DeleteViewBuilderConfig(ctx, project, "default", prepared.Revision.ID); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("deleting the current default error = %v, want ErrDraftConflict", err)
	}

	if err := explorers.SaveViewBuilderDefaults(ctx, project, viewer.ViewBuilderDefaultsRequest{
		ExpectedVersion: settings.Version, DefaultConfigID: "files", DefaultFrameID: viewer.ViewBuilderFrameID("recipe-a", "output"),
	}); err != nil {
		t.Fatalf("move default to second saved config: %v", err)
	}
	if err := explorers.DeleteViewBuilderConfig(ctx, project, "default", prepared.Revision.ID); err != nil {
		t.Fatalf("delete nondefault view while retaining catalog: %v", err)
	}
	if _, err := explorers.Get(ctx, project, "default"); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("deleted config owner read error = %v, want ErrNotFound", err)
	}
	deletedRevision, err := explorers.GetRevision(ctx, prepared.Revision.ID)
	if err != nil || deletedRevision.Status != explorer.RevisionSuperseded {
		t.Fatalf("deleted view history = %+v, %v; want retained superseded revision", deletedRevision, err)
	}
	settings, err = explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || settings.Catalogs["recipe-a"] != first.CatalogSnapshot.ID || settings.DefaultConfigID != "files" {
		t.Fatalf("deleting a view changed its independent catalog/default: %+v, %v", settings, err)
	}
	if err := explorers.AdoptViewBuilderCatalogs(ctx, project); err != nil {
		t.Fatalf("idempotent adoption after deletion: %v", err)
	}
	if _, err := explorers.Get(ctx, project, "default"); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("adoption recreated deleted view: %v", err)
	}

	secondPublication := projectConfigPublication(t, project, "files", "recipe-a", "Edited Files", "revision-2-files", "generation-2", "execution-2", bundle)
	second := projectConfigPublishRequest(t, ctx, client, generations, project, "generation-2", "execution-2", bundle,
		"commit-2", active.Revision, secondPublication)
	second.ExpectedConfigRevisionIDs = map[string]string{"files": filesPrepared.Revision.ID}
	second.CatalogSnapshot = viewBuilderTestCatalogSnapshot(t, secondPublication)
	active, err = explorers.PublishProjectConfigs(ctx, second)
	if err != nil {
		t.Fatalf("rebind remaining views to second execution: %v", err)
	}
	settings, err = explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || settings.Catalogs["recipe-a"] != second.CatalogSnapshot.ID || settings.DefaultConfigID != "files" || settings.DefaultFrameID != "recipe-a/output" {
		t.Fatalf("source rebind did not preserve defaults/update catalog: %+v, %v", settings, err)
	}
	if _, err := explorers.Get(ctx, project, "default"); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("source publication recreated a deleted view: %v", err)
	}
	reboundFiles, err := explorers.Get(ctx, project, "files")
	if err != nil || reboundFiles.ActiveRevisionID != "revision-2-files" || reboundFiles.ProjectConfig.Title != "Edited Files" {
		t.Fatalf("source publication did not preserve the saved view definition: %+v, %v", reboundFiles, err)
	}

	staleSave, err := projectconfig.PrepareViewBuilderConfig(filesDefinition, *catalogSnapshot, "editor@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := explorers.SaveViewBuilderConfig(ctx, explorer.ViewBuilderMutation{
		Project: project, ExpectedRevisionID: "revision-2-files", CatalogRevisionID: first.CatalogSnapshot.ID, Publication: staleSave,
	}); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("stale catalog save error = %v, want ErrDraftConflict", err)
	}

	thirdPublication := projectConfigPublication(t, project, "files", "recipe-a", "Edited Files", "revision-3-files", "generation-3", "execution-3", bundle)
	third := projectConfigPublishRequest(t, ctx, client, generations, project, "generation-3", "execution-3", bundle,
		"commit-3", active.Revision, thirdPublication)
	third.ExpectedConfigRevisionIDs = map[string]string{"files": "revision-1-files"}
	third.CatalogSnapshot = viewBuilderTestCatalogSnapshot(t, thirdPublication)
	if _, err := explorers.PublishProjectConfigs(ctx, third); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("stale source owner head error = %v, want ErrDraftConflict", err)
	}
	if _, err := explorers.GetRevision(ctx, thirdPublication.Revision.ID); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("stale source publication left a candidate revision: %v", err)
	}
}

func viewBuilderTestCatalogSnapshot(t *testing.T, publication explorer.ProjectConfigPublication) *explorer.Revision {
	t.Helper()
	snapshot := publicationdb.AdoptedCatalogSnapshot(publication.Revision, publication.Definition.RecipeRef.Name)
	return &snapshot
}

func TestDecodeNormalizesNumericUpdatedAt(t *testing.T) {
	timestamp := float64(1776620000000)
	value, err := db.Decode[struct {
		UpdatedAt time.Time `json:"updatedAt"`
	}](map[string]any{"updatedAt": timestamp})
	if err != nil {
		t.Fatal(err)
	}
	if value.UpdatedAt.UnixMilli() != int64(timestamp) {
		t.Fatalf("updatedAt=%v, want %d", value.UpdatedAt, int64(timestamp))
	}
}

type queryCall struct {
	query string
	binds map[string]any
}

type revisionClient struct {
	doc map[string]any
}

func (c *revisionClient) WithTransaction(ctx context.Context, _ storepkg.TransactionCollections, fn storepkg.TransactionFunc) error {
	return fn(ctx, c)
}

func (c *revisionClient) QueryRows(_ context.Context, query string, _ int, binds map[string]any, visit storepkg.RowVisitor) error {
	if strings.Contains(query, "RETURN {new: NEW, old: OLD}") {
		incoming := binds["doc"].(map[string]any)
		if c.doc == nil {
			c.doc = incoming
			return visit(map[string]any{"new": incoming, "old": nil})
		}
		return visit(map[string]any{"new": c.doc, "old": c.doc})
	}
	return nil
}

func TestInsertRevisionRejectsChangedImmutableContent(t *testing.T) {
	client := &revisionClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	first := explorer.Revision{ID: "revision-a", Project: "project-a", ExplorerID: "patients", Config: []byte(`{"version":1}`), RecipeDigest: "recipe-a"}
	stored, err := adapter.InsertRevision(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored.Config) != string(first.Config) {
		t.Fatalf("stored config = %s, want %s", stored.Config, first.Config)
	}
	changed := first
	changed.Config = []byte(`{"version":2}`)
	if _, err := adapter.InsertRevision(context.Background(), changed); err != explorer.ErrImmutableRevision {
		t.Fatalf("changed revision error = %v, want ErrImmutableRevision", err)
	}
	if got := client.doc["config"]; got == nil {
		t.Fatal("changed revision removed the original document")
	}
	retried, err := adapter.InsertRevision(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if string(retried.Config) != string(first.Config) {
		t.Fatalf("identical retry config = %s, want %s", retried.Config, first.Config)
	}
}

type activationClient struct{ calls []queryCall }

func (c *activationClient) WithTransaction(ctx context.Context, _ storepkg.TransactionCollections, fn storepkg.TransactionFunc) error {
	return fn(ctx, c)
}

func (c *activationClient) QueryRows(_ context.Context, query string, _ int, binds map[string]any, visit storepkg.RowVisitor) error {
	c.calls = append(c.calls, queryCall{query: query, binds: binds})
	if strings.Contains(query, "RETURN {manifest:") {
		return visit(compositeActivationRow())
	}
	if strings.Contains(query, "RETURN {owner:") {
		return visit(ownerActivationRow())
	}
	return visit(map[string]any{"ok": true})
}

func ownerActivationRow() map[string]any {
	return map[string]any{
		"owner": map[string]any{"_key": "owner-key", "project": "project-a", "explorerId": "default", "managementMode": "REPOSITORY"},
		"candidate": map[string]any{
			"_key": "revision-a", "id": "revision-a", "project": "project-a", "explorerId": "default", "status": "READY",
			"config": map[string]any{}, "sourceGeneration": "generation-a", "publication": map[string]any{},
			"materializations": []any{}, "emittedColumns": []any{}, "diagnostics": []any{},
		},
		"prior": nil,
	}
}

func compositeActivationRow() map[string]any {
	row := ownerActivationRow()
	row["manifest"] = map[string]any{"_key": "manifest-key", "recordType": "manifest", "state": "STAGED", "dataset": map[string]any{"project": "project-a", "generation": "generation-a"}}
	row["active"] = map[string]any{"_key": "active-key", "recordType": "active_generation", "project": "project-a"}
	row["execution"] = map[string]any{"project": "project-a", "datasetGeneration": "generation-a", "state": "PUBLISHED"}
	return row
}

func TestActivateRepositoryGenerationUsesCompositeGuards(t *testing.T) {
	client := &activationClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.ActivateRepositoryGeneration(context.Background(), "project-a", "generation-a", "revision-a"); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 4 {
		t.Fatalf("calls=%d", len(client.calls))
	}
	query := client.calls[0].query
	for _, required := range []string{
		"@@lifecycle", "@@explorers", "@@revisions", "@@executions",
		"manifest.state IN [\"STAGED\", \"READY\"]",
		"candidate.status IN [\"READY\", \"FAILED\"]",
		"candidate.status == \"ACTIVE\" AND owner.activeRevisionId == @revisionKey",
		"execution.project == @project", "execution.datasetGeneration == @generation", "execution.state == \"PUBLISHED\"",
	} {
		if !strings.Contains(query, required) {
			t.Fatalf("query missing %q:\n%s", required, query)
		}
	}
	allQueries := ""
	for _, call := range client.calls {
		allQueries += call.query
	}
	if got := strings.Count(allQueries, "UPDATE d WITH"); got != 2 {
		t.Fatalf("composite activation revision/dataset updates=%d, want 2:\n%s", got, allQueries)
	}
	if !strings.Contains(allQueries, "REPLACE d WITH MERGE(UNSET(d") {
		t.Fatalf("owner activation does not remove legacy generated fields:\n%s", allQueries)
	}
	if got := client.calls[0].binds["generation"]; got != "generation-a" {
		t.Fatalf("generation bind=%v", got)
	}
	patch, ok := client.calls[2].binds["patch"].(map[string]any)
	if !ok || patch["status"] != explorer.RevisionActive || patch["failedAt"] != nil {
		t.Fatalf("revision activation patch=%#v", patch)
	}
	if diagnostics, ok := patch["diagnostics"].([]any); !ok || len(diagnostics) != 0 {
		t.Fatalf("activation diagnostics=%#v, want cleared", patch["diagnostics"])
	}
	publication, ok := patch["publication"].(map[string]any)
	if !ok || publication["state"] != string(explorer.RevisionActive) || publication["revisionId"] != "revision-a" || publication["generation"] != "generation-a" {
		t.Fatalf("activation publication=%#v", patch["publication"])
	}
	if updatedAt, ok := publication["updatedAt"].(time.Time); !ok || updatedAt.IsZero() {
		t.Fatalf("activation publication updatedAt=%#v", publication["updatedAt"])
	}
}

func TestActivationUsesTransactionsAndSingleModificationQueries(t *testing.T) {
	tests := []struct {
		name string
		call func(context.Context, *Store) error
	}{
		{
			name: "repository generation",
			call: func(ctx context.Context, store *Store) error {
				return store.ActivateRepositoryGeneration(ctx, "project-a", "generation-a", "revision-a")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &activationClient{}
			store, err := New(client)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.call(context.Background(), store); err != nil {
				t.Fatal(err)
			}
			if len(client.calls) < 3 {
				t.Fatalf("calls=%d, want read plus updates", len(client.calls))
			}
			for _, call := range client.calls {
				for _, invalid := range []string{
					"? null : UPDATE", "LET active = UPDATE", "LET explorer = UPDATE", "LET owner = UPDATE",
					"LET activateDataset = UPDATE", "LET activateRevision = UPDATE", "LET activateExplorer = UPDATE",
					"FOR priorDoc IN [prior]",
				} {
					if strings.Contains(call.query, invalid) {
						t.Fatalf("invalid AQL activation form %q:\n%s", invalid, call.query)
					}
				}
			}
		})
	}
}

func TestProjectConfigCatalogAnchorPublishRetryAndRebindAgainstArango(t *testing.T) {
	ctx, client, explorers, generations := projectConfigPublicationDB(t)
	project := "project-config-catalog-anchor"
	bundle := projectConfigTestBundle("recipe-a", "translation-1")
	firstPublication := projectConfigPublication(t, project, "default", "recipe-a", "Default", "revision-1", "generation-1", "execution-1", bundle)
	first := projectConfigPublishRequest(t, ctx, client, generations, project, "generation-1", "execution-1", bundle, "commit-1", 0, firstPublication)
	first.ExpectedConfigRevisionIDs = map[string]string{"default": ""}
	first.CatalogSnapshot = projectConfigCatalogSnapshotForTest(t, firstPublication)
	active, err := explorers.PublishProjectConfigs(ctx, first)
	if err != nil {
		t.Fatalf("publish source and catalog anchor: %v", err)
	}
	settings, err := explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || settings.Version != 1 || settings.Catalogs["recipe-a"] != first.CatalogSnapshot.ID {
		t.Fatalf("published anchor=%+v, err=%v", settings, err)
	}
	stored, err := explorers.GetRevision(ctx, settings.Catalogs["recipe-a"])
	if err != nil || stored.ExplorerID != "" || stored.Status != explorer.RevisionReady || len(stored.Config) != 0 || stored.Publication.ExecutionID != "execution-1" {
		t.Fatalf("stored source catalog=%+v, err=%v", stored, err)
	}
	if _, err := explorers.PublishProjectConfigs(ctx, first); err != nil {
		t.Fatalf("retry exact source publication: %v", err)
	}
	retried, err := explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || retried.Version != settings.Version || retried.Catalogs["recipe-a"] != settings.Catalogs["recipe-a"] {
		t.Fatalf("exact retry changed catalog anchor: %+v, err=%v", retried, err)
	}
	secondPublication := projectConfigPublication(t, project, "default", "recipe-a", "Default", "revision-2", "generation-2", "execution-2", bundle)
	second := projectConfigPublishRequest(t, ctx, client, generations, project, "generation-2", "execution-2", bundle, "commit-2", active.Revision, secondPublication)
	second.ExpectedConfigRevisionIDs = map[string]string{"default": "revision-1"}
	second.CatalogSnapshot = projectConfigCatalogSnapshotForTest(t, secondPublication)
	if _, err := explorers.PublishProjectConfigs(ctx, second); err != nil {
		t.Fatalf("rebind catalog to new source execution: %v", err)
	}
	updated, err := explorers.GetViewBuilderSettings(ctx, project)
	if err != nil || updated.Version != settings.Version+1 || updated.Catalogs["recipe-a"] != second.CatalogSnapshot.ID {
		t.Fatalf("source rebind did not advance catalog anchor: %+v, err=%v", updated, err)
	}

}

func projectConfigCatalogSnapshotForTest(t *testing.T, publication explorer.ProjectConfigPublication) *explorer.Revision {
	t.Helper()
	source := publication.Revision
	id := "catalog_" + db.Key(source.Project, publication.Definition.RecipeRef.Name, source.Publication.ExecutionID, source.RecipeDigest, source.Dataset.SchemaDigest, string(source.PublicOutputContract))
	snapshot := source
	snapshot.ID = id
	snapshot.ExplorerID = ""
	snapshot.Config = nil
	snapshot.AuthoringBundle = nil
	snapshot.IntentDigest = ""
	snapshot.CompilationReceiptID = ""
	snapshot.CreatedBy = ""
	snapshot.Diagnostics = []viewer.Diagnostic{}
	snapshot.Status = explorer.RevisionReady
	snapshot.ActivatedAt = nil
	snapshot.FailedAt = nil
	snapshot.Publication.State = string(explorer.RevisionReady)
	snapshot.Publication.RevisionID = id
	return &snapshot
}
