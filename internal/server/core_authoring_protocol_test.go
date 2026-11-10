package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

type coreProtocolWireColumn struct {
	Label string `json:"label"`
}

type coreProtocolWireDocument struct {
	Output struct {
		ID string `json:"id"`
	} `json:"output"`
	Columns []coreProtocolWireColumn `json:"columns"`
}

type coreProtocolWireWorkspace struct {
	Documents []coreProtocolWireDocument `json:"documents"`
}

func coreProtocolDecodeWorkspace(t *testing.T, body, field string) coreProtocolWireWorkspace {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, body)
	}
	raw, ok := envelope[field]
	if !ok || len(raw) == 0 {
		t.Fatalf("missing %s in %s", field, body)
	}
	var workspace coreProtocolWireWorkspace
	if err := json.Unmarshal(raw, &workspace); err != nil {
		t.Fatalf("decode %s: %v", field, err)
	}
	return workspace
}

func coreProtocolWorkspaceLabels(workspace coreProtocolWireWorkspace, outputID string) []string {
	var labels []string
	for _, document := range workspace.Documents {
		if document.Output.ID != outputID {
			continue
		}
		for _, column := range document.Columns {
			labels = append(labels, column.Label)
		}
	}
	return labels
}

func coreProtocolExpectLabels(t *testing.T, body, field, outputID, want string) {
	t.Helper()
	labels := coreProtocolWorkspaceLabels(coreProtocolDecodeWorkspace(t, body, field), outputID)
	if len(labels) != 1 || labels[0] != want {
		t.Fatalf("workspace %s labels=%v want [%s]", field, labels, want)
	}
}

func coreProtocolDo(app *fiber.App, method, path, body, requestID string) (testHTTPResponse, error) {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if requestID != "" {
		request.Header.Set("X-Request-ID", requestID)
	}
	response, err := app.Test(request, fiber.TestConfig{Timeout: 15 * time.Second, FailOnTimeout: true})
	if err != nil {
		return testHTTPResponse{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return testHTTPResponse{}, err
	}
	return testHTTPResponse{StatusCode: response.StatusCode, Body: string(raw), Headers: response.Header.Clone()}, nil
}

func coreProtocolRequest(t *testing.T, app *fiber.App, method, path, body, requestID string) testHTTPResponse {
	t.Helper()
	response, err := coreProtocolDo(app, method, path, body, requestID)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

type coreProtocolFixture struct {
	t             *testing.T
	app           *fiber.App
	store         *testExplorerStore
	snapshot      capability.Snapshot
	workspace     authoringv2.Workspace
	initialDigest string
	basePath      string
	beforeCompile func()
}

// newCoreProtocolFixture mounts the production generated strict adapter with the
// real authoring handlers, lifecycle service, compile chain and persisted receipt
// store. CompileReceipt/PreviewReceipt are the repository's established test-owned
// service seams (mirroring the injected production implementations); the compile
// callback stays available for scheduling interleavings at that existing boundary.
func newCoreProtocolFixture(t *testing.T) *coreProtocolFixture {
	t.Helper()
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
	store := newTestExplorerStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
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
	fixture := &coreProtocolFixture{t: t, store: store, snapshot: snapshot, workspace: workspace, initialDigest: digest, basePath: "/api/v1/projects/project-a/explorers/core-protocol/authoring/v2"}
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
			if fixture.beforeCompile != nil {
				fixture.beforeCompile()
			}
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
	fixture.app = app
	return fixture
}

func (f *coreProtocolFixture) getBuilder() (testHTTPResponse, loomapi.BuilderState) {
	f.t.Helper()
	response := coreProtocolRequest(f.t, f.app, http.MethodGet, f.basePath+"/builder", "", "")
	var state loomapi.BuilderState
	if err := json.Unmarshal([]byte(response.Body), &state); err != nil {
		f.t.Fatalf("decode builder state status=%d body=%s: %v", response.StatusCode, response.Body, err)
	}
	return response, state
}

func (f *coreProtocolFixture) saveLabel(commandID, label string, expectedVersion int64, expectedDigest string) (testHTTPResponse, loomapi.ApplyCommandsResponse) {
	f.t.Helper()
	edited := f.workspace
	edited.Documents[0].Columns[0].Label = label
	columnValue, err := json.Marshal(edited.Documents[0].Columns[0])
	if err != nil {
		f.t.Fatal(err)
	}
	body := fmt.Sprintf(`{"commandId":%q,"semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"UPDATE_COLUMN","outputId":"patients","column":"c_patient","columnValue":%s}]}`,
		commandID, authoringv2.CurrentSemanticsVersion, f.snapshot.Token, expectedVersion, expectedDigest, columnValue)
	response := coreProtocolRequest(f.t, f.app, http.MethodPost, f.basePath+"/commands", body, "builder-command-"+commandID)
	var ack loomapi.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(response.Body), &ack); err != nil {
		f.t.Fatalf("decode commands status=%d body=%s: %v", response.StatusCode, response.Body, err)
	}
	return response, ack
}

func (f *coreProtocolFixture) reconcile(draftVersion int64, draftDigest, requestID string) (testHTTPResponse, error) {
	body := fmt.Sprintf(`{"snapshotToken":%q,"draftVersion":%d,"draftDigest":%q}`, f.snapshot.Token, draftVersion, draftDigest)
	return coreProtocolDo(f.app, http.MethodPost, f.basePath+"/reconcile", body, requestID)
}

func (f *coreProtocolFixture) MustReconcile(draftVersion int64, draftDigest, requestID string) testHTTPResponse {
	f.t.Helper()
	response, err := f.reconcile(draftVersion, draftDigest, requestID)
	if err != nil {
		f.t.Fatal(err)
	}
	return response
}

func (f *coreProtocolFixture) preview(receiptID string) testHTTPResponse {
	f.t.Helper()
	body := fmt.Sprintf(`{"receiptId":%q,"outputId":"patients","limit":10}`, receiptID)
	return coreProtocolRequest(f.t, f.app, http.MethodPost, f.basePath+"/preview", body, "")
}

func (f *coreProtocolFixture) decodeCompile(response testHTTPResponse) loomapi.CompileResponse {
	f.t.Helper()
	if response.StatusCode != http.StatusOK {
		f.t.Fatalf("reconcile status=%d body=%s", response.StatusCode, response.Body)
	}
	var compiled loomapi.CompileResponse
	if err := json.Unmarshal([]byte(response.Body), &compiled); err != nil {
		f.t.Fatalf("decode compile: %v body=%s", err, response.Body)
	}
	if compiled.ReceiptId == "" || len(compiled.Outputs) != 1 || compiled.Outputs[0].OutputId != "patients" {
		f.t.Fatalf("compile response outputs=%+v receipt=%q", compiled.Outputs, compiled.ReceiptId)
	}
	return compiled
}

func (f *coreProtocolFixture) receiptEmittedLabel(t *testing.T, receiptID, publicColumn string) string {
	t.Helper()
	receipt, ok := f.store.receipts[receiptID]
	if !ok {
		t.Fatalf("receipt %s not persisted", receiptID)
	}
	for _, column := range receipt.EmittedColumns {
		if column.OutputID == "patients" && column.PublicColumn == publicColumn {
			return column.Label
		}
	}
	t.Fatalf("receipt %s lacks emitted column %s: %+v", receiptID, publicColumn, receipt.EmittedColumns)
	return ""
}

// TestCoreAuthoringProtocolVerticalWitness is the first joined production witness
// (plan step 4 / M2): public Builder GET hydration, real POST commands save with
// CAS identity, public reconcile compile with receipt validation and persistence,
// and receipt-bound preview. Requests and identities are the ones the production
// UI client actually sends. Rows come from the controlled execution boundary;
// semantic row correctness is a separate milestone.
func TestCoreAuthoringProtocolVerticalWitness(t *testing.T) {
	fixture := newCoreProtocolFixture(t)

	// 1. Initial hydration through the production read handler.
	first, hydrated := fixture.getBuilder()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("builder status=%d body=%s", first.StatusCode, first.Body)
	}
	if hydrated.Catalog.SnapshotToken != fixture.snapshot.Token || hydrated.DraftVersion != 1 || hydrated.DraftDigest != fixture.initialDigest {
		t.Fatalf("builder identity mismatch: %+v", hydrated)
	}

	// 2. Real save with the identities the production UI client sends.
	saved, ack := fixture.saveLabel("core-witness-save-1", "Patient Identifier", hydrated.DraftVersion, hydrated.DraftDigest)
	if saved.StatusCode != http.StatusOK {
		t.Fatalf("commands status=%d body=%s", saved.StatusCode, saved.Body)
	}
	if ack.CommandId != "core-witness-save-1" {
		t.Fatalf("commands acknowledgement does not identify the request: %+v", ack)
	}
	if ack.DraftVersion != 2 || ack.DraftDigest == "" || ack.DraftDigest == fixture.initialDigest {
		t.Fatalf("commands acknowledgement does not identify a saved revision: %+v", ack)
	}
	wantRevision := "draft_revision_core-protocol_1"
	if ack.PreviousDraftRevisionId == nil || *ack.PreviousDraftRevisionId != wantRevision {
		t.Fatalf("acknowledgement previous revision=%v want %s", ack.PreviousDraftRevisionId, wantRevision)
	}
	coreProtocolExpectLabels(t, saved.Body, "workspace", "patients", "Patient Identifier")
	stored := fixture.store.explorers[testExplorerKey("project-a", "core-protocol")]
	if stored.DraftVersion != 2 || stored.DraftDigest != ack.DraftDigest {
		t.Fatalf("persisted head disagrees with acknowledgement: %+v", stored)
	}
	if !strings.Contains(string(stored.DraftConfig), "Patient Identifier") {
		t.Fatalf("persisted draft bytes missing saved label")
	}
	prior, err := fixture.store.GetDraftRevision(context.Background(), "project-a", "core-protocol", wantRevision)
	if err != nil || prior.DraftVersion != 1 || prior.DraftDigest != fixture.initialDigest || !strings.Contains(string(prior.DraftConfig), "Patient ID") {
		t.Fatalf("history revision did not capture prior head: %+v err=%v", prior, err)
	}

	// 3. Public reload agrees with the acknowledged revision (later coherent read).
	second, reloaded := fixture.getBuilder()
	if second.StatusCode != http.StatusOK || reloaded.DraftVersion != 2 || reloaded.DraftDigest != ack.DraftDigest || reloaded.PreviousDraftRevisionId == nil || *reloaded.PreviousDraftRevisionId != wantRevision {
		t.Fatalf("reload disagrees with acknowledgement: %+v", reloaded)
	}

	// 4. Reconciling the obsolete pre-save head is rejected at the CAS boundary.
	stale, err := fixture.reconcile(1, fixture.initialDigest, "builder-1-1")
	if err != nil {
		t.Fatal(err)
	}
	if stale.StatusCode != http.StatusConflict || !strings.Contains(stale.Body, "DRAFT_CONFLICT") {
		t.Fatalf("stale reconcile status=%d body=%s", stale.StatusCode, stale.Body)
	}

	// 5. Reconcile the saved head: real compile, validation and receipt persistence.
	response := fixture.MustReconcile(2, ack.DraftDigest, "builder-1-2")
	compiled := fixture.decodeCompile(response)
	coreProtocolExpectLabels(t, response.Body, "builder", "patients", "Patient Identifier")
	persistedReceipt, ok := fixture.store.receipts[compiled.ReceiptId]
	if !ok {
		t.Fatalf("receipt %s not persisted", compiled.ReceiptId)
	}
	if persistedReceipt.Project != "project-a" || persistedReceipt.ExplorerID != "core-protocol" || persistedReceipt.SnapshotToken != fixture.snapshot.Token {
		t.Fatalf("persisted receipt identity mismatch: %+v", persistedReceipt)
	}
	if label := fixture.receiptEmittedLabel(t, compiled.ReceiptId, "c_patient"); label != "Patient Identifier" {
		t.Fatalf("persisted receipt contract label=%q want Patient Identifier", label)
	}

	// 6. Receipt-bound preview keeps the accepted compilation identity.
	previewed := fixture.preview(compiled.ReceiptId)
	if previewed.StatusCode != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", previewed.StatusCode, previewed.Body)
	}
	var preview loomapi.PreviewResponse
	if err := json.Unmarshal([]byte(previewed.Body), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.ReceiptId != compiled.ReceiptId || preview.OutputId != "patients" || preview.Rows == nil {
		t.Fatalf("preview identity not bound to receipt: %+v", preview)
	}
	if !strings.Contains(previewed.Body, `"rows":[]`) {
		t.Fatalf("controlled execution must emit an empty array, not null: %s", previewed.Body)
	}
	previewLabels := make([]string, 0, len(preview.Columns))
	for _, column := range preview.Columns {
		previewLabels = append(previewLabels, column.Label)
	}
	if len(previewLabels) != 1 || previewLabels[0] != "Patient Identifier" {
		t.Fatalf("preview contract labels=%v", previewLabels)
	}

	// 7. A substituted receipt label cannot publish rows as success.
	foreign := coreProtocolRequest(t, fixture.app, http.MethodPost, fixture.basePath+"/preview", `{"receiptId":"core-witness-foreign-receipt","outputId":"patients","limit":10}`, "")
	if foreign.StatusCode == http.StatusOK || strings.Contains(foreign.Body, "Patient Identifier") {
		t.Fatalf("foreign receipt label unexpectedly admitted: status=%d body=%s", foreign.StatusCode, foreign.Body)
	}
}

// TestCoreAuthoringProtocolCompetingSaveDuringReconcile exercises one competing
// save/refetch schedule: reconcile of saved head A is paused inside the compile
// service seam, a concurrent commands request saves head B, then A's compile
// completes. Acceptance: A's receipt and returned workspace describe the captured
// CAS-validated A (not the later head), the public head is B, A's retained receipt
// still executes (immutable-receipt contract), and B independently compiles and
// previews. UI admission of A against B's current ownership is a separate mounted
// driver boundary; this witness owns the server-side contract observations.
func TestCoreAuthoringProtocolCompetingSaveDuringReconcile(t *testing.T) {
	fixture := newCoreProtocolFixture(t)
	first, hydrated := fixture.getBuilder()
	if first.StatusCode != http.StatusOK || hydrated.DraftVersion != 1 || hydrated.DraftDigest != fixture.initialDigest {
		t.Fatalf("initial hydration failed: %+v", hydrated)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fixture.beforeCompile = func() {
		once.Do(func() {
			close(entered)
			<-release
		})
	}
	competing := make(chan testHTTPResponse, 1)
	go func() {
		response, err := fixture.reconcile(1, fixture.initialDigest, "builder-1-1")
		if err != nil {
			t.Errorf("competing reconcile transport: %v", err)
			response = testHTTPResponse{StatusCode: -1}
		}
		competing <- response
	}()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("compile seam was not reached")
	}

	// Save B while A's compile is suspended after CAS validation.
	savedB, ackB := fixture.saveLabel("core-competing-save-b", "Patient Competing B", 1, fixture.initialDigest)
	if savedB.StatusCode != http.StatusOK {
		close(release)
		t.Fatalf("competing save status=%d body=%s", savedB.StatusCode, savedB.Body)
	}
	if ackB.DraftVersion != 2 || ackB.DraftDigest == "" || ackB.DraftDigest == fixture.initialDigest {
		close(release)
		t.Fatalf("competing save acknowledgement invalid: %+v", ackB)
	}
	close(release)

	response := <-competing
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reconcile of validated A must complete after the concurrent save; status=%d body=%s", response.StatusCode, response.Body)
	}
	compiled := fixture.decodeCompile(response)

	// The receipt and the returned workspace describe captured A, never B.
	coreProtocolExpectLabels(t, response.Body, "builder", "patients", "Patient ID")
	if label := fixture.receiptEmittedLabel(t, compiled.ReceiptId, "c_patient"); label != "Patient ID" {
		t.Fatalf("receipt from A compile described %q, not the captured saved head", label)
	}
	if strings.Contains(response.Body, "Patient Competing B") {
		t.Fatalf("reconcile response leaked the later head into the validated workspace: %s", response.Body)
	}

	// The authoritative head advanced to B; the later coherent read shows it.
	second, reloaded := fixture.getBuilder()
	if second.StatusCode != http.StatusOK || reloaded.DraftVersion != 2 || reloaded.DraftDigest != ackB.DraftDigest {
		t.Fatalf("head did not advance to the concurrent save: %+v", reloaded)
	}

	// A retained authorized historical receipt executes despite the newer head.
	previewA := fixture.preview(compiled.ReceiptId)
	if previewA.StatusCode != http.StatusOK {
		t.Fatalf("historical receipt execution rejected: status=%d body=%s", previewA.StatusCode, previewA.Body)
	}
	var previewResponse loomapi.PreviewResponse
	if err := json.Unmarshal([]byte(previewA.Body), &previewResponse); err != nil {
		t.Fatal(err)
	}
	if previewResponse.ReceiptId != compiled.ReceiptId || previewResponse.OutputId != "patients" {
		t.Fatalf("historical preview identity mismatch: %+v", previewResponse)
	}

	// The current head B compiles, previews and returns its own contract.
	responseB := fixture.MustReconcile(2, ackB.DraftDigest, "builder-1-2")
	compiledB := fixture.decodeCompile(responseB)
	if compiledB.ReceiptId == compiled.ReceiptId {
		t.Fatal("B reused A's receipt identity")
	}
	coreProtocolExpectLabels(t, responseB.Body, "builder", "patients", "Patient Competing B")
	if label := fixture.receiptEmittedLabel(t, compiledB.ReceiptId, "c_patient"); label != "Patient Competing B" {
		t.Fatalf("receipt B contract label=%q", label)
	}
	previewB := fixture.preview(compiledB.ReceiptId)
	if previewB.StatusCode != http.StatusOK || !strings.Contains(previewB.Body, `"rows":[]`) || !strings.Contains(previewB.Body, "Patient Competing B") {
		t.Fatalf("current preview B rejected or unbound: status=%d body=%s", previewB.StatusCode, previewB.Body)
	}

	// A's older receipt remains distinguishable by its frozen contract after B exists.
	if labelA := fixture.receiptEmittedLabel(t, compiled.ReceiptId, "c_patient"); labelA != "Patient ID" {
		t.Fatalf("frozen receipt A was mutated by the later head: %q", labelA)
	}
}

// coreProtocolSeedStoredDraft replaces the persisted draft head through the same
// CAS store write the production command path performs (testExplorerStore.SaveDraft,
// http_test_helpers_test.go:142) so malformed or poisoned persisted state is seeded
// without substituting any production service seam. It returns the accepted head.
func coreProtocolSeedStoredDraft(t *testing.T, fixture *coreProtocolFixture, raw []byte, digest string) (int64, string) {
	t.Helper()
	current, err := fixture.store.Get(context.Background(), "project-a", "core-protocol")
	if err != nil {
		t.Fatalf("load persisted head for seeding: %v", err)
	}
	seeded := *current
	seeded.DraftConfig = append([]byte(nil), raw...)
	seeded.DraftDigest = digest
	stored, err := fixture.store.SaveDraft(context.Background(), seeded, current.DraftVersion, current.DraftDigest)
	if err != nil {
		t.Fatalf("seed persisted draft: %v", err)
	}
	return stored.DraftVersion, stored.DraftDigest
}

// coreProtocolBoundaryFailure asserts the production strict route reported the typed
// boundary failure (status, error code, error stage) and published no successful
// compile/preview payload. Every authored error reaches this shape through
// authoringErrorForOpenAPI (openapi_explorer_authoring_direct.go:738), which recovers the
// lifecycle class/code/stage even when the preview transport wrapped it
// (previewRouteError unwraps via explorer.AuthoringError.Unwrap,
// explorer/authoring_errors.go:26).
func coreProtocolBoundaryFailure(t *testing.T, response testHTTPResponse, stage string, wantStatus int, wantCode, wantErrorStage, wantMessageContains string) {
	t.Helper()
	if response.StatusCode != wantStatus {
		t.Fatalf("%s status=%d want %d body=%s", stage, response.StatusCode, wantStatus, response.Body)
	}
	var envelope struct {
		Error struct {
			Code       string `json:"code"`
			Message    string `json:"message"`
			Diagnostic struct {
				Code  string `json:"code"`
				Stage string `json:"stage"`
			} `json:"diagnostic"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(response.Body), &envelope); err != nil {
		t.Fatalf("%s body is not an error envelope: %v body=%s", stage, err, response.Body)
	}
	if envelope.Error.Code != wantCode || envelope.Error.Diagnostic.Code != wantCode {
		t.Fatalf("%s error code=%q diagnostic code=%q want %q body=%s", stage, envelope.Error.Code, envelope.Error.Diagnostic.Code, wantCode, response.Body)
	}
	if envelope.Error.Diagnostic.Stage != wantErrorStage {
		t.Fatalf("%s error stage=%q want %q body=%s", stage, envelope.Error.Diagnostic.Stage, wantErrorStage, response.Body)
	}
	if wantMessageContains != "" && !strings.Contains(envelope.Error.Message, wantMessageContains) {
		t.Fatalf("%s error message=%q want it to preserve %q body=%s", stage, envelope.Error.Message, wantMessageContains, response.Body)
	}
	if strings.Contains(response.Body, `"receiptId"`) || strings.Contains(response.Body, `"outputs"`) {
		t.Fatalf("%s published a partial successful result: %s", stage, response.Body)
	}
}

// TestCoreAuthoringProtocolErrorBoundaries is the C18 error family
// (docs/verification/CORE_PROTOCOL_VERIFICATION_PLAN.md:335): malformed/incomplete
// decoded data, missing output/receipt, compile failure, unsupported receipt contract
// and invalid preview response must each be reported by the correct boundary as the
// intended typed failure, without publishing a partial successful result, while the
// relevant valid companion keeps working and retained prior state stays distinguishable
// from newly accepted state. Requests go only through the production generated strict
// adapter, handlers, lifecycle service, compile chain and persisted receipt store; the
// only test-side action is persisted-state seeding through the exposed store handle.
func TestCoreAuthoringProtocolErrorBoundaries(t *testing.T) {
	// Malformed/incomplete decoded data at the reconcile boundary.
	// ReconcileWithWorkspace decodes the persisted draft after CAS
	// (authoring.go:559-561) and reports conflict AUTHORING_STATE_MISSING, so a draft
	// that fails workspace decoding is a 409 typed failure, never a partial compile.
	// The same undecodable head is refused by hydration as DRAFT_STATE_INVALID
	// (query.go:127-130) rather than a partially hydrated Builder state.
	t.Run("malformed-stored-draft", func(t *testing.T) {
		fixture := newCoreProtocolFixture(t)
		if _, hydrated := fixture.getBuilder(); hydrated.DraftVersion != 1 || hydrated.DraftDigest != fixture.initialDigest {
			t.Fatalf("baseline hydration identity mismatch: %+v", hydrated)
		}
		receiptsBefore := len(fixture.store.receipts)

		// (a) Truncated JSON: the persisted bytes cannot be decoded at all.
		truncated := []byte(`{"apiVersion":"explorer.calypr.org/authoringv2"`)
		if _, err := authoringv2.DecodeWorkspace(truncated); err == nil {
			t.Fatal("truncated seed unexpectedly decodes as a workspace")
		}
		truncatedVersion, truncatedDigest := coreProtocolSeedStoredDraft(t, fixture, truncated, fixture.initialDigest)
		response, err := fixture.reconcile(truncatedVersion, truncatedDigest, "errors-malformed-truncated")
		if err != nil {
			t.Fatal(err)
		}
		coreProtocolBoundaryFailure(t, response, "reconcile of truncated stored draft", http.StatusConflict, "AUTHORING_STATE_MISSING", "reconcile", "")
		hydrated, _ := fixture.getBuilder()
		coreProtocolBoundaryFailure(t, hydrated, "builder hydration of truncated stored draft", http.StatusConflict, "DRAFT_STATE_INVALID", "builder", "")

		// (b) Complete JSON that violates the persisted-workspace contract: the same
		// canonical bytes with a blank explorer.title, which the persisted decoder rejects
		// through authoringv2.Workspace.Validate ("explorer.title is required")
		// (canonical.go:343-351). The seeded bytes are re-checked against the very decoder
		// the handler uses, so this case cannot silently degrade into valid input.
		raw, err := fixture.workspace.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		var envelope map[string]any
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("decode canonical baseline: %v", err)
		}
		metadata, ok := envelope["explorer"].(map[string]any)
		if !ok {
			t.Fatalf("canonical baseline has no explorer metadata: %s", raw)
		}
		metadata["title"] = ""
		incompleteRaw, err := json.Marshal(envelope)
		if err != nil {
			t.Fatalf("encode untitled variant: %v", err)
		}
		if _, err := authoringv2.DecodeWorkspace(incompleteRaw); err == nil {
			t.Fatalf("seeded variant unexpectedly still decodes: %s", incompleteRaw)
		}
		incompleteVersion, incompleteDigest := coreProtocolSeedStoredDraft(t, fixture, incompleteRaw, fixture.initialDigest)
		response, err = fixture.reconcile(incompleteVersion, incompleteDigest, "errors-malformed-incomplete")
		if err != nil {
			t.Fatal(err)
		}
		coreProtocolBoundaryFailure(t, response, "reconcile of contract-invalid stored draft", http.StatusConflict, "AUTHORING_STATE_MISSING", "reconcile", "")
		if len(fixture.store.receipts) != receiptsBefore {
			t.Fatalf("rejected malformed drafts persisted receipts: %d new", len(fixture.store.receipts)-receiptsBefore)
		}

		// Valid companion: the same route accepts and compiles restored valid state.
		restoredVersion, restoredDigest := coreProtocolSeedStoredDraft(t, fixture, raw, fixture.initialDigest)
		saved, ack := fixture.saveLabel("core-errors-malformed-companion", "Patient Identifier", restoredVersion, restoredDigest)
		if saved.StatusCode != http.StatusOK || ack.DraftVersion != restoredVersion+1 {
			t.Fatalf("valid companion save rejected: status=%d ack=%+v body=%s", saved.StatusCode, ack, saved.Body)
		}
		compiledResponse := fixture.MustReconcile(ack.DraftVersion, ack.DraftDigest, "errors-malformed-companion-reconcile")
		compiled := fixture.decodeCompile(compiledResponse)
		coreProtocolExpectLabels(t, compiledResponse.Body, "builder", "patients", "Patient Identifier")
		previewed := fixture.preview(compiled.ReceiptId)
		if previewed.StatusCode != http.StatusOK || !strings.Contains(previewed.Body, `"rows":[]`) || !strings.Contains(previewed.Body, "Patient Identifier") {
			t.Fatalf("valid companion preview rejected: status=%d body=%s", previewed.StatusCode, previewed.Body)
		}
	})

	// Missing receipt at the preview boundary. Preview looks the receipt up through the
	// configured lookup (preview.go:28) which is domain.CompilationReceiptForExplorer for
	// this fixture; a never-issued id yields explorer.ErrNotFound, wrapped as
	// ClassNotFound COMPILE_RECEIPT_NOT_FOUND (lifecycle/errors.go:89-94) and mapped to 404
	// by lifecycleErrorStatus (openapi_explorer_authoring_direct.go:810).
	t.Run("missing-receipt-preview", func(t *testing.T) {
		fixture := newCoreProtocolFixture(t)
		compiled := fixture.decodeCompile(fixture.MustReconcile(1, fixture.initialDigest, "errors-missing-receipt-baseline"))
		if _, ok := fixture.store.receipts[compiled.ReceiptId]; !ok {
			t.Fatalf("baseline receipt %s not persisted", compiled.ReceiptId)
		}
		receiptsBefore, headBefore := len(fixture.store.receipts), coreProtocolStoredHeadVersion(t, fixture)

		unknown := coreProtocolRequest(t, fixture.app, http.MethodPost, fixture.basePath+"/preview", `{"receiptId":"core-protocol-never-issued-receipt","outputId":"patients","limit":10}`, "")
		coreProtocolBoundaryFailure(t, unknown, "preview of never-issued receipt", http.StatusNotFound, "COMPILE_RECEIPT_NOT_FOUND", "receipt", "")
		if strings.Contains(unknown.Body, `"rows"`) {
			t.Fatalf("missing receipt preview published rows: %s", unknown.Body)
		}
		if len(fixture.store.receipts) != receiptsBefore {
			t.Fatalf("missing receipt preview persisted receipts: %d new", len(fixture.store.receipts)-receiptsBefore)
		}
		if headBefore != coreProtocolStoredHeadVersion(t, fixture) {
			t.Fatalf("missing receipt preview moved the saved head")
		}

		// Valid companion: the real receipt still previews.
		valid := fixture.preview(compiled.ReceiptId)
		if valid.StatusCode != http.StatusOK || !strings.Contains(valid.Body, `"rows":[]`) {
			t.Fatalf("valid companion preview rejected: status=%d body=%s", valid.StatusCode, valid.Body)
		}
	})

	// Compile failure at the reconcile/compile boundary, driven by input the real engine
	// rejects rather than by a substituted seam: the saved column source addresses a field
	// for which the resolved capability snapshot has no candidate, so the document fails
	// with the compiler's typed intent STALE_FIELD (semantic_compile.go:124-127), which
	// CompileWorkspace reports as compile DOCUMENT_COMPILE_FAILED carrying that cause and
	// JSON path (compile.go:184); lifecycle then maps *explorercompilation.Error to
	// ClassUnprocessable with compilationErrorCode (authoring.go:410-415) → 422
	// (openapi_explorer_authoring_direct.go:814). The fixture hook surface (beforeCompile,
	// core_authoring_protocol_test.go:173-178) only observes the existing compile seam and
	// cannot inject a compile error; the repository's existing tests drive it purely for
	// scheduling interleavings (core_authoring_protocol_formal_replay_test.go:87-94), so it
	// is deliberately not reused or widened here.
	t.Run("compile-failure", func(t *testing.T) {
		fixture := newCoreProtocolFixture(t)
		retained := fixture.decodeCompile(fixture.MustReconcile(1, fixture.initialDigest, "errors-compile-retained"))
		retainedPreview := fixture.preview(retained.ReceiptId)
		if retainedPreview.StatusCode != http.StatusOK {
			t.Fatalf("retained receipt preview rejected: status=%d body=%s", retainedPreview.StatusCode, retainedPreview.Body)
		}
		receiptsBefore := len(fixture.store.receipts)

		baseline, err := fixture.workspace.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		variant, err := authoringv2.DecodeWorkspace(baseline)
		if err != nil {
			t.Fatalf("decode compile-failure variant base: %v", err)
		}
		variant.Documents[0].Columns[0].Source.Field.Path = "uncompilableGhostField"
		variantRaw, err := variant.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		// The rejection must come from the compiler, not from an undecodable draft.
		if _, err := authoringv2.DecodeWorkspace(variantRaw); err != nil {
			t.Fatalf("compiler-rejection variant must stay a decodable draft: %v", err)
		}
		variantDigest, err := variant.Digest()
		if err != nil {
			t.Fatal(err)
		}
		rejectedVersion, rejectedDigest := coreProtocolSeedStoredDraft(t, fixture, variantRaw, variantDigest)

		response, err := fixture.reconcile(rejectedVersion, rejectedDigest, "errors-compile-rejected")
		if err != nil {
			t.Fatal(err)
		}
		coreProtocolBoundaryFailure(t, response, "reconcile of compiler-rejected saved draft", http.StatusUnprocessableEntity, "DOCUMENT_COMPILE_FAILED", "compile", "STALE_FIELD")

		// No receipt published for the rejected attempt, retained state is untouched.
		if len(fixture.store.receipts) != receiptsBefore {
			t.Fatalf("failed compile persisted %d new receipts", len(fixture.store.receipts)-receiptsBefore)
		}
		if _, ok := fixture.store.receipts[retained.ReceiptId]; !ok {
			t.Fatalf("retained receipt %s was not still persisted", retained.ReceiptId)
		}
		if label := fixture.receiptEmittedLabel(t, retained.ReceiptId, "c_patient"); label != "Patient ID" {
			t.Fatalf("retained receipt contract changed to %q", label)
		}
		retainedAfter := fixture.preview(retained.ReceiptId)
		if retainedAfter.StatusCode != http.StatusOK || !strings.Contains(retainedAfter.Body, `"rows":[]`) {
			t.Fatalf("retained receipt no longer previews after a failed compile: status=%d body=%s", retainedAfter.StatusCode, retainedAfter.Body)
		}
		// Retained vs newly accepted: the head carries the rejected draft identity while
		// the only persisted receipt still describes the earlier accepted compilation.
		if _, head := fixture.getBuilder(); head.DraftVersion != rejectedVersion || head.DraftDigest != rejectedDigest {
			t.Fatalf("rejected draft did not remain the described head: %+v", head)
		}

		// Valid companion. Restoring the identical draft first shows the receipt store's
		// content-addressed identity (the same compilation resolves to the same persisted
		// receipt), so the newly accepted state is demonstrated with a real saved edit: a
		// distinct receipt is published while the retained earlier receipt keeps its frozen
		// contract and keeps previewing.
		restoredVersion, restoredDigest := coreProtocolSeedStoredDraft(t, fixture, baseline, fixture.initialDigest)
		restoredCompile := fixture.decodeCompile(fixture.MustReconcile(restoredVersion, restoredDigest, "errors-compile-companion-restore"))
		if restoredCompile.ReceiptId != retained.ReceiptId {
			t.Fatalf("recompiling identical content changed the receipt identity: %s want %s", restoredCompile.ReceiptId, retained.ReceiptId)
		}
		saved, ack := fixture.saveLabel("core-errors-compile-companion", "Patient Compile Companion", restoredVersion, restoredDigest)
		if saved.StatusCode != http.StatusOK || ack.DraftVersion != restoredVersion+1 {
			t.Fatalf("companion save rejected: status=%d ack=%+v body=%s", saved.StatusCode, ack, saved.Body)
		}
		compiled := fixture.decodeCompile(fixture.MustReconcile(ack.DraftVersion, ack.DraftDigest, "errors-compile-companion"))
		if compiled.ReceiptId == retained.ReceiptId {
			t.Fatal("newly accepted compilation reused the retained receipt identity")
		}
		if _, ok := fixture.store.receipts[compiled.ReceiptId]; !ok {
			t.Fatalf("companion receipt %s not persisted", compiled.ReceiptId)
		}
		if label := fixture.receiptEmittedLabel(t, compiled.ReceiptId, "c_patient"); label != "Patient Compile Companion" {
			t.Fatalf("newly accepted receipt contract label=%q", label)
		}
		if previewed := fixture.preview(compiled.ReceiptId); previewed.StatusCode != http.StatusOK || !strings.Contains(previewed.Body, "Patient Compile Companion") {
			t.Fatalf("companion preview rejected: status=%d body=%s", previewed.StatusCode, previewed.Body)
		}
		if len(fixture.store.receipts) != receiptsBefore+1 {
			t.Fatalf("companion published %d receipts, want exactly the one new acceptance", len(fixture.store.receipts)-receiptsBefore)
		}
		if retainedStill := fixture.preview(retained.ReceiptId); retainedStill.StatusCode != http.StatusOK || !strings.Contains(retainedStill.Body, "Patient ID") {
			t.Fatalf("retained receipt stopped describing its own frozen contract: status=%d body=%s", retainedStill.StatusCode, retainedStill.Body)
		}
	})

	// Unsupported receipt contract at the preview boundary. The fixture wires
	// ReceiptLookup straight to domain.CompilationReceiptForExplorer
	// (core_authoring_protocol_test.go:179-181) and exposes no lookup hook, so this
	// variant is driven only by persisted receipt state through the production lookup
	// chain (service.go:357-359 → store.GetCompilationReceiptForExplorer) and the
	// contract branches of validateReceiptRoute (policy.go:125-142): a receipt whose
	// compiler contract is not executable is conflict RECEIPT_RECOMPILE_REQUIRED, and so
	// is an incomplete receipt identity. Both are 409 via lifecycleErrorStatus.
	// Boundaries recorded honestly instead of faked: a receipt with a foreign project or
	// Explorer is rejected before any contract branch, at the lookup, as
	// COMPILE_RECEIPT_NOT_FOUND (http_test_helpers_test.go:200-206) — that rejection is
	// T1's coverage and is not re-asserted here; and the public-output-contract decode
	// branch of validateReceiptOutputContract (policy.go:206-225) collapses into the same
	// typed UNKNOWN_AUTHORING_OUTPUT as recipe membership, asserted in
	// invalid-preview-response.
	t.Run("unsupported-receipt-contract", func(t *testing.T) {
		fixture := newCoreProtocolFixture(t)
		compiled := fixture.decodeCompile(fixture.MustReconcile(1, fixture.initialDigest, "errors-contract-baseline"))
		genuine, ok := fixture.store.receipts[compiled.ReceiptId]
		if !ok {
			t.Fatalf("baseline receipt %s not persisted", compiled.ReceiptId)
		}
		if previewed := fixture.preview(compiled.ReceiptId); previewed.StatusCode != http.StatusOK {
			t.Fatalf("baseline preview rejected: status=%d body=%s", previewed.StatusCode, previewed.Body)
		}
		receiptsBefore := len(fixture.store.receipts)

		// (a) Historical compiler contract: readable, but not executable natively.
		// Constants mirror the unexported legacy identities in
		// internal/explorer/compilation_receipt.go:27,36.
		unsupported := genuine
		unsupported.ID = "core-protocol-unsupported-contract"
		unsupported.ReceiptFormatVersion = 3
		unsupported.CompilerContractVersion = "loom.explorer.compiler/v12"
		if _, err := fixture.store.InsertCompilationReceipt(context.Background(), unsupported); err != nil {
			t.Fatalf("seed unsupported-contract receipt: %v", err)
		}
		response := coreProtocolRequest(t, fixture.app, http.MethodPost, fixture.basePath+"/preview", `{"receiptId":"core-protocol-unsupported-contract","outputId":"patients","limit":10}`, "")
		coreProtocolBoundaryFailure(t, response, "preview of unsupported-contract receipt", http.StatusConflict, "RECEIPT_RECOMPILE_REQUIRED", "receipt", "")
		if strings.Contains(response.Body, `"rows"`) || strings.Contains(response.Body, "Patient ID") {
			t.Fatalf("unsupported-contract receipt executed: %s", response.Body)
		}

		// (b) Incomplete receipt identity (no recipe digest / no compiled outputs).
		incomplete := genuine
		incomplete.ID = "core-protocol-incomplete-contract"
		incomplete.RecipeDigest = ""
		incomplete.Bundle.Outputs = nil
		if _, err := fixture.store.InsertCompilationReceipt(context.Background(), incomplete); err != nil {
			t.Fatalf("seed incomplete-contract receipt: %v", err)
		}
		response = coreProtocolRequest(t, fixture.app, http.MethodPost, fixture.basePath+"/preview", `{"receiptId":"core-protocol-incomplete-contract","outputId":"patients","limit":10}`, "")
		coreProtocolBoundaryFailure(t, response, "preview of incomplete receipt identity", http.StatusConflict, "RECEIPT_RECOMPILE_REQUIRED", "receipt", "")
		if len(fixture.store.receipts) != receiptsBefore+2 {
			t.Fatalf("preview failures changed persisted receipts: before=%d now=%d", receiptsBefore, len(fixture.store.receipts))
		}

		// Valid companion: the genuine current-contract receipt is unaffected.
		valid := fixture.preview(compiled.ReceiptId)
		if valid.StatusCode != http.StatusOK || !strings.Contains(valid.Body, `"rows":[]`) || !strings.Contains(valid.Body, "Patient ID") {
			t.Fatalf("genuine receipt preview rejected: status=%d body=%s", valid.StatusCode, valid.Body)
		}
	})

	// Invalid preview request/response at the preview boundary. Missing or blank
	// identities are the malformed class of Preview itself, which trims them away
	// (preview.go:13-15 → MALFORMED_REQUEST → 400), and an output the receipt never
	// emitted is unprocessable UNKNOWN_AUTHORING_OUTPUT (preview.go:42-44). Neither can
	// publish state. Note a nil body (openapi_explorer_authoring_direct.go:501-503) is
	// unreachable through the generated strict adapter, which always binds a JSON object
	// into the request struct (api.gen.go:20945-20949), so MALFORMED_AUTHORING_REQUEST is
	// not exercisable from this route and is recorded as a boundary, not faked.
	t.Run("invalid-preview-response", func(t *testing.T) {
		fixture := newCoreProtocolFixture(t)
		compiled := fixture.decodeCompile(fixture.MustReconcile(1, fixture.initialDigest, "errors-preview-baseline"))
		if previewed := fixture.preview(compiled.ReceiptId); previewed.StatusCode != http.StatusOK {
			t.Fatalf("baseline preview rejected: status=%d body=%s", previewed.StatusCode, previewed.Body)
		}
		receiptsBefore, headBefore := len(fixture.store.receipts), coreProtocolStoredHeadVersion(t, fixture)

		for _, request := range []struct {
			name string
			body string
		}{
			{"missing-receipt-id", `{"outputId":"patients","limit":10}`},
			{"missing-output-id", fmt.Sprintf(`{"receiptId":%q,"limit":10}`, compiled.ReceiptId)},
			{"blank-identities", `{"receiptId":" ","outputId":"  ","limit":10}`},
		} {
			response := coreProtocolRequest(t, fixture.app, http.MethodPost, fixture.basePath+"/preview", request.body, "")
			coreProtocolBoundaryFailure(t, response, "preview with "+request.name, http.StatusBadRequest, "MALFORMED_REQUEST", "preview", "")
			if strings.Contains(response.Body, `"rows"`) {
				t.Fatalf("preview with %s published rows: %s", request.name, response.Body)
			}
		}

		// An output the receipt never emitted, whether absent from the recipe or outside
		// its public contract, is one typed rejection at the same boundary.
		response := coreProtocolRequest(t, fixture.app, http.MethodPost, fixture.basePath+"/preview", fmt.Sprintf(`{"receiptId":%q,"outputId":"never-emitted-output","limit":10}`, compiled.ReceiptId), "")
		coreProtocolBoundaryFailure(t, response, "preview of output absent from the receipt", http.StatusUnprocessableEntity, "UNKNOWN_AUTHORING_OUTPUT", "preview", "")

		if len(fixture.store.receipts) != receiptsBefore {
			t.Fatalf("invalid preview requests persisted receipts: %d new", len(fixture.store.receipts)-receiptsBefore)
		}
		if headBefore != coreProtocolStoredHeadVersion(t, fixture) {
			t.Fatalf("invalid preview requests moved the saved head")
		}

		// Valid companion: the well-formed receipt-bound request still succeeds.
		valid := fixture.preview(compiled.ReceiptId)
		var preview loomapi.PreviewResponse
		if err := json.Unmarshal([]byte(valid.Body), &preview); err != nil {
			t.Fatal(err)
		}
		if valid.StatusCode != http.StatusOK || preview.ReceiptId != compiled.ReceiptId || preview.OutputId != "patients" || preview.Rows == nil {
			t.Fatalf("valid companion preview rejected: status=%d body=%s", valid.StatusCode, valid.Body)
		}
	})
}

func coreProtocolStoredHeadVersion(t *testing.T, fixture *coreProtocolFixture) int64 {
	t.Helper()
	current, err := fixture.store.Get(context.Background(), "project-a", "core-protocol")
	if err != nil {
		t.Fatalf("read persisted head: %v", err)
	}
	return current.DraftVersion
}
