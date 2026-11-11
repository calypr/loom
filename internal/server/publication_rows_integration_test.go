package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	arangofactory "github.com/calypr/loom/internal/backends/arango"
	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	publicationclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	storeclickhouse "github.com/calypr/loom/internal/store/clickhouse"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// A bounded assembled publication checkpoint: actual AQL/compiler/stream,
// ClickHouse materialization, registry and atomic Explorer/release activation.
// Authorization/capability discovery are fixture-owned. Fresh clients prove
// stored read-back, not database process restart or browser rendering.
func TestPublicationRowsAndFreshViewerAgainstArangoClickHouse(t *testing.T) {
	arangoURL, clickhouseURL := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_CLICKHOUSE_URL")
	if arangoURL == "" || clickhouseURL == "" {
		t.Skip("requires owned LOOM_TEST_ARANGO_URL and LOOM_CLICKHOUSE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	database := "loom_publication_rows_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: arangoURL, Database: database})
	if err != nil {
		t.Fatal(err)
	}
	graph := arangoBackend.Client
	t.Cleanup(func() {
		graph.Close(context.Background())
		request, _ := http.NewRequest(http.MethodDelete, strings.TrimRight(arangoURL, "/")+"/_api/database/"+database, nil)
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err != nil {
			t.Error(err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("owned graph cleanup: HTTP%d", response.StatusCode)
		}
	})
	for _, spec := range []arangofactory.BootstrapSpec{arangofactory.ExplorerBootstrapSpec(), arangofactory.PublishedBootstrapSpec(), arangofactory.DatasetBootstrapSpec(), {Collections: []arangofactory.CollectionSpec{{Name: "Patient"}}}} {
		if err := graph.Bootstrap(ctx, spec); err != nil {
			t.Fatal(err)
		}
	}
	var input []json.RawMessage
	for _, row := range []struct{ key, id, project, generation string }{{"row_b", "p2", "project-a", "generation-a"}, {"row_a", "p1", "project-a", "generation-a"}, {"foreign", "foreign", "project-b", "generation-a"}, {"old", "old", "project-a", "generation-old"}} {
		raw, _ := json.Marshal(map[string]any{"_key": row.key, "id": row.id, "resourceType": "Patient", "project": row.project, "project_id": row.project, "dataset_generation": row.generation, "payload": map[string]any{"id": row.id, "resourceType": "Patient"}})
		input = append(input, raw)
	}
	if err := graph.InsertBatchRaw(ctx, "Patient", input, false, "document"); err != nil {
		t.Fatal(err)
	}
	ch, err := storeclickhouse.New(storeclickhouse.Options{URL: clickhouseURL, Database: os.Getenv("LOOM_CLICKHOUSE_DATABASE"), Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"), Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ch.Close(); err != nil {
			t.Error(err)
		}
	})
	registry, err := arangofactory.NewPublishedRegistry(graph)
	if err != nil {
		t.Fatal(err)
	}
	target, err := publicationclickhouse.NewBundleStore(ch, registry)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	t.Cleanup(func() {
		for _, table := range tables {
			if err := ch.DropTable(context.Background(), table); err != nil {
				t.Error(err)
			}
		}
	})
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store, err := arangofactory.NewExplorerStore(graph)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(ctx, explorer.Explorer{Project: "project-a", ExplorerID: "published-rows", Title: "Published rows", ManagementMode: explorer.ManagementInteractive, DraftConfig: raw, DraftDigest: digest, DraftVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget: compiler.ArangoTarget(), Registry: compilerTestRegistry{}, SourceRows: func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
			command := request.Command
			batch := request.BatchSize
			query, vars, deriveErr := command.AQLParameters()
			if deriveErr != nil {
				return deriveErr
			}

			return graph.QueryRows(ctx, query, batch, vars, arangofactory.RowVisitor(visit))
		}})
	if err != nil {
		t.Fatal(err)
	}
	authorized := lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}
	receipt, err := compileExplorerReceipt(ctx, lifecycle.CompileReceiptRequest{Project: "project-a", ExplorerID: "published-rows", Workspace: workspace, SnapshotToken: snapshot.Token, Authorized: authorized}, nil, engine, service, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	releases, err := arangofactory.NewDatasetStore(graph)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := dataset.NewSchemaSnapshot("urn:publication-test", "", strings.Repeat("a", 64), []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := dataset.NewRef("project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := dataset.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = releases.CreateManifest(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = releases.TransitionManifest(ctx, manifest, dataset.StateStaged)
	if err != nil {
		t.Fatal(err)
	}
	if err := releases.Activate(ctx, manifest); err != nil {
		t.Fatalf("activate staged generation before authoring publication: %v", err)
	}
	release := dataset.ProjectRelease{ID: "published-rows-release", Project: "project-a", Generation: "generation-a"}
	config := lifecycle.Config{Capability: lifecycle.CapabilityResolver{Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil }, ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) { return authorized, nil }}, ValidateReleaseGeneration: func(ctx context.Context, project, generation string) error {
		m, err := releases.ReadManifest(ctx, dataset.Ref{Project: project, Generation: generation})
		if err != nil {
			return err
		}
		if !m.IsStaged() {
			return fmt.Errorf("generation not staged")
		}
		return nil
	}, PrepareRelease: func(ctx context.Context, project, generation string, selectors []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
		want := []dataset.DataframeSelector{{Recipe: receipt.Bundle.Name, TranslationVersion: receipt.Bundle.TranslationVersion, Output: "patients"}}
		if project != "project-a" || generation != "generation-a" || !reflect.DeepEqual(selectors, want) {
			return dataset.ProjectRelease{}, 0, fmt.Errorf("wrong release binding: %s/%s %#v", project, generation, selectors)
		}
		execution, output, err := registry.FindExecutionBySelector(ctx, project, generation, selectors[0])
		if err != nil {
			return dataset.ProjectRelease{}, 0, err
		}
		if !output.Queryable() {
			return dataset.ProjectRelease{}, 0, fmt.Errorf("release output not queryable")
		}
		release.Publications = []dataset.ReleasePublication{{Selector: selectors[0], ExecutionID: execution.ID, Generation: generation, Required: true, VerifiedAt: *output.VerifiedAt}}
		saved, err := releases.SaveRelease(ctx, release)
		return saved, 0, err
	}, MaterializeReceipt: explorerReceiptMaterializer(engine, target, registry, nil, nil, 2, 1024*1024, 100, 100)}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	path := "/api/v1/projects/project-a/explorers/published-rows"
	response := requestJSON(t, app, http.MethodPost, path+"/authoring/v2/publish", fmt.Sprintf(`{"receiptId":%q}`, receipt.ID))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("publication HTTP%d: %s", response.StatusCode, response.Body)
	}
	var result struct {
		ReceiptID  string `json:"receiptId"`
		RevisionID string `json:"revisionId"`
	}
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.ReceiptID != receipt.ID || result.RevisionID == "" {
		t.Fatalf("publication identity: %#v", result)
	}
	// Later draft/source edits must not change the already materialized revision.
	workspace.Documents[0].Columns[0].Label = "Unpublished label"
	laterRaw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	laterDigest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.Get(ctx, "project-a", "published-rows")
	if err != nil {
		t.Fatal(err)
	}
	owner.DraftConfig, owner.DraftDigest = laterRaw, laterDigest
	if _, err := store.SaveDraft(ctx, *owner, owner.DraftVersion); err != nil {
		t.Fatal(err)
	}
	if err := graph.InsertBatchRaw(ctx, "Patient", []json.RawMessage{json.RawMessage(`{"_key":"later","id":"p3","resourceType":"Patient","project":"project-a","project_id":"project-a","dataset_generation":"generation-a","payload":{"id":"p3","resourceType":"Patient"}}`)}, false, "document"); err != nil {
		t.Fatal(err)
	}
	repeated := requestJSON(t, app, http.MethodPost, path+"/authoring/v2/publish", fmt.Sprintf(`{"receiptId":%q}`, receipt.ID))
	if repeated.StatusCode != http.StatusOK {
		t.Fatalf("repeat publication HTTP%d: %s", repeated.StatusCode, repeated.Body)
	}
	var repeat struct {
		ReceiptID  string `json:"receiptId"`
		RevisionID string `json:"revisionId"`
	}
	if err := json.Unmarshal([]byte(repeated.Body), &repeat); err != nil {
		t.Fatal(err)
	}
	if repeat.ReceiptID != result.ReceiptID || repeat.RevisionID != result.RevisionID {
		t.Fatalf("repeat changed immutable publication: %#v", repeat)
	}
	for read := 0; read < 2; read++ {
		arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: arangoURL, Database: database})
		if err != nil {
			t.Fatal(err)
		}
		freshGraph := arangoBackend.Client
		freshStore, err := arangofactory.NewExplorerStore(freshGraph)
		if err != nil {
			t.Fatal(err)
		}
		freshService, err := explorer.NewService(freshStore)
		if err != nil {
			t.Fatal(err)
		}
		freshApp := fiber.New()
		registerGeneratedExplorerTestRoutes(freshApp, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, freshService, config)
		viewerResponse := requestJSON(t, freshApp, http.MethodGet, path, "")
		if viewerResponse.StatusCode != http.StatusOK {
			t.Fatalf("fresh Viewer HTTP%d: %s", viewerResponse.StatusCode, viewerResponse.Body)
		}
		var viewer explorer.ExplorerStateV1
		if err := json.Unmarshal([]byte(viewerResponse.Body), &viewer); err != nil {
			t.Fatal(err)
		}
		revision, err := freshService.ActiveRevision(ctx, "project-a", "published-rows")
		if err != nil {
			t.Fatal(err)
		}
		if revision.ID != result.RevisionID || revision.CompilationReceiptID != receipt.ID || viewer.Active.RevisionID != result.RevisionID || viewer.Runtime == nil || len(viewer.Runtime.Outputs) != 1 {
			t.Fatalf("fresh publication mismatch: revision=%#v viewer=%#v", revision, viewer)
		}
		if viewer.Draft.Digest != laterDigest || string(viewer.Active.Bundle) != string(receipt.NormalizedBundle) {
			t.Fatal("fresh Viewer lost immutable published workspace or current draft")
		}
		output := viewer.Runtime.Outputs[0]
		if output.OutputID != "patients" || output.Materialization == nil || output.Selector != (dataset.DataframeSelector{Recipe: receipt.Bundle.Name, TranslationVersion: receipt.Bundle.TranslationVersion, Output: "patients"}) || len(output.Columns) != 1 || output.Columns[0].Label != "Patient ID" {
			t.Fatalf("Viewer output mismatch: %#v", output)
		}
		freshRegistry, err := arangofactory.NewPublishedRegistry(freshGraph)
		if err != nil {
			t.Fatal(err)
		}
		execution, err := freshRegistry.GetExecution(ctx, output.Materialization.MaterializationID)
		if err != nil {
			t.Fatal(err)
		}
		if read == 0 {
			for _, storedOutput := range execution.Outputs {
				if storedOutput.PhysicalTable != "" {
					tables = append(tables, storedOutput.PhysicalTable)
				}
			}
		}
		if execution.ReceiptID != receipt.ID || execution.DatasetGeneration != "generation-a" || len(execution.Outputs) != 1 || execution.Outputs[0].Name != "patients" || execution.Outputs[0].RowCount != 2 || !execution.Outputs[0].Queryable() {
			t.Fatalf("persisted execution mismatch: %#v", execution)
		}
		table := execution.Outputs[0].PhysicalTable
		freshCH, err := storeclickhouse.New(storeclickhouse.Options{URL: clickhouseURL, Database: os.Getenv("LOOM_CLICKHOUSE_DATABASE"), Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"), Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"), Timeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		rows, err := freshCH.QueryRowsArgs(ctx, "SELECT c_patient FROM "+table+" ORDER BY c_patient", []string{"c_patient"})
		if err != nil {
			t.Fatal(err)
		}
		if closeErr := freshCH.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		want := []map[string]any{{"c_patient": "p1"}, {"c_patient": "p2"}}
		if !reflect.DeepEqual(rows, want) {
			t.Fatalf("published rows: got=%#v want=%#v", rows, want)
		}
		active, err := releases.ReadActiveRelease(ctx, "project-a")
		if err != nil {
			t.Fatal(err)
		}
		if active.Release.ID != release.ID || active.Revision != 1 || len(active.Release.Publications) != 1 || active.Release.Publications[0].ExecutionID != execution.ID || active.Release.Publications[0].Selector != output.Selector {
			t.Fatalf("release identity=%#v", active)
		}
		freshGraph.Close(ctx)
	}
}
