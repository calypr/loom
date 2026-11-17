package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestConstructionColumnPopulationValidatesExactStageAndCachesOnlyConclusiveResults(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	for index := range workspace.Documents[0].Columns {
		workspace.Documents[0].Columns[index].LogicalType = "string"
	}
	workspace.Documents[0].Columns[0].ColumnID = "base-column"
	column := workspace.Documents[0].Columns[0]
	column.ColumnID = "status_column"
	column.Column = "status"
	column.Label = "Status"
	column.Source.Field.Path = "status"
	workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, column)
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig, store.created.DraftDigest = encoded, digest
	document := workspace.Documents[0]
	request := ConstructionColumnPopulationRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
		ColumnIDs: []string{document.Columns[0].ColumnID, document.Columns[1].ColumnID},
	}

	calls := 0
	fail := true
	service.config.ScanColumnPopulation = func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, scan dataframeexecution.ColumnPopulationRequest) (dataframeexecution.ColumnPopulationResult, error) {
		calls++
		if receipt == nil || receipt.ID == "" || bindings.SelectionProject != store.created.Project || scan.Output != request.OutputID || scan.StageID != request.StageID || len(scan.ColumnIDs) != 2 {
			t.Fatalf("scanner was not bound to the exact request: receipt=%#v bindings=%#v request=%#v", receipt, bindings, scan)
		}
		if fail {
			fail = false
			return dataframeexecution.ColumnPopulationResult{}, errors.New("backend timeout")
		}
		return dataframeexecution.ColumnPopulationResult{Columns: []dataframeexecution.ColumnPopulation{
			{ColumnID: scan.ColumnIDs[0], Status: dataframeexecution.ColumnPopulationPopulated},
			{ColumnID: scan.ColumnIDs[1], Status: dataframeexecution.ColumnPopulationEmpty},
		}}, nil
	}

	stale := request
	stale.StageID = "stale-stage"
	if _, err := service.GetConstructionColumnPopulation(context.Background(), stale); lifecycleErrorCode(err) != "STALE_STAGE_REFERENCE" {
		t.Fatalf("stale stage error = %v", err)
	}
	stale = request
	stale.ColumnIDs = []string{"stale-column"}
	if _, err := service.GetConstructionColumnPopulation(context.Background(), stale); lifecycleErrorCode(err) != "INVALID_COLUMN_ID" {
		t.Fatalf("stale column error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("scanner ran before exact stage/columns validated: %d", calls)
	}

	if _, err := service.GetConstructionColumnPopulation(context.Background(), request); lifecycleErrorCode(err) != "COLUMN_POPULATION_SCAN_FAILED" {
		t.Fatalf("scan failure = %v", err)
	}
	response, err := service.GetConstructionColumnPopulation(context.Background(), request)
	if err != nil || calls != 2 || len(response.Columns) != 2 || response.Columns[0].Status != "POPULATED" || response.Columns[1].Status != "EMPTY" {
		t.Fatalf("population response = %#v, calls=%d, error=%v", response, calls, err)
	}
	if _, err := service.GetConstructionColumnPopulation(context.Background(), request); err != nil || calls != 2 {
		t.Fatalf("cached population call = calls %d, error %v", calls, err)
	}

	stale = request
	stale.ExpectedDraftDigest = "stale-digest"
	if _, err := service.GetConstructionColumnPopulation(context.Background(), stale); lifecycleErrorCode(err) == "" {
		t.Fatal("cached summary bypassed current draft revalidation")
	}
}

func TestColumnPopulationCacheExpiresBoundsAndScopesEntries(t *testing.T) {
	now := time.Unix(100, 0)
	cache := newColumnPopulationCache(time.Second, 2, func() time.Time { return now })
	key := columnPopulationCacheKey{Project: "p", Generation: "g1", ReceiptID: "r", OutputID: "o", StageID: "s", ColumnID: "c", Scope: "scope-a"}
	cache.Put(key, "EMPTY")
	if status, ok := cache.Get(key); !ok || status != "EMPTY" {
		t.Fatalf("cached status = %q, %v", status, ok)
	}
	cache.Put(key, "UNKNOWN")
	if status, ok := cache.Get(key); !ok || status != "EMPTY" {
		t.Fatalf("UNKNOWN replaced conclusive cached status: %q, %v", status, ok)
	}
	otherScope := key
	otherScope.Scope = "scope-b"
	if _, ok := cache.Get(otherScope); ok {
		t.Fatal("cache entry crossed authorization scope")
	}
	now = now.Add(2 * time.Second)
	if _, ok := cache.Get(key); ok || len(cache.order) != 0 {
		t.Fatalf("expired cache entry or order retained: %#v", cache.order)
	}
	cache.Put(key, "POPULATED")
	if len(cache.order) != 1 {
		t.Fatalf("expiry/reinsert duplicated cache order: %#v", cache.order)
	}
	cache.Put(otherScope, "EMPTY")
	third := key
	third.ColumnID = "third"
	cache.Put(third, "POPULATED")
	if len(cache.entries) != 2 || len(cache.order) != 2 {
		t.Fatalf("cache exceeded its bound: entries=%d order=%d", len(cache.entries), len(cache.order))
	}
	cache.Put(columnPopulationCacheKey{ColumnID: "uncacheable"}, "EMPTY")
	if len(cache.entries) != 2 {
		t.Fatal("cached result without an exact receipt identity")
	}
}
