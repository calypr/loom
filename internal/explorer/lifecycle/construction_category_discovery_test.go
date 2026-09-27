package lifecycle

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestDiscoverConstructionCategoriesBindsCompleteScanToExactStageAndPair(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	for index := range workspace.Documents[0].Columns {
		workspace.Documents[0].Columns[index].LogicalType = "string"
	}
	column := workspace.Documents[0].Columns[0]
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
	upgraded, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.Construction == nil || len(upgraded.Columns) != 2 {
		t.Fatalf("upgraded source columns = %#v", upgraded)
	}
	request := ConstructionCategoryDiscoveryRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
		CategoryColumnID: upgraded.Columns[1].ColumnID,
		ValueColumnID:    upgraded.Columns[0].ColumnID,
	}

	calls := 0
	incomplete := false
	service.config.ScanCategories = func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, scan dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		calls++
		if receipt == nil || bindings.Project != request.Project || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != request.OutputID {
			t.Fatalf("scanner received unbound receipt or runtime bindings: %#v %#v", receipt, bindings)
		}
		if scan.Output != request.OutputID || scan.StageID != request.StageID || scan.ColumnID != request.CategoryColumnID || scan.ValueColumnID != request.ValueColumnID || scan.MaxValues != 256 {
			t.Fatalf("scanner received a different stage or pair: %#v", scan)
		}
		var category explorer.ReceiptConstructionStageColumn
		for _, stage := range receipt.ConstructionStages[request.OutputID] {
			if stage.ID != request.StageID {
				continue
			}
			for _, column := range stage.Columns {
				if column.ID == request.CategoryColumnID {
					category = column
				}
			}
		}
		if category.ID == "" {
			t.Fatalf("requested category column %q was absent from the stage receipt", request.CategoryColumnID)
		}
		return dataframeexecution.CategoryScanResult{
			Values:   []dataframeexecution.CategoryValue{{Present: true, Value: "final"}, {Present: false}},
			Complete: !incomplete,
			Proof:    compilerCategoryScanProof(request, category),
		}, nil
	}

	stale := request
	stale.StageID = "stale-stage"
	if _, err := service.DiscoverConstructionCategories(context.Background(), stale); lifecycleErrorCode(err) != "STALE_STAGE_REFERENCE" {
		t.Fatalf("stale stage error = %v", err)
	}
	stale = request
	stale.CategoryColumnID = "stale-column"
	if _, err := service.DiscoverConstructionCategories(context.Background(), stale); lifecycleErrorCode(err) != "INVALID_CATEGORY_COLUMN_ID" {
		t.Fatalf("stale category column error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("scanner ran before stage and column IDs were validated: %d calls", calls)
	}

	response, err := service.DiscoverConstructionCategories(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !response.Complete || response.StageID != request.StageID || response.CategoryColumnID != request.CategoryColumnID || response.ValueColumnID != request.ValueColumnID || response.ProofFingerprint == "" {
		t.Fatalf("category discovery response = %#v; calls = %d", response, calls)
	}
	if len(response.Categories) != 2 || response.Categories[0].Label != "final" || response.Categories[0].Key.String == nil || *response.Categories[0].Key.String != "final" || response.Categories[1].Label != "Missing" || response.Categories[1].Key.Kind != authoringv2.TableScalarMissing {
		t.Fatalf("typed category values = %#v", response.Categories)
	}

	incomplete = true
	if _, err := service.DiscoverConstructionCategories(context.Background(), request); lifecycleErrorCode(err) != "CATEGORY_SCAN_INCOMPLETE" {
		t.Fatalf("incomplete scan error = %v", err)
	}
	service.config.ScanCategories = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		return dataframeexecution.CategoryScanResult{}, context.DeadlineExceeded
	}
	if _, err := service.DiscoverConstructionCategories(context.Background(), request); lifecycleErrorCode(err) != "CATEGORY_SCAN_TIMEOUT" {
		t.Fatalf("timed-out scan error = %v", err)
	}
}

func compilerCategoryScanProof(request ConstructionCategoryDiscoveryRequest, column explorer.ReceiptConstructionStageColumn) compiler.CategoryScanProof {
	return compiler.CategoryScanProof{
		Version: 2, Output: request.OutputID, StageID: request.StageID, ColumnID: request.CategoryColumnID,
		ValueColumnID: request.ValueColumnID, Column: column.Name, Kind: column.Type,
		Cardinality: string(column.Cardinality), MaxValues: compiler.MaxCategoryScanValues,
		OutputSchemaDigest: "schema", PlanFingerprint: "plan", QueryFingerprint: "query", Fingerprint: "proof",
	}
}
