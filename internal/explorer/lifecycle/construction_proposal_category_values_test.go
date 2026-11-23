package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/explorer/construction"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestProposeConstructionPivotCategoryValues(t *testing.T) {
	observed := lifecycleTestStringScalar("final")
	cases := []struct {
		name                  string
		categories            []authoringv2.ConstructionPivotCategory
		scanValues            []dataframeexecution.CategoryValue
		scanEnabled           bool
		scanOverflow          bool
		scanConclusiveMissing bool
		wantCode              string
		wantSaved             bool
	}{
		{
			name:        "observed category is accepted",
			categories:  []authoringv2.ConstructionPivotCategory{{Key: observed, OutputColumnID: "pivot_final"}},
			scanValues:  []dataframeexecution.CategoryValue{{Present: true, Value: "final"}},
			scanEnabled: true,
			wantSaved:   true,
		},
		{
			name:        "fabricated category is rejected before persistence",
			categories:  []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("ZZZ"), OutputColumnID: "pivot_final"}},
			scanValues:  []dataframeexecution.CategoryValue{{Present: true, Value: "final"}},
			scanEnabled: true,
			wantCode:    "PIVOT_CATEGORY_HAS_NO_VALUES",
		},
		{
			name:        "zero discovered categories are rejected",
			categories:  []authoringv2.ConstructionPivotCategory{{Key: observed, OutputColumnID: "pivot_final"}},
			scanValues:  nil,
			scanEnabled: true,
			wantCode:    "PIVOT_NO_OBSERVED_CATEGORIES",
		},
		{
			name:         "valid category refuses when the limit blocks its proof",
			categories:   []authoringv2.ConstructionPivotCategory{{Key: observed, OutputColumnID: "pivot_final"}},
			scanValues:   nil,
			scanEnabled:  true,
			scanOverflow: true,
			wantCode:     "PIVOT_CATEGORIES_UNVERIFIABLE",
		},
		{
			name:                  "valid category refuses when missing presence blocks its proof",
			categories:            []authoringv2.ConstructionPivotCategory{{Key: observed, OutputColumnID: "pivot_final"}},
			scanValues:            nil,
			scanEnabled:           true,
			scanConclusiveMissing: true,
			wantCode:              "PIVOT_CATEGORIES_UNVERIFIABLE",
		},
		{
			name:       "scan unavailable path is unchanged",
			categories: []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("ZZZ"), OutputColumnID: "pivot_final"}},
			wantSaved:  true,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service, store, snapshot, categoryColumnID, valueColumnID := constructionProposalPivotFixture(t)
			scans := 0
			service.config.ScanCategories = nil
			if test.scanEnabled {
				service.config.ScanCategories = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, scan dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
					scans++
					if receipt == nil || scan.Output != "patients" || scan.StageID != recipe.ConstructionSourceProjectionID ||
						scan.ColumnID != categoryColumnID || scan.ValueColumnID != valueColumnID || !scan.RequireValue {
						t.Fatalf("verification scan request = %#v", scan)
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
					if category.ID == "" {
						t.Fatalf("category column %q is missing from the receipt stage", scan.ColumnID)
					}
					proof := compilerCategoryScanProof(construction.ConstructionCategoryDiscoveryRequest{
						OutputID: scan.Output, StageID: scan.StageID,
						CategoryColumnID: scan.ColumnID, ValueColumnID: scan.ValueColumnID,
					}, category)
					if test.scanConclusiveMissing {
						proof.OverflowWitnessFingerprint = "witness"
					}
					return dataframeexecution.CategoryScanResult{
						Values:            test.scanValues,
						Complete:          !test.scanOverflow && !test.scanConclusiveMissing,
						Overflow:          test.scanOverflow,
						ConclusiveMissing: test.scanConclusiveMissing,
						Proof:             proof,
					}, nil
				}
			}
			request := constructionProposalPivotRequest(store.created, snapshot, test.categories)
			proposal, err := service.ProposeConstruction(context.Background(), request)
			if test.wantCode != "" {
				if got := lifecycleErrorCode(err); got != test.wantCode {
					t.Fatalf("proposal error code = %q, want %q; error = %v", got, test.wantCode, err)
				}
				if proposal.ProposalID != "" || proposal.PreviewStatus != "" {
					t.Fatalf("rejected proposal returned response %#v", proposal)
				}
				if store.saveDraftCalls != 0 {
					t.Fatalf("rejected proposal persisted %d draft saves", store.saveDraftCalls)
				}
				if store.receipt != nil && store.receipt.ConstructionProposal != nil {
					t.Fatal("rejected proposal persisted a candidate receipt")
				}
				return
			}
			if err != nil {
				t.Fatalf("proposal: %v", err)
			}
			if !test.wantSaved || proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" {
				t.Fatalf("proposal response = %#v", proposal)
			}
			if test.scanEnabled && scans != 1 {
				t.Fatalf("verification scans = %d, want 1", scans)
			}
			if !test.scanEnabled && scans != 0 {
				t.Fatalf("scan unavailable path ran %d scans", scans)
			}
		})
	}
}

func TestProposeConstructionReportsUnchangedPivotProbeDiagnostics(t *testing.T) {
	cases := []struct {
		name        string
		probeError  error
		overflow    bool
		missing     bool
		values      []dataframeexecution.CategoryValue
		wantOutcome string
	}{
		{
			name:        "execution error is reported",
			probeError:  errors.New("probe unavailable"),
			wantOutcome: "PROBE_ERROR",
		},
		{
			name:        "limit exceeded is reported",
			overflow:    true,
			wantOutcome: "LIMIT_EXCEEDED",
		},
		{
			name:        "missing presence is reported",
			missing:     true,
			wantOutcome: "MISSING_UNSUPPORTED",
		},
		{
			name:        "no observed categories is reported",
			values:      nil,
			wantOutcome: "NO_OBSERVED_CATEGORIES",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service, store, snapshot, _, _ := constructionProposalPivotFixtureWithUnrelatedStep(t)
			service.config.ScanCategories = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, scan dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
				if test.probeError != nil {
					return dataframeexecution.CategoryScanResult{}, test.probeError
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
				proof := compilerCategoryScanProof(construction.ConstructionCategoryDiscoveryRequest{
					OutputID: scan.Output, StageID: scan.StageID,
					CategoryColumnID: scan.ColumnID, ValueColumnID: scan.ValueColumnID,
				}, category)
				if test.missing {
					proof.OverflowWitnessFingerprint = "witness"
				}
				return dataframeexecution.CategoryScanResult{
					Values:            test.values,
					Complete:          !test.overflow && !test.missing,
					Overflow:          test.overflow,
					ConclusiveMissing: test.missing,
					Proof:             proof,
				}, nil
			}
			request := constructionProposalPivotRequest(store.created, snapshot, []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("final"), OutputColumnID: "pivot_final"}})
			request.CandidateConstruction.Steps = append(request.CandidateConstruction.Steps, constructionPivotFilterStep())
			request.ChangedStepID = "keep_positive"
			proposal, err := service.ProposeConstruction(context.Background(), request)
			if err != nil {
				t.Fatalf("unrelated saved Pivot must not reject the proposal: %v", err)
			}
			if proposal.ProposalID == "" || proposal.PreviewStatus != "PREVIEW_PENDING" {
				t.Fatalf("successful proposal response = %#v", proposal)
			}
			if len(proposal.Diagnostics) != 1 {
				t.Fatalf("proposal diagnostics = %#v, want one skipped proof", proposal.Diagnostics)
			}
			diagnostic := proposal.Diagnostics[0]
			if diagnostic.Code != "PIVOT_CATEGORIES_UNVERIFIABLE" || diagnostic.StepID != "pivot_status" ||
				diagnostic.Outcome != test.wantOutcome || diagnostic.Message == "" {
				t.Fatalf("proposal diagnostic = %#v", diagnostic)
			}
		})
	}
}

func TestProposeConstructionChangedPivotProbeErrorStillRejects(t *testing.T) {
	service, store, snapshot, _, _ := constructionProposalPivotFixture(t)
	service.config.ScanCategories = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		return dataframeexecution.CategoryScanResult{}, errors.New("probe unavailable")
	}
	request := constructionProposalPivotRequest(store.created, snapshot, []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("final"), OutputColumnID: "pivot_final"}})
	proposal, err := service.ProposeConstruction(context.Background(), request)
	if err == nil {
		t.Fatalf("changed Pivot probe error must reject the proposal, got %#v", proposal)
	}
	if proposal.ProposalID != "" || proposal.PreviewStatus != "" {
		t.Fatalf("rejected proposal returned response %#v", proposal)
	}
	if len(proposal.Diagnostics) != 0 {
		t.Fatalf("rejected proposal returned diagnostics %#v", proposal.Diagnostics)
	}
}

func constructionProposalPivotFixtureWithUnrelatedStep(t *testing.T) (*Service, *fakeStore, capability.Snapshot, string, string) {
	t.Helper()
	service, store, snapshot, categoryColumnID, valueColumnID := constructionProposalPivotFixture(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Construction.Steps = append(workspace.Documents[0].Construction.Steps, constructionPivotFilterStep())
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate unrelated filter step: %v", err)
	}
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig, store.created.DraftDigest = encoded, digest
	return service, store, snapshot, categoryColumnID, valueColumnID
}

func constructionPivotFilterStep() authoringv2.ConstructionStep {
	return authoringv2.ConstructionStep{
		ID: "keep_positive", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "pivot_status"}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
			ColumnID: "group_id", Operator: authoringv2.ConstructionFilterExists,
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: "group_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
			{ID: "pivot_final", Name: "final_value", Label: "Final", Type: "string"},
		},
	}
}

func constructionProposalPivotFixture(t *testing.T) (*Service, *fakeStore, capability.Snapshot, string, string) {
	t.Helper()
	service, store, snapshot := constructionProposalService(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
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
	store.created.DraftConfig, store.created.DraftDigest = encoded, digest
	return service, store, snapshot, "category_id", "value_id"
}

func constructionProposalPivotRequest(
	owner *explorer.Explorer,
	snapshot capability.Snapshot,
	categories []authoringv2.ConstructionPivotCategory,
) construction.ConstructionProposalRequest {
	pivot := &authoringv2.ConstructionPivot{
		ConstructionID: "pivot_status", GroupKeyIDs: []string{"group_id"},
		CategoryColumnID: "category_id", ValueColumnID: "value_id",
		Categories:             append([]authoringv2.ConstructionPivotCategory(nil), categories...),
		DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateError,
		MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
		UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
	}
	return construction.ConstructionProposalRequest{
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
