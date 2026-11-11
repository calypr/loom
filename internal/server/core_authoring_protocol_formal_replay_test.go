package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	arangofactory "github.com/calypr/loom/internal/backends/arango"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// Formal replay harness for the CoreAuthoringProtocol TLA model
// (verification/builder-interactions/CoreAuthoringProtocol.tla). The adapter
// scripts/lib/formal-core-protocol-replay.mjs schedules the exact server-side
// interleavings the model explores and validates the recorded wire observations
// against the model's invariants. This test never asserts on semantic anomalies:
// it records what the production handlers did and lets the model adjudicate.
//
// Plan C19 (required-store schedules): every schedule runs against the in-memory
// map store by default, and additionally against a real ArangoDB store owned by
// the test (same container-side pattern as TestCoreAuthoringProtocolAgainstArango)
// whenever LOOM_TEST_ARANGO_URL is set. Both modes execute one shared schedule
// body; the only store-coupled operations are the two persisted-state reads
// behind coreProtocolReplayStores, which go through each store's own read path
// in both modes (never a direct map iteration shared with Arango mode). The
// map-mode witness file keeps the exact adapter-pinned shape (schemaVersion +
// results with the registered observation keys, one row per case); when the
// Arango mode is active the witness additionally records the store mode per
// schedule because a single output file then carries both modes' rows.

const (
	coreProtocolReplayInputEnv  = "LOOM_FORMAL_CORE_PROTOCOL_INPUT"
	coreProtocolReplayOutputEnv = "LOOM_FORMAL_CORE_PROTOCOL_OUTPUT"
	// The concrete-prefix lane (plan C14) runs the SAME production-boundary
	// schedules with a concrete value-identity prefix A -> B -> A, where the
	// model's abstract head channels merge the two A histories; the concrete
	// wire record is what justifies that merge to scripts/lib/formal-core-
	// protocol-replay.mjs instead of a prose justification.
	coreProtocolConcreteInputEnv  = "LOOM_FORMAL_CORE_PROTOCOL_CONCRETE_INPUT"
	coreProtocolConcreteOutputEnv = "LOOM_FORMAL_CORE_PROTOCOL_CONCRETE_OUTPUT"
	// coreProtocolReplayArangoURLEnv is the same owned-stack endpoint contract
	// TestCoreAuthoringProtocolAgainstArango uses; absent means map-store mode.
	coreProtocolReplayArangoURLEnv = "LOOM_TEST_ARANGO_URL"

	coreProtocolReplayOwningStep    = "Replay all generated schedules and population attachment cases"
	coreProtocolReplayOwningCommand = "node scripts/verify-builder-formal-core.mjs --phase generated"
	coreProtocolReplayNotThisMode   = "core authoring protocol replay executes only under the harness: the required step \"" + coreProtocolReplayOwningStep + "\" in .github/workflows/builder-formal.yml runs `" + coreProtocolReplayOwningCommand + " --out <dir>` (adapter scripts/lib/formal-core-protocol-replay.mjs); ordinary go test carries no replay fixtures, and deleting that step or its harness wiring fails the Go unit tests job through TestCoreAuthoringProtocolReplayHarnessWired"

	coreProtocolReplayProject         = "project-a"
	coreProtocolReplayExplorerID      = "core-protocol"
	coreProtocolReplayDatabasePrefix  = "loom_core_authoring_replay_"
	coreProtocolReplayStoreNameMap    = "map"
	coreProtocolReplayStoreNameArango = "arango"
)

type coreProtocolReplayCase struct {
	ID              string `json:"id"`
	RTarget         int    `json:"rTarget"`
	STarget         int    `json:"sTarget"`
	SavePosition    string `json:"savePosition"`
	PreviewAfterAdv bool   `json:"previewAfterAdvance"`
}

type coreProtocolReplayEvent struct {
	Name   string         `json:"name"`
	Claims map[string]any `json:"claims"`
}

type coreProtocolReplayObservation struct {
	ID              string `json:"id"`
	ReconcileStatus int    `json:"reconcileStatus"`
	ReconcilePhase  string `json:"reconcilePhase"`
	ReceiptReported bool   `json:"receiptReported"`
	ReceiptEmitted  bool   `json:"receiptEmitted"`
	ReceiptLabel    string `json:"receiptLabel"`
	PreviewStatus   int    `json:"previewStatus"`
	PreviewOK       bool   `json:"previewOk"`
	PreviewLabel    string `json:"previewLabel"`
	SaveStatus      int    `json:"saveStatus"`
	SaveApplied     bool   `json:"saveApplied"`
	HeadVersion     int    `json:"headVersion"`
	HeadLabel       string `json:"headLabel"`
	Error           string `json:"error"`

	// Events is the ordered replay log: one entry per real call boundary of
	// this case, recorded at the awaited call site in production call order.
	// The adapter validates this stream against the model's legal transitions;
	// a skipped entry CAS or a receipt persisted without capture cannot produce
	// a legal log because each event is appended where the production handler
	// boundary it names actually completes (or is proven to have completed via
	// the existing beforeCompile/receipt-decode seams).
	Events []coreProtocolReplayEvent `json:"events"`

	reconcileReceiptID string
	notes              []string
}

const (
	coreProtocolInitialLabel   = "Patient ID"
	coreProtocolCompetingLabel = "Patient Competing B"
	coreProtocolActionSave     = "SaveDispatch"
	coreProtocolActionSaveAck  = "SaveAck"
	coreProtocolActionEntryAck = "REntryAccept"
	coreProtocolActionEntryRej = "REntryReject"
	coreProtocolActionCapture  = "RCapture"
	coreProtocolActionPersist  = "RPersist"
	coreProtocolActionComplete = "RComplete"
	coreProtocolActionPreview  = "PreviewExec"
	coreProtocolActionObserve  = "Observe"
)

func coreProtocolHeadForLabel(label string) int {
	if label == coreProtocolCompetingLabel {
		return 2
	}
	return 1
}

func (ob *coreProtocolReplayObservation) event(name string, claims map[string]any) int {
	ob.Events = append(ob.Events, coreProtocolReplayEvent{Name: name, Claims: claims})
	return len(ob.Events) - 1
}

// coreProtocolAbsentHeadDigest stands in for a head identity that was never
// persisted (model schedule S5: reconcile of a head the save never committed).
const coreProtocolAbsentHeadDigest = "sha256:formal-replay-absent-head-2-identity"

// coreProtocolConcreteProgram selects the concrete-prefix schedule. The plan's
// C14 row requires replaying the concrete A -> B -> A prefix whose two A
// histories the model's head channels merge; the program below drives exactly
// that prefix through the production handlers and records the wire evidence the
// validator needs (identity comparisons are adjudicated validator-side, never
// asserted here, so a semantically wrong server stays a red claim instead of
// aborting the witness).
type coreProtocolConcreteProgram struct {
	ID string `json:"id"`
}

// coreProtocolConcreteAttempt records one reconcile call against the
// production handler: the identity it presented and what the handler did.
type coreProtocolConcreteAttempt struct {
	RequestID    string `json:"requestId"`
	DraftVersion int64  `json:"draftVersion"`
	DraftDigest  string `json:"draftDigest"`
	Success      bool   `json:"success"`
	HeadVersion  int    `json:"headVersion"`
	HeadDigest   string `json:"headDigest"`
	HasReceipt   bool   `json:"hasReceipt"`
	ReceiptID    string `json:"receiptId,omitempty"`
}

// coreProtocolConcreteRevision captures one acknowledged draft revision read
// back through the store seam, so value-equal heads (merged by the model) can
// be proven persisted as distinct revisions.
type coreProtocolConcreteRevision struct {
	RevisionID   string `json:"revisionId"`
	Found        bool   `json:"found"`
	DraftVersion int    `json:"draftVersion"`
	DraftDigest  string `json:"draftDigest"`
}

// coreProtocolConcreteObservation is the wire record of the concrete-prefix
// schedule. The merged-value evidence is value-level, not digest-level: the
// save path re-canonicalizes through MigrateLegacyContributors,
// MigrateLosslessDefaults and NormalizePresentationOrders
// (ApplyCommands, internal/explorer/authoringv2/commands.go), so a value-equal
// revert stores a distinct digest; the value identity that the model's head
// channels merge is witnessed by the emitted receipt label of head 3 equaling
// the seed column label while the three stored digests stay pairwise distinct.
// The record also carries the three stale rejections (both-stale plus the
// two single-coordinate probes isolating each entry CAS clause), the admitted
// fresh A, the replayed request, the preview bound to the persisted receipt,
// and the two acknowledged revisions.
type coreProtocolConcreteObservation struct {
	ID             string                         `json:"id"`
	SeedVersion    int                            `json:"seedVersion"`
	SeedDigest     string                         `json:"seedDigest"`
	SaveBStatus    int                            `json:"saveBStatus"`
	SaveBDigest    string                         `json:"saveBDigest"`
	SaveAStatus    int                            `json:"saveAStatus"`
	SaveAVersion   int                            `json:"saveAVersion"`
	SaveADigest    string                         `json:"saveADigest"`
	MergeValueZone bool                           `json:"mergeValueZone"`
	StaleAttempt   coreProtocolConcreteAttempt    `json:"staleAttempt"`
	Stale2Attempt  coreProtocolConcreteAttempt    `json:"stale2Attempt"`
	Stale3Attempt  coreProtocolConcreteAttempt    `json:"stale3Attempt"`
	ValidAttempt   coreProtocolConcreteAttempt    `json:"validAttempt"`
	ReplayAttempt  coreProtocolConcreteAttempt    `json:"replayAttempt"`
	PreviewStatus  int                            `json:"previewStatus"`
	PreviewReceipt string                         `json:"previewReceipt,omitempty"`
	ValidLabel     string                         `json:"validLabel,omitempty"`
	Revisions      []coreProtocolConcreteRevision `json:"revisions"`
	StaleError     string                         `json:"staleError,omitempty"`
	Stale2Error    string                         `json:"stale2Error,omitempty"`
	Stale3Error    string                         `json:"stale3Error,omitempty"`
	StaleNotes     []string                       `json:"staleNotes,omitempty"`
	Notes          []string                       `json:"notes,omitempty"`
}

func (ob *coreProtocolConcreteObservation) note(format string, args ...any) {
	ob.Notes = append(ob.Notes, fmt.Sprintf(format, args...))
}

func (ob *coreProtocolReplayObservation) note(format string, args ...any) {
	ob.notes = append(ob.notes, fmt.Sprintf(format, args...))
	if ob.Error == "" {
		ob.Error = ob.notes[0]
		return
	}
	for _, prior := range ob.notes[:len(ob.notes)-1] {
		if prior == ob.notes[len(ob.notes)-1] {
			return
		}
	}
	ob.Error = ob.notes[len(ob.notes)-1]
}

// coreProtocolReplayStores is the single parameterization seam of the shared
// schedule body: the fixture under replay plus the two persisted-state reads
// the observation records. Map mode binds them to the test store's own read
// path; Arango mode binds them to the Arango repository's read path. The
// schedule logic itself has exactly one implementation.
type coreProtocolReplayStores struct {
	mode    string
	fixture *coreProtocolFixture
	// receipt reads one persisted receipt identity through the store interface
	// (map: testExplorerStore.GetCompilationReceiptForExplorer; arango: the
	// repository read that admits receipts through validateReceipt), mirroring
	// the previous direct fixture.store.receipts lookup without a map-only
	// assumption.
	receipt func(context.Context, string) (*explorer.CompilationReceipt, bool)
	// hasReceipts reports whether the store holds any receipt for this case's
	// explorer identity, replacing the direct len(fixture.store.receipts) read.
	hasReceipts func(context.Context) bool
	// history reads one acknowledged draft revision back through the store's
	// own read path (map: testExplorerStore.GetDraftRevision; arango: the
	// repository's GetDraftRevision), used by the concrete-prefix lane to
	// prove value-equal heads persisted as distinct revisions.
	history func(context.Context, string) (explorer.DraftRevision, bool)
}

// coreProtocolReplayMapStores wires the schedule body to the in-memory map
// store fixture exactly as it ran before the required-store extension.
func coreProtocolReplayMapStores(t *testing.T) coreProtocolReplayStores {
	t.Helper()
	fixture := newCoreProtocolFixture(t)
	store := fixture.store
	return coreProtocolReplayStores{
		mode:    coreProtocolReplayStoreNameMap,
		fixture: fixture,
		receipt: func(ctx context.Context, id string) (*explorer.CompilationReceipt, bool) {
			receipt, err := store.GetCompilationReceiptForExplorer(ctx, coreProtocolReplayProject, coreProtocolReplayExplorerID, id)
			if err != nil {
				return nil, false
			}
			return receipt, true
		},
		hasReceipts: func(context.Context) bool { return store.countCompilationReceipts() > 0 },
		history: func(ctx context.Context, revisionID string) (explorer.DraftRevision, bool) {
			revision, err := store.GetDraftRevision(ctx, coreProtocolReplayProject, coreProtocolReplayExplorerID, revisionID)
			if err != nil || revision == nil {
				return explorer.DraftRevision{}, false
			}
			return *revision, true
		},
	}
}

// coreProtocolReplayArangoStores wires the schedule body to the real Arango
// store. Fixture wiring is the exact same mount as newCoreProtocolFixture
// (fiber + generated strict adapter routes + authscope.AllowAllAuthorizer +
// no-op dataframe execution engine + domain through explorer.NewService), and
// the seed goes through store.Create with the identical workspace/snapshot
// bytes and the identical project-a/core-protocol identity: the workspace,
// capability snapshot and initial digest are taken from the map fixture
// itself, so both modes describe exactly the same protocol identity. Isolation
// between schedules comes from the per-schedule disposable database, not from
// identity mutation: the capability snapshot pins its identity project
// (compileExplorerReceipt rejects any request whose project does not canonicalize
// to the snapshot's, so identities must stay exactly at the map fixture's).
func coreProtocolReplayArangoStores(t *testing.T, client *arangofactory.Client, graph *arangofactory.ExplorerStore, database string) coreProtocolReplayStores {
	t.Helper()
	// The map fixture is the single source for the seed bytes; constructing it
	// and discarding its app/store costs one rebuild and guarantees the seeded
	// DraftConfig, snapshot token, generation and digest equal map mode's.
	probe := newCoreProtocolFixture(t)
	workspace, snapshot, digest := probe.workspace, probe.snapshot, probe.initialDigest
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	seedCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := graph.Create(seedCtx, explorer.Explorer{
		Project: coreProtocolReplayProject, ExplorerID: coreProtocolReplayExplorerID, Title: "Patients", ManagementMode: explorer.ManagementInteractive,
		DraftConfig: raw, DraftVersion: 1, DraftDigest: digest,
		DraftSnapshotToken: snapshot.Token, DraftSourceGeneration: snapshot.Identity.Generation,
		DraftAuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
	}); err != nil {
		t.Fatalf("seed arango explorer %s/%s: %v", coreProtocolReplayProject, coreProtocolReplayExplorerID, err)
	}
	domain, err := explorer.NewService(graph)
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
	fixture := &coreProtocolFixture{
		t: t, snapshot: snapshot, workspace: workspace, initialDigest: digest,
		basePath: fmt.Sprintf("/api/v1/projects/%s/explorers/%s/authoring/v2", coreProtocolReplayProject, coreProtocolReplayExplorerID),
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
			if fixture.beforeCompile != nil {
				fixture.beforeCompile()
			}
			return compileExplorerReceipt(ctx, request, nil, engine, domain, nil, nil)
		},
		ReceiptLookup: func(ctx context.Context, requestProject, requestExplorerID, receiptID string) (*explorer.CompilationReceipt, error) {
			return domain.CompilationReceiptForExplorer(ctx, requestProject, requestExplorerID, receiptID)
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
	t.Logf("core protocol replay store=arango: disposable database=%s project=%s explorerId=%s (identity bytes identical to map mode; isolation comes from the per-schedule database)", database, coreProtocolReplayProject, coreProtocolReplayExplorerID)
	t.Log("store=arango documented divergence: draft revision ids are sha256-derived by the Arango store (internal/backends/arango/internal/explorer/drafts.go via key() in store.go), not the map store's draft_revision_core-protocol_N literal; the replay body never hardcodes a revision id, so both modes drive head identity solely from the save acknowledgement and stored bytes")
	return coreProtocolReplayStores{
		mode:    coreProtocolReplayStoreNameArango,
		fixture: fixture,
		receipt: func(_ context.Context, id string) (*explorer.CompilationReceipt, bool) {
			readCtx, readCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer readCancel()
			receipt, err := graph.GetCompilationReceiptForExplorer(readCtx, coreProtocolReplayProject, coreProtocolReplayExplorerID, id)
			if err != nil {
				return nil, false
			}
			return receipt, true
		},
		hasReceipts: func(context.Context) bool {
			readCtx, readCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer readCancel()
			return countDocuments(readCtx, t, client, arangofactory.CompilationReceiptsCollection, coreProtocolReplayProject, coreProtocolReplayExplorerID) > 0
		},
		history: func(_ context.Context, revisionID string) (explorer.DraftRevision, bool) {
			readCtx, readCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer readCancel()
			revision, err := graph.GetDraftRevision(readCtx, coreProtocolReplayProject, coreProtocolReplayExplorerID, revisionID)
			if err != nil || revision == nil {
				return explorer.DraftRevision{}, false
			}
			return *revision, true
		},
	}
}

// countCompilationReceipts answers the Observe hasReceipts claim through the
// test store's own lock, mirroring how the Arango mode counts receipt documents
// through a raw AQL read instead of reaching into map internals.
func (s *testExplorerStore) countCompilationReceipts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.receipts)
}

func runCoreProtocolReplayCase(t *testing.T, replayCase coreProtocolReplayCase, stores coreProtocolReplayStores) coreProtocolReplayObservation {
	t.Helper()
	fixture := stores.fixture
	observation := coreProtocolReplayObservation{ID: replayCase.ID, HeadVersion: -1, Events: []coreProtocolReplayEvent{}}

	_, hydrated := fixture.getBuilder()
	observation.event(coreProtocolActionObserve, map[string]any{"headVersion": int(hydrated.DraftVersion)})
	if hydrated.DraftVersion != 1 || hydrated.DraftDigest != fixture.initialDigest {
		observation.note("unexpected initial hydration: %+v", hydrated)
		return observation
	}

	compileEntered := make(chan struct{})
	compileRelease := make(chan struct{})
	var compileOnce sync.Once
	if replayCase.SavePosition == "in-compile" {
		fixture.beforeCompile = func() {
			compileOnce.Do(func() {
				close(compileEntered)
				<-compileRelease
			})
		}
	}

	savedAck := loomapi.ApplyCommandsResponse{}
	commitSave := func() {
		expectedVersion := int64(1)
		if replayCase.STarget == 2 {
			// A stale client believes the head is already 2; the server CAS must reject.
			expectedVersion = 2
		}
		saved, ack := fixture.saveLabel("formal-save-"+replayCase.ID, "Patient Competing B", expectedVersion, fixture.initialDigest)
		observation.SaveStatus = saved.StatusCode
		observation.SaveApplied = saved.StatusCode == http.StatusOK && ack.DraftVersion == 2 &&
			ack.DraftDigest != "" && ack.DraftDigest != fixture.initialDigest
		if observation.SaveApplied {
			savedAck = ack
		}
		observation.event(coreProtocolActionSave, map[string]any{"status": saved.StatusCode, "applied": observation.SaveApplied})
		if observation.SaveApplied {
			observation.event(coreProtocolActionSaveAck, map[string]any{"headVersion": int(ack.DraftVersion)})
		}
		if saved.StatusCode != http.StatusOK && saved.StatusCode != http.StatusConflict {
			observation.note("unexpected commands status=%d body=%s", saved.StatusCode, saved.Body)
		}
	}
	reconcileVersion := int64(1)
	reconcileDigest := fixture.initialDigest
	if replayCase.SavePosition == "before-entry" {
		commitSave()
	}
	// The reconcile click targets exactly the model-captured head identity:
	// head 1 is the seeded revision, head 2 exists only once a save committed it
	// (before-entry) and its digest is then only knowable from the acknowledgement.
	// Reconciling an uncommitted head 2 uses a stand-in digest nobody ever stored.
	if replayCase.RTarget == 2 {
		reconcileVersion = 2
		switch {
		case replayCase.SavePosition == "before-entry" && savedAck.DraftVersion == 2 && savedAck.DraftDigest != "":
			reconcileDigest = savedAck.DraftDigest
		default:
			reconcileDigest = coreProtocolAbsentHeadDigest
		}
	}
	// Model schedules where reconcile targets a head no save committed (S5).
	if replayCase.RTarget == 2 && replayCase.SavePosition != "before-entry" {
		reconcileVersion, reconcileDigest = 2, coreProtocolAbsentHeadDigest
	}

	var reconcileResponse testHTTPResponse
	entryEventIndex := -1
	if replayCase.SavePosition == "in-compile" {
		reconcileResult := make(chan coreProtocolReplayReconcile, 1)
		go func() {
			response, err := fixture.reconcile(reconcileVersion, reconcileDigest, "formal-reconcile-"+replayCase.ID)
			if err != nil {
				response = testHTTPResponse{StatusCode: -1, Body: err.Error()}
			}
			reconcileResult <- coreProtocolReplayReconcile{response: response}
		}()
		select {
		case <-compileEntered:
			// The existing compile seam only fires after the production entry CAS
			// validated the requested identity and the handler decoded (captured)
			// the saved workspace (authoring.go:552-564), so entry acceptance and
			// capture have really completed at this awaited boundary. The captured
			// head claim is filled from the persisted receipt evidence below.
			entryEventIndex = observation.event(coreProtocolActionEntryAck, map[string]any{"captured": 0})
			observation.event(coreProtocolActionCapture, map[string]any{})
		case <-time.After(15 * time.Second):
			observation.note("compile seam not reached for %s", replayCase.ID)
			close(compileRelease)
			return observation
		}
		commitSave()
		close(compileRelease)
		result := <-reconcileResult
		reconcileResponse = result.response
	} else {
		response, err := fixture.reconcile(reconcileVersion, reconcileDigest, "formal-reconcile-"+replayCase.ID)
		if err != nil {
			response = testHTTPResponse{StatusCode: -1, Body: err.Error()}
		}
		reconcileResponse = response
	}
	observation.ReconcileStatus = reconcileResponse.StatusCode
	switch reconcileResponse.StatusCode {
	case http.StatusOK:
		observation.ReconcilePhase = "completed"
		var compiled loomapi.CompileResponse
		if err := json.Unmarshal([]byte(reconcileResponse.Body), &compiled); err != nil {
			observation.note("decode compile: %v body=%s", err, reconcileResponse.Body)
		} else if compiled.ReceiptId == "" {
			observation.note("completed reconcile without receipt id: %s", reconcileResponse.Body)
		} else {
			observation.reconcileReceiptID = compiled.ReceiptId
			observation.ReceiptReported = true
			// Persisted-receipt evidence is read through the replay store seam
			// (store interface in both modes), never a map-store internals read.
			if receipt, ok := stores.receipt(context.Background(), compiled.ReceiptId); ok {
				observation.ReceiptEmitted = true
				for _, column := range receipt.EmittedColumns {
					if column.OutputID == "patients" && column.PublicColumn == "c_patient" {
						observation.ReceiptLabel = column.Label
					}
				}
				if observation.ReceiptLabel == "" {
					observation.note("persisted receipt %s lacks emitted patient column", compiled.ReceiptId)
				}
			}
		}
		// The reconcile entered and captured for real (a 200 with a receipt id only
		// follows the accepted entry CAS, decode and compile chain); claim the
		// captured head from the persisted receipt's own label evidence, never from
		// the schedule. An unbacked capture stays claimed 0 so the validator rejects
		// it instead of trusting a completed-looking response.
		capturedClaim := 0
		if observation.ReceiptEmitted && observation.ReceiptLabel != "" {
			capturedClaim = coreProtocolHeadForLabel(observation.ReceiptLabel)
		}
		if replayCase.SavePosition != "in-compile" {
			observation.event(coreProtocolActionEntryAck, map[string]any{"captured": capturedClaim})
			observation.event(coreProtocolActionCapture, map[string]any{})
		} else if entryEventIndex >= 0 {
			observation.Events[entryEventIndex].Claims["captured"] = capturedClaim
		}
		if observation.ReceiptEmitted {
			observation.event(coreProtocolActionPersist, map[string]any{"receiptLabel": observation.ReceiptLabel})
		}
		if observation.ReceiptReported {
			observation.event(coreProtocolActionComplete, map[string]any{"status": reconcileResponse.StatusCode})
		}
	case http.StatusConflict:
		observation.ReconcilePhase = "rejected"
		if replayCase.SavePosition != "in-compile" {
			observation.event(coreProtocolActionEntryRej, map[string]any{"status": reconcileResponse.StatusCode})
		}
		observeReconcileConflictBody(t, &observation, reconcileResponse.Body)
	default:
		observation.ReconcilePhase = "rejected"
		if replayCase.SavePosition != "in-compile" {
			observation.event(coreProtocolActionEntryRej, map[string]any{"status": reconcileResponse.StatusCode})
		}
		observation.note("reconcile status=%d body=%s", reconcileResponse.StatusCode, reconcileResponse.Body)
	}
	if observation.reconcileReceiptID != "" {
		runCoreProtocolReplayPreview(t, fixture, &observation, "first")
		if replayCase.PreviewAfterAdv {
			runCoreProtocolReplayPreview(t, fixture, &observation, "after-advance")
		}
	}

	headResponse, head := fixture.getBuilder()
	observation.HeadVersion = int(head.DraftVersion)
	labels := coreProtocolWorkspaceLabels(coreProtocolDecodeWorkspace(fixture.t, headResponse.Body, "workspace"), "patients")
	if len(labels) == 1 {
		observation.HeadLabel = labels[0]
	}
	observation.event(coreProtocolActionObserve, map[string]any{"headVersion": observation.HeadVersion, "hasReceipts": stores.hasReceipts(context.Background())})
	return observation
}

type coreProtocolReplayReconcile struct {
	response testHTTPResponse
}

func runCoreProtocolReplayPreview(t *testing.T, fixture *coreProtocolFixture, observation *coreProtocolReplayObservation, _ string) {
	t.Helper()
	response := fixture.preview(observation.reconcileReceiptID)
	observation.PreviewStatus = response.StatusCode
	var preview loomapi.PreviewResponse
	if err := json.Unmarshal([]byte(response.Body), &preview); err != nil {
		observation.note("decode preview: %v body=%s", err, response.Body)
		observation.PreviewOK = false
		observation.PreviewLabel = ""
		observation.event(coreProtocolActionPreview, map[string]any{
			"receiptLabel": observation.ReceiptLabel, "status": response.StatusCode, "previewLabel": "",
		})
		return
	}
	var labels []string
	for _, column := range preview.Columns {
		labels = append(labels, column.Label)
	}
	if len(labels) == 1 {
		observation.PreviewLabel = labels[0]
	} else if len(labels) > 1 {
		joined := ""
		for index, label := range labels {
			if index > 0 {
				joined += "|"
			}
			joined += label
		}
		observation.PreviewLabel = joined
	} else {
		observation.PreviewLabel = ""
	}
	observation.PreviewOK = response.StatusCode == http.StatusOK &&
		preview.ReceiptId == observation.reconcileReceiptID &&
		preview.OutputId == "patients" &&
		preview.Rows != nil &&
		observation.PreviewLabel != ""
	observation.event(coreProtocolActionPreview, map[string]any{
		"receiptLabel": observation.ReceiptLabel,
		"status":       response.StatusCode,
		"previewLabel": observation.PreviewLabel,
	})
}

func observeReconcileConflictBody(t *testing.T, observation *coreProtocolReplayObservation, body string) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Logf("conflict body not an error envelope: %s", body)
	}
}

// runCoreProtocolReplayAgainstArango owns the container-side Arango pattern of
// TestCoreAuthoringProtocolAgainstArango: endpoint from LOOM_TEST_ARANGO_URL, a
// per-test disposable database auto-created by arangofactory.Open, t.Cleanup that
// closes with a fresh context and then raw-DELETEs the database (t.Error on
// failure, still attempting the drop), Bootstrap + arangofactory.NewExplorerStore, and the
// schedules S1-S8 executed through the SAME shared replay body as map mode.
func runCoreProtocolReplayAgainstArango(t *testing.T, replayCases []coreProtocolReplayCase, endpoint string, observations *[]coreProtocolReplayObservation) {
	t.Helper()
	for _, replayCase := range replayCases {
		replayCase := replayCase
		t.Run(replayCase.ID, func(t *testing.T) {
			// Every schedule gets its own auto-created disposable database, so a
			// real Arango run replays each case against exactly the state the map
			// mode's fresh per-case store holds.
			database := coreProtocolReplayDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: endpoint, Database: database})
			if err != nil {
				t.Fatal(err)
			}
			client := arangoBackend.Client
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
			if err := client.Bootstrap(ctx, arangofactory.ExplorerBootstrapSpec()); err != nil {
				t.Fatal(err)
			}
			graph, err := arangofactory.NewExplorerStore(client)
			if err != nil {
				t.Fatal(err)
			}
			*observations = append(*observations, runCoreProtocolReplayCase(t, replayCase, coreProtocolReplayArangoStores(t, client, graph, database)))
		})
	}
}

// coreProtocolReplayWitnessRow is only used for witness files that carry more
// than one store mode: the map-only record keeps the exact adapter-pinned
// observation shape (formal-core-protocol-replay.mjs checkCoreProtocolResults
// pins the key set and one row per case), while a dual-mode record tags every
// per-schedule row with the store that produced it.
type coreProtocolReplayWitnessRow struct {
	coreProtocolReplayObservation
	Store string `json:"store"`
}

func TestCoreAuthoringProtocolModelReplay(t *testing.T) {
	inputPath := os.Getenv(coreProtocolReplayInputEnv)
	outputPath := os.Getenv(coreProtocolReplayOutputEnv)
	if inputPath == "" && outputPath == "" {
		t.Skip(coreProtocolReplayNotThisMode)
	}
	if inputPath == "" || outputPath == "" {
		t.Fatalf("%s and %s must name the replay case input and observation output", coreProtocolReplayInputEnv, coreProtocolReplayOutputEnv)
	}
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read replay input: %v", err)
	}
	var replayCases []coreProtocolReplayCase
	if err := json.Unmarshal(raw, &replayCases); err != nil {
		t.Fatalf("decode replay cases: %v", err)
	}
	if len(replayCases) == 0 {
		t.Fatal("replay input contains no cases")
	}
	arangoEndpoint := strings.TrimSpace(os.Getenv(coreProtocolReplayArangoURLEnv))
	if arangoEndpoint != "" {
		t.Log("core protocol replay witness: LOOM_TEST_ARANGO_URL is set, every schedule runs in both store modes; each witness row records its store mode (map-only runs keep the adapter-pinned row shape)")
	}

	mapObservations := make([]coreProtocolReplayObservation, 0, len(replayCases))
	t.Run("store="+coreProtocolReplayStoreNameMap, func(t *testing.T) {
		for _, replayCase := range replayCases {
			replayCase := replayCase
			t.Run(replayCase.ID, func(t *testing.T) {
				t.Log("core protocol replay store mode: map (in-memory CAS test store)")
				mapObservations = append(mapObservations, runCoreProtocolReplayCase(t, replayCase, coreProtocolReplayMapStores(t)))
			})
		}
	})
	arangoObservations := make([]coreProtocolReplayObservation, 0, len(replayCases))
	if arangoEndpoint != "" {
		t.Run("store="+coreProtocolReplayStoreNameArango, func(t *testing.T) {
			runCoreProtocolReplayAgainstArango(t, replayCases, arangoEndpoint, &arangoObservations)
		})
	}

	var record any
	if arangoEndpoint == "" {
		record = struct {
			SchemaVersion int                             `json:"schemaVersion"`
			Results       []coreProtocolReplayObservation `json:"results"`
		}{SchemaVersion: 1, Results: mapObservations}
	} else {
		rows := make([]coreProtocolReplayWitnessRow, 0, len(mapObservations)+len(arangoObservations))
		for _, observation := range mapObservations {
			rows = append(rows, coreProtocolReplayWitnessRow{coreProtocolReplayObservation: observation, Store: coreProtocolReplayStoreNameMap})
		}
		for _, observation := range arangoObservations {
			rows = append(rows, coreProtocolReplayWitnessRow{coreProtocolReplayObservation: observation, Store: coreProtocolReplayStoreNameArango})
		}
		record = struct {
			SchemaVersion int                            `json:"schemaVersion"`
			Results       []coreProtocolReplayWitnessRow `json:"results"`
		}{SchemaVersion: 1, Results: rows}
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("encode observations: %v", err)
	}
	temporaryPath := outputPath + ".tmp"
	if err := os.WriteFile(temporaryPath, encoded, 0o644); err != nil {
		t.Fatalf("write observations: %v", err)
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		t.Fatalf("publish observations: %v", err)
	}
}

// derefRevisionID reads one optional acknowledged revision identity. Revision
// addressing is always through the identity the server itself acknowledged
// (previousDraftRevisionId), never a constructed literal, so the same wire
// evidence holds in map and Arango modes where revision ids differ.
func derefRevisionID(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// runCoreProtocolReplayConcreteCase drives the plan-C14 value-identity prefix
// A -> B -> A (save-to-B, value-equal revert back to A) through the production
// handlers on one store seam, then attempts the merged-value schedules: a
// stale reconcile presenting the head-1 identity while the server head is the
// value-equal head 3, a fresh reconcile at the real head identity, a
// same-request replay, the preview bound to the persisted receipt, and the
// read-back of both acknowledged revisions. It records wire facts only; every
// identity comparison is adjudicated validator-side so a semantically wrong
// server produces a red claim instead of aborting the witness.
func runCoreProtocolReplayConcreteCase(program coreProtocolConcreteProgram, stores coreProtocolReplayStores) coreProtocolConcreteObservation {
	fixture := stores.fixture
	observation := coreProtocolConcreteObservation{ID: program.ID}
	headResponse, head := fixture.getBuilder()
	observation.SeedVersion = int(head.DraftVersion)
	observation.SeedDigest = head.DraftDigest
	if head.DraftVersion != 1 || head.DraftDigest != fixture.initialDigest {
		observation.note("unexpected concrete seed: version=%d digest=%q", head.DraftVersion, head.DraftDigest)
	}
	_ = headResponse

	// A -> B: the first real save, CASed on the seeded identity. The digest is
	// sha256 of canonical workspace JSON (internal/explorer/authoringv2/canonical.go),
	// so B carries a distinct digest.
	saveB, ackB := fixture.saveLabel("formal-c14-"+program.ID+"-b", coreProtocolCompetingLabel, 1, fixture.initialDigest)
	observation.SaveBStatus = saveB.StatusCode
	observation.SaveBDigest = ackB.DraftDigest
	if saveB.StatusCode != http.StatusOK || ackB.DraftVersion != 2 || ackB.DraftDigest == "" || ackB.DraftDigest == fixture.initialDigest {
		observation.note("save B rejected: status=%d ack=%+v body=%s", saveB.StatusCode, ackB, saveB.Body)
	}

	// B -> A: a fresh save of the ORIGINAL column value, so head 3 and head 1
	// carry the same authored value (label 'Patient ID'). Whether the stored
	// digest of that revert equals the seed digest is a property of the save
	// path's canonicalization, not of value identity: ApplyCommands migrates
	// and normalizes before hashing, so the stored digests of the two
	// value-equal heads legitimately differ (observed). mergeValueZone
	// records the digest-level fact verbatim; the value-level identity the
	// model merges is witnessed by the emitted receipt label (validLabel).
	// The stale client below presents exactly the head-1 identity while the
	// server head is version 3.
	saveA, ackA := fixture.saveLabel("formal-c14-"+program.ID+"-a", coreProtocolInitialLabel, 2, ackB.DraftDigest)
	observation.SaveAStatus = saveA.StatusCode
	observation.SaveAVersion = int(ackA.DraftVersion)
	observation.SaveADigest = ackA.DraftDigest
	if saveA.StatusCode != http.StatusOK || ackA.DraftVersion != 3 || ackA.DraftDigest == "" {
		observation.note("save A rejected: status=%d ack=%+v body=%s", saveA.StatusCode, ackA, saveA.Body)
	}
	observation.MergeValueZone = saveA.StatusCode == http.StatusOK && ackA.DraftDigest == fixture.initialDigest

	// Three stale identities, all attempted BEFORE any receipt exists, so the
	// only production mechanism that can own the head for any of them is the
	// reconcile entry CAS (authoring.go): the realistic both-stale value-equal
	// probe (stored head-1 version and digest) plus the two single-coordinate
	// probes that make the CAS's two clauses independently falsifiable. If a
	// fault removes only the version clause the current-version/stale-digest
	// probe is still rejected by digest and the stale-version/current-digest
	// probe alone is admitted; removing only the digest clause isolates the
	// other clause exactly. All other schedules keep their 409s under either
	// single-clause fault (the replay S lane's rejected schedules present
	// coreProtocolAbsentHeadDigest, and every completed identity matches on
	// both coordinates), so each probe is the unique falsifier of its clause.
	staleProbe := func(version int64, digest, suffix string) (coreProtocolConcreteAttempt, string) {
		response, responseErr := fixture.reconcile(version, digest, "formal-c14-"+program.ID+"-"+suffix)
		if responseErr != nil {
			response = testHTTPResponse{StatusCode: -1, Body: responseErr.Error()}
		}
		_, headAfter := fixture.getBuilder()
		attempt := coreProtocolConcreteAttempt{
			RequestID:    "formal-c14-" + program.ID + "-" + suffix,
			DraftVersion: version,
			DraftDigest:  digest,
			Success:      response.StatusCode == http.StatusOK,
			HeadVersion:  int(headAfter.DraftVersion),
			HeadDigest:   headAfter.DraftDigest,
			HasReceipt:   stores.hasReceipts(context.Background()),
		}
		code := ""
		if response.StatusCode != http.StatusConflict {
			observation.note("%s reconcile status=%d body=%s head=%d/%q", suffix, response.StatusCode, response.Body, headAfter.DraftVersion, headAfter.DraftDigest)
		} else {
			var envelope struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(response.Body), &envelope); err != nil {
				observation.StaleNotes = append(observation.StaleNotes, fmt.Sprintf("%s conflict body not an error envelope: %s", suffix, response.Body))
			} else {
				code = envelope.Error.Code
				if code != "DRAFT_CONFLICT" {
					observation.StaleNotes = append(observation.StaleNotes, fmt.Sprintf("%s conflict code=%q", suffix, code))
				}
			}
		}
		return attempt, code
	}
	observation.StaleAttempt, observation.StaleError = staleProbe(1, fixture.initialDigest, "stale")
	observation.Stale2Attempt, observation.Stale2Error = staleProbe(3, fixture.initialDigest, "stale2")
	observation.Stale3Attempt, observation.Stale3Error = staleProbe(1, ackA.DraftDigest, "stale3")

	// Valid fresh-A reconcile at the real head 3 identity.
	valid, validErr := fixture.reconcile(3, ackA.DraftDigest, "formal-c14-"+program.ID+"-valid")
	if validErr != nil {
		valid = testHTTPResponse{StatusCode: -1, Body: validErr.Error()}
	}
	_, headAfterValid := fixture.getBuilder()
	observation.ValidAttempt = coreProtocolConcreteAttempt{
		RequestID:    "formal-c14-" + program.ID + "-valid",
		DraftVersion: 3,
		DraftDigest:  ackA.DraftDigest,
		Success:      valid.StatusCode == http.StatusOK,
		HeadVersion:  int(headAfterValid.DraftVersion),
		HeadDigest:   headAfterValid.DraftDigest,
	}
	var compiled loomapi.CompileResponse
	if valid.StatusCode == http.StatusOK {
		if err := json.Unmarshal([]byte(valid.Body), &compiled); err != nil {
			observation.note("decode valid compile: %v body=%s", err, valid.Body)
		} else {
			observation.ValidAttempt.ReceiptID = compiled.ReceiptId
			if receipt, ok := stores.receipt(context.Background(), compiled.ReceiptId); ok && receipt != nil {
				observation.ValidAttempt.HasReceipt = true
				for _, column := range receipt.EmittedColumns {
					if column.OutputID == "patients" && column.PublicColumn == "c_patient" {
						observation.ValidLabel = column.Label
					}
				}
				if observation.ValidLabel == "" {
					observation.note("valid receipt %s lacks emitted column c_patient", compiled.ReceiptId)
				}
			}
		}
	} else {
		observation.note("fresh-A reconcile status=%d body=%s", valid.StatusCode, valid.Body)
	}

	// Same-request replay of the fresh-A reconcile (idempotence boundary). For
	// a digest-keyed receipt identity this must not produce a second head or a
	// divergent head identity; the validator compares this attempt against the
	// valid one.
	replay, replayErr := fixture.reconcile(3, ackA.DraftDigest, "formal-c14-"+program.ID+"-valid")
	if replayErr != nil {
		replay = testHTTPResponse{StatusCode: -1, Body: replayErr.Error()}
	}
	_, headAfterReplay := fixture.getBuilder()
	observation.ReplayAttempt = coreProtocolConcreteAttempt{
		RequestID:    "formal-c14-" + program.ID + "-valid",
		DraftVersion: 3,
		DraftDigest:  ackA.DraftDigest,
		Success:      replay.StatusCode == http.StatusOK,
		HeadVersion:  int(headAfterReplay.DraftVersion),
		HeadDigest:   headAfterReplay.DraftDigest,
	}
	if replay.StatusCode == http.StatusOK {
		var replayed loomapi.CompileResponse
		if err := json.Unmarshal([]byte(replay.Body), &replayed); err != nil {
			observation.note("decode replay compile: %v body=%s", err, replay.Body)
		} else {
			observation.ReplayAttempt.ReceiptID = replayed.ReceiptId
		}
	}

	// Preview through the handler path bound to the persisted valid receipt.
	if observation.ValidAttempt.ReceiptID != "" {
		previewed := fixture.preview(observation.ValidAttempt.ReceiptID)
		observation.PreviewStatus = previewed.StatusCode
		if previewed.StatusCode != http.StatusOK {
			observation.note("preview status=%d body=%s", previewed.StatusCode, previewed.Body)
		} else {
			var preview loomapi.PreviewResponse
			if err := json.Unmarshal([]byte(previewed.Body), &preview); err != nil {
				observation.note("decode preview: %v", err)
			} else {
				observation.PreviewReceipt = preview.ReceiptId
				if preview.ReceiptId != observation.ValidAttempt.ReceiptID {
					observation.note("preview receiptId=%q not the valid receipt", preview.ReceiptId)
				}
			}
		}
	}

	// Persisted-history seam: each save acknowledgement names the revision the
	// store committed for the head it replaced (store.SaveDraft persists the
	// prior identity then stamps it on the owner), so the two reads below
	// recover the intermediate head-1/seed and head-2/B revisions through the
	// store's own reader. Distinct persisted identities for value-equal and
	// replaced heads are what justify the model merging the two A histories
	// into one head channel without erasing the B history.
	for _, revisionID := range []string{derefRevisionID(ackB.PreviousDraftRevisionId), derefRevisionID(ackA.PreviousDraftRevisionId)} {
		if revisionID == "" {
			observation.note("an acknowledgement omitted previousDraftRevisionId")
			observation.Revisions = append(observation.Revisions, coreProtocolConcreteRevision{})
			continue
		}
		revision, ok := stores.history(context.Background(), revisionID)
		observation.Revisions = append(observation.Revisions, coreProtocolConcreteRevision{
			RevisionID:   revisionID,
			Found:        ok,
			DraftVersion: int(revision.DraftVersion),
			DraftDigest:  revision.DraftDigest,
		})
		if !ok {
			observation.note("acknowledged revision %s not readable through the store seam", revisionID)
		}
	}
	return observation
}

// coreProtocolConcreteWitnessRow tags one concrete-prefix observation with the
// store mode that produced it, mirroring coreProtocolReplayWitnessRow: a
// map-only run keeps the plain observation shape, a dual-mode run records the
// store per row in one output file.
type coreProtocolConcreteWitnessRow struct {
	coreProtocolConcreteObservation
	Store string `json:"store"`
}

// runCoreProtocolConcreteAgainstArango mirrors the owned-disposable-database
// pattern of runCoreProtocolReplayAgainstArango for the concrete-prefix lane:
// each program gets its own auto-created database so the seeded A -> B -> A
// prefix runs against exactly the fresh state the map mode provides.
func runCoreProtocolConcreteAgainstArango(t *testing.T, programs []coreProtocolConcreteProgram, endpoint string, observations *[]coreProtocolConcreteObservation) {
	t.Helper()
	for _, program := range programs {
		program := program
		t.Run(program.ID, func(t *testing.T) {
			database := coreProtocolReplayDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: endpoint, Database: database})
			if err != nil {
				t.Fatal(err)
			}
			client := arangoBackend.Client
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
			if err := client.Bootstrap(ctx, arangofactory.ExplorerBootstrapSpec()); err != nil {
				t.Fatal(err)
			}
			graph, err := arangofactory.NewExplorerStore(client)
			if err != nil {
				t.Fatal(err)
			}
			*observations = append(*observations, runCoreProtocolReplayConcreteCase(program, coreProtocolReplayArangoStores(t, client, graph, database)))
		})
	}
}

// TestCoreAuthoringProtocolConcretePrefixReplay is the C14 concrete-prefix
// lane: the adapter hands it the concrete programs and validates the recorded
// wire record, in addition to the abstract model schedules the S-lane entry
// records. It is skipped when neither concrete env pair is set (supporting
// unit runs keep their original shape) and fails when only half the pair is.
func TestCoreAuthoringProtocolConcretePrefixReplay(t *testing.T) {
	inputPath := os.Getenv(coreProtocolConcreteInputEnv)
	outputPath := os.Getenv(coreProtocolConcreteOutputEnv)
	if inputPath == "" && outputPath == "" {
		t.Skip("concrete-prefix lane runs only when the replay adapter sets both " + coreProtocolConcreteInputEnv + " and " + coreProtocolConcreteOutputEnv)
	}
	if inputPath == "" || outputPath == "" {
		t.Fatalf("%s and %s must name the concrete program input and observation output together", coreProtocolConcreteInputEnv, coreProtocolConcreteOutputEnv)
	}
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read concrete replay input: %v", err)
	}
	var programs []coreProtocolConcreteProgram
	if err := json.Unmarshal(raw, &programs); err != nil {
		t.Fatalf("decode concrete replay programs: %v", err)
	}
	if len(programs) == 0 {
		t.Fatal("concrete replay input contains no programs")
	}
	arangoEndpoint := strings.TrimSpace(os.Getenv(coreProtocolReplayArangoURLEnv))
	if arangoEndpoint != "" {
		t.Log("core protocol concrete-prefix witness: LOOM_TEST_ARANGO_URL is set, every program runs in both store modes; each witness row records its store mode")
	}
	mapObservations := make([]coreProtocolConcreteObservation, 0, len(programs))
	t.Run("store="+coreProtocolReplayStoreNameMap, func(t *testing.T) {
		for _, program := range programs {
			program := program
			t.Run(program.ID, func(t *testing.T) {
				t.Log("core protocol concrete store mode: map (in-memory CAS test store)")
				mapObservations = append(mapObservations, runCoreProtocolReplayConcreteCase(program, coreProtocolReplayMapStores(t)))
			})
		}
	})
	arangoObservations := make([]coreProtocolConcreteObservation, 0, len(programs))
	if arangoEndpoint != "" {
		t.Run("store="+coreProtocolReplayStoreNameArango, func(t *testing.T) {
			runCoreProtocolConcreteAgainstArango(t, programs, arangoEndpoint, &arangoObservations)
		})
	}
	var record any
	if arangoEndpoint == "" {
		record = struct {
			SchemaVersion int                               `json:"schemaVersion"`
			Results       []coreProtocolConcreteObservation `json:"results"`
		}{SchemaVersion: 1, Results: mapObservations}
	} else {
		rows := make([]coreProtocolConcreteWitnessRow, 0, len(mapObservations)+len(arangoObservations))
		for _, observation := range mapObservations {
			rows = append(rows, coreProtocolConcreteWitnessRow{coreProtocolConcreteObservation: observation, Store: coreProtocolReplayStoreNameMap})
		}
		for _, observation := range arangoObservations {
			rows = append(rows, coreProtocolConcreteWitnessRow{coreProtocolConcreteObservation: observation, Store: coreProtocolReplayStoreNameArango})
		}
		record = struct {
			SchemaVersion int                              `json:"schemaVersion"`
			Results       []coreProtocolConcreteWitnessRow `json:"results"`
		}{SchemaVersion: 1, Results: rows}
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("encode concrete observations: %v", err)
	}
	temporaryPath := outputPath + ".tmp"
	if err := os.WriteFile(temporaryPath, encoded, 0o644); err != nil {
		t.Fatalf("write concrete observations: %v", err)
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		t.Fatalf("publish concrete observations: %v", err)
	}
}

func TestCoreAuthoringProtocolReplayHarnessWired(t *testing.T) {
	start, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	workflowPath := ""
	for current := start; ; {
		candidate := filepath.Join(current, ".github", "workflows", "builder-formal.yml")
		if _, err := os.Stat(candidate); err == nil {
			workflowPath = candidate
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatalf("locate .github/workflows/builder-formal.yml from %s", start)
		}
		current = parent
	}
	workflow, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read required replay owner %s: %v", workflowPath, err)
	}
	owner := regexp.MustCompile(`(?m)^      - name: ` + regexp.QuoteMeta(coreProtocolReplayOwningStep) + `
        run: ` + regexp.QuoteMeta(coreProtocolReplayOwningCommand) + ` --out \S+$`)
	if !owner.Match(workflow) {
		t.Fatalf("%s must keep the required step %q running unfiltered %q; without it the core authoring protocol replay loses its owning enforcement step", workflowPath, coreProtocolReplayOwningStep, coreProtocolReplayOwningCommand)
	}
	for _, replacement := range []string{"true", coreProtocolReplayOwningCommand + " --model apply-settlement"} {
		mutated := strings.Replace(string(workflow), coreProtocolReplayOwningCommand, replacement, 1)
		if owner.MatchString(mutated) {
			t.Fatalf("workflow enforcement accepted a removed or narrowed generated replay: %s", replacement)
		}
	}
	harnessPath := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(workflowPath))), "scripts", "verify-builder-formal-core.mjs")
	harness, err := os.ReadFile(harnessPath)
	if err != nil {
		t.Fatalf("read harness %s: %v", harnessPath, err)
	}
	for _, required := range []string{
		"CoreAuthoringProtocol: 'scripts/lib/formal-core-protocol-replay.mjs'",
		"['all', 'generated'].includes(options.phase) && names.includes('CoreAuthoringProtocol')) await coreProtocolGraph()",
	} {
		if !strings.Contains(string(harness), required) {
			t.Fatalf("%s must keep the model-generated core protocol replay wired to scripts/lib/formal-core-protocol-replay.mjs; missing %q means the required replay silently stops executing", harnessPath, required)
		}
	}
}
