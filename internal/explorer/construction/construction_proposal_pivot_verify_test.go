package construction

import (
	"context"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
)

func TestVerifyConstructionProposalPivotCategoriesReportsUnchangedPivot(t *testing.T) {
	service, store, snapshot, _, _ := constructionPivotVerifyFixture(t)
	scans := 0
	service.config.ScanCategories = func(_ context.Context, receipt *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, scan dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		scans++
		if scan.StageID != recipe.ConstructionSourceProjectionID || !scan.RequireValue {
			t.Fatalf("unchanged Pivot scan request = %#v", scan)
		}
		var category receiptmodel.ReceiptConstructionStageColumn
		for _, stage := range receipt.ConstructionStages["patients"] {
			if stage.ID != scan.StageID {
				continue
			}
			for _, column := range stage.Columns {
				if column.ID == scan.ColumnID {
					category = column
				}
			}
		}
		proof := constructionCompilerCategoryScanProof(ConstructionCategoryDiscoveryRequest{
			OutputID: scan.Output, StageID: scan.StageID,
			CategoryColumnID: scan.ColumnID, ValueColumnID: scan.ValueColumnID,
		}, category)
		return dataframeexecution.CategoryScanResult{
			Values:   []dataframeexecution.CategoryValue{{Present: true, Value: "final"}},
			Complete: true, Proof: proof,
		}, nil
	}
	request := constructionPivotVerifyRequest(store, snapshot, []ws.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("ZZZ"), OutputColumnID: "pivot_final"}})
	request.ChangedStepID = "keep_positive"
	candidate := request.CandidateConstruction
	diagnostics, err := service.verifyConstructionProposalPivotCategoriesExcept(context.Background(), request, candidate, nil, nil)
	if err != nil {
		t.Fatalf("unchanged Pivot verification must not reject the edit: %v", err)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != "PIVOT_CATEGORIES_UNVERIFIABLE" ||
		diagnostics[0].StepID != "pivot_status" || diagnostics[0].Outcome != "CATEGORY_HAS_NO_VALUES" {
		t.Fatalf("unchanged Pivot diagnostics = %#v, want one CATEGORY_HAS_NO_VALUES probe diagnostic", diagnostics)
	}
	if scans != 1 {
		t.Fatalf("unchanged Pivot verification scans = %d, want 1", scans)
	}
}

func TestVerifyConstructionProposalPivotCategoriesRejectsModifiedDownstreamPivot(t *testing.T) {
	service, store, snapshot, _, _ := constructionPivotVerifyFixture(t)
	service.config.ScanCategories = func(_ context.Context, receipt *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, scan dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		var category receiptmodel.ReceiptConstructionStageColumn
		for _, stage := range receipt.ConstructionStages["patients"] {
			if stage.ID != scan.StageID {
				continue
			}
			for _, column := range stage.Columns {
				if column.ID == scan.ColumnID {
					category = column
				}
			}
		}
		proof := constructionCompilerCategoryScanProof(ConstructionCategoryDiscoveryRequest{
			OutputID: scan.Output, StageID: scan.StageID,
			CategoryColumnID: scan.ColumnID, ValueColumnID: scan.ValueColumnID,
		}, category)
		return dataframeexecution.CategoryScanResult{
			Values:   []dataframeexecution.CategoryValue{{Present: true, Value: "final"}},
			Complete: true, Proof: proof,
		}, nil
	}
	request := constructionPivotVerifyRequest(store, snapshot, []ws.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("ZZZ"), OutputColumnID: "pivot_final"}})
	request.ChangedStepID = "unrelated_step"
	candidate := request.CandidateConstruction
	workspace, err := ws.DecodeWorkspace(store.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	base := workspace.Documents[0].Construction
	if base == nil {
		t.Fatal("fixture has no base construction")
	}
	if _, err := service.verifyConstructionProposalPivotCategoriesExcept(context.Background(), request, candidate, base, nil); lifecycleErrorCode(err) != "PIVOT_CATEGORY_HAS_NO_VALUES" {
		t.Fatalf("modified downstream Pivot verification error = %v, want PIVOT_CATEGORY_HAS_NO_VALUES", err)
	}
}

func constructionCompilerCategoryScanProof(request ConstructionCategoryDiscoveryRequest, column receiptmodel.ReceiptConstructionStageColumn) facade.CategoryScanProof {
	return facade.CategoryScanProof{
		Version: 2, Output: request.OutputID, StageID: request.StageID, ColumnID: request.CategoryColumnID,
		ValueColumnID: request.ValueColumnID, Column: column.Name, Kind: column.Type,
		Cardinality: string(column.Cardinality), MaxValues: 256, ValuePresenceRequired: true,
		OutputSchemaDigest: "schema", PlanFingerprint: "plan", QueryFingerprint: "query", Fingerprint: "proof",
	}
}

func constructionPivotVerifyFixture(t *testing.T) (*Service, *explorer.Explorer, capability.Snapshot, string, string) {
	t.Helper()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "pivot-verify-snapshot", scope)
	snapshot.Nodes = []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.Columns = []ws.Column{
		{ColumnID: "category_id", Column: "category", Label: "Category", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "gender", ProjectionMode: "VALUE"}}},
		{ColumnID: "value_id", Column: "value", Label: "Value", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}},
		{ColumnID: "group_id", Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "name", ProjectionMode: "VALUE"}}},
	}
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "pivot_status", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
			ConstructionID: "pivot_status", GroupKeyIDs: []string{"group_id"},
			CategoryColumnID: "category_id", ValueColumnID: "value_id",
			Categories:             []ws.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("final"), OutputColumnID: "pivot_final"}},
			DuplicatePolicy:        ws.ConstructionPivotDuplicateError,
			MissingCellPolicy:      ws.ConstructionPivotMissingNull,
			UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
		}},
		Outputs: []ws.StageColumn{
			{ID: "group_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
			{ID: "pivot_final", Name: "final_value", Label: "Final", Type: "string"},
		},
	}}}
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate pivot proposal fixture: %v", err)
	}
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	owner := &explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", DraftConfig: encoded, DraftVersion: 7, DraftDigest: digest}
	catalog := ws.CatalogFromCapability(snapshot, "patients")
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope.Clone()}
	service, err := NewService(Config{
		Capability: ports.CapabilityResolver{
			Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return authorized, nil
			},
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return authorized, nil
			},
			Catalog: func(capability.Snapshot, string) ws.CatalogSnapshot { return catalog },
		},
		GetOwner: func(context.Context, string, string) (*explorer.Explorer, error) { return owner, nil },
		Compile: func(_ context.Context, request CompileRequest) (*receiptmodel.CompilationReceipt, error) {
			receipt := lifecycleConstructionReceipt(snapshot, request.Workspace)
			receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{
				"patients": testConstructionStageDescriptors(request.Workspace, "patients"),
			}
			return receipt, nil
		},
		CompileReceipt: func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			receipt := lifecycleConstructionReceipt(snapshot, request.Workspace)
			receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{
				"patients": testConstructionStageDescriptors(request.Workspace, "patients"),
			}
			return receipt, nil
		},
		VerifyProposalReceipt: func(_ context.Context, _ string, _ *receiptmodel.CompilationReceipt, _, _, _ string, _ capability.Snapshot, expected *ws.Workspace) (ws.Workspace, error) {
			return *expected, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, owner, snapshot, "category_id", "value_id"
}

func constructionPivotVerifyRequest(owner *explorer.Explorer, snapshot capability.Snapshot, categories []ws.ConstructionPivotCategory) ConstructionProposalRequest {
	pivot := &ws.ConstructionPivot{
		ConstructionID: "pivot_status", GroupKeyIDs: []string{"group_id"},
		CategoryColumnID: "category_id", ValueColumnID: "value_id",
		Categories:             append([]ws.ConstructionPivotCategory(nil), categories...),
		DuplicatePolicy:        ws.ConstructionPivotDuplicateError,
		MissingCellPolicy:      ws.ConstructionPivotMissingNull,
		UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
	}
	return ConstructionProposalRequest{
		Project: owner.Project, ExplorerID: owner.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
		OutputID: "patients", ChangedStepID: "pivot_status",
		CandidateConstruction: ws.Construction{
			Version: ws.ConstructionVersion,
			Steps: []ws.ConstructionStep{{
				ID: "pivot_status", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: pivot},
				Outputs: []ws.StageColumn{
					{ID: "group_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
					{ID: "pivot_final", Name: "final_value", Label: "Final", Type: "string"},
				},
			}},
		},
	}
}
