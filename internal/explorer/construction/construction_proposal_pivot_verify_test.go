package construction

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
)

func TestVerifyConstructionProposalPivotCategoriesReportsUnchangedPivot(t *testing.T) {
	service, store, snapshot, _, _ := constructionPivotVerifyFixture(t)
	scans := 0
	service.config.ScanCategories = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, scan dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		scans++
		if scan.StageID != recipe.ConstructionSourceProjectionID || !scan.RequireValue {
			t.Fatalf("unchanged Pivot scan request = %#v", scan)
		}
		var category explorer.ReceiptConstructionStageColumn
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
	request := constructionPivotVerifyRequest(store, snapshot, []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("ZZZ"), OutputColumnID: "pivot_final"}})
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
	service.config.ScanCategories = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, scan dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		var category explorer.ReceiptConstructionStageColumn
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
	request := constructionPivotVerifyRequest(store, snapshot, []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("ZZZ"), OutputColumnID: "pivot_final"}})
	request.ChangedStepID = "unrelated_step"
	candidate := request.CandidateConstruction
	workspace, err := authoringv2.DecodeWorkspace(store.DraftConfig)
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

func constructionCompilerCategoryScanProof(request ConstructionCategoryDiscoveryRequest, column explorer.ReceiptConstructionStageColumn) facade.CategoryScanProof {
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
	workspace, err := authoringv2.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.Columns = []authoringv2.Column{
		{ColumnID: "category_id", Column: "category", Label: "Category", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "gender", ProjectionMode: "VALUE"}}},
		{ColumnID: "value_id", Column: "value", Label: "Value", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}},
		{ColumnID: "group_id", Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "name", ProjectionMode: "VALUE"}}},
	}
	document.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "pivot_status", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: &authoringv2.ConstructionPivot{
			ConstructionID: "pivot_status", GroupKeyIDs: []string{"group_id"},
			CategoryColumnID: "category_id", ValueColumnID: "value_id",
			Categories:             []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("final"), OutputColumnID: "pivot_final"}},
			DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateError,
			MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
			UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
		}},
		Outputs: []authoringv2.StageColumn{
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
	catalog := authoringv2.CatalogFromCapability(snapshot, "patients")
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
			Catalog: func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return catalog },
		},
		GetOwner: func(context.Context, string, string) (*explorer.Explorer, error) { return owner, nil },
		Compile: func(_ context.Context, request CompileRequest) (*explorer.CompilationReceipt, error) {
			receipt := lifecycleConstructionReceipt(snapshot, request.Workspace)
			receipt.ConstructionStages = map[string][]explorer.ReceiptConstructionStage{
				"patients": testConstructionStageDescriptors(request.Workspace, "patients"),
			}
			return receipt, nil
		},
		CompileReceipt: func(_ context.Context, request ports.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			receipt := lifecycleConstructionReceipt(snapshot, request.Workspace)
			receipt.ConstructionStages = map[string][]explorer.ReceiptConstructionStage{
				"patients": testConstructionStageDescriptors(request.Workspace, "patients"),
			}
			return receipt, nil
		},
		VerifyProposalReceipt: func(_ context.Context, _ string, _ *explorer.CompilationReceipt, _, _, _ string, _ capability.Snapshot, expected *authoringv2.Workspace) (authoringv2.Workspace, error) {
			return *expected, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, owner, snapshot, "category_id", "value_id"
}

func constructionPivotVerifyRequest(owner *explorer.Explorer, snapshot capability.Snapshot, categories []authoringv2.ConstructionPivotCategory) ConstructionProposalRequest {
	pivot := &authoringv2.ConstructionPivot{
		ConstructionID: "pivot_status", GroupKeyIDs: []string{"group_id"},
		CategoryColumnID: "category_id", ValueColumnID: "value_id",
		Categories:             append([]authoringv2.ConstructionPivotCategory(nil), categories...),
		DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateError,
		MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
		UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
	}
	return ConstructionProposalRequest{
		Project: owner.Project, ExplorerID: owner.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
		OutputID: "patients", ChangedStepID: "pivot_status",
		CandidateConstruction: authoringv2.Construction{
			Version: authoringv2.ConstructionVersion,
			Steps: []authoringv2.ConstructionStep{{
				ID: "pivot_status", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: pivot},
				Outputs: []authoringv2.StageColumn{
					{ID: "group_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
					{ID: "pivot_final", Name: "final_value", Label: "Final", Type: "string"},
				},
			}},
		},
	}
}
