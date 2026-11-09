package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"net/http"
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

// This file closes the SERVER half of plan row C15
// (docs/verification/CORE_PROTOCOL_VERIFICATION_PLAN.md:384): restore a previous
// revision while a command, reconcile, recovery GET or preview is pending, then
// complete the old work. Every claim is read back from the real contracts:
//
//   - restore is an ordinary command through the same compare-and-swap boundary as
//     any edit (lifecycle/draft_restore.go:26-46 owner/Undo-target checks, then
//     explorer/service.go:324 SaveDraft),
//   - reconcile admits on the head identity read at entry and afterwards compiles
//     only that captured workspace (lifecycle/authoring.go:552-568),
//   - preview admits on the retained receipt and never requires head equality
//     (lifecycle/preview.go:28-44),
//   - hydration reads version/digest/workspace from one persisted-state snapshot
//     (lifecycle/query.go:101-135).
//
// Client-side completion ordering (the mounted-driver half of C15) is out of scope.

const (
	coreProtocolRestoreOriginalLabel  = "Patient ID"
	coreProtocolRestoreSavedLabel     = "Patient Head A"
	coreProtocolRestoreCompetingLabel = "Patient Competing Save"
	coreProtocolRestoreObsoleteLabel  = "Patient Obsolete Intent"
	coreProtocolRestoreFollowUpLabel  = "Patient Follow-Up After Restore"
)

// coreProtocolRevisionID mirrors the server-owned history identity the persisted
// store mints on every accepted draft CAS (http_test_helpers_test.go:156).
func coreProtocolRevisionID(explorerID string, draftVersion int64) string {
	return fmt.Sprintf("draft_revision_%s_%d", explorerID, draftVersion)
}

func coreProtocolRestoreBody(f *coreProtocolFixture, commandID, revisionID string, expectedVersion int64, expectedDigest string) string {
	return fmt.Sprintf(`{"commandId":%q,"semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"RESTORE_DRAFT_REVISION","draftRevisionId":%q}]}`,
		commandID, authoringv2.CurrentSemanticsVersion, f.snapshot.Token, expectedVersion, expectedDigest, revisionID)
}

func coreProtocolSaveLabelBody(f *coreProtocolFixture, commandID, label string, expectedVersion int64, expectedDigest string) (string, error) {
	edited := f.workspace
	edited.Documents[0].Columns[0].Label = label
	columnValue, err := json.Marshal(edited.Documents[0].Columns[0])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"commandId":%q,"semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"UPDATE_COLUMN","outputId":"patients","column":"c_patient","columnValue":%s}]}`,
		commandID, authoringv2.CurrentSemanticsVersion, f.snapshot.Token, expectedVersion, expectedDigest, columnValue), nil
}

func coreProtocolDecodeAck(t *testing.T, response testHTTPResponse) authoringv2.ApplyCommandsResponse {
	t.Helper()
	var ack authoringv2.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(response.Body), &ack); err != nil {
		t.Fatalf("decode commands acknowledgement status=%d body=%s: %v", response.StatusCode, response.Body, err)
	}
	return ack
}

// restoreRevision posts the production RESTORE_DRAFT_REVISION command with the exact
// CAS identity the Builder client sends. The acknowledgement is decoded leniently so
// a rejected attempt can still be asserted through its typed error envelope.
func (f *coreProtocolFixture) restoreRevision(commandID, revisionID string, expectedVersion int64, expectedDigest string) (testHTTPResponse, authoringv2.ApplyCommandsResponse) {
	f.t.Helper()
	response := coreProtocolRequest(f.t, f.app, http.MethodPost, f.basePath+"/commands", coreProtocolRestoreBody(f, commandID, revisionID, expectedVersion, expectedDigest), "builder-command-"+commandID)
	return response, coreProtocolDecodeAck(f.t, response)
}

// restoreAttempt is restoreRevision without the t.Fatal on transport failure, for
// calls that must run suspended in a goroutine.
func (f *coreProtocolFixture) restoreAttempt(commandID, revisionID string, expectedVersion int64, expectedDigest string) (testHTTPResponse, authoringv2.ApplyCommandsResponse, error) {
	response, err := coreProtocolDo(f.app, http.MethodPost, f.basePath+"/commands", coreProtocolRestoreBody(f, commandID, revisionID, expectedVersion, expectedDigest), "builder-command-"+commandID)
	if err != nil || response.StatusCode != http.StatusOK {
		return response, authoringv2.ApplyCommandsResponse{}, err
	}
	return response, coreProtocolDecodeAck(f.t, response), err
}

// saveLabelAttempt is fixture.saveLabel's shape for goroutine-suspended calls.
func (f *coreProtocolFixture) saveLabelAttempt(commandID, label string, expectedVersion int64, expectedDigest string) (testHTTPResponse, authoringv2.ApplyCommandsResponse, error) {
	body, err := coreProtocolSaveLabelBody(f, commandID, label, expectedVersion, expectedDigest)
	if err != nil {
		return testHTTPResponse{}, authoringv2.ApplyCommandsResponse{}, err
	}
	response, err := coreProtocolDo(f.app, http.MethodPost, f.basePath+"/commands", body, "builder-command-"+commandID)
	if err != nil || response.StatusCode != http.StatusOK {
		return response, authoringv2.ApplyCommandsResponse{}, err
	}
	return response, coreProtocolDecodeAck(f.t, response), nil
}

// MustRestoreRevision restores the hydrated head's server-resolved Undo target and
// returns the accepted head identity.
func (f *coreProtocolFixture) MustRestoreRevision(commandID string, hydrated loomapi.BuilderState) (int64, string, string) {
	f.t.Helper()
	if hydrated.PreviousDraftRevisionId == nil || *hydrated.PreviousDraftRevisionId == "" {
		f.t.Fatalf("hydrated head exposes no Undo target: %+v", hydrated)
	}
	revisionID := *hydrated.PreviousDraftRevisionId
	response, ack := f.restoreRevision(commandID, revisionID, hydrated.DraftVersion, hydrated.DraftDigest)
	if response.StatusCode != http.StatusOK {
		f.t.Fatalf("restore of %s status=%d body=%s", revisionID, response.StatusCode, response.Body)
	}
	if len(ack.Results) != 1 || ack.Results[0].Type != authoringv2.CommandResultDraftRestored {
		f.t.Fatalf("restore did not report one DRAFT_RESTORED result: %+v", ack.Results)
	}
	return ack.DraftVersion, ack.DraftDigest, revisionID
}

// previewPending is fixture.preview's transport without the t.Fatal on transport
// error, so a preview can be suspended in a goroutine at the preview seam.
func (f *coreProtocolFixture) previewPending(receiptID string) (testHTTPResponse, error) {
	body := fmt.Sprintf(`{"receiptId":%q,"outputId":"patients","limit":10}`, receiptID)
	return coreProtocolDo(f.app, http.MethodPost, f.basePath+"/preview", body, "")
}

// coreProtocolSeedRestorableHead saves one accepted edit onto the fixture's first head
// and returns that saved head A's identity plus its server-resolved Undo target.
func coreProtocolSeedRestorableHead(t *testing.T, fixture *coreProtocolFixture, commandID, label string) (int64, string, string) {
	t.Helper()
	_, hydrated := fixture.getBuilder()
	if hydrated.DraftVersion != 1 || hydrated.DraftDigest != fixture.initialDigest {
		t.Fatalf("baseline hydration identity mismatch: %+v", hydrated)
	}
	saved, ack := fixture.saveLabel(commandID, label, hydrated.DraftVersion, hydrated.DraftDigest)
	if saved.StatusCode != http.StatusOK {
		t.Fatalf("seed save status=%d body=%s", saved.StatusCode, saved.Body)
	}
	if ack.PreviousDraftRevisionId == nil || *ack.PreviousDraftRevisionId == "" {
		t.Fatalf("seed save exposed no Undo target: %+v", ack)
	}
	if got := ack.Workspace.Documents[0].Columns[0].Label; got != label {
		t.Fatalf("seed save acknowledged label %q want %q", got, label)
	}
	return ack.DraftVersion, ack.DraftDigest, *ack.PreviousDraftRevisionId
}

// coreProtocolSeedDraftRevisionRecord persists a well-formed history record through the
// exposed store handle (the same seeding surface openapi_explorer_draft_restore_test.go
// uses) with the current head's source context, so a browser-supplied draftRevisionId
// that passes every identity/adjacency check but is NOT the head's recorded Undo target
// tests the Undo-ownership guard on its own.
func coreProtocolSeedDraftRevisionRecord(t *testing.T, fixture *coreProtocolFixture, revisionID string, draftVersion int64, raw []byte, digest string) {
	t.Helper()
	head, err := fixture.store.Get(context.Background(), "project-a", "core-protocol")
	if err != nil || head == nil {
		t.Fatalf("read head for revision seeding: %v", err)
	}
	fixture.store.mu.Lock()
	defer fixture.store.mu.Unlock()
	fixture.store.draftRevisions[testExplorerKey("project-a", "core-protocol")+"\x00"+revisionID] = explorer.DraftRevision{
		ID: revisionID, Project: head.Project, ExplorerID: head.ExplorerID, DraftVersion: draftVersion, DraftDigest: digest,
		DraftConfig: append([]byte(nil), raw...), Title: head.Title,
		SnapshotToken: head.DraftSnapshotToken, SourceGeneration: head.DraftSourceGeneration,
		AuthorizationScopeDigest: head.DraftAuthorizationScopeDigest, UpdatedBy: head.UpdatedBy, UpdatedAt: head.UpdatedAt,
	}
}

func coreProtocolStoredHead(t *testing.T, fixture *coreProtocolFixture) explorer.Explorer {
	t.Helper()
	current, err := fixture.store.Get(context.Background(), "project-a", "core-protocol")
	if err != nil {
		t.Fatalf("read persisted head: %v", err)
	}
	return *current
}

// coreProtocolExpectHead proves the persisted head still is the expected revision; it
// is how each case shows the completed or rejected stale work moved nothing.
func coreProtocolExpectHead(t *testing.T, fixture *coreProtocolFixture, wantVersion int64, wantDigest, stage string) explorer.Explorer {
	t.Helper()
	stored := coreProtocolStoredHead(t, fixture)
	if stored.DraftVersion != wantVersion || stored.DraftDigest != wantDigest {
		t.Fatalf("%s changed the persisted head: got version=%d digest=%q want version=%d digest=%q", stage, stored.DraftVersion, stored.DraftDigest, wantVersion, wantDigest)
	}
	return stored
}

func coreProtocolAwait(t *testing.T, opened <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-opened:
	case <-time.After(15 * time.Second):
		t.Fatalf("%s never happened", what)
	}
}

// coreProtocolDecodeHydratedState decodes the public Builder hydration envelope and
// re-derives the workspace digest, which is what lets the recovery-GET cases prove the
// announced version/digest pair belongs to the state actually returned.
func coreProtocolDecodeHydratedState(t *testing.T, response testHTTPResponse, wantVersion int64, wantDigest, wantUndoTarget, wantLabel, stage string) {
	t.Helper()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", stage, response.StatusCode, response.Body)
	}
	var envelope struct {
		loomapi.BuilderState
		Workspace *authoringv2.Workspace `json:"workspace"`
	}
	if err := json.Unmarshal([]byte(response.Body), &envelope); err != nil {
		t.Fatalf("%s decode hydration envelope: %v body=%s", stage, err, response.Body)
	}
	if envelope.Workspace == nil {
		t.Fatalf("%s returned no workspace: %s", stage, response.Body)
	}
	if envelope.DraftVersion != wantVersion {
		t.Fatalf("%s announced draftVersion=%d want %d", stage, envelope.DraftVersion, wantVersion)
	}
	if envelope.DraftDigest != wantDigest {
		t.Fatalf("%s announced draftDigest=%q want %q", stage, envelope.DraftDigest, wantDigest)
	}
	if envelope.PreviousDraftRevisionId == nil || *envelope.PreviousDraftRevisionId != wantUndoTarget {
		t.Fatalf("%s announced Undo target=%v want %s", stage, envelope.PreviousDraftRevisionId, wantUndoTarget)
	}
	digest, err := envelope.Workspace.Digest()
	if err != nil {
		t.Fatalf("%s digest hydrated workspace: %v", stage, err)
	}
	if digest != wantDigest {
		t.Fatalf("%s mixed envelope: announced digest=%q but the returned workspace digests to %q", stage, wantDigest, digest)
	}
	if len(envelope.Workspace.Documents) != 1 || len(envelope.Workspace.Documents[0].Columns) != 1 || envelope.Workspace.Documents[0].Columns[0].Label != wantLabel {
		t.Fatalf("%s returned workspace does not belong to the announced revision: %+v", stage, envelope.Workspace.Documents)
	}
}

// coreProtocolSaveGate records and parks the persisted draft CAS of specific accepted
// commands so two writers can be provably inside the compare-and-swap window at the
// same time. It wraps the same store SaveDraft the production save path performs
// (explorer/service.go:324 → http_test_helpers_test.go:142); it never decides the CAS
// outcome, it only observes the attempts and releases parked calls on request.
type coreProtocolSaveGate struct {
	mu       sync.Mutex
	entries  map[string]*coreProtocolSaveGateEntry
	watched  map[string]bool
	attempts []coreProtocolSaveAttempt
}

type coreProtocolSaveAttempt struct {
	commandID          string
	expectedVersion    int64
	expectedDigest     string
	acceptedByStoreCAS bool
}

type coreProtocolSaveGateEntry struct {
	arrived     chan struct{}
	released    chan struct{}
	persisted   chan struct{}
	arriveOnce  sync.Once
	releaseOnce sync.Once
	persistOnce sync.Once
}

func newCoreProtocolSaveGate() *coreProtocolSaveGate {
	return &coreProtocolSaveGate{entries: map[string]*coreProtocolSaveGateEntry{}, watched: map[string]bool{}}
}

func (g *coreProtocolSaveGate) arm(commandID string) *coreProtocolSaveGateEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry := &coreProtocolSaveGateEntry{arrived: make(chan struct{}), released: make(chan struct{}), persisted: make(chan struct{})}
	g.entries[commandID] = entry
	g.watched[commandID] = true
	return entry
}

func (g *coreProtocolSaveGate) take(commandID string) *coreProtocolSaveGateEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.entries[commandID]
	if !ok {
		return nil
	}
	delete(g.entries, commandID)
	return entry
}

func (g *coreProtocolSaveGate) releaseAll() {
	g.mu.Lock()
	entries := make([]*coreProtocolSaveGateEntry, 0, len(g.entries))
	for _, entry := range g.entries {
		entries = append(entries, entry)
	}
	g.mu.Unlock()
	for _, entry := range entries {
		entry.release()
	}
}

// record logs only the compare-and-swap calls of the watched racing commands, so the
// witness can prove which attempt the store accepted and which it rejected.
func (g *coreProtocolSaveGate) record(attempt coreProtocolSaveAttempt) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.watched[attempt.commandID] {
		return
	}
	g.attempts = append(g.attempts, attempt)
}

func (g *coreProtocolSaveGate) recordedAttempts() []coreProtocolSaveAttempt {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]coreProtocolSaveAttempt(nil), g.attempts...)
}

func (e *coreProtocolSaveGateEntry) markArrived()   { e.arriveOnce.Do(func() { close(e.arrived) }) }
func (e *coreProtocolSaveGateEntry) release()       { e.releaseOnce.Do(func() { close(e.released) }) }
func (e *coreProtocolSaveGateEntry) markPersisted() { e.persistOnce.Do(func() { close(e.persisted) }) }

type coreProtocolGatedStore struct {
	*testExplorerStore
	gate *coreProtocolSaveGate
}

func (s *coreProtocolGatedStore) SaveDraft(ctx context.Context, value explorer.Explorer, expected int64, expectedDigest ...string) (*explorer.Explorer, error) {
	entry := s.gate.take(value.LastAuthoringCommandID)
	wantDigest := ""
	if len(expectedDigest) > 0 {
		wantDigest = expectedDigest[0]
	}
	if entry == nil {
		stored, err := s.testExplorerStore.SaveDraft(ctx, value, expected, expectedDigest...)
		s.gate.record(coreProtocolSaveAttempt{commandID: value.LastAuthoringCommandID, expectedVersion: expected, expectedDigest: wantDigest, acceptedByStoreCAS: err == nil})
		return stored, err
	}
	entry.markArrived()
	<-entry.released
	stored, err := s.testExplorerStore.SaveDraft(ctx, value, expected, expectedDigest...)
	s.gate.record(coreProtocolSaveAttempt{commandID: value.LastAuthoringCommandID, expectedVersion: expected, expectedDigest: wantDigest, acceptedByStoreCAS: err == nil})
	entry.markPersisted()
	return stored, err
}

// coreProtocolPendingFixture reuses the shared core-protocol fixture (same persisted
// store and seed, same wire helpers, same beforeCompile compile seam) and mounts the
// production route stack over that store with two extra test-owned scheduling
// observations, both at boundaries the fixture contract already declares test-owned:
// entry to PreviewReceipt execution (core_authoring_protocol_test.go:121-123) and
// entry to the persisted draft CAS. The compile/preview closures are the same
// compileExplorerReceipt and compileValidatedReceiptResolution+PreviewOutput calls the
// shared fixture installs, so no production behaviour is substituted.
type coreProtocolPendingFixture struct {
	*coreProtocolFixture
	gate          *coreProtocolSaveGate
	beforePreview func()
}

func newCoreProtocolPendingFixture(t *testing.T) *coreProtocolPendingFixture {
	t.Helper()
	base := newCoreProtocolFixture(t)
	pending := &coreProtocolPendingFixture{coreProtocolFixture: base, gate: newCoreProtocolSaveGate()}
	queryRows := func(ctx context.Context, command compiler.CompiledCommand, _ int, visit dataframeexecution.RowVisitor) error {
		_, _, deriveErr := command.AQLParameters()
		if deriveErr != nil {
			return deriveErr
		}
		return nil
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{Registry: compilerTestRegistry{}, QueryRows: queryRows, PreviewQueryRows: queryRows})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := explorer.NewService(&coreProtocolGatedStore{testExplorerStore: base.store, gate: pending.gate})
	if err != nil {
		t.Fatal(err)
	}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	serviceConfig := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return base.snapshot, nil },
			Token:   func(context.Context, string, string) (capability.Snapshot, error) { return base.snapshot, nil },
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: base.snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: base.snapshot, Scope: readScope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			if base.beforeCompile != nil {
				base.beforeCompile()
			}
			return compileExplorerReceipt(ctx, request, nil, engine, domain, nil, nil)
		},
		ReceiptLookup: func(ctx context.Context, project, explorerID, receiptID string) (*explorer.CompilationReceipt, error) {
			return domain.CompilationReceiptForExplorer(ctx, project, explorerID, receiptID)
		},
		PreviewReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if pending.beforePreview != nil {
				pending.beforePreview()
			}
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
	base.app = app
	return pending
}

// TestCoreAuthoringProtocolRestoreDuringPendingWork is the C15 server-contract
// witness. Each subtest parks one kind of pending work at a real boundary, commits a
// restore of an older revision while it is parked, then completes the parked work and
// reads back both persisted and public state.
func TestCoreAuthoringProtocolRestoreDuringPendingWork(t *testing.T) {
	if label := newCoreProtocolFixture(t).workspace.Documents[0].Columns[0].Label; label != coreProtocolRestoreOriginalLabel {
		t.Fatalf("fixture baseline label=%q, this witness pins it as the restore target", label)
	}

	// 1. A reconcile of captured head A stays parked inside the shared compile seam
	// while a restore commits head R. The completed reconcile may only describe
	// captured A: its returned workspace and its persisted receipt keep A's identity,
	// the head stays R, and a fresh attempt at the obsolete reconcile is refused.
	t.Run("reconcile-pending-while-restore-commits", func(t *testing.T) {
		fixture := newCoreProtocolFixture(t)
		headVersion, headDigest, undoRevision := coreProtocolSeedRestorableHead(t, fixture, "c15-reconcile-save-a", coreProtocolRestoreSavedLabel)

		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		fixture.beforeCompile = func() {
			once.Do(func() {
				close(entered)
				<-release
			})
		}
		inflight := make(chan testHTTPResponse, 1)
		go func() {
			response, err := fixture.reconcile(headVersion, headDigest, "c15-reconcile-a")
			if err != nil {
				t.Errorf("pending reconcile transport: %v", err)
				response = testHTTPResponse{StatusCode: -1}
			}
			inflight <- response
		}()
		coreProtocolAwait(t, entered, "reconcile compile seam")

		restored, ackRestore := fixture.restoreRevision("c15-reconcile-restore-r", undoRevision, headVersion, headDigest)
		if restored.StatusCode != http.StatusOK {
			close(release)
			t.Fatalf("restore while reconcile pending: status=%d body=%s", restored.StatusCode, restored.Body)
		}
		wantRestoredVersion := headVersion + 1
		if ackRestore.DraftVersion != wantRestoredVersion || ackRestore.DraftDigest == "" || ackRestore.DraftDigest == headDigest {
			close(release)
			t.Fatalf("restore did not commit exactly one new head: %+v", ackRestore)
		}
		if len(ackRestore.Results) != 1 || ackRestore.Results[0].Type != authoringv2.CommandResultDraftRestored {
			close(release)
			t.Fatalf("restore result=%+v", ackRestore.Results)
		}
		if got := ackRestore.Workspace.Documents[0].Columns[0].Label; got != coreProtocolRestoreOriginalLabel {
			close(release)
			t.Fatalf("restore acknowledged %q, want the captured older revision %q", got, coreProtocolRestoreOriginalLabel)
		}
		if ackRestore.PreviousDraftRevisionID != coreProtocolRevisionID("core-protocol", headVersion) {
			close(release)
			t.Fatalf("restored head Undo target=%q want %s", ackRestore.PreviousDraftRevisionID, coreProtocolRevisionID("core-protocol", headVersion))
		}
		headWhilePending := coreProtocolExpectHead(t, fixture, wantRestoredVersion, ackRestore.DraftDigest, "restore during pending reconcile")
		if strings.Contains(string(headWhilePending.DraftConfig), coreProtocolRestoreSavedLabel) {
			close(release)
			t.Fatalf("restored head still carries the obsolete saved intent: %s", headWhilePending.DraftConfig)
		}
		// Hydration inside the same window already describes the restored head.
		coreProtocolDecodeHydratedState(t, coreProtocolRequest(t, fixture.app, http.MethodGet, fixture.basePath+"/builder", "", ""),
			wantRestoredVersion, ackRestore.DraftDigest, coreProtocolRevisionID("core-protocol", headVersion), coreProtocolRestoreOriginalLabel, "hydration during pending reconcile and pending restore")

		close(release)
		response := <-inflight
		if response.StatusCode != http.StatusOK {
			t.Fatalf("the reconcile CAS-validated on A must still complete after the restore; status=%d body=%s", response.StatusCode, response.Body)
		}
		compiled := fixture.decodeCompile(response)
		coreProtocolExpectLabels(t, response.Body, "builder", "patients", coreProtocolRestoreSavedLabel)
		if label := fixture.receiptEmittedLabel(t, compiled.ReceiptId, "c_patient"); label != coreProtocolRestoreSavedLabel {
			t.Fatalf("receipt from the parked A reconcile describes %q, not captured A", label)
		}
		if receipt, ok := fixture.store.receipts[compiled.ReceiptId]; !ok || receipt.Project != "project-a" || receipt.ExplorerID != "core-protocol" || receipt.SnapshotToken != fixture.snapshot.Token {
			t.Fatalf("parked receipt is not the persisted A compilation: %+v ok=%v", receipt, ok)
		}

		storedAfter := coreProtocolExpectHead(t, fixture, wantRestoredVersion, ackRestore.DraftDigest, "completed stale reconcile")
		if strings.Contains(string(storedAfter.DraftConfig), coreProtocolRestoreSavedLabel) {
			t.Fatalf("completed stale reconcile reintroduced obsolete intent into the head: %s", storedAfter.DraftConfig)
		}
		coreProtocolDecodeHydratedState(t, coreProtocolRequest(t, fixture.app, http.MethodGet, fixture.basePath+"/builder", "", ""),
			wantRestoredVersion, ackRestore.DraftDigest, coreProtocolRevisionID("core-protocol", headVersion), coreProtocolRestoreOriginalLabel, "post-restore hydration")
		if label := fixture.receiptEmittedLabel(t, compiled.ReceiptId, "c_patient"); label != coreProtocolRestoreSavedLabel {
			t.Fatalf("retained A receipt was rewritten toward the restored head: %q", label)
		}
		stale, err := fixture.reconcile(headVersion, headDigest, "c15-reconcile-stale-replay")
		if err != nil {
			t.Fatal(err)
		}
		coreProtocolBoundaryFailure(t, stale, "fresh reconcile against the replaced head", http.StatusConflict, "DRAFT_CONFLICT", "reconcile", "")
	})

	// 2. After the restore commits R, admission follows the CAS/ownership contracts in
	// both directions: obsolete intent is refused (obsolete-identity restore and
	// obsolete-identity command), the committed restore replays without re-applying,
	// and the next command binds to R's identity and advances the head. Only the
	// current Undo target is restorable, so a later legitimate restore still works.
	t.Run("next-command-binds-to-restored-head", func(t *testing.T) {
		fixture := newCoreProtocolFixture(t)
		headVersion, headDigest, undoRevision := coreProtocolSeedRestorableHead(t, fixture, "c15-command-save-a", coreProtocolRestoreSavedLabel)
		_, hydratedBeforeRestore := fixture.getBuilder()
		restoredVersion, restoredDigest, _ := fixture.MustRestoreRevision("c15-command-restore-r", hydratedBeforeRestore)

		// The bounded last-command replay record makes the committed restore an
		// idempotent acknowledgement; a replayed restore must never re-apply.
		replay, ackReplay := fixture.restoreRevision("c15-command-restore-r", undoRevision, headVersion, headDigest)
		if replay.StatusCode != http.StatusOK {
			t.Fatalf("committed restore must replay as an idempotent acknowledgement: status=%d body=%s", replay.StatusCode, replay.Body)
		}
		if ackReplay.DraftVersion != restoredVersion || ackReplay.DraftDigest != restoredDigest {
			t.Fatalf("replayed restore acknowledged a different revision: %+v", ackReplay)
		}
		coreProtocolExpectHead(t, fixture, restoredVersion, restoredDigest, "replayed restore command")

		obsoleteRestore, ackObsoleteRestore := fixture.restoreRevision("c15-command-obsolete-restore", undoRevision, headVersion, headDigest)
		coreProtocolBoundaryFailure(t, obsoleteRestore, "restore against the replaced head", http.StatusConflict, "DRAFT_CONFLICT", "commands", "")
		if ackObsoleteRestore.DraftVersion != 0 || len(ackObsoleteRestore.Results) != 0 {
			t.Fatalf("rejected restore acknowledged accepted work: %+v", ackObsoleteRestore)
		}

		obsoleteSave, ackObsoleteSave, err := fixture.saveLabelAttempt("c15-command-obsolete", coreProtocolRestoreObsoleteLabel, headVersion, headDigest)
		if err != nil {
			t.Fatal(err)
		}
		coreProtocolBoundaryFailure(t, obsoleteSave, "command bound to the replaced head", http.StatusConflict, "DRAFT_CONFLICT", "commands", "")
		if ackObsoleteSave.DraftVersion != 0 || ackObsoleteSave.DraftDigest != "" || len(ackObsoleteSave.Results) != 0 {
			t.Fatalf("rejected command acknowledged accepted work: %+v", ackObsoleteSave)
		}
		if got := coreProtocolExpectHead(t, fixture, restoredVersion, restoredDigest, "rejected obsolete command"); strings.Contains(string(got.DraftConfig), coreProtocolRestoreObsoleteLabel) {
			t.Fatalf("rejected obsolete intent reached persisted state: %s", got.DraftConfig)
		}

		saved, ackFollowUp := fixture.saveLabel("c15-command-follow-up", coreProtocolRestoreFollowUpLabel, restoredVersion, restoredDigest)
		if saved.StatusCode != http.StatusOK {
			t.Fatalf("command bound to the restored head rejected: status=%d body=%s", saved.StatusCode, saved.Body)
		}
		if ackFollowUp.DraftVersion != restoredVersion+1 || ackFollowUp.DraftDigest == "" || ackFollowUp.DraftDigest == restoredDigest {
			t.Fatalf("follow-up command did not bind to a new revision of the restored head: %+v", ackFollowUp)
		}
		if ackFollowUp.PreviousDraftRevisionId == nil || *ackFollowUp.PreviousDraftRevisionId != coreProtocolRevisionID("core-protocol", restoredVersion) {
			t.Fatalf("follow-up Undo target=%v want %s", ackFollowUp.PreviousDraftRevisionId, coreProtocolRevisionID("core-protocol", restoredVersion))
		}
		coreProtocolExpectLabels(t, saved.Body, "workspace", "patients", coreProtocolRestoreFollowUpLabel)

		staleUndo, ackStaleUndo := fixture.restoreRevision("c15-command-stale-undo", undoRevision, ackFollowUp.DraftVersion, ackFollowUp.DraftDigest)
		coreProtocolBoundaryFailure(t, staleUndo, "restore of an obsolete revision (version adjacency)", http.StatusConflict, "STALE_DRAFT_REVISION", "commands", "")
		if ackStaleUndo.DraftVersion != 0 {
			t.Fatalf("rejected stale Undo acknowledged a revision: %+v", ackStaleUndo)
		}
		coreProtocolExpectHead(t, fixture, ackFollowUp.DraftVersion, ackFollowUp.DraftDigest, "rejected stale Undo")

		// The Undo-ownership guard on its own: a persisted history record whose
		// version is exactly head-1 (so version adjacency would pass, and whose bytes
		// and digest are identical to the legitimate Undo target) must still be refused
		// because it is not the head's recorded Undo target. The positive control is the
		// identical content restored through the real Undo target below.
		foreignRaw, foreignErr := fixture.workspace.CanonicalJSON()
		if foreignErr != nil {
			t.Fatal(foreignErr)
		}
		const foreignUndoTarget = "draft_revision_core-protocol_foreign_undo"
		coreProtocolSeedDraftRevisionRecord(t, fixture, foreignUndoTarget, ackFollowUp.DraftVersion-1, foreignRaw, fixture.initialDigest)
		foreignUndo, ackForeignUndo := fixture.restoreRevision("c15-command-foreign-undo", foreignUndoTarget, ackFollowUp.DraftVersion, ackFollowUp.DraftDigest)
		coreProtocolBoundaryFailure(t, foreignUndo, "restore of a revision that is not the head's Undo target", http.StatusConflict, "STALE_DRAFT_REVISION", "commands", "")
		if ackForeignUndo.DraftVersion != 0 {
			t.Fatalf("rejected foreign Undo target acknowledged a revision: %+v", ackForeignUndo)
		}
		coreProtocolExpectHead(t, fixture, ackFollowUp.DraftVersion, ackFollowUp.DraftDigest, "rejected foreign Undo target")

		_, hydratedAfterFollowUp := fixture.getBuilder()
		redoneVersion, redoneDigest, _ := fixture.MustRestoreRevision("c15-command-redo", hydratedAfterFollowUp)
		if redoneVersion != ackFollowUp.DraftVersion+1 {
			t.Fatalf("legitimate restore of the current Undo target did not advance the head: %+v", hydratedAfterFollowUp)
		}
		if redoneDigest != fixture.initialDigest {
			t.Fatalf("legitimate restore returned %q, want the identical content the refused foreign record carried (%q)", redoneDigest, fixture.initialDigest)
		}
		coreProtocolDecodeHydratedState(t, coreProtocolRequest(t, fixture.app, http.MethodGet, fixture.basePath+"/builder", "", ""),
			redoneVersion, redoneDigest, coreProtocolRevisionID("core-protocol", ackFollowUp.DraftVersion), coreProtocolRestoreOriginalLabel, "restore after follow-up")
	})

	// 3. A restore commits while a preview for a retained receipt of A is parked at
	// the execution boundary. The preview keeps executing per the immutable-receipt
	// contract (lifecycle/preview.go:28-44 admits on the receipt, never on head
	// equality) and its displayed server data stays A's frozen contract; afterwards a
	// fresh reconcile of R produces a receipt bound to R.
	t.Run("preview-pending-while-restore-commits", func(t *testing.T) {
		fixture := newCoreProtocolPendingFixture(t)
		base := fixture.coreProtocolFixture
		headVersion, headDigest, undoRevision := coreProtocolSeedRestorableHead(t, base, "c15-preview-save-a", coreProtocolRestoreSavedLabel)
		receiptA := fixture.decodeCompile(fixture.MustReconcile(headVersion, headDigest, "c15-preview-reconcile-a"))
		if label := fixture.receiptEmittedLabel(t, receiptA.ReceiptId, "c_patient"); label != coreProtocolRestoreSavedLabel {
			t.Fatalf("baseline receipt describes %q", label)
		}

		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		fixture.beforePreview = func() {
			once.Do(func() {
				close(entered)
				<-release
			})
		}
		inflight := make(chan testHTTPResponse, 1)
		go func() {
			response, err := fixture.previewPending(receiptA.ReceiptId)
			if err != nil {
				t.Errorf("pending preview transport: %v", err)
				response = testHTTPResponse{StatusCode: -1}
			}
			inflight <- response
		}()
		coreProtocolAwait(t, entered, "preview execution seam")

		restored, ackRestore := fixture.restoreRevision("c15-preview-restore-r", undoRevision, headVersion, headDigest)
		if restored.StatusCode != http.StatusOK {
			close(release)
			t.Fatalf("restore failed while a preview was executing: status=%d body=%s", restored.StatusCode, restored.Body)
		}
		wantRestoredVersion := headVersion + 1
		coreProtocolExpectHead(t, base, wantRestoredVersion, ackRestore.DraftDigest, "restore during pending preview")

		close(release)
		response := <-inflight
		if response.StatusCode != http.StatusOK {
			t.Fatalf("historical receipt execution was refused after the restore (head==receipt is not the contract): status=%d body=%s", response.StatusCode, response.Body)
		}
		var preview loomapi.PreviewResponse
		if err := json.Unmarshal([]byte(response.Body), &preview); err != nil {
			t.Fatal(err)
		}
		if preview.ReceiptId != receiptA.ReceiptId || preview.OutputId != "patients" || preview.Rows == nil {
			t.Fatalf("completed preview is not bound to the retained receipt: %+v", preview)
		}
		labels := make([]string, 0, len(preview.Columns))
		for _, column := range preview.Columns {
			labels = append(labels, column.Label)
		}
		if len(labels) != 1 || labels[0] != coreProtocolRestoreSavedLabel {
			t.Fatalf("preview displayed server data changed under the pending preview: %v want [%s]", labels, coreProtocolRestoreSavedLabel)
		}
		if !strings.Contains(response.Body, `"rows":[]`) {
			t.Fatalf("preview rows lost: %s", response.Body)
		}

		restoredCompile := fixture.decodeCompile(fixture.MustReconcile(wantRestoredVersion, ackRestore.DraftDigest, "c15-preview-reconcile-r"))
		if restoredCompile.ReceiptId == receiptA.ReceiptId {
			t.Fatal("reconcile of the restored head reused A's receipt identity")
		}
		if label := fixture.receiptEmittedLabel(t, restoredCompile.ReceiptId, "c_patient"); label != coreProtocolRestoreOriginalLabel {
			t.Fatalf("receipt of the restored head describes %q, want %q", label, coreProtocolRestoreOriginalLabel)
		}
		restoredReceipt, ok := fixture.store.receipts[restoredCompile.ReceiptId]
		if !ok || restoredReceipt.Project != "project-a" || restoredReceipt.ExplorerID != "core-protocol" {
			t.Fatalf("receipt of the restored head is not persisted for this Explorer: %+v ok=%v", restoredReceipt, ok)
		}
		if compiledA, retained := fixture.store.receipts[receiptA.ReceiptId]; !retained || restoredReceipt.IntentDigest == compiledA.IntentDigest {
			t.Fatalf("restored-head receipt does not carry its own compile identity: %q vs %q", restoredReceipt.IntentDigest, compiledA.IntentDigest)
		}
		if after := fixture.preview(restoredCompile.ReceiptId); after.StatusCode != http.StatusOK || !strings.Contains(after.Body, `"rows":[]`) || !strings.Contains(after.Body, coreProtocolRestoreOriginalLabel) {
			t.Fatalf("restored-head preview rejected: status=%d body=%s", after.StatusCode, after.Body)
		}
		if label := fixture.receiptEmittedLabel(t, receiptA.ReceiptId, "c_patient"); label != coreProtocolRestoreSavedLabel {
			t.Fatalf("A's retained receipt was rewritten by the restored head: %q", label)
		}
	})

	// 4. A restore and a competing command save race at the store CAS. Both writers are
	// provably parked inside the compare-and-swap window with the same expected
	// identity, exactly one CAS is accepted, the loser's rejection comes from the CAS
	// itself (recorded by the store as a rejected compare-and-swap for the pre-race
	// identity), and head plus history revision stay coherent for either winner.
	t.Run("restore-races-command-save", func(t *testing.T) {
		for _, winner := range []string{"restore", "save"} {
			winner := winner
			t.Run(winner+"-wins", func(t *testing.T) {
				fixture := newCoreProtocolPendingFixture(t)
				raceLabel := coreProtocolRestoreCompetingLabel
				base := fixture.coreProtocolFixture
				headVersion, headDigest, undoRevision := coreProtocolSeedRestorableHead(t, base, "c15-race-save-a", raceLabel)
				replaced := coreProtocolStoredHead(t, base)
				if replaced.DraftVersion != headVersion || replaced.DraftDigest != headDigest {
					t.Fatalf("seeding disagreement between acknowledgement and persisted head: %+v", replaced)
				}

				const restoreCommandID = "c15-race-restore"
				const competingCommandID = "c15-race-save-b"
				restoreEntry := fixture.gate.arm(restoreCommandID)
				competingEntry := fixture.gate.arm(competingCommandID)
				t.Cleanup(fixture.gate.releaseAll)

				type outcome struct {
					response testHTTPResponse
					ack      authoringv2.ApplyCommandsResponse
				}
				restoreDone := make(chan outcome, 1)
				saveDone := make(chan outcome, 1)
				go func() {
					response, ack, err := fixture.restoreAttempt(restoreCommandID, undoRevision, headVersion, headDigest)
					if err != nil {
						t.Errorf("racing restore transport: %v", err)
					}
					restoreDone <- outcome{response: response, ack: ack}
				}()
				go func() {
					response, ack, err := fixture.saveLabelAttempt(competingCommandID, coreProtocolRestoreFollowUpLabel, headVersion, headDigest)
					if err != nil {
						t.Errorf("racing save transport: %v", err)
					}
					saveDone <- outcome{response: response, ack: ack}
				}()

				coreProtocolAwait(t, restoreEntry.arrived, "restore draft CAS call")
				coreProtocolAwait(t, competingEntry.arrived, "competing save draft CAS call")

				first, second := restoreEntry, competingEntry
				if winner == "save" {
					first, second = competingEntry, restoreEntry
				}
				first.release()
				coreProtocolAwait(t, first.persisted, "winning draft CAS")
				second.release()
				coreProtocolAwait(t, second.persisted, "losing draft CAS")

				restore, save := <-restoreDone, <-saveDone
				var won, lost outcome
				var wonLabel, lostLabel string
				switch winner {
				case "restore":
					won, lost = restore, save
					wonLabel, lostLabel = coreProtocolRestoreOriginalLabel, coreProtocolRestoreFollowUpLabel
				default:
					won, lost = save, restore
					wonLabel, lostLabel = coreProtocolRestoreFollowUpLabel, coreProtocolRestoreOriginalLabel
				}
				if won.response.StatusCode != http.StatusOK {
					t.Fatalf("winner rejected: status=%d body=%s", won.response.StatusCode, won.response.Body)
				}
				if won.ack.DraftVersion != headVersion+1 || won.ack.DraftDigest == "" || won.ack.DraftDigest == headDigest {
					t.Fatalf("winner did not commit exactly one revision: %+v", won.ack)
				}
				if winner == "restore" && (len(won.ack.Results) != 1 || won.ack.Results[0].Type != authoringv2.CommandResultDraftRestored) {
					t.Fatalf("restore winner lacks DRAFT_RESTORED: %+v", won.ack.Results)
				}
				coreProtocolBoundaryFailure(t, lost.response, "losing racer", http.StatusConflict, "DRAFT_CONFLICT", "commands", "")

				attempts := fixture.gate.recordedAttempts()
				if len(attempts) != 2 {
					t.Fatalf("racing writers performed %d draft CAS calls, want exactly 2: %+v", len(attempts), attempts)
				}
				winnerCommandID, loserCommandID := restoreCommandID, competingCommandID
				if winner == "save" {
					winnerCommandID, loserCommandID = competingCommandID, restoreCommandID
				}
				if attempts[0].commandID != winnerCommandID || !attempts[0].acceptedByStoreCAS {
					t.Fatalf("first recorded CAS is not the accepted winner: %+v", attempts[0])
				}
				// Common origin: CAS semantics accept a writer only against the head it
				// read, so two accepted writers would be legitimate only if the second
				// were a different origin riding the first's result. That would be a
				// chained/sequential dispatch masquerading as a race, so both recorded
				// CAS attempts must compare-and-swap the single pre-race head identity
				// (C15, docs/verification/CORE_PROTOCOL_VERIFICATION_PLAN.md:384: restore
				// and subsequent admission follow the actual CAS/ownership contract).
				if attempts[0].expectedVersion != headVersion || attempts[0].expectedDigest != headDigest {
					t.Fatalf("winner did not race from the common pre-race head: %+v want version=%d digest=%s", attempts[0], headVersion, headDigest)
				}
				if attempts[1].commandID != loserCommandID || attempts[1].acceptedByStoreCAS || attempts[1].expectedVersion != headVersion || attempts[1].expectedDigest != headDigest {
					t.Fatalf("loser was not rejected by the store CAS against the pre-race identity: %+v", attempts[1])
				}

				stored := coreProtocolExpectHead(t, base, headVersion+1, won.ack.DraftDigest, "raced head")
				workspace, decodeErr := authoringv2.DecodeWorkspace(stored.DraftConfig)
				if decodeErr != nil {
					t.Fatalf("raced head does not decode (torn state): %v bytes=%s", decodeErr, stored.DraftConfig)
				}
				recomputed, digestErr := workspace.Digest()
				if digestErr != nil {
					t.Fatalf("digest raced head: %v", digestErr)
				}
				if recomputed != won.ack.DraftDigest {
					t.Fatalf("raced head digest=%q but its canonical workspace digests to %q (torn state)", won.ack.DraftDigest, recomputed)
				}
				if len(workspace.Documents) != 1 || len(workspace.Documents[0].Columns) != 1 || workspace.Documents[0].Columns[0].Label != wonLabel {
					t.Fatalf("head semantics do not match the winner: %+v want label %q", workspace.Documents, wonLabel)
				}
				if strings.Contains(string(stored.DraftConfig), lostLabel) {
					t.Fatalf("loser intent leaked into the persisted head: %s", stored.DraftConfig)
				}
				if stored.PreviousDraftRevisionID != coreProtocolRevisionID("core-protocol", headVersion) {
					t.Fatalf("winner head points at Undo target %q want %s", stored.PreviousDraftRevisionID, coreProtocolRevisionID("core-protocol", headVersion))
				}
				prior, err := fixture.store.GetDraftRevision(context.Background(), "project-a", "core-protocol", stored.PreviousDraftRevisionID)
				if err != nil {
					t.Fatalf("history revision of the raced head is missing: %v", err)
				}
				if prior.DraftVersion != headVersion || prior.DraftDigest != headDigest || !json.Valid(prior.DraftConfig) || !strings.Contains(string(prior.DraftConfig), raceLabel) {
					t.Fatalf("history revision lost the raced head's coherent state: %+v", prior)
				}
				earlier, err := fixture.store.GetDraftRevision(context.Background(), "project-a", "core-protocol", undoRevision)
				if err != nil || earlier.DraftVersion != headVersion-1 || !json.Valid(earlier.DraftConfig) || !strings.Contains(string(earlier.DraftConfig), coreProtocolRestoreOriginalLabel) {
					t.Fatalf("earlier history revision was disturbed by the race: %+v err=%v", earlier, err)
				}

				coreProtocolDecodeHydratedState(t, coreProtocolRequest(t, fixture.app, http.MethodGet, fixture.basePath+"/builder", "", ""),
					headVersion+1, won.ack.DraftDigest, coreProtocolRevisionID("core-protocol", headVersion), wonLabel, "post-race hydration")

				next, ackNext := fixture.saveLabel("c15-race-next-"+winner, "Patient Next After Race", won.ack.DraftVersion, won.ack.DraftDigest)
				if next.StatusCode != http.StatusOK {
					t.Fatalf("next command cannot bind to the winning coherent head: status=%d body=%s", next.StatusCode, next.Body)
				}
				if ackNext.DraftVersion != headVersion+2 {
					t.Fatalf("next command did not advance from the winning head: %+v", ackNext)
				}
			})
		}
	})

	// 5. Recovery GET (Builder hydration) while the captured-A reconcile is still
	// parked returns a coherent envelope, checked before the restore commits and again
	// after, with the announced digest re-derived from the returned workspace so a
	// mixed version/digest pair cannot pass. Completing the parked reconcile afterwards
	// leaves head R coherent.
	t.Run("recovery-get-during-pending-reconcile", func(t *testing.T) {
		fixture := newCoreProtocolFixture(t)
		headVersion, headDigest, undoRevision := coreProtocolSeedRestorableHead(t, fixture, "c15-recovery-save-a", coreProtocolRestoreSavedLabel)

		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		fixture.beforeCompile = func() {
			once.Do(func() {
				close(entered)
				<-release
			})
		}
		inflight := make(chan testHTTPResponse, 1)
		go func() {
			response, err := fixture.reconcile(headVersion, headDigest, "c15-recovery-reconcile-a")
			if err != nil {
				t.Errorf("pending reconcile transport: %v", err)
				response = testHTTPResponse{StatusCode: -1}
			}
			inflight <- response
		}()
		coreProtocolAwait(t, entered, "reconcile compile seam")

		coreProtocolDecodeHydratedState(t, coreProtocolRequest(t, fixture.app, http.MethodGet, fixture.basePath+"/builder", "", ""),
			headVersion, headDigest, undoRevision, coreProtocolRestoreSavedLabel, "hydration during pending reconcile")

		restored, ackRestore := fixture.restoreRevision("c15-recovery-restore-r", undoRevision, headVersion, headDigest)
		if restored.StatusCode != http.StatusOK {
			close(release)
			t.Fatalf("restore during pending reconcile: status=%d body=%s", restored.StatusCode, restored.Body)
		}
		wantRestoredVersion := headVersion + 1

		coreProtocolDecodeHydratedState(t, coreProtocolRequest(t, fixture.app, http.MethodGet, fixture.basePath+"/builder", "", ""),
			wantRestoredVersion, ackRestore.DraftDigest, coreProtocolRevisionID("core-protocol", headVersion), coreProtocolRestoreOriginalLabel, "hydration after restore during pending reconcile")

		close(release)
		response := <-inflight
		if response.StatusCode != http.StatusOK {
			t.Fatalf("parked reconcile must complete; status=%d body=%s", response.StatusCode, response.Body)
		}
		coreProtocolExpectLabels(t, response.Body, "builder", "patients", coreProtocolRestoreSavedLabel)
		compiled := fixture.decodeCompile(response)
		if label := fixture.receiptEmittedLabel(t, compiled.ReceiptId, "c_patient"); label != coreProtocolRestoreSavedLabel {
			t.Fatalf("parked reconcile receipt describes %q", label)
		}
		coreProtocolExpectHead(t, fixture, wantRestoredVersion, ackRestore.DraftDigest, "hydration case after completed reconcile")
		coreProtocolDecodeHydratedState(t, coreProtocolRequest(t, fixture.app, http.MethodGet, fixture.basePath+"/builder", "", ""),
			wantRestoredVersion, ackRestore.DraftDigest, coreProtocolRevisionID("core-protocol", headVersion), coreProtocolRestoreOriginalLabel, "hydration after pending work settled")
	})
}
