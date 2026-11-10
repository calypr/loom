package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	explorerarango "github.com/calypr/loom/internal/explorer/arango"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// TestCoreAuthoringProtocolAgainstArango is the joined authoring-protocol witness
// against the real Arango Explorer repository (plan C10/C12/C16/C17): every claim is
// read back from persisted Arango bytes through the repository (or a second
// independent client), never from a service echo or the in-memory fixture. It drives
// the same production generated strict adapter, handlers, lifecycle service and
// compile chain as newCoreProtocolFixture, and owns assertions the map-store
// witnesses cannot make: restart persistence through a fresh repository instance,
// compare-and-swap rejection against real Arango transaction state, and
// exactly-one-document integrity through a raw AQL count.
func TestCoreAuthoringProtocolAgainstArango(t *testing.T) {
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("requires owned LOOM_TEST_ARANGO_URL")
	}
	database := "loom_core_authoring_conformance_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client, err := storearango.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
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
			t.Errorf("remove owned disposable database status=%d", response.StatusCode)
		}
	})
	if err := client.Bootstrap(ctx, explorerarango.BootstrapSpec()); err != nil {
		t.Fatal(err)
	}
	store, err := explorerarango.New(client)
	if err != nil {
		t.Fatal(err)
	}

	// Seed through the same store with the exact identity values used by the
	// map-store fixture (core_authoring_protocol_test.go:126-149) so both witnesses
	// describe the same protocol; the DraftConfig raw bytes are identical.
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
		Explorer:  authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(), Output: authoringv2.Output{ID: "patients", Title: "Patients", RowLabel: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Columns: []authoringv2.Column{{ColumnID: "id", Column: "c_patient", Label: "Patient ID", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}}}},
		Tabs:      []authoringv2.Tab{{ID: "patients-tab", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, explorer.Explorer{
		Project: "project-a", ExplorerID: "core-protocol", Title: "Patients", ManagementMode: explorer.ManagementInteractive,
		DraftConfig: raw, DraftVersion: 1, DraftDigest: digest,
		DraftSnapshotToken: snapshot.Token, DraftSourceGeneration: snapshot.Identity.Generation,
		DraftAuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
	}); err != nil {
		t.Fatal(err)
	}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	queryRows := func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		command := request.Command
		_, _, deriveErr := command.AQLParameters()
		if deriveErr != nil {
			return deriveErr
		}
		return nil
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{CompileTarget: compiler.ArangoTarget(), Registry: compilerTestRegistry{}, SourceRows: queryRows, PreviewSourceRows: queryRows})
	if err != nil {
		t.Fatal(err)
	}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	serviceConfig := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
			Token:   func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, engine, domain, nil, nil)
		},
		ReceiptLookup: func(ctx context.Context, project, explorerID, receiptID string) (*explorer.CompilationReceipt, error) {
			return domain.CompilationReceiptForExplorer(ctx, project, explorerID, receiptID)
		},
		PreviewReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			resolved, resolveErr := compileValidatedReceiptResolution(ctx, engine, receipt, bindings)
			if resolveErr != nil {
				return dataframeexecution.PreviewSummary{}, resolveErr
			}
			return engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{
				Output: bindings.OutputNames[0], Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity,
			}, visit)
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, domain, serviceConfig)
	fixture := &coreProtocolFixture{t: t, app: app, snapshot: snapshot, workspace: workspace, initialDigest: digest, basePath: "/api/v1/projects/project-a/explorers/core-protocol/authoring/v2"}

	// (17) Ack identity is what the Arango document actually carries. The map-store
	// witness sees the same in-memory object the handler manipulated; this reads the
	// committed document back through the Arango repository.
	saved, ack := fixture.saveLabel("core-arango-save-1", "Patient Identifier", 1, digest)
	if saved.StatusCode != http.StatusOK {
		t.Fatalf("commands status=%d body=%s", saved.StatusCode, saved.Body)
	}
	if ack.CommandId != "core-arango-save-1" {
		t.Fatalf("acknowledgement does not identify the command: want commandId=core-arango-save-1 got=%+v body=%s", ack, saved.Body)
	}
	if ack.DraftVersion != 2 || ack.DraftDigest == "" || ack.DraftDigest == digest {
		t.Fatalf("acknowledgement does not identify a saved revision: want draftVersion=2 and a new digest, got=%+v", ack)
	}
	if ack.PreviousDraftRevisionId == nil || *ack.PreviousDraftRevisionId == "" {
		t.Fatalf("acknowledgement lacks the previous revision identity: %+v", ack)
	}

	persisted, err := store.Get(ctx, "project-a", "core-protocol")
	if err != nil {
		t.Fatalf("read persisted head from Arango: %v", err)
	}
	if persisted.DraftVersion != ack.DraftVersion {
		t.Fatalf("persisted draftVersion=%d does not match acknowledged draftVersion=%d", persisted.DraftVersion, ack.DraftVersion)
	}
	if persisted.DraftDigest != ack.DraftDigest {
		t.Fatalf("persisted draftDigest=%q does not match acknowledged draftDigest=%q", persisted.DraftDigest, ack.DraftDigest)
	}
	if persisted.PreviousDraftRevisionID != *ack.PreviousDraftRevisionId {
		t.Fatalf("persisted head points to previousDraftRevisionId=%q, acknowledged=%q", persisted.PreviousDraftRevisionID, *ack.PreviousDraftRevisionId)
	}

	// The persisted bytes themselves round-trip to the exact post-command workspace.
	expectedPost := workspace
	expectedPost.SemanticsVersion = authoringv2.CurrentSemanticsVersion
	expectedPost.Documents[0].Columns[0].Label = "Patient Identifier"
	expectedRaw, err := expectedPost.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	persistedWorkspace, err := authoringv2.DecodeWorkspace(persisted.DraftConfig)
	if err != nil {
		t.Fatalf("decode persisted Arango draft bytes: %v raw=%s", err, persisted.DraftConfig)
	}
	persistedRaw, err := persistedWorkspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(persistedRaw, expectedRaw) {
		t.Fatalf("persisted Arango draft config differs from the expected post-command workspace\nexpected=%s\npersisted=%s", expectedRaw, persistedRaw)
	}

	// The acknowledged previous revision id resolves to the exact pre-command head
	// bytes in Arango (the revision id scheme lives in drafts.go; never hardcoded).
	prior, err := store.GetDraftRevision(ctx, "project-a", "core-protocol", *ack.PreviousDraftRevisionId)
	if err != nil {
		t.Fatalf("read acknowledged previous revision %s from Arango: %v", *ack.PreviousDraftRevisionId, err)
	}
	if prior.ID != *ack.PreviousDraftRevisionId {
		t.Fatalf("previous revision id=%q does not match acknowledged %q", prior.ID, *ack.PreviousDraftRevisionId)
	}
	if prior.DraftVersion != 1 || prior.DraftDigest != digest {
		t.Fatalf("previous revision does not capture the pre-command head: got version=%d digest=%q, want version=1 digest=%q", prior.DraftVersion, prior.DraftDigest, digest)
	}
	priorWorkspace, err := authoringv2.DecodeWorkspace(prior.DraftConfig)
	if err != nil {
		t.Fatalf("decode previous revision bytes: %v raw=%s", err, prior.DraftConfig)
	}
	priorRaw, err := priorWorkspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(priorRaw, raw) {
		t.Fatalf("previous revision bytes differ from the pre-command head\nexpected=%s\npersisted=%s", raw, priorRaw)
	}

	// (16) The compile receipt is persisted in Arango and references the exact
	// committed head identity. CompilationReceipt carries no draftVersion/draftDigest
	// header field; the strongest identity binding the type actually has is the
	// workspace content IntentDigest, the content-addressed ID, and admission
	// through the store's validateReceipt on read — asserted below.
	compiled := fixture.decodeCompile(fixture.MustReconcile(ack.DraftVersion, ack.DraftDigest, "core-arango-reconcile"))
	persistedReceipt, err := store.GetCompilationReceiptForExplorer(ctx, "project-a", "core-protocol", compiled.ReceiptId)
	if err != nil {
		t.Fatalf("receipt %s not persisted in Arango: %v", compiled.ReceiptId, err)
	}
	t.Log("CompilationReceipt has no persisted draftVersion/draftDigest field; asserting the head binding through IntentDigest (workspace content digest of the committed head), receipt-content-addressed ID recomputation, and GetCompilationReceiptForExplorer's validateReceipt admission")
	if persistedReceipt.ID != compiled.ReceiptId || persistedReceipt.Project != "project-a" || persistedReceipt.ExplorerID != "core-protocol" {
		t.Fatalf("persisted receipt identity mismatch: got id=%q project=%q explorerId=%q, want id=%q project=%q explorerId=%q", persistedReceipt.ID, persistedReceipt.Project, persistedReceipt.ExplorerID, compiled.ReceiptId, "project-a", "core-protocol")
	}
	if persistedReceipt.IntentDigest != ack.DraftDigest {
		t.Fatalf("persisted receipt intentDigest=%q does not reference the committed head draftDigest=%q acknowledged by save", persistedReceipt.IntentDigest, ack.DraftDigest)
	}
	if persistedReceipt.IntentDigest != digestForWorkspace(t, expectedPost) {
		t.Fatalf("persisted receipt intentDigest=%q does not reference the expected post-command head digest=%q", persistedReceipt.IntentDigest, digestForWorkspace(t, expectedPost))
	}
	recomputedID, err := explorer.ReceiptID(*persistedReceipt)
	if err != nil {
		t.Fatalf("recompute receipt content id: %v", err)
	}
	if recomputedID != persistedReceipt.ID {
		t.Fatalf("persisted receipt is not content-addressed: stored id=%q recomputed=%q", persistedReceipt.ID, recomputedID)
	}
	if persistedReceipt.SnapshotToken != snapshot.Token || persistedReceipt.SourceGeneration != snapshot.Identity.Generation {
		t.Fatalf("persisted receipt source identity mismatch: token=%q generation=%q want token=%q generation=%q", persistedReceipt.SnapshotToken, persistedReceipt.SourceGeneration, snapshot.Token, snapshot.Identity.Generation)
	}
	if persistedReceipt.OutputContractDigest == "" || persistedReceipt.RecipeDigest == "" || len(persistedReceipt.Bundle.Outputs) != 1 {
		t.Fatalf("persisted receipt lacks the digests that bind the executable contract: outputContractDigest=%q recipeDigest=%q outputs=%d", persistedReceipt.OutputContractDigest, persistedReceipt.RecipeDigest, len(persistedReceipt.Bundle.Outputs))
	}
	receiptLabel := ""
	for _, column := range persistedReceipt.EmittedColumns {
		if column.OutputID == "patients" && column.PublicColumn == "c_patient" {
			receiptLabel = column.Label
		}
	}
	if receiptLabel != "Patient Identifier" {
		t.Fatalf("persisted receipt public contract label=%q want %q", receiptLabel, "Patient Identifier")
	}

	// (16) Receipt-bound preview re-execution keeps the accepted identity: rows are
	// the fixture's controlled empty array and the single contract label is the
	// saved label (same expectations as TestCoreAuthoringProtocolVerticalWitness).
	previewed := fixture.preview(compiled.ReceiptId)
	if previewed.StatusCode != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", previewed.StatusCode, previewed.Body)
	}
	var preview loomapi.PreviewResponse
	if err := json.Unmarshal([]byte(previewed.Body), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.ReceiptId != compiled.ReceiptId || preview.OutputId != "patients" || preview.Rows == nil || !strings.Contains(previewed.Body, `"rows":[]`) {
		t.Fatalf("preview identity not bound to the receipt or rows are not the controlled empty array: %+v body=%s", preview, previewed.Body)
	}
	previewLabels := make([]string, 0, len(preview.Columns))
	for _, column := range preview.Columns {
		previewLabels = append(previewLabels, column.Label)
	}
	if len(previewLabels) != 1 || previewLabels[0] != "Patient Identifier" {
		t.Fatalf("preview contract labels=%v want [Patient Identifier]", previewLabels)
	}

	// (12) Restart persistence: a fresh Open + fresh repository + fresh service on
	// the same database must reproduce the committed truth from persisted bytes.
	restartClient, err := storearango.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restartClient.Close(context.Background()) })
	restartStore, err := explorerarango.New(restartClient)
	if err != nil {
		t.Fatal(err)
	}
	restartDomain, err := explorer.NewService(restartStore)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := restartDomain.Get(ctx, "project-a", "core-protocol")
	if err != nil {
		t.Fatalf("fresh service lost the persisted head: %v", err)
	}
	if restarted.DraftVersion != ack.DraftVersion || restarted.DraftDigest != ack.DraftDigest {
		t.Fatalf("fresh service head disagrees with the acknowledged identity: got version=%d digest=%q, want version=%d digest=%q", restarted.DraftVersion, restarted.DraftDigest, ack.DraftVersion, ack.DraftDigest)
	}
	restartedWorkspace, err := authoringv2.DecodeWorkspace(restarted.DraftConfig)
	if err != nil {
		t.Fatalf("decode fresh-service persisted bytes: %v raw=%s", err, restarted.DraftConfig)
	}
	restartedRaw, err := restartedWorkspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restartedRaw, expectedRaw) {
		t.Fatalf("fresh service draft bytes differ from the committed truth\nexpected=%s\npersisted=%s", expectedRaw, restartedRaw)
	}
	restartedReceipt, err := restartDomain.CompilationReceiptForExplorer(ctx, "project-a", "core-protocol", compiled.ReceiptId)
	if err != nil {
		t.Fatalf("fresh service lost the persisted receipt %s: %v", compiled.ReceiptId, err)
	}
	if restartedReceipt.ID != persistedReceipt.ID || restartedReceipt.IntentDigest != persistedReceipt.IntentDigest || restartedReceipt.RecipeDigest != persistedReceipt.RecipeDigest || restartedReceipt.OutputContractDigest != persistedReceipt.OutputContractDigest {
		t.Fatalf("fresh service receipt disagrees with the first read: got id=%q intentDigest=%q recipeDigest=%q contractDigest=%q want id=%q intentDigest=%q recipeDigest=%q contractDigest=%q", restartedReceipt.ID, restartedReceipt.IntentDigest, restartedReceipt.RecipeDigest, restartedReceipt.OutputContractDigest, persistedReceipt.ID, persistedReceipt.IntentDigest, persistedReceipt.RecipeDigest, persistedReceipt.OutputContractDigest)
	}
	restartedPrior, err := restartStore.GetDraftRevision(ctx, "project-a", "core-protocol", *ack.PreviousDraftRevisionId)
	if err != nil {
		t.Fatalf("fresh repository lost the acknowledged previous revision %s: %v", *ack.PreviousDraftRevisionId, err)
	}
	if restartedPrior.DraftVersion != 1 || restartedPrior.DraftDigest != digest {
		t.Fatalf("fresh service previous revision identity drifted: got version=%d digest=%q want version=1 digest=%q", restartedPrior.DraftVersion, restartedPrior.DraftDigest, digest)
	}

	// (10) Compare-and-swap truth on real Arango: replaying a save with the
	// pre-command identity must be refused at the store's transactional CAS, and
	// the committed document must be unchanged (no partial write, no phantom
	// second revision).
	stale, staleAck := fixture.saveLabel("core-arango-stale-save", "Patient Stale Ghost", 1, digest)
	if stale.StatusCode != http.StatusConflict || !strings.Contains(stale.Body, "DRAFT_CONFLICT") {
		t.Fatalf("stale save expected 409 DRAFT_CONFLICT, got status=%d body=%s ack=%+v", stale.StatusCode, stale.Body, staleAck)
	}
	unchanged, err := restartStore.Get(ctx, "project-a", "core-protocol")
	if err != nil {
		t.Fatalf("re-read head after refused stale save: %v", err)
	}
	if unchanged.DraftVersion != ack.DraftVersion || unchanged.DraftDigest != ack.DraftDigest {
		t.Fatalf("refused stale save moved the committed head: got version=%d digest=%q want version=%d digest=%q", unchanged.DraftVersion, unchanged.DraftDigest, ack.DraftVersion, ack.DraftDigest)
	}
	unchangedWorkspace, err := authoringv2.DecodeWorkspace(unchanged.DraftConfig)
	if err != nil {
		t.Fatalf("decode head after refused stale save: %v", err)
	}
	unchangedRaw, err := unchangedWorkspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(unchangedRaw, expectedRaw) {
		t.Fatalf("refused stale save partially overwrote the draft config\nexpected=%s\npersisted=%s", expectedRaw, unchangedRaw)
	}
	if strings.Contains(string(unchanged.DraftConfig), "Patient Stale Ghost") {
		t.Fatalf("refused stale save wrote ghost bytes into the persisted draft")
	}

	// (10) Independent-probe integrity: raw AQL counts through the second, exec-free
	// client prove exactly one explorer document and exactly one draft revision for
	// this identity — the rejected save and repeated compiles created no shadows.
	if count := countDocuments(ctx, t, restartClient, explorerarango.ExplorersCollection, "project-a", "core-protocol"); count != 1 {
		t.Fatalf("loom_explorers holds %d documents for project-a/core-protocol, want exactly 1", count)
	}
	if count := countDocuments(ctx, t, restartClient, explorerarango.DraftRevisionsCollection, "project-a", "core-protocol"); count != 1 {
		t.Fatalf("loom_explorer_draft_revisions holds %d documents for project-a/core-protocol, want exactly 1 (the captured pre-command head)", count)
	}
}

// digestForWorkspace is the committed-head digest the server itself reports for an
// expected post-command workspace.
func digestForWorkspace(t *testing.T, workspace authoringv2.Workspace) string {
	t.Helper()
	value, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// countDocuments runs an exec-free raw AQL count through the given client for one
// (project, explorerId) identity.
func countDocuments(ctx context.Context, t *testing.T, client *storearango.Client, collection, project, explorerID string) int64 {
	t.Helper()
	var count int64
	err := client.QueryRows(ctx, `FOR d IN @@c FILTER d.project == @project AND d.explorerId == @explorerId COLLECT WITH COUNT INTO total RETURN {total: total}`, 1, map[string]any{"@c": collection, "project": project, "explorerId": explorerID}, func(row map[string]any) error {
		switch value := row["total"].(type) {
		case float64:
			count = int64(value)
		case int64:
			count = value
		case json.Number:
			parsed, parseErr := value.Int64()
			if parseErr != nil {
				return parseErr
			}
			count = parsed
		default:
			return fmt.Errorf("unexpected count row type %T in %s", row["total"], collection)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("raw AQL count for %s: %v", collection, err)
	}
	return count
}
