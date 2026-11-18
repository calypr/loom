package clickhouse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pubpublication "github.com/calypr/loom/internal/dataframe/publication"
	pubarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/calypr/loom/internal/testkit/arangotest"
)

const pppDatabasePrefix = "loom_verify_ppp_"

const pppChOwnedPrefix = "ch_ppp"

type pppPublishCall struct {
	PointerName    string
	Expected       string
	ExecutionID    string
	Delegated      bool
	LegOneApplied  bool
	LegTwoExecuted bool
	InjectedError  string
}

type pppCatalog struct {
	*pubarango.Registry
	client *storearango.Client

	mu         sync.Mutex
	faultArmed bool
	calls      []pppPublishCall
}

func newPPPCatalog(registry *pubarango.Registry, client *storearango.Client) *pppCatalog {
	return &pppCatalog{Registry: registry, client: client}
}

func (c *pppCatalog) armFault() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.faultArmed = true
}

func (c *pppCatalog) snapshotCalls() []pppPublishCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]pppPublishCall(nil), c.calls...)
}

const pppLegOneAQL = `LET pointer = DOCUMENT(@@pointers, @pointerKey)
LET execution = DOCUMENT(@@executions, @executionKey)
LET lease = DOCUMENT(@@leases, @leaseKey)
LET pointerNameParam = @name
LET updatedAtParam = @updatedAt
FILTER execution != null
  AND lease != null
  AND lease.ownerId == @owner
  AND lease.expiresAt >= @now
  AND (pointer == null OR pointer.executionId == @expected)
UPDATE execution WITH @execution IN @@executions
RETURN {updated: true}`

func pppPointerDocumentKey(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

func (c *pppCatalog) PublishExecution(ctx context.Context, name, expected string, execution pubpublication.BundleExecution) error {
	data, err := json.Marshal(execution)
	if err != nil {
		return err
	}
	var executionDoc map[string]any
	if err := json.Unmarshal(data, &executionDoc); err != nil {
		return err
	}
	c.mu.Lock()
	call := pppPublishCall{PointerName: name, Expected: expected, ExecutionID: execution.ID}
	armed := c.faultArmed
	c.faultArmed = false
	c.mu.Unlock()

	if !armed {
		call.Delegated = true
		err = c.Registry.PublishExecution(ctx, name, expected, execution)
		if err == nil {
			call.LegTwoExecuted = true
		}
		c.mu.Lock()
		c.calls = append(c.calls, call)
		c.mu.Unlock()
		return err
	}

	updated := false
	err = c.client.QueryRows(ctx, pppLegOneAQL, 32, map[string]interface{}{
		"@pointers":    pubarango.BundlePointersCollection,
		"@executions":  pubarango.BundleExecutionsCollection,
		"@leases":      pubarango.BundleLeasesCollection,
		"pointerKey":   pppPointerDocumentKey(name),
		"executionKey": execution.ID,
		"leaseKey":     execution.Key,
		"owner":        execution.OwnerID,
		"now":          time.Now().UTC(),
		"name":         name,
		"expected":     expected,
		"updatedAt":    execution.UpdatedAt,
		"execution":    executionDoc,
	}, func(map[string]any) error { updated = true; return nil })
	c.mu.Lock()
	call.LegOneApplied = updated
	if err != nil {
		call.InjectedError = err.Error()
	}
	c.calls = append(c.calls, call)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if !updated {
		return errors.New("ppp fault precondition: the exec UPDATE leg was filtered out; execution/lease/pointer guards did not hold")
	}

	injected := errors.New("write conflict: document revision conflict while applying visibility pointer UPSERT (injected partial-application fault)")
	c.mu.Lock()
	c.calls[len(c.calls)-1].InjectedError = injected.Error()
	c.mu.Unlock()
	return injected
}

type pppHarness struct {
	ctx      context.Context
	client   *storearango.Client
	registry *pubarango.Registry
	catalog  *pppCatalog
	ch       *clickhouse.Client
	database string
	suffix   string

	mu            sync.Mutex
	trackedTables []string
	canaryTable   string
}

func (h *pppHarness) trackTable(table string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, existing := range h.trackedTables {
		if existing == table {
			return
		}
	}
	h.trackedTables = append(h.trackedTables, table)
}

func pppOpen(t *testing.T) *pppHarness {
	t.Helper()
	database := arangotest.Owned(t, pppDatabasePrefix)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	t.Cleanup(cancel)
	client := database.Client
	registry, err := pubarango.New(client)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := client.Bootstrap(ctx, pubarango.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap bundle collections: %v", err)
	}
	ch := arangotest.ClickHouse(t)
	harness := &pppHarness{
		ctx: ctx, client: client, registry: registry, catalog: newPPPCatalog(registry, client), ch: ch,
		database: database.Name, suffix: database.Name[len(pppDatabasePrefix):],
	}

	t.Cleanup(func() { harness.dropAndVerifyClickHouse(t) })
	harness.canaryTable = pppChOwnedPrefix + "_canary_" + harness.suffix
	if err := ch.CreateTable(ctx, harness.canaryTable, []clickhouse.Column{{Name: "__loom_row_id", Type: "UInt64"}}); err != nil {
		t.Fatalf("create canary table %s: %v", harness.canaryTable, err)
	}
	harness.trackTable(harness.canaryTable)
	t.Logf("PPP_CLICKHOUSE_DATABASE=%s PPP_CANARY_TABLE=%s", os.Getenv("LOOM_CLICKHOUSE_DATABASE"), harness.canaryTable)
	return harness
}

func (h *pppHarness) dropAndVerifyClickHouse(t *testing.T) {
	t.Helper()
	h.mu.Lock()
	tables := append([]string(nil), h.trackedTables...)
	h.mu.Unlock()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, table := range tables {
		if err := h.ch.DropTable(cleanupCtx, table); err != nil {
			t.Errorf("PPP CH drop %s: %v", table, err)
		}
	}
	names, err := h.ch.QueryRowsArgs(cleanupCtx, fmt.Sprintf("SELECT name FROM system.tables WHERE database = currentDatabase() AND (name LIKE '%s%%' OR name LIKE 'loom_bundle_ppp%%')", pppChOwnedPrefix), []string{"name"})
	if err != nil {
		t.Errorf("PPP CH residue query (prefixes %s%%, loom_bundle_ppp%%): %v", pppChOwnedPrefix, err)
		return
	}
	if len(names) != 0 {
		t.Errorf("PPP CH residue under owned prefixes is not empty: %v", names)
		return
	}
	for _, table := range tables {
		rows, err := h.ch.QueryRowsArgs(cleanupCtx, "SELECT count() AS c FROM system.tables WHERE database = currentDatabase() AND name = ?", []string{"c"}, table)
		if err != nil {
			t.Errorf("PPP CH exact check %s: %v", table, err)
			continue
		}
		if len(rows) != 1 {
			t.Errorf("PPP CH exact check %s returned %d rows", table, len(rows))
			continue
		}
		if count, ok := pppCount(rows[0]["c"]); !ok {
			t.Errorf("PPP CH exact check %s unreadable: %#v", table, rows[0]["c"])
		} else if count != 0 {
			t.Errorf("PPP CH residue: tracked table %s still present", table)
		}
	}
	t.Logf("PPP_CH_CLEANUP_VERIFIED dropped=%v and prefixes '%s%%'/'loom_bundle_ppp%%' empty in system.tables", tables, pppChOwnedPrefix)
	_ = h.ch.Close()
}

type pppPublication struct {
	identity  pubpublication.PublicationIdentity
	key       string
	pointer   string
	outputs   []pubpublication.OutputStream
	tableName string
	rows      int
}

func pppNewPublication(label, project string, h *pppHarness) pppPublication {
	identity := pubpublication.PublicationIdentity{
		Name: "ppp-recipe-" + label, TranslationVersion: "v1", OutputName: "ppp_out_" + label,
		Project: project, DatasetGeneration: "ppp-gen-" + h.suffix + "-" + label,
		RecipeDigest:  "sha256:ppp-recipe-" + label + "-" + h.suffix,
		SchemaDigest:  "sha256:ppp-schema-" + label + "-" + h.suffix,
		ScopeDigest:   "sha256:ppp-scope-" + label + "-" + h.suffix,
		EngineVersion: "ppp-engine",
	}
	bundle := pubpublication.BundleIdentity{
		Name: identity.Name, TranslationVersion: identity.TranslationVersion, OutputName: identity.OutputName,
		Project: identity.Project, DatasetGeneration: identity.DatasetGeneration,
		RecipeDigest: identity.RecipeDigest, SchemaDigest: identity.SchemaDigest,
		ScopeDigest: identity.ScopeDigest, EngineVersion: identity.EngineVersion,
	}
	outputs := []pubpublication.OutputStream{{
		Name:    identity.OutputName,
		Columns: []pubpublication.LogicalColumn{{ID: "col-ppp-value", Name: "value", Kind: "integer", Provenance: pubpublication.ColumnExplicit}},
		Stream: func(ctx context.Context, visit func(map[string]any) error) error {
			for i := 1; i <= 3; i++ {
				if err := visit(map[string]any{"value": int64(i)}); err != nil {
					return err
				}
			}
			return nil
		},
	}}
	return pppPublication{identity: identity, key: bundle.Key(), pointer: bundle.PointerName(), outputs: outputs, rows: 3}
}

func (p pppPublication) selector() pubpublication.DataframeSelector {
	return pubpublication.DataframeSelector{Recipe: p.identity.Name, TranslationVersion: p.identity.TranslationVersion, Output: p.identity.OutputName}
}

func pppProductionPublish(ctx context.Context, store *ClickHouseBundleStore, p pppPublication) (pubpublication.Result, error) {
	return pubpublication.Publish(ctx, store, p.identity, p.outputs, pubpublication.Limits{})
}

func pppRawExecutionRow(t *testing.T, client *storearango.Client, ctx context.Context, execID string) (state, errField string) {
	t.Helper()
	err := client.QueryRows(ctx, `RETURN DOCUMENT(@@collection, @key)`, 32, map[string]interface{}{
		"@collection": pubarango.BundleExecutionsCollection, "key": execID,
	}, func(row map[string]any) error {
		data, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var decoded struct {
			State string `json:"state"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &decoded); err != nil {
			return err
		}
		state, errField = decoded.State, decoded.Error
		return nil
	})
	if err != nil {
		t.Fatalf("ppp raw read of execution %s: %v", execID, err)
	}
	return state, errField
}

func pppCount(value any) (int64, bool) {
	switch typed := value.(type) {
	case uint64:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed), true
		}
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func pppTablePresent(t *testing.T, ch *clickhouse.Client, ctx context.Context, table string) bool {
	t.Helper()
	rows, err := ch.QueryRowsArgs(ctx, "SELECT count() AS c FROM system.tables WHERE database = currentDatabase() AND name = ?", []string{"c"}, table)
	if err != nil {
		t.Fatalf("ppp system.tables check for %s: %v", table, err)
	}
	if len(rows) != 1 {
		t.Fatalf("ppp system.tables check for %s returned %d rows", table, len(rows))
	}
	count, ok := pppCount(rows[0]["c"])
	if !ok {
		t.Fatalf("ppp system.tables count for %s unreadable: %#v", table, rows[0]["c"])
	}
	return count == 1
}

func pppTableRows(t *testing.T, ch *clickhouse.Client, ctx context.Context, table string) int64 {
	t.Helper()
	rows, err := ch.QueryRowsArgs(ctx, fmt.Sprintf("SELECT count() AS c FROM `%s`", table), []string{"c"})
	if err != nil {
		t.Fatalf("ppp row count for %s: %v", table, err)
	}
	if len(rows) != 1 {
		t.Fatalf("ppp row count for %s returned %d rows", table, len(rows))
	}
	count, ok := pppCount(rows[0]["c"])
	if !ok {
		t.Fatalf("ppp row count for %s unreadable: %#v", table, rows[0]["c"])
	}
	return count
}

type pppReleaseStore interface {
	GetExecution(context.Context, string) (pubpublication.BundleExecution, error)
	GetPointer(context.Context, string) (pubpublication.BundlePointer, error)
}

func pppBulkGateCheck(ctx context.Context, store pppReleaseStore, project, generation, executionID string) (string, error) {
	execution, err := store.GetExecution(ctx, executionID)
	if err != nil {
		return "execution-lookup", fmt.Errorf("load dataframe release %q: %w", executionID, err)
	}
	if execution.Project != project || execution.DatasetGeneration != generation || !execution.State.Successful() || len(execution.Outputs) == 0 {
		return "execution-invalid", fmt.Errorf("dataframe release %q is not a published bundle for %s/%s", executionID, project, generation)
	}
	for _, output := range execution.Outputs {
		if !output.State.Successful() {
			return "output-invalid", fmt.Errorf("dataframe release %q output %q is not published", executionID, output.Name)
		}
	}
	pointer, err := store.GetPointer(ctx, execution.PointerName())
	if err != nil {
		return "pointer-lookup", fmt.Errorf("resolve dataframe release pointer: %w", err)
	}
	if pointer.ExecutionID != execution.ID {
		return "pointer-mismatch", fmt.Errorf("dataframe release %q is no longer active", executionID)
	}
	return "gate-passed", nil
}

func TestPublishPartialPointerNeverAdvertisedAgainstArango(t *testing.T) {
	h := pppOpen(t)
	ctx := h.ctx

	store, err := NewBundleStore(h.ch, h.catalog)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}

	t.Logf("PRE-STATED EXPECT (control): publication.Publish leaves the pointer naming exec CONTROL, state PUBLISHED, and the physical ClickHouse table present")
	control := pppNewPublication("control", "ppp-control-project", h)
	controlResult, controlErr := pppProductionPublish(ctx, store, control)
	if controlErr != nil {
		t.Fatalf("control publication through production entry failed: %v", controlErr)
	}
	if len(controlResult.Outputs) != 1 || controlResult.Outputs[0].RowCount != int64(control.rows) {
		t.Fatalf("control publication result malformed: %+v", controlResult.Outputs)
	}
	calls := h.catalog.snapshotCalls()
	if len(calls) != 1 || !calls[0].Delegated || calls[0].ExecutionID == "" {
		t.Fatalf("control precondition: expected exactly one delegated real PublishExecution, got %+v", calls)
	}
	controlID := calls[0].ExecutionID
	controlTable := controlResult.Outputs[0].PhysicalName
	h.trackTable(controlTable)
	controlExec, err := h.registry.GetExecution(ctx, controlID)
	if err != nil {
		t.Fatalf("control GetExecution %s: %v", controlID, err)
	}
	if controlExec.State.Canonical() != pubpublication.BundlePublished {
		t.Fatalf("control precondition: exec CONTROL state %q, want PUBLISHED", controlExec.State)
	}
	controlPointer, err := h.registry.GetPointer(ctx, control.pointer)
	if err != nil || controlPointer.ExecutionID != controlID {
		t.Fatalf("control precondition: pointer %q does not name exec CONTROL %s (pointer=%+v err=%v)", control.pointer, controlID, controlPointer, err)
	}
	if !pppTablePresent(t, h.ch, ctx, controlTable) {
		t.Fatalf("control precondition: physical table %s missing from ClickHouse system.tables", controlTable)
	}
	t.Logf("OBSERVED STEP 1 CONTROL: production publication.Publish published exec %q state=%q pointer->%q physical table %q present with %d rows via real ClickHouse", controlID, controlExec.State, controlPointer.ExecutionID, controlTable, pppTableRows(t, h.ch, ctx, controlTable))

	t.Logf("PRE-STATED EXPECT (fault-injection control): Commit returns ErrBundleCommitUncertain (errors.Is) and the tx is closed; the execution remains queryable PUBLISHED (FindExecutionByKey/GetExecution) while GetPointer -> ErrBundleNotFound")
	faulty := pppNewPublication("fault", "ppp-fault-project", h)
	h.catalog.armFault()
	faultResult, faultErr := pppProductionPublish(ctx, store, faulty)
	faultID := ""
	faultTable := ""
	calls = h.catalog.snapshotCalls()
	if len(calls) == 2 && calls[1].ExecutionID != "" {
		faultID = calls[1].ExecutionID
	}
	if faultID != "" {
		if exec, execErr := h.registry.GetExecution(ctx, faultID); execErr == nil && len(exec.Outputs) == 1 {
			faultTable = exec.Outputs[0].PhysicalTable
			h.trackTable(faultTable)
		}
	}
	if !errors.Is(faultErr, ErrBundleCommitUncertain) {
		t.Fatalf("fault scenario did not end in ErrBundleCommitUncertain: err=%v result=%+v calls=%+v", faultErr, faultResult, calls)
	}
	if len(calls) != 2 {
		t.Fatalf("fault precondition: expected the armed PublishExecution to fire once, calls=%+v", calls)
	}
	if !calls[1].LegOneApplied || calls[1].LegTwoExecuted {
		t.Fatalf("fault precondition: expected ONLY the exec UPDATE leg applied and the pointer UPSERT leg skipped, got %+v", calls[1])
	}
	if faultID == "" {
		t.Fatalf("fault precondition: execution id never observed")
	}
	viaGet := mustGetExecution(t, h, ctx, faultID, "GetExecution")
	viaKey := mustGetExecution(t, h, ctx, faulty.key, "FindExecutionByKey")
	if viaGet.State.Canonical() != pubpublication.BundlePublished || viaKey.State.Canonical() != pubpublication.BundlePublished {
		t.Fatalf("fault precondition: partial Application left the row state GetExecution=%q FindExecutionByKey=%q, want PUBLISHED on both", viaGet.State, viaKey.State)
	}
	faultPointer, pointerErr := h.registry.GetPointer(ctx, faulty.pointer)
	if !errors.Is(pointerErr, pubpublication.ErrBundleNotFound) {
		t.Fatalf("fault precondition: pointer under a partial application must be missing; GetPointer returned pointer=%+v err=%v", faultPointer, pointerErr)
	}

	if !pppTablePresent(t, h.ch, ctx, faultTable) {
		t.Fatalf("fault precondition: the possibly-published table %q was deleted despite the uncertain-commit closed flag", faultTable)
	}
	rawState, rawError := pppRawExecutionRow(t, h.client, ctx, faultID)
	if strings.TrimSpace(rawError) != "" {
		t.Fatalf("fault precondition: the runner demoted the execution (raw error field %q); the closed-fault guard did not hold", rawError)
	}
	t.Logf("OBSERVED STEP 2 FAULT: Commit path returned errors.Is(err, ErrBundleCommitUncertain)=true errChain=[%s]; exec UPDATE leg applied=%v pointer UPSERT leg executed=%v; exec %q queryable PUBLISHED via GetExecution and FindExecutionByKey (raw state=%q); GetPointer(%q) -> ErrBundleNotFound; possibly-published table %q preserved (%d rows)",
		errChain(faultErr), calls[1].LegOneApplied, calls[1].LegTwoExecuted, faultID, rawState, faulty.pointer, faultTable, pppTableRows(t, h.ch, ctx, faultTable))

	t.Logf("An identical retry reuses existing published outputs; this fabricated legacy split is outside atomic publication and does not claim pointer repair.")
	repairResult, repairErr := pppProductionPublish(ctx, store, faulty)
	if repairErr != nil {
		t.Fatalf("repair attempt: production retry of the identical identity errored instead of succeeding idempotently: %v", repairErr)
	}
	if len(repairResult.Outputs) != 1 || repairResult.Outputs[0].PhysicalName != faultTable || repairResult.Outputs[0].RowCount != int64(faulty.rows) {
		t.Fatalf("repair attempt: idempotent Result did not come from ExistingPublishedOutputs: %+v", repairResult.Outputs)
	}
	calls = h.catalog.snapshotCalls()
	if len(calls) != 2 {
		t.Fatalf("repair attempt: the idempotent retry unexpectedly invoked PublishExecution: %+v", calls)
	}
	pointerAfterRetry, pointerAfterErr := h.registry.GetPointer(ctx, faulty.pointer)
	repaired := pointerAfterErr == nil && pointerAfterRetry.ExecutionID == faultID
	t.Logf("OBSERVED STEP 3 REPAIR: identical retry published.Result={Outputs:%+v} err=%v; PublishExecution invocations still %d (idempotent path never publishes); GetPointer after retry -> pointer=%+v err=%v; repaired=%v",
		repairResult.Outputs, repairErr, len(calls), pointerAfterRetry, pointerAfterErr, repaired)

	selectorExec, selectorOutput, selectorErr := h.registry.FindExecutionBySelector(ctx, faulty.identity.Project, faulty.identity.DatasetGeneration, faulty.selector())
	t.Logf("OBSERVED STEP 4a name-latest selector: FindExecutionBySelector=%q state=%q output=%q physicalTable=%q err=%v (matches orphan %v) while GetPointer for the same namespace errors=%v (repaired=%v)",
		selectorExec.ID, selectorExec.State, selectorOutput.Name, selectorOutput.PhysicalTable, selectorErr, selectorExec.ID == faultID, pointerAfterErr, repaired)
	if selectorErr != nil || selectorExec.ID != faultID {
		t.Fatalf("STEP 4a broken: name-latest selector must return the orphan %q: got %q err=%v", faultID, selectorExec.ID, selectorErr)
	}
	orphanBranch, orphanGateErr := pppBulkGateCheck(ctx, h.catalog, faulty.identity.Project, faulty.identity.DatasetGeneration, faultID)
	t.Logf("OBSERVED STEP 4b bulk-load gate on the orphan (activation refused per service.go simulation): branch taken=%q error=[%v]", orphanBranch, orphanGateErr)
	controlBranch, controlGateErr := pppBulkGateCheck(ctx, h.catalog, control.identity.Project, control.identity.DatasetGeneration, controlID)
	t.Logf("OBSERVED STEP 4c bulk-load gate on the control (liveness counterpart): branch taken=%q error=%v", controlBranch, controlGateErr)
	if controlBranch != "gate-passed" || controlGateErr != nil {
		t.Fatalf("STEP 4c broken: the gate must pass a fully published, pointer-named execution; got branch=%q err=%v", controlBranch, controlGateErr)
	}
	if orphanGateErr == nil {
		t.Fatalf("STEP 4b broken: the bulk-load gate accepted an execution the pointer never named; branch=%q", orphanBranch)
	}

	if repaired {
		t.Fatalf("Idempotent replay unexpectedly changed the pointer for the fabricated legacy split; review the changed retry contract")
	}
	if calls[1].Delegated {
		t.Fatalf("VERDICT INVALID: the split was produced by the REAL Registry.PublishExecution, not the harness bypass — the r26 atomic-publish repair has regressed; investigate via publish_atomicity_e2e_test.go arms A/B first")
	}
	t.Logf("VERDICT (r26 repaired; fault-control verified): the PUBLISHED-never-advertised state for %q exists ONLY because the harness bypassed the repaired production PublishExecution (wrapper ran the pre-fix legs itself; Delegated=false). For such fabricated/legacy rows the harm is real and persists exactly as originally documented: name-latest selector serves the orphan (%v), the bulk-load gate refuses it, the idempotent replay does not heal the pointer, and reconcile never visits PUBLISHED (:1043). The product-level guarantee (production publish cannot produce this state) is asserted by STEP 1's delegated real publish here and proved with rollback in publish_atomicity_e2e_test.go; the pre-fix FAILED-ON-PURPOSE history is preserved in FINDINGS.md Round 11.", faultID, selectorExec.ID == faultID)
}

func mustGetExecution(t *testing.T, h *pppHarness, ctx context.Context, idOrKey, via string) pubpublication.BundleExecution {
	t.Helper()
	var (
		execution pubpublication.BundleExecution
		err       error
	)
	if via == "GetExecution" {
		execution, err = h.registry.GetExecution(ctx, idOrKey)
	} else {
		execution, err = h.registry.FindExecutionByKey(ctx, idOrKey)
	}
	if err != nil {
		t.Fatalf("%s of %s: %v", via, idOrKey, err)
	}
	return execution
}

func errChain(err error) string {
	if err == nil {
		return "<nil>"
	}
	parts := make([]string, 0, 6)
	for current := err; current != nil; current = errors.Unwrap(current) {
		parts = append(parts, current.Error())
	}
	return strings.Join(parts, " <- ")
}
