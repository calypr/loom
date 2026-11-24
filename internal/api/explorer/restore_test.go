package explorerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	explorerarango "github.com/calypr/loom/internal/explorer/arango"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	receiptapi "github.com/calypr/loom/internal/explorer/receipt"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
	"github.com/gofiber/fiber/v3"
)

const (
	coreProtocolRestoreOriginalLabel  = "Patient ID"
	coreProtocolRestoreSavedLabel     = "Patient Head A"
	coreProtocolRestoreCompetingLabel = "Patient Competing Save"
	coreProtocolRestoreObsoleteLabel  = "Patient Obsolete Intent"
	coreProtocolRestoreFollowUpLabel  = "Patient Follow-Up After Restore"
)

func coreProtocolRevisionID(explorerID string, draftVersion int64) string {
	return fmt.Sprintf("draft_revision_%s_%d", explorerID, draftVersion)
}

func coreProtocolRestoreBody(f *coreProtocolFixture, commandID, revisionID string, expectedVersion int64, expectedDigest string) string {
	return fmt.Sprintf(`{"commandId":%q,"semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"RESTORE_DRAFT_REVISION","draftRevisionId":%q}]}`,
		commandID, ws.CurrentSemanticsVersion, f.snapshot.Token, expectedVersion, expectedDigest, revisionID)
}

func coreProtocolSaveLabelBody(f *coreProtocolFixture, commandID, label string, expectedVersion int64, expectedDigest string) (string, error) {
	edited := f.workspace
	edited.Documents[0].Columns[0].Label = label
	columnValue, err := json.Marshal(edited.Documents[0].Columns[0])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"commandId":%q,"semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"UPDATE_COLUMN","outputId":"patients","column":"c_patient","columnValue":%s}]}`,
		commandID, ws.CurrentSemanticsVersion, f.snapshot.Token, expectedVersion, expectedDigest, columnValue), nil
}

func coreProtocolDecodeAck(t *testing.T, response testHTTPResponse) authoringv2.ApplyCommandsResponse {
	t.Helper()
	var ack authoringv2.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(response.Body), &ack); err != nil {
		t.Fatalf("decode commands acknowledgement status=%d body=%s: %v", response.StatusCode, response.Body, err)
	}
	return ack
}

func (f *coreProtocolFixture) restoreRevision(commandID, revisionID string, expectedVersion int64, expectedDigest string) (testHTTPResponse, authoringv2.ApplyCommandsResponse) {
	f.t.Helper()
	response := coreProtocolRequest(f.t, f.app, http.MethodPost, f.basePath+"/commands", coreProtocolRestoreBody(f, commandID, revisionID, expectedVersion, expectedDigest), "builder-command-"+commandID)
	return response, coreProtocolDecodeAck(f.t, response)
}

func (f *coreProtocolFixture) restoreAttempt(commandID, revisionID string, expectedVersion int64, expectedDigest string) (testHTTPResponse, authoringv2.ApplyCommandsResponse, error) {
	response, err := coreProtocolDo(f.app, http.MethodPost, f.basePath+"/commands", coreProtocolRestoreBody(f, commandID, revisionID, expectedVersion, expectedDigest), "builder-command-"+commandID)
	if err != nil || response.StatusCode != http.StatusOK {
		return response, authoringv2.ApplyCommandsResponse{}, err
	}
	return response, coreProtocolDecodeAck(f.t, response), err
}

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

func (f *coreProtocolFixture) previewPending(receiptID string) (testHTTPResponse, error) {
	body := fmt.Sprintf(`{"receiptId":%q,"outputId":"patients","limit":10}`, receiptID)
	return coreProtocolDo(f.app, http.MethodPost, f.basePath+"/preview", body, "")
}

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

func coreProtocolSeedDraftRevisionRecord(t *testing.T, fixture *coreProtocolFixture, revisionID string, draftVersion int64, raw []byte, digest string) {
	t.Helper()
	head, err := fixture.store.Get(context.Background(), "project-a", "core-protocol")
	if err != nil || head == nil {
		t.Fatalf("read head for revision seeding: %v", err)
	}
	fixture.store.Mu.Lock()
	defer fixture.store.Mu.Unlock()
	fixture.store.DraftRevisions[explorerfixtures.Key("project-a", "core-protocol")+"\x00"+revisionID] = explorer.DraftRevision{
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

func coreProtocolDecodeHydratedState(t *testing.T, response testHTTPResponse, wantVersion int64, wantDigest, wantUndoTarget, wantLabel, stage string) {
	t.Helper()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", stage, response.StatusCode, response.Body)
	}
	var envelope struct {
		loomapi.BuilderState
		Workspace *ws.Workspace `json:"workspace"`
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

func (e *coreProtocolSaveGateEntry) markArrived() { e.arriveOnce.Do(func() { close(e.arrived) }) }

func (e *coreProtocolSaveGateEntry) release() { e.releaseOnce.Do(func() { close(e.released) }) }

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

type coreProtocolPendingFixture struct {
	*coreProtocolFixture
	gate          *coreProtocolSaveGate
	beforePreview func()
}

func newCoreProtocolPendingFixture(t *testing.T) *coreProtocolPendingFixture {
	t.Helper()
	base := newCoreProtocolFixture(t)
	pending := &coreProtocolPendingFixture{coreProtocolFixture: base, gate: newCoreProtocolSaveGate()}
	queryRows := func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }
	engine, err := dataframeexecution.New(dataframeexecution.Config{Registry: compilerTestRegistry{}, QueryRows: queryRows, PreviewQueryRows: queryRows})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := explorerfixtures.NewService(&coreProtocolGatedStore{testExplorerStore: base.store, gate: pending.gate})
	if err != nil {
		t.Fatal(err)
	}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	serviceConfig := lifecycle.Config{
		Capability: ports.CapabilityResolver{
			Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return base.snapshot, nil },
			Token:   func(context.Context, string, string) (capability.Snapshot, error) { return base.snapshot, nil },
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: base.snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: base.snapshot, Scope: readScope}, nil
			},
			Catalog: ws.CatalogFromCapability,
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			if base.beforeCompile != nil {
				base.beforeCompile()
			}
			return receiptapi.Compile(ctx, request, nil, engine, domain, nil, nil)
		},
		ReceiptLookup: func(ctx context.Context, project, explorerID, receiptID string) (*receiptmodel.CompilationReceipt, error) {
			return domain.CompilationReceiptForExplorer(ctx, project, explorerID, receiptID)
		},
		PreviewReceipt: func(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if pending.beforePreview != nil {
				pending.beforePreview()
			}
			resolved, resolveErr := receiptapi.CompileValidatedResolution(ctx, engine, receipt, bindings)
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

func TestCoreAuthoringProtocolRestoreDuringPendingWork(t *testing.T) {
	if label := newCoreProtocolFixture(t).workspace.Documents[0].Columns[0].Label; label != coreProtocolRestoreOriginalLabel {
		t.Fatalf("fixture baseline label=%q, this witness pins it as the restore target", label)
	}

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
		if receipt, ok := fixture.store.Receipts[compiled.ReceiptId]; !ok || receipt.Project != "project-a" || receipt.ExplorerID != "core-protocol" || receipt.SnapshotToken != fixture.snapshot.Token {
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

	t.Run("next-command-binds-to-restored-head", func(t *testing.T) {
		fixture := newCoreProtocolFixture(t)
		headVersion, headDigest, undoRevision := coreProtocolSeedRestorableHead(t, fixture, "c15-command-save-a", coreProtocolRestoreSavedLabel)
		_, hydratedBeforeRestore := fixture.getBuilder()
		restoredVersion, restoredDigest, _ := fixture.MustRestoreRevision("c15-command-restore-r", hydratedBeforeRestore)

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
		restoredReceipt, ok := fixture.store.Receipts[restoredCompile.ReceiptId]
		if !ok || restoredReceipt.Project != "project-a" || restoredReceipt.ExplorerID != "core-protocol" {
			t.Fatalf("receipt of the restored head is not persisted for this Explorer: %+v ok=%v", restoredReceipt, ok)
		}
		if compiledA, retained := fixture.store.Receipts[receiptA.ReceiptId]; !retained || restoredReceipt.IntentDigest == compiledA.IntentDigest {
			t.Fatalf("restored-head receipt does not carry its own compile identity: %q vs %q", restoredReceipt.IntentDigest, compiledA.IntentDigest)
		}
		if after := fixture.preview(restoredCompile.ReceiptId); after.StatusCode != http.StatusOK || !strings.Contains(after.Body, `"rows":[]`) || !strings.Contains(after.Body, coreProtocolRestoreOriginalLabel) {
			t.Fatalf("restored-head preview rejected: status=%d body=%s", after.StatusCode, after.Body)
		}
		if label := fixture.receiptEmittedLabel(t, receiptA.ReceiptId, "c_patient"); label != coreProtocolRestoreSavedLabel {
			t.Fatalf("A's retained receipt was rewritten by the restored head: %q", label)
		}
	})

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

				if attempts[0].expectedVersion != headVersion || attempts[0].expectedDigest != headDigest {
					t.Fatalf("winner did not race from the common pre-race head: %+v want version=%d digest=%s", attempts[0], headVersion, headDigest)
				}
				if attempts[1].commandID != loserCommandID || attempts[1].acceptedByStoreCAS || attempts[1].expectedVersion != headVersion || attempts[1].expectedDigest != headDigest {
					t.Fatalf("loser was not rejected by the store CAS against the pre-race identity: %+v", attempts[1])
				}

				stored := coreProtocolExpectHead(t, base, headVersion+1, won.ack.DraftDigest, "raced head")
				workspace, decodeErr := ws.DecodeWorkspace(stored.DraftConfig)
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

type routeRoundtripStore interface {
	explorer.ExplorerStore
	explorer.RevisionStore
}

func TestSavedRelatedRouteProposalApplyRestoreAndFreshRead(t *testing.T) {
	savedRelatedRouteRoundtrip(t, nil)
}

func savedRelatedRouteRoundtrip(t *testing.T, rowClient *storearango.Client) {
	t.Helper()
	snapshot := testAuthoringV2CapabilitySnapshot()
	for _, resource := range []string{"Observation", "Group", "Specimen"} {
		snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "n_" + strings.ToLower(resource), ResourceType: resource, Populated: true, DocumentCount: 2})
	}
	edge := func(id, from, to, relationship string) capability.Edge {
		return capability.Edge{ID: id, FromNodeID: "n_" + strings.ToLower(from), ToNodeID: "n_" + strings.ToLower(to), SourceResourceType: from, TargetResourceType: to, Label: relationship, StorageDirection: "INBOUND"}
	}
	snapshot.Edges = []capability.Edge{edge("route_a", "Patient", "Observation", "subject_Patient"), edge("route_b_1", "Patient", "Group", "member_entity_Patient"), edge("route_b_2", "Group", "Specimen", "subject_Group"), edge("route_b_3", "Specimen", "Observation", "specimen_Specimen")}
	route := func(indices ...int) []capability.ConstructionRouteStep {
		var result []capability.ConstructionRouteStep
		for _, i := range indices {
			e := snapshot.Edges[i]
			result = append(result, capability.ConstructionRouteStep{EdgeID: e.ID, FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, FromResourceType: e.SourceResourceType, ToResourceType: e.TargetResourceType, Relationship: e.Label, StorageDirection: e.StorageDirection, MatchMode: "OPTIONAL"})
		}
		return result
	}
	routeA, routeB := route(0), route(1, 2, 3)
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	outputs := []ws.StageColumn{}
	for _, column := range document.Columns {
		outputs = append(outputs, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	outputs = append(outputs, ws.StageColumn{ID: "observation_id", Name: "observation_id", Label: "Observation FHIR resource ID", Type: "string"})
	construction := func(path []capability.ConstructionRouteStep) ws.Construction {
		choice, err := capability.NewConstructionRelatedResourceRouteChoiceFromAnchor(snapshot.Token, "source_projection", "_key", "root", "n_patient", "Patient", "n_observation", "Observation", path)
		if err != nil {
			t.Fatal(err)
		}
		return ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{ID: "expand_observations", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}}, Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedExpand, RelatedExpand: &ws.ConstructionRelatedExpand{AnchorColumnID: "_key", ChoiceID: choice.ChoiceID, TargetNodeID: "n_observation", TargetResourceType: "Observation", Route: path, ContributorRule: ws.ConstructionRelatedContributorRule{Policy: ws.ConstructionRelatedAllMatches}, EmptyPolicy: ws.ConstructionExpandEmptyExclude, RelatedRecordColumnID: "observation_id"}}, Outputs: outputs}}}
	}
	initial := construction(routeA)
	document.Construction = &initial
	workspace.Documents[0] = document
	workspace, err = ws.MigrateLegacyContributors(workspace, ws.CatalogFromCapability(snapshot, "route-roundtrip"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, ws.CatalogFromCapability(snapshot, "route-roundtrip")).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatal(err)
	}
	original, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	var store routeRoundtripStore = explorerfixtures.NewStore()
	if rowClient != nil {
		if err := rowClient.Bootstrap(context.Background(), explorerarango.BootstrapSpec()); err != nil {
			t.Fatal(err)
		}
		store, err = newTestExplorerArangoStore(rowClient)
		if err != nil {
			t.Fatal(err)
		}
	}
	owner := explorer.Explorer{Project: "project-a", ExplorerID: "route-roundtrip", Title: "Patients", DraftConfig: original, DraftVersion: 3, DraftDigest: digest, DraftSnapshotToken: snapshot.Token, DraftSourceGeneration: snapshot.Identity.Generation, DraftAuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, ActiveRevisionID: "published_original"}
	if _, err := store.Create(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertRevision(context.Background(), explorer.Revision{ID: owner.ActiveRevisionID, Project: owner.Project, ExplorerID: owner.ExplorerID, SourceGeneration: snapshot.Identity.Generation, AuthoringBundle: original}); err != nil {
		t.Fatal(err)
	}
	service, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	queryRows := func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }
	if rowClient != nil {
		queryRows = func(ctx context.Context, query string, batch int, variables map[string]any, visit func(map[string]any) error) error {
			return rowClient.QueryRows(ctx, query, batch, variables, storearango.RowVisitor(visit))
		}
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{Registry: compilerTestRegistry{}, QueryRows: queryRows})
	if err != nil {
		t.Fatal(err)
	}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	compiled := 0
	config := lifecycle.Config{Capability: ports.CapabilityResolver{Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil }, Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil }, ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}, ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}, Catalog: ws.CatalogFromCapability}, CompileReceipt: func(ctx context.Context, r ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		result, err := receiptapi.Compile(ctx, r, nil, engine, service, nil, nil)
		if err == nil {
			compiled++
		}
		return result, err
	}, PreviewReceipt: func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"c_patient", "observation_id"}, RowCount: 0, Complete: true}, nil
	}}
	if rowClient != nil {
		config.PreviewReceipt = func(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			resolved, err := receiptapi.CompileValidatedResolution(ctx, engine, receipt, bindings)
			if err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{Output: bindings.OutputNames[0], Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity}, visit)
		}
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	base := "/api/v1/projects/project-a/explorers/route-roundtrip/authoring/v2"
	type saved struct {
		Workspace *ws.Workspace `json:"workspace"`
		Version   int64         `json:"draftVersion"`
		Digest    string        `json:"draftDigest"`
		Previous  string        `json:"previousDraftRevisionId"`
	}
	load := func() saved {
		t.Helper()
		r := requestJSON(t, app, http.MethodGet, base+"/builder", "")
		if r.StatusCode != http.StatusOK {
			t.Fatalf("fresh read status=%d body=%s", r.StatusCode, r.Body)
		}
		var v saved
		if err := json.Unmarshal([]byte(r.Body), &v); err != nil {
			t.Fatal(err)
		}
		if v.Workspace == nil {
			t.Fatal("fresh read omitted workspace")
		}
		return v
	}
	assertRoute := func(v saved, path []capability.ConstructionRouteStep) {
		t.Helper()
		d := v.Workspace.Documents[0]
		if d.Output.ID != "patients" || d.Construction == nil || len(d.Construction.Steps) != 1 || d.Construction.Steps[0].ID != "expand_observations" || d.Construction.Steps[0].Operation.RelatedExpand.RelatedRecordColumnID != "observation_id" || !reflect.DeepEqual(d.Construction.Steps[0].Operation.RelatedExpand.Route, path) {
			t.Fatalf("fresh read lost exact route or stable IDs: %#v", d)
		}
		actualOutputs := d.Construction.Steps[0].Outputs
		if len(actualOutputs) != len(outputs) {
			t.Fatal("route edit changed output column count")
		}
		for i, expected := range outputs {
			if actualOutputs[i].ID != expected.ID || actualOutputs[i].Name != expected.Name {
				t.Fatalf("route edit changed column identity/order: got=%#v want=%#v", actualOutputs, outputs)
			}
		}
	}
	assertRoute(load(), routeA)
	reconcile := func(version int64, draftDigest string) string {
		t.Helper()
		response := requestJSON(t, app, http.MethodPost, base+"/reconcile", fmt.Sprintf(`{"snapshotToken":%q,"draftVersion":%d,"draftDigest":%q,"outputId":"patients"}`, snapshot.Token, version, draftDigest))
		if response.StatusCode != http.StatusOK {
			t.Fatalf("route compiler status=%d body=%s", response.StatusCode, response.Body)
		}
		var value struct {
			ReceiptID   string `json:"receiptId"`
			Diagnostics []struct {
				Severity string `json:"severity"`
			} `json:"diagnostics"`
		}
		if err := json.Unmarshal([]byte(response.Body), &value); err != nil {
			t.Fatal(err)
		}
		if value.ReceiptID == "" {
			t.Fatalf("route compiler returned no receipt: %s", response.Body)
		}
		for _, diagnostic := range value.Diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("route compiler rejected route: %s", response.Body)
			}
		}
		return value.ReceiptID
	}
	assertRows := func(receiptID string, want []string) {
		t.Helper()
		if rowClient == nil {
			return
		}
		response := requestJSON(t, app, http.MethodPost, base+"/preview", fmt.Sprintf(`{"receiptId":%q,"outputId":"patients","limit":20}`, receiptID))
		if response.StatusCode != http.StatusOK {
			t.Fatalf("actual route row query status=%d body=%s", response.StatusCode, response.Body)
		}
		var preview struct {
			Rows      []map[string]any `json:"rows"`
			ReceiptID string           `json:"receiptId"`
			OutputID  string           `json:"outputId"`
			RowCount  int              `json:"rowCount"`
		}
		if err := json.Unmarshal([]byte(response.Body), &preview); err != nil {
			t.Fatal(err)
		}
		var pairs []string
		for _, row := range preview.Rows {
			pairs = append(pairs, fmt.Sprintf("%v/%v", row["c_patient"], row["observation_id"]))
		}
		sort.Strings(pairs)
		if preview.ReceiptID != receiptID || preview.OutputID != "patients" || preview.RowCount != len(want) || !reflect.DeepEqual(pairs, want) {
			t.Fatalf("real compiled route rows = %v (count=%d), want literal %v; body=%s", pairs, preview.RowCount, want, response.Body)
		}
	}
	assertRows(reconcile(3, digest), []string{"p1/o1", "p2/o2"})
	candidate, _ := json.Marshal(construction(routeB))
	proposalHTTP := requestJSON(t, app, http.MethodPost, base+"/construction-proposals", fmt.Sprintf(`{"snapshotToken":%q,"expectedDraftVersion":3,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"expand_observations","candidateConstruction":%s}`, snapshot.Token, digest, candidate))
	if proposalHTTP.StatusCode != http.StatusOK {
		t.Fatalf("route B proposal status=%d body=%s", proposalHTTP.StatusCode, proposalHTTP.Body)
	}
	var proposed loomapi.ConstructionProposalResponse
	if err := json.Unmarshal([]byte(proposalHTTP.Body), &proposed); err != nil {
		t.Fatal(err)
	}
	if proposed.ProposalId == nil || proposed.Preview == nil || proposed.Preview.ReceiptId != *proposed.ProposalId {
		t.Fatalf("proposal missing reviewed preview: %#v", proposed)
	}

	canceled := load()
	raw, _ := canceled.Workspace.CanonicalJSON()
	if canceled.Version != 3 || canceled.Digest != digest || !bytes.Equal(raw, original) {
		t.Fatal("unapplied proposal changed saved draft")
	}
	command := func(id string, version int64, expected string, commands string) string {
		return fmt.Sprintf(`{"commandId":%q,"semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":%s}`, id, ws.CurrentSemanticsVersion, snapshot.Token, version, expected, commands)
	}
	apply := fmt.Sprintf(`[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":%q}]`, *proposed.ProposalId)
	rejected := requestJSON(t, app, http.MethodPost, base+"/commands", command("stale-route-apply", 2, digest, apply))
	if rejected.StatusCode != http.StatusConflict {
		t.Fatalf("stale Apply status=%d body=%s", rejected.StatusCode, rejected.Body)
	}
	assertRoute(load(), routeA)
	appliedHTTP := requestJSON(t, app, http.MethodPost, base+"/commands", command("apply-route-b", 3, digest, apply))
	if appliedHTTP.StatusCode != http.StatusOK {
		t.Fatalf("Apply status=%d body=%s", appliedHTTP.StatusCode, appliedHTTP.Body)
	}
	applied := load()
	assertRoute(applied, routeB)
	if applied.Version != 4 || applied.Digest == digest || applied.Previous == "" {
		t.Fatalf("Apply lost CAS/Undo identity: %#v", applied)
	}
	if rowClient != nil {
		assertRows(reconcile(applied.Version, applied.Digest), []string{"p1/o2"})
	}
	replay := requestJSON(t, app, http.MethodPost, base+"/commands", command("apply-route-b", 3, digest, apply))
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("duplicate Apply status=%d body=%s", replay.StatusCode, replay.Body)
	}
	if got := load(); got.Version != applied.Version || got.Digest != applied.Digest {
		t.Fatal("duplicate Apply mutated saved draft")
	}
	restoredHTTP := requestJSON(t, app, http.MethodPost, base+"/commands", command("restore-route-a", applied.Version, applied.Digest, fmt.Sprintf(`[{"type":"RESTORE_DRAFT_REVISION","draftRevisionId":%q}]`, applied.Previous)))
	if restoredHTTP.StatusCode != http.StatusOK {
		t.Fatalf("Restore status=%d body=%s", restoredHTTP.StatusCode, restoredHTTP.Body)
	}
	restored := load()
	assertRoute(restored, routeA)
	restoredRaw, _ := restored.Workspace.CanonicalJSON()
	if restored.Version != 5 || restored.Digest != digest || !bytes.Equal(restoredRaw, original) {
		t.Fatalf("restore did not recover exact route-A workspace/version: %#v", restored)
	}
	lateApply := requestJSON(t, app, http.MethodPost, base+"/commands", command("late-route-b", 3, digest, apply))
	if lateApply.StatusCode != http.StatusConflict {
		t.Fatalf("late Apply after restore status=%d body=%s", lateApply.StatusCode, lateApply.Body)
	}
	if got := load(); got.Version != restored.Version || got.Digest != restored.Digest {
		t.Fatal("late Apply changed restored CAS")
	}
	assertRoute(load(), routeA)
	assertRows(reconcile(restored.Version, restored.Digest), []string{"p1/o1", "p2/o2"})
	stored, err := service.Get(context.Background(), owner.Project, owner.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ActiveRevisionID != owner.ActiveRevisionID {
		t.Fatal("draft roundtrip changed published revision")
	}
	if rowClient != nil {

		fresh, err := newTestExplorerArangoStore(rowClient)
		if err != nil {
			t.Fatal(err)
		}
		persisted, err := fresh.Get(context.Background(), owner.Project, owner.ExplorerID)
		if err != nil {
			t.Fatal(err)
		}
		persistedWorkspace, err := ws.DecodeWorkspace(persisted.DraftConfig)
		if err != nil {
			t.Fatal(err)
		}
		persistedRaw, err := persistedWorkspace.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if persisted.DraftVersion != restored.Version || persisted.DraftDigest != digest || !bytes.Equal(persistedRaw, original) {
			t.Fatalf("new Arango repository read lost restored draft: version=%d digest=%s workspace=%s", persisted.DraftVersion, persisted.DraftDigest, persistedRaw)
		}
		prior, err := fresh.GetDraftRevision(context.Background(), owner.Project, owner.ExplorerID, applied.Previous)
		if err != nil {
			t.Fatal(err)
		}
		priorWorkspace, err := ws.DecodeWorkspace(prior.DraftConfig)
		if err != nil {
			t.Fatal(err)
		}
		priorRaw, err := priorWorkspace.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if prior.DraftVersion != 3 || prior.DraftDigest != digest || !bytes.Equal(priorRaw, original) {
			t.Fatal("Arango history overwrote original route-A revision")
		}
	}

	if compiled < 3 {
		t.Fatal("initial, proposed or restored route bypassed production compiler")
	}
}

func TestRestoreDraftRevisionPublicAPIIsScopedAndReloadVisible(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	previous, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	previous.Explorer.Title = "Previous draft"
	previousRaw, err := previous.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	previousDigest, err := previous.Digest()
	if err != nil {
		t.Fatal(err)
	}
	current := previous
	current.Explorer.Title = "Current draft"
	currentRaw, err := current.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	currentDigest, err := current.Digest()
	if err != nil {
		t.Fatal(err)
	}

	store := explorerfixtures.NewStore()
	owner := explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: current.Explorer.Title,
		ManagementMode: viewer.ManagementInteractive, DraftConfig: currentRaw, DraftVersion: 4, DraftDigest: currentDigest,
		DraftSnapshotToken: snapshot.Token, DraftSourceGeneration: snapshot.Identity.Generation,
		DraftAuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		PreviousDraftRevisionID:       "draft_revision_custom_3", ActiveRevisionID: "active_published",
	}
	if _, err := store.Create(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	store.DraftRevisions[explorerfixtures.Key(owner.Project, owner.ExplorerID)+"\x00"+owner.PreviousDraftRevisionID] = explorer.DraftRevision{
		ID: owner.PreviousDraftRevisionID, Project: owner.Project, ExplorerID: owner.ExplorerID, DraftVersion: 3,
		DraftDigest: previousDigest, DraftConfig: previousRaw, Title: previous.Explorer.Title,
		SnapshotToken: snapshot.Token, SourceGeneration: snapshot.Identity.Generation,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
	}
	store.Revisions[owner.ActiveRevisionID] = explorer.Revision{
		ID: owner.ActiveRevisionID, Project: owner.Project, ExplorerID: owner.ExplorerID,
		SourceGeneration: snapshot.Identity.Generation, AuthoringBundle: previousRaw,
	}
	domain, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	config := lifecycle.Config{Capability: ports.CapabilityResolver{
		Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
		Token:   func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
		ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
		},
		Catalog: ws.CatalogFromCapability,
	}}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, domain, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"
	commandBody := fmt.Sprintf(`{"commandId":"restore-draft-3","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":4,"expectedDraftDigest":%q,"commands":[{"type":"RESTORE_DRAFT_REVISION","draftRevisionId":%q}]}`,
		ws.CurrentSemanticsVersion, snapshot.Token, currentDigest, owner.PreviousDraftRevisionID)
	response := requestJSON(t, app, http.MethodPost, basePath+"/commands", commandBody)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", response.StatusCode, response.Body)
	}
	var restored authoringv2.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(response.Body), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.DraftVersion != 5 || restored.Workspace.Explorer.Title != "Previous draft" || len(restored.Results) != 1 || restored.Results[0].Type != authoringv2.CommandResultDraftRestored {
		t.Fatalf("restore response = %#v", restored)
	}
	if restored.PreviousDraftRevisionID == "" || restored.PreviousDraftRevisionID == owner.PreviousDraftRevisionID {
		t.Fatalf("restore response did not expose the new Undo target: %#v", restored)
	}
	updated, err := domain.Get(context.Background(), owner.Project, owner.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ActiveRevisionID != owner.ActiveRevisionID || updated.DraftVersion != 5 {
		t.Fatalf("restore changed publication or missed CAS version: active=%q draftVersion=%d", updated.ActiveRevisionID, updated.DraftVersion)
	}

	assertReload := func() {
		t.Helper()
		builder := requestJSON(t, app, http.MethodGet, basePath+"/builder", "")
		if builder.StatusCode != http.StatusOK {
			t.Fatalf("reload status=%d body=%s", builder.StatusCode, builder.Body)
		}
		var loaded struct {
			Workspace               *ws.Workspace `json:"workspace"`
			DraftVersion            int64         `json:"draftVersion"`
			DraftDigest             string        `json:"draftDigest"`
			PreviousDraftRevisionID string        `json:"previousDraftRevisionId"`
		}
		if err := json.Unmarshal([]byte(builder.Body), &loaded); err != nil {
			t.Fatal(err)
		}
		if loaded.Workspace == nil || loaded.DraftVersion != 5 || loaded.DraftDigest != previousDigest || loaded.PreviousDraftRevisionID != restored.PreviousDraftRevisionID {
			t.Fatalf("reload lost restored draft identity or Undo target: %#v", loaded)
		}
		actual, err := loaded.Workspace.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, previousRaw) {
			t.Fatalf("fresh Builder read did not return the exact restored workspace: got=%s want=%s", actual, previousRaw)
		}
	}
	assertReload()

	replayed := requestJSON(t, app, http.MethodPost, basePath+"/commands", commandBody)
	if replayed.StatusCode != http.StatusOK || !containsJSONField(replayed.Body, "draftVersion", "5") {
		t.Fatalf("restore idempotency status=%d body=%s", replayed.StatusCode, replayed.Body)
	}

	assertReload()

	staleCASBody := fmt.Sprintf(`{"commandId":"stale-restore-cas","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":4,"expectedDraftDigest":%q,"commands":[{"type":"RESTORE_DRAFT_REVISION","draftRevisionId":%q}]}`,
		ws.CurrentSemanticsVersion, snapshot.Token, currentDigest, restored.PreviousDraftRevisionID)
	staleCAS := requestJSON(t, app, http.MethodPost, basePath+"/commands", staleCASBody)
	if staleCAS.StatusCode != http.StatusConflict || !strings.Contains(staleCAS.Body, `"code":"DRAFT_CONFLICT"`) {
		t.Fatalf("stale restore CAS status=%d body=%s", staleCAS.StatusCode, staleCAS.Body)
	}

	assertReload()

	store.Mu.Lock()
	key := explorerfixtures.Key(owner.Project, owner.ExplorerID) + "\x00" + restored.PreviousDraftRevisionID
	priorRevision := store.DraftRevisions[key]
	priorRevision.AuthorizationScopeDigest = "different-scope"
	store.DraftRevisions[key] = priorRevision
	store.Mu.Unlock()
	staleScopeBody := fmt.Sprintf(`{"commandId":"stale-restore-scope","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":5,"expectedDraftDigest":%q,"commands":[{"type":"RESTORE_DRAFT_REVISION","draftRevisionId":%q}]}`,
		ws.CurrentSemanticsVersion, snapshot.Token, restored.DraftDigest, restored.PreviousDraftRevisionID)
	staleScope := requestJSON(t, app, http.MethodPost, basePath+"/commands", staleScopeBody)
	if staleScope.StatusCode != http.StatusConflict || !strings.Contains(staleScope.Body, `"code":"STALE_DRAFT_SOURCE_CONTEXT"`) {
		t.Fatalf("stale restore source scope status=%d body=%s", staleScope.StatusCode, staleScope.Body)
	}
	assertReload()
}

func containsJSONField(raw, key, value string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil {
		return false
	}
	var decoded string
	if err := json.Unmarshal(fields[key], &decoded); err == nil {
		return decoded == value
	}
	return string(fields[key]) == value
}
