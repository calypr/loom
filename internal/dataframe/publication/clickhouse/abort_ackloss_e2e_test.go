package clickhouse

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

// Candidate finding F-B2: clickHouseBundleTx.Abort (bundle_store.go:861-897)
// reaches claimExecutionCleanup → ClaimExecutionCleanup, whose real AQL
// (bundle_registry.go:411-427) filters only expired pins and the cleanup slot
// — no pointer and no published-state recheck — and then unconditionally
// DropTables every output. Hypothesis: a runner whose PublishExecution AQL
// succeeded server-side but whose ACK was lost calls Abort, deleting the
// physical tables of the bundle the live pointer now advertises.
//
// The wrapper injects ONLY a lost publish ACK: the real Registry AQL runs to
// completion against the real database (so the server has committed both the
// PUBLISHED row and the pointer), and the wrapper discards the client-side
// result and returns a network-style error, mirroring a connection failure
// after server commit. ClaimExecutionCleanup is the real AQL; no pins exist
// (not this test's variable). ClickHouse effects come from the established
// tocFakeClickHouse recorder.
//
// Opt-in like reconcile_toctou_e2e_test.go; the test owns a UUID database and
// deletes only that database:
//
//	docker exec loom-dev-qwen-current-loom-api-1 sh -c \
//	  'cd /workspace && LOOM_TEST_ARANGO_URL=http://arangodb:8529 go test \
//	   -count=1 -run "TestAbort.*AgainstArango" -v ./internal/dataframe/publication/clickhouse/'

type fbxCatalog struct {
	*arangofactory.PublishedRegistry
	publishArmed  bool
	publishRan    bool
	publishLanded bool
	observedID    string
}

// PublishExecution runs the real AQL verbatim. Once armed, a server-committed
// publish has its successful result discarded and is replaced by a network
// error, so the production caller believes the ACK was lost after commit.
// Genuine server-side rejections are passed through untouched.
func (c *fbxCatalog) PublishExecution(ctx context.Context, name, expected string, execution pubpublication.BundleExecution) error {
	err := c.PublishedRegistry.PublishExecution(ctx, name, expected, execution)
	if c.publishArmed && !c.publishRan {
		c.publishRan = true
		if err == nil {
			c.publishLanded = true
			return errors.New("connection reset by peer while awaiting publish acknowledgement")
		}
	}
	return err
}

// SaveExecution delegates verbatim; it only records the execution UUID that
// beginBundle generated so the test can re-read the row by key afterwards.
func (c *fbxCatalog) SaveExecution(ctx context.Context, execution pubpublication.BundleExecution, fenceOwner string) error {
	if c.observedID == "" && execution.OwnerID != "" {
		c.observedID = execution.ID
	}
	return c.PublishedRegistry.SaveExecution(ctx, execution, fenceOwner)
}

func fbxCleanupDatabase(t *testing.T, endpoint, database string) {
	t.Helper()
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cleanupCancel()
	request, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
	if err != nil {
		t.Errorf("build cleanup request: %v", err)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Errorf("drop database %s: %v", database, err)
		return
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusForbidden {
		t.Errorf("drop database %s: status %d", database, response.StatusCode)
	}
}

func fbxSetup(t *testing.T) (*arangofactory.PublishedRegistry, context.Context, string) {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	database := "loom_verify_fbx_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Logf("owned arango database: %s", database)
	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: endpoint, Database: database})
	if err != nil {
		t.Fatalf("open arango: %v", err)
	}
	client := arangoBackend.Client
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		fbxCleanupDatabase(t, endpoint, database)
	})
	if err := client.Bootstrap(ctx, arangofactory.PublishedBootstrapSpec()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	registry, err := arangofactory.NewPublishedRegistry(client)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return registry, ctx, database[len("loom_verify_fbx_"):]
}

func fbxBundleIdentity(suffix string) pubpublication.BundleIdentity {
	return pubpublication.BundleIdentity{
		Name: "fbx-abort-ackloss", TranslationVersion: "1", OutputName: "main",
		Project: "loom_verify", DatasetGeneration: "fbx-" + suffix,
		RecipeDigest: "r-" + suffix, SchemaDigest: "s-" + suffix,
		ScopeDigest: "sc-" + suffix, EngineVersion: "1",
	}
}

func fbxPublicationIdentity(identity pubpublication.BundleIdentity) pubpublication.PublicationIdentity {
	return pubpublication.PublicationIdentity{
		Name: identity.Name, TranslationVersion: identity.TranslationVersion, OutputName: identity.OutputName,
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration,
		RecipeDigest: identity.RecipeDigest, SchemaDigest: identity.SchemaDigest,
		ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion,
	}
}

func fbxSchemas() []pubpublication.OutputSchema {
	return []pubpublication.OutputSchema{{Name: "main", Columns: []pubpublication.LogicalColumn{
		{ID: "col-value", Name: "value", Kind: "integer"},
	}}}
}

func fbxRows() []map[string]any {
	return []map[string]any{{"value": int64(1)}, {"value": int64(2)}}
}

// fbxClickHouse reuses the tocFakeClickHouse effect semantics and additionally
// records the names handed to CreateTable/DropTable so the test can report the
// created versus dropped physical-table set against real registry state.
type fbxClickHouse struct {
	*tocFakeClickHouse
	created []string
	dropped []string
}

func (f *fbxClickHouse) CreateTable(ctx context.Context, table string, columns []clickhouse.Column) error {
	f.created = append(f.created, table)
	return f.tocFakeClickHouse.CreateTable(ctx, table, columns)
}

func (f *fbxClickHouse) DropTable(ctx context.Context, table string) error {
	err := f.tocFakeClickHouse.DropTable(ctx, table)
	f.dropped = append(f.dropped, table)
	return err
}

func fbxDropped(set []string, table string) bool {
	for _, name := range set {
		if name == table {
			return true
		}
	}
	return false
}

// TestAbortDropsServerPublishedBundleAgainstArango drives the real
// Begin/publish/Abort flow with ONLY the publish ACK lost after a real
// server-side commit. It fails t.Fatalf("CONFIRMED ...") iff, after Abort
// returns, the real registry still advertises the execution through the
// pointer (row PUBLISHED) while its physical tables were deleted. If
// production refuse gates hold, the test passes and logs the refusing code.
func TestAbortDropsServerPublishedBundleAgainstArango(t *testing.T) {
	registry, ctx, suffix := fbxSetup(t)
	identity := fbxBundleIdentity(suffix)
	pointerName := identity.PointerName()
	wrapper := &fbxCatalog{PublishedRegistry: registry, publishArmed: true}
	ch := &fbxClickHouse{tocFakeClickHouse: &tocFakeClickHouse{}}
	store, err := NewBundleStore(ch, wrapper)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}

	tx, err := store.Begin(ctx, fbxPublicationIdentity(identity), fbxSchemas())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tx.WriteBatch(ctx, "main", fbxRows()); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	_, commitErr := tx.Commit(ctx)
	abortErr := tx.Abort(ctx, errors.New("runner aborts: publish acknowledgement was never received"))

	if !wrapper.publishLanded {
		t.Fatalf("precondition: the real publish AQL never committed server-side; the harness did not create the lost-ACK-after-success scenario (commitErr=%v)", commitErr)
	}
	executionID := wrapper.observedID
	if executionID == "" {
		t.Fatalf("precondition: no execution id observed from the real Begin/save flow")
	}
	if len(ch.created) != 1 {
		t.Fatalf("precondition: expected exactly one created physical table, got %v", ch.created)
	}
	physicalTable := ch.created[0]
	tableGone := fbxDropped(ch.dropped, physicalTable)
	row, rowErr := registry.GetExecution(ctx, executionID)
	pointer, pointerErr := registry.GetPointer(ctx, pointerName)
	if rowErr != nil {
		t.Fatalf("real GetExecution after abort: %v", rowErr)
	}
	if pointerErr != nil && !errors.Is(pointerErr, pubpublication.ErrBundleNotFound) {
		t.Fatalf("real GetPointer after abort: %v", pointerErr)
	}
	pointerAdvertises := pointerErr == nil && pointer.ExecutionID == executionID
	rowPublished := row.State.Canonical() == pubpublication.BundlePublished

	if pointerAdvertises && tableGone {
		t.Fatalf("CONFIRMED against real Arango (candidate F-B2): clickHouseBundleTx.Abort dropped the physical tables of the bundle the pointer advertises. Real state after Abort: row %q state=%q, pointer %q names executionId=%q, created tables=%v, dropped tables=%v; publish ACK was lost after verified server commit; commitErr=%v abortErr=%v. ClaimExecutionCleanup (bundle_registry.go:411-427) granted the claim with no pointer or published-state recheck.",
			executionID, row.State, pointer.Name, pointer.ExecutionID, ch.created, ch.dropped, commitErr, abortErr)
	}
	t.Logf("not reproduced: real state after Abort: row %q state=%q published=%v, pointer-advertises=%v, created=%v, dropped=%v, commitErr=%v, abortErr=%v",
		executionID, row.State, rowPublished, pointerAdvertises, ch.created, ch.dropped, commitErr, abortErr)
	if !rowPublished {
		t.Fatalf("precondition: the server-committed publish did not leave the row PUBLISHED in Arango: state=%q", row.State)
	}
	if !pointerAdvertises {
		t.Fatalf("precondition: the server-committed publish did not leave a live pointer naming %q: %+v", executionID, pointer)
	}
	if tableGone {
		t.Fatalf("CONFIRMED variant: the published bundle's tables were dropped even though the flow was gated: dropped=%v", ch.dropped)
	}
	t.Logf("PREVENTED on unmodified production code: with the publish AQL committed server-side and only the client ACK lost, Commit's confirmCommit fallback (bundle_store.go:813-822, 841-858) recovered the commit (Commit treated it as successful), which marked the tx closed so Abort (bundle_store.go:862) refused, and the real ClaimExecutionCleanup path was never reached. commitErr=%v abortErr=%v", commitErr, abortErr)
}

// TestAbortDropsUnpublishedExecutionAgainstArango is the control: the same
// real Begin/Abort flow with no publish issued at all. The abort drop must
// proceed (no pointer exists, the row ends FAILED) — pinning that any harm in
// the finding test comes from the lost-ACK-after-success, not the harness.
func TestAbortDropsUnpublishedExecutionAgainstArango(t *testing.T) {
	registry, ctx, suffix := fbxSetup(t)
	identity := fbxBundleIdentity(suffix)
	wrapper := &fbxCatalog{PublishedRegistry: registry}
	ch := &fbxClickHouse{tocFakeClickHouse: &tocFakeClickHouse{}}
	store, err := NewBundleStore(ch, wrapper)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}

	tx, err := store.Begin(ctx, fbxPublicationIdentity(identity), fbxSchemas())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tx.WriteBatch(ctx, "main", fbxRows()); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if wrapper.publishRan {
		t.Fatalf("precondition: publish was issued in the no-publish control")
	}
	cause := errors.New("runner aborts before any publish was issued")
	abortErr := tx.Abort(ctx, cause)
	// Abort echoes the runner cause back through the cleanup join
	// (bundle_store.go:891-896, failPhase :951). A declined cleanup claim
	// would surface as ErrExecutionReadPinActive instead.
	if !errors.Is(abortErr, cause) || errors.Is(abortErr, pubpublication.ErrExecutionReadPinActive) {
		t.Fatalf("legitimate Abort of an unpublished execution must drop and echo the runner cause, got: %v", abortErr)
	}
	if len(ch.created) != 1 {
		t.Fatalf("precondition: expected exactly one created physical table, got %v", ch.created)
	}
	if !fbxDropped(ch.dropped, ch.created[0]) {
		t.Fatalf("regression: Abort of an unpublished execution failed to drop its staging table; created=%v dropped=%v", ch.created, ch.dropped)
	}
	if _, err := registry.GetPointer(ctx, identity.PointerName()); !errors.Is(err, pubpublication.ErrBundleNotFound) {
		t.Fatalf("control: pointer must not exist for a never-published execution: %v", err)
	}
	row, err := registry.GetExecution(ctx, wrapper.observedID)
	if err != nil {
		t.Fatalf("real GetExecution after control abort: %v", err)
	}
	if row.State != pubpublication.BundleFailed {
		t.Fatalf("control: never-published execution must end FAILED after Abort, got %q", row.State)
	}
	t.Logf("control OK: unpublished execution %q dropped legitimately (dropped=%v), no pointer exists, row FAILED", wrapper.observedID, ch.dropped)
}
