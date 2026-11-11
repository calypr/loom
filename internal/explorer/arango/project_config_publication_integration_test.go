package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	publicationdata "github.com/calypr/loom/internal/dataframe/publication"
	publicationarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
	projectstore "github.com/calypr/loom/internal/store/arango"
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

	// An acknowledgement-loss replay uses the same immutable request and stale
	// expected revision. The release's content-addressed already-active branch
	// and the matching config revisions must converge without a second bump.
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
		{Project: project, ExplorerID: "other-recipe", Title: otherDefinition.Title, ManagementMode: explorer.ManagementRepository, ProjectConfig: &otherDefinition, DraftVersion: 1, DraftDigest: otherDigest, UpdatedAt: createdAt},
		{Project: project, ExplorerID: "interactive", Title: "Builder owner", ManagementMode: explorer.ManagementInteractive, DraftVersion: 1, UpdatedAt: createdAt},
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
	if err != nil || interactive.ManagementMode != explorer.ManagementInteractive || interactive.ProjectConfig != nil {
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
	if _, err := explorers.Create(ctx, explorer.Explorer{Project: project, ExplorerID: "taken", Title: "Interactive", ManagementMode: explorer.ManagementInteractive, UpdatedAt: time.Now().UTC()}); err != nil {
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
	if _, err := explorers.Create(ctx, explorer.Explorer{Project: legacyProject, ExplorerID: "default", Title: "Legacy repository", ManagementMode: explorer.ManagementRepository, DraftVersion: 1, UpdatedAt: time.Now().UTC()}); err != nil {
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
	endpoint := os.Getenv("LOOM_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set LOOM_ARANGO_URL to run project config publication against a disposable database")
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

func projectConfigTestDefinition(id, recipeName, title string) explorer.ProjectConfig {
	return explorer.ProjectConfig{
		ID: id, Title: title, RecipeRef: explorer.ProjectConfigRecipeRef{Name: recipeName},
		Views: []explorer.ConfigView{{
			ID: "view", Title: title, Output: "output",
			Table:   explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: "extension_code", Visible: true}}},
			Filters: []explorer.ConfigFilter{{Column: "extension_code"}},
			Charts:  []explorer.ConfigChart{{Column: "extension_code", Type: "pie", Title: "Extension codes"}},
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
	explorerJSON, err := json.Marshal(explorer.ConfigExplorer{ID: id, Title: title, Management: "repository"})
	if err != nil {
		t.Fatal(err)
	}
	configJSON := json.RawMessage(fmt.Sprintf(`{ "views": %s, "project": %q, "kind": "ExplorerConfig", "explorer": %s, "apiVersion": %q }`, viewsJSON, project, explorerJSON, explorer.ConfigV2APIVersion))
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
			EmittedColumns: []explorer.EmittedColumn{{
				EmissionID: "dynamic:extension_code", OutputID: "output", PublicColumn: "extension_code",
				Label: "Extension code", LogicalType: "string", Cardinality: "optional_one",
				Nullable: true, Shape: "scalar", Lossless: true, Filterable: true, Chartable: true,
			}},
			SourceGeneration: generation, Dataset: explorer.DatasetMetadata{
				Generation: generation, SchemaDigest: strings.Repeat("c", 64),
				Outputs: []explorer.DatasetOutput{{Name: "output", State: "PUBLISHED", Queryable: true, Selector: &selector, Columns: []publicationdata.PhysicalColumn{physical}}},
			},
			Materializations: []explorer.Materialization{{OutputID: "output", Output: "output", MaterializationID: executionID, Selector: &selector, Columns: []publicationdata.PhysicalColumn{physical}}},
			QualityReports:   []publicationdata.QualityReport{projectConfigTestQuality(project, generation, executionID)},
			Publication:      explorer.PublicationMetadata{State: "READY", Generation: generation, ExecutionID: executionID, UpdatedAt: now},
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
	doc, err := document(execution, execution.ID)
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
	if owner.ManagementMode != explorer.ManagementRepository || owner.ProjectConfig == nil || owner.ActiveRevisionID != revisionID {
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
