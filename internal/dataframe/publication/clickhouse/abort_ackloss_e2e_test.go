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

type fbxCatalog struct {
	*arangofactory.PublishedRegistry
	publishArmed  bool
	publishRan    bool
	publishLanded bool
	observedID    string
}

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
