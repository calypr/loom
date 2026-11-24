package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/rowsource"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
)

const (
	rs05PivotID      = "pivot-one"
	rs05OtherPivotID = "pivot-two"
	rs05OldColumn    = "old-private-input"
	rs05NewColumn    = "pivot-new-input"
	rs05OtherColumn  = "pivot-two-private"
	rs05HelperID     = "pv_helper"
	rs05Helper2ID    = "pv_helper_two"
	rs05HelperColumn = "pcol"
	rs05FilterID     = "filter-active"
	rs05CategoryCol  = "public-category"
	rs05ValueCol     = "public-value"
)

func rs05RootProjection(columnID, ownerStepID string) ws.ConstructionSourceProjection {
	return constructionfixtures.PivotSourceRootProjection(columnID, ownerStepID, filterSourceField)
}

func rs05PivotStep(id string, groupKeys ...string) ws.ConstructionStep {
	return constructionfixtures.PivotSourceStep(id, groupKeys, rs05CategoryCol, rs05ValueCol)
}

func rs05CountOwned(projections []ws.ConstructionSourceProjection, ownerStepID string) int {
	count := 0
	for _, projection := range projections {
		if projection.OwnerStepID == ownerStepID {
			count++
		}
	}
	return count
}

func rs05HasProjection(projections []ws.ConstructionSourceProjection, columnID, ownerStepID string) bool {
	for _, projection := range projections {
		if projection.ColumnID == columnID && projection.OwnerStepID == ownerStepID {
			return true
		}
	}
	return false
}

func rs05HasOwnedHelper(steps []ws.ConstructionStep, ownerID string) bool {
	for _, step := range steps {
		if step.ID == rs05HelperID && step.OwnerStepID == ownerID &&
			step.Operation.Kind == ws.ConstructionOperationRelatedField {
			return true
		}
	}
	return false
}

func rs05CloneConstruction(construction ws.Construction) ws.Construction {
	return constructionfixtures.CloneConstructionSlices(construction)
}

func rs05HelperStep() ws.ConstructionStep {
	return constructionfixtures.PivotSourceHelperStep(rs05PivotID, rs05HelperID, rs05HelperColumn, filterSourceRootNode, filterSourceRootType, filterSourceField, filterSourceIdentity)
}

func rs05SeedPivotStep() ws.ConstructionStep {
	return constructionfixtures.PivotSourceSeedPivotStep(rs05PivotID, rs05HelperID, rs05HelperColumn, filterSourceIdentity)
}

func rs05SeedFilterStep(columnID string, outputs []ws.StageColumn) ws.ConstructionStep {
	return constructionfixtures.FilterStep(rs05FilterID, columnID, outputs)
}

func rs05SeedDurable() ws.Construction {
	return constructionfixtures.PivotSourceSeedDurable(rs05FilterID, filterSourceIdentity, rs05HelperID, rs05HelperColumn, rs05PivotID, filterSourceIdentity, filterSourceField, filterSourceIdentityColumns())
}

func rs05SeedCleared() ws.Construction {
	return ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05SeedFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
			rs05SeedPivotStep(),
		},
	}
}

func rs05FilterOwnedProjection() ws.ConstructionSourceProjection {
	return rs05RootProjection(filterSourceColumn, rs05FilterID)
}

func rs05AssertionPivotCleared(t *testing.T, name string, prepared ws.Construction) {
	t.Helper()
	if rs05HasOwnedHelper(prepared.Steps, rs05PivotID) {
		t.Errorf("REGRESSION [%s]: preparation RE-ADDED the cleared pivot-owned RELATED_FIELD helper step %s", name, rs05HelperID)
	}
	if rs05HasProjection(prepared.SourceProjections, rs05HelperColumn, rs05PivotID) {
		t.Errorf("REGRESSION [%s]: preparation RE-ADDED the cleared pivot-owned SourceProjection %s", name, rs05HelperColumn)
	}
}

func TestXopPortFilterPreparationRestoresClearedPivotInputs(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-filter-restores-pivot")
	service := filterSourceService(t)
	durable := rs05SeedDurable()
	cleared := rs05SeedCleared()
	base := filterSourceBase(snapshot, durable)

	preparedA, err := service.rowSourceService.ConstructionCandidateWithFilterSources(context.Background(), base, rowsourceProposalRequestForTest(filterSourceProposalRequest(rs05FilterID, nil)), rs05CloneConstruction(cleared))
	if err != nil {
		t.Fatalf("prepare filter (no filterSources) failed: %v", err)
	}
	rs05AssertionPivotCleared(t, "no filterSources", preparedA)

	clearedB := rs05CloneConstruction(cleared)
	clearedB.Steps[0] = rs05SeedFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn()))
	preparedB, err := service.rowSourceService.ConstructionCandidateWithFilterSources(context.Background(), base, rowsourceProposalRequestForTest(filterSourceProposalRequest(rs05FilterID,
		[]rowsource.ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)})), clearedB)
	if err != nil {
		t.Fatalf("prepare filter with signed filterSource failed: %v", err)
	}
	rs05AssertionPivotCleared(t, "signed filterSource", preparedB)
	if count := rs05CountOwned(preparedB.SourceProjections, rs05FilterID); count != 1 {
		t.Fatalf("filter-owned projection count = %d, want the one signed projection: %#v", count, preparedB.SourceProjections)
	}
}

func TestXopPortPerOwnerIsolationOnlyEditedOwnerPreserves(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-owner-isolation")
	service := filterSourceService(t)
	durable := rs05SeedDurable()
	durable.Steps = append(durable.Steps, rs05PivotStep(rs05OtherPivotID, "public-key"))
	base := filterSourceBase(snapshot, durable)

	candidate := rs05SeedCleared()
	candidate.Steps = append(candidate.Steps, rs05PivotStep(rs05OtherPivotID, "public-key"))
	request := construction.ConstructionProposalRequest{
		Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
		ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:xop", OutputID: filterSourceOutput,
		ChangedStepID: rs05FilterID,
	}
	prepared, err := service.rowSourceService.ConstructionCandidateWithFilterSources(context.Background(), base, rowsourceProposalRequestForTest(request), candidate)
	if err != nil {
		t.Fatalf("filter prepare with foreign pivot-two: %v", err)
	}
	if constructionStepIndex(prepared.Steps, rs05OtherPivotID) < 0 {
		t.Errorf("ownership over-stripped: foreign pivot-two disappeared during filter edit")
	}
	rs05AssertionPivotCleared(t, "per-owner isolation", prepared)
}

func TestXopPortControlDurableAlreadyClearedStaysCleared(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-cleared-durable")
	service := filterSourceService(t)
	cleared := rs05SeedCleared()
	base := filterSourceBase(snapshot, rs05CloneConstruction(cleared))

	clearedForFilter := rs05CloneConstruction(cleared)
	clearedForFilter.Steps[0] = rs05SeedFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn()))
	prepared, err := service.rowSourceService.ConstructionCandidateWithFilterSources(context.Background(), base, rowsourceProposalRequestForTest(filterSourceProposalRequest(rs05FilterID,
		[]rowsource.ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)})), clearedForFilter)
	if err != nil {
		t.Fatalf("prepare filter against cleared durable: %v", err)
	}
	rs05AssertionPivotCleared(t, "control cleared durable", prepared)
	if !rs05HasProjection(prepared.SourceProjections, filterSourceColumn, rs05FilterID) {
		t.Errorf("control: filter's own signed projection did not materialize; harness broken")
	}
}

func TestXopPortFilterSetBranchDoesNotReImportClearedPivotProjection(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-filter-set-pivot-clear")
	service := filterSourceService(t)
	baseConstruction := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05SeedFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
			rs05PivotStep(rs05OtherPivotID, "public-key"),
		},
		SourceProjections: []ws.ConstructionSourceProjection{rs05RootProjection(rs05OtherColumn, rs05OtherPivotID)},
	}
	candidate := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05SeedFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn())),
			rs05PivotStep(rs05OtherPivotID, "public-key"),
		},
	}
	prepared, err := service.rowSourceService.ConstructionCandidateWithFilterSources(context.Background(), filterSourceBase(snapshot, baseConstruction), rowsourceProposalRequestForTest(filterSourceProposalRequest(rs05FilterID,
		[]rowsource.ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)})), candidate)
	if err != nil {
		t.Fatalf("filter SET while the other pivot source was cleared: %v", err)
	}
	if rs05HasProjection(prepared.SourceProjections, rs05OtherColumn, rs05OtherPivotID) {
		t.Fatalf("filter SET re-imported the cleared foreign pivot-owned projection: %#v", prepared.SourceProjections)
	}
	if !rs05HasProjection(prepared.SourceProjections, filterSourceColumn, rs05FilterID) {
		t.Fatalf("filter SET did not materialize its own signed projection: %#v", prepared.SourceProjections)
	}
}

func rs05ProposeService(t *testing.T) (*Service, *fakeStore, capability.Snapshot) {
	t.Helper()
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	donor := filterSourceSnapshot("rs05-propose")
	snapshot.Nodes = donor.Nodes
	snapshot.Candidates = donor.Candidates
	resolver := filterSourceResolver(t)
	setup.Config.RowChoicePlanner, setup.Config.RowChoiceResolver = resolver, resolver
	setup.Config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		receipt := lifecycleConstructionReceipt(snapshot, request.Workspace)
		receipt.ConstructionProposal = authoringcore.CloneConstructionProposalBinding(request.ConstructionProposal)
		receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{
			"patients": testConstructionStageDescriptors(request.Workspace, "patients"),
		}
		var err error
		receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = receiptmodel.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}
	setup.Config.PreviewReceipt = func(_ context.Context, receipt *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil {
			return dataframeexecution.PreviewSummary{}, context.Canceled
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"body_structure_id"}, Complete: true}, nil
	}
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}
	setup.Config.Capability.Token = func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil }
	setup.Config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) { return authorized, nil }
	setup.Config.Capability.ForExecution = func(context.Context, string, string) (ports.AuthorizedCapability, error) { return authorized, nil }
	return newTestServiceFromSetup(t, setup), store, snapshot
}

func rs05ProposeSaveDraft(t *testing.T, store *fakeStore, construction ws.Construction) {
	t.Helper()
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.SemanticsVersion = ws.CurrentSemanticsVersion
	document := ws.Document{
		Kind:             ws.Kind,
		Output:           ws.Output{ID: "patients", Title: "Body structures"},
		RootResourceType: filterSourceRootType,
		Route:            ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: filterSourceRootType},
		Rows:             ws.RecordsRowDefinition(),
		Columns: []ws.Column{
			{
				ColumnID: filterSourceIdentity, Column: "body_structure_id", Label: "Body structure ID",
				LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
				Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
			},
			{
				ColumnID: rs05CategoryCol, Column: "public_category", Label: "Public category",
				LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
				Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
			},
			{
				ColumnID: rs05ValueCol, Column: "public_value", Label: "Public value",
				LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
				Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "description", ProjectionMode: "VALUE"}},
			},
		},
		Construction: &construction,
	}
	workspace.Documents = []ws.Document{document}
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate rs05 propose draft: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig, store.created.DraftDigest = draft, digest
}

func rs05PublicOutputs(withPcol bool) []ws.StageColumn {
	outputs := []ws.StageColumn{
		{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
		{ID: rs05CategoryCol, Name: "public_category", Label: "Public category", Type: "string"},
		{ID: rs05ValueCol, Name: "public_value", Label: "Public value", Type: "string"},
	}
	if withPcol {
		outputs = append(outputs, ws.StageColumn{
			ID: rs05OldColumn, Name: ws.ConstructionSourceProjectionName(rs05OldColumn), Label: "BodyStructure.active", Type: "boolean"})
	}
	return outputs
}

func rs05ProposeFilterStep(columnID string, outputs []ws.StageColumn) ws.ConstructionStep {
	return constructionfixtures.FilterStep(rs05FilterID, columnID, outputs)
}

func rs05ProposePivotStep(groupKeys ...string) ws.ConstructionStep {
	outputs := make([]ws.StageColumn, 0, len(groupKeys)+1)
	for _, key := range groupKeys {
		keyType, keyName := "string", key
		switch key {
		case rs05OldColumn:
			keyType = "boolean"
			keyName = ws.ConstructionSourceProjectionName(rs05OldColumn)
		case filterSourceIdentity:
			keyName = "body_structure_id"
		}
		outputs = append(outputs, ws.StageColumn{ID: key, Name: keyName, Label: key, Type: keyType})
	}
	outputs = append(outputs, ws.StageColumn{ID: "pval", Name: "pval", Label: "pval", Type: "string"})
	return ws.ConstructionStep{
		ID:     rs05PivotID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: rs05FilterID}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
			ConstructionID: rs05PivotID, GroupKeyIDs: groupKeys, CategoryColumnID: rs05CategoryCol, ValueColumnID: rs05ValueCol,
			Categories:             []ws.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("t"), OutputColumnID: "pval"}},
			DuplicatePolicy:        ws.ConstructionPivotDuplicateError,
			MissingCellPolicy:      ws.ConstructionPivotMissingNull,
			UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
		}},
		Outputs: outputs,
	}
}

func rs05ProposeRequest(store *explorer.Explorer, snapshot capability.Snapshot, changedStepID string, candidate ws.Construction) construction.ConstructionProposalRequest {
	return construction.ConstructionProposalRequest{
		Project: store.Project, ExplorerID: store.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.DraftVersion, ExpectedDraftDigest: store.DraftDigest,
		OutputID: "patients", ChangedStepID: changedStepID, CandidateConstruction: candidate,
	}
}

func TestProposeConstructionFilterEditDoesNotRestoreClearedPivotSourceInput(t *testing.T) {
	service, store, snapshot := rs05ProposeService(t)
	durable := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05ProposeFilterStep(filterSourceIdentity, rs05PublicOutputs(true)),
			rs05ProposePivotStep(filterSourceIdentity, rs05OldColumn),
		},
		SourceProjections: []ws.ConstructionSourceProjection{rs05RootProjection(rs05OldColumn, rs05PivotID)},
	}
	rs05ProposeSaveDraft(t, store, durable)
	acceptedBefore := string(store.created.DraftConfig)
	cleared := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05ProposeFilterStep(filterSourceIdentity, rs05PublicOutputs(false)),
			rs05ProposePivotStep(filterSourceIdentity),
		},
	}
	proposal, err := service.ProposeConstruction(context.Background(),
		rs05ProposeRequest(store.created, snapshot, rs05FilterID, cleared))
	if err != nil {
		t.Fatalf("propose filter edit with cleared Pivot source input: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" {
		t.Fatalf("proposal status = %q, want a previewable candidate", proposal.PreviewStatus)
	}
	if len(proposal.CandidateConstruction.SourceProjections) != 0 {
		t.Fatalf("REGRESSION: proposal restored the cleared Pivot-owned source input: %#v", proposal.CandidateConstruction.SourceProjections)
	}
	if constructionStepIndex(proposal.CandidateConstruction.Steps, rs05PivotID) < 0 ||
		constructionStepIndex(proposal.CandidateConstruction.Steps, rs05FilterID) < 0 {
		t.Fatalf("proposal lost a surviving step: %#v", proposal.CandidateConstruction.Steps)
	}
	if string(store.created.DraftConfig) != acceptedBefore {
		t.Fatal("proposal changed the accepted draft")
	}
}

func TestProposeConstructionFilterSourceSetDoesNotRestoreClearedPivotSourceInput(t *testing.T) {
	service, store, snapshot := rs05ProposeService(t)
	durable := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05ProposeFilterStep(filterSourceIdentity, rs05PublicOutputs(true)),
			rs05ProposePivotStep(filterSourceIdentity, rs05OldColumn),
		},
		SourceProjections: []ws.ConstructionSourceProjection{rs05RootProjection(rs05OldColumn, rs05PivotID)},
	}
	rs05ProposeSaveDraft(t, store, durable)
	cleared := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05ProposeFilterStep(filterSourceColumn, append(rs05PublicOutputs(false), filterSourceProjectionColumn())),
			rs05ProposePivotStep(filterSourceIdentity),
		},
	}
	request := rs05ProposeRequest(store.created, snapshot, rs05FilterID, cleared)
	request.FilterSources = []rowsource.ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}
	proposal, err := service.ProposeConstruction(context.Background(), request)
	if err != nil {
		t.Fatalf("propose filter source SET with cleared Pivot source input: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" {
		t.Fatalf("proposal status = %q, want a previewable candidate", proposal.PreviewStatus)
	}
	if rs05HasProjection(proposal.CandidateConstruction.SourceProjections, rs05OldColumn, rs05PivotID) {
		t.Fatalf("REGRESSION: filter SET restored the cleared Pivot input: %#v", proposal.CandidateConstruction.SourceProjections)
	}
	if !rs05HasProjection(proposal.CandidateConstruction.SourceProjections, filterSourceColumn, rs05FilterID) {
		t.Fatalf("filter SET did not materialize its signed projection: %#v", proposal.CandidateConstruction.SourceProjections)
	}
}

func TestProposeConstructionPivotClearKeepsForeignFilterSourceInput(t *testing.T) {
	service, store, snapshot := rs05ProposeService(t)
	durable := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05ProposeFilterStep(filterSourceColumn, append(rs05PublicOutputs(true), filterSourceProjectionColumn())),
			rs05ProposePivotStep(filterSourceIdentity, rs05OldColumn),
		},
		SourceProjections: []ws.ConstructionSourceProjection{
			rs05RootProjection(rs05OldColumn, rs05PivotID), rs05FilterOwnedProjection(),
		},
	}
	rs05ProposeSaveDraft(t, store, durable)
	cleared := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05ProposeFilterStep(filterSourceColumn, append(rs05PublicOutputs(false), filterSourceProjectionColumn())),
			rs05ProposePivotStep(filterSourceIdentity),
		},
		SourceProjections: []ws.ConstructionSourceProjection{rs05FilterOwnedProjection()},
	}
	request := rs05ProposeRequest(store.created, snapshot, rs05PivotID, cleared)
	request.PivotSources = []construction.ConstructionPivotSourceSelection{}
	proposal, err := service.ProposeConstruction(context.Background(), request)
	if err != nil {
		t.Fatalf("propose pivot [] clear with a foreign filter source: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" {
		t.Fatalf("proposal status = %q, want a previewable candidate", proposal.PreviewStatus)
	}
	if rs05HasProjection(proposal.CandidateConstruction.SourceProjections, rs05OldColumn, rs05PivotID) {
		t.Fatalf("pivot clear restored its cleared owned input: %#v", proposal.CandidateConstruction.SourceProjections)
	}
	if !rs05HasProjection(proposal.CandidateConstruction.SourceProjections, filterSourceColumn, rs05FilterID) {
		t.Fatalf("pivot clear over-cleared: the foreign filter-owned projection vanished: %#v", proposal.CandidateConstruction.SourceProjections)
	}
}

func TestProposeConstructionFilterEditAgainstAlreadyClearedDurable(t *testing.T) {
	service, store, snapshot := rs05ProposeService(t)
	clearedDurable := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05ProposeFilterStep(filterSourceIdentity, rs05PublicOutputs(false)),
			rs05ProposePivotStep(filterSourceIdentity),
		},
	}
	rs05ProposeSaveDraft(t, store, clearedDurable)
	proposal, err := service.ProposeConstruction(context.Background(),
		rs05ProposeRequest(store.created, snapshot, rs05FilterID, rs05CloneConstruction(clearedDurable)))
	if err != nil {
		t.Fatalf("propose filter edit against cleared durable: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" || len(proposal.CandidateConstruction.SourceProjections) != 0 {
		t.Fatalf("cleared-durable proposal = %#v, want a clean previewable candidate", proposal)
	}
	if reflect.DeepEqual(proposal.CandidateConstruction.Steps, []ws.ConstructionStep(nil)) {
		t.Fatal("control lost the candidate steps")
	}
}

const (
	rs05DeriveID     = "derive-total"
	rs05DeriveColumn = "derive-private-input"
)

func rs05DeriveLaneStep(literal int64) ws.ConstructionStep {
	return constructionfixtures.DeriveLaneStep(rs05DeriveID, filterSourceIdentity, literal)
}

func rs05DeriveOwnedProjection() ws.ConstructionSourceProjection {
	return ws.ConstructionSourceProjection{
		ColumnID: rs05DeriveColumn, OwnerStepID: rs05DeriveID, OccurrenceID: ws.RootOccurrenceID,
		FieldPath: filterSourceField, FHIRType: "integer", LogicalType: "integer", Label: "BodyStructure.precedence",
	}
}

func rs05DeriveRequest(changedStepID string) construction.ConstructionProposalRequest {
	return construction.ConstructionProposalRequest{
		Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
		ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:xop", OutputID: filterSourceOutput,
		ChangedStepID: changedStepID,
	}
}

func TestXopPortDerivePreparationDoesNotRestoreClearedPivotInput(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-derive-restores-pivot")
	service := filterSourceService(t)
	baseConstruction := rs05SeedDurable()
	baseConstruction.Steps = append(baseConstruction.Steps, rs05DeriveLaneStep(2))
	base := filterSourceBase(snapshot, baseConstruction)
	cleared := rs05SeedCleared()
	cleared.Steps = append(cleared.Steps, rs05DeriveLaneStep(9))

	prepared, err := service.rowSourceService.ConstructionCandidateWithDeriveSources(context.Background(), base, rowsourceProposalRequestForTest(rs05DeriveRequest(rs05DeriveID)), rs05CloneConstruction(cleared))
	if err != nil {
		t.Fatalf("derive preparation after the pivot clear: %v", err)
	}
	rs05AssertionPivotCleared(t, "derive preparation", prepared)
	if constructionStepIndex(prepared.Steps, rs05DeriveID) < 0 || constructionStepIndex(prepared.Steps, rs05PivotID) < 0 {
		t.Fatalf("derive preparation lost a surviving step: %#v", prepared.Steps)
	}
}

func TestXopPortFilterSetBranchDoesNotReImportClearedDeriveProjection(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-filter-set-derive-clear")
	service := filterSourceService(t)
	baseConstruction := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05SeedFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
			rs05DeriveLaneStep(2),
		},
		SourceProjections: []ws.ConstructionSourceProjection{rs05DeriveOwnedProjection()},
	}
	candidate := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05SeedFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn())),
			rs05DeriveLaneStep(2),
		},
	}
	prepared, err := service.rowSourceService.ConstructionCandidateWithFilterSources(context.Background(), filterSourceBase(snapshot, baseConstruction), rowsourceProposalRequestForTest(filterSourceProposalRequest(rs05FilterID,
		[]rowsource.ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)})), candidate)
	if err != nil {
		t.Fatalf("filter SET while the derive source was cleared: %v", err)
	}
	if rs05HasProjection(prepared.SourceProjections, rs05DeriveColumn, rs05DeriveID) {
		t.Fatalf("filter SET re-imported the cleared DERIVE-owned durable input: %#v", prepared.SourceProjections)
	}
	if !rs05HasProjection(prepared.SourceProjections, filterSourceColumn, rs05FilterID) {
		t.Fatalf("filter SET did not materialize its own signed projection: %#v", prepared.SourceProjections)
	}
}

func rs05ProposeDeriveStep(literal int64) ws.ConstructionStep {
	value := literal
	return ws.ConstructionStep{
		ID:     rs05DeriveID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: rs05FilterID}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{
			ConstructionID:     rs05DeriveID,
			OutputColumnID:     "derived_total",
			Operation:          ws.ConstructionDerivedAdd,
			Left:               ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: &value}},
			Right:              ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: func() *int64 { three := int64(3); return &three }()}},
			MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
		}},
		Outputs: []ws.StageColumn{
			{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			{ID: rs05CategoryCol, Name: "public_category", Label: "Public category", Type: "string"},
			{ID: rs05ValueCol, Name: "public_value", Label: "Public value", Type: "string"},
			{ID: rs05OldColumn, Name: ws.ConstructionSourceProjectionName(rs05OldColumn), Label: "BodyStructure.active", Type: "boolean"},
			{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"},
		},
	}
}

func TestProposeConstructionDeriveEditDoesNotRestoreClearedPivotSourceInput(t *testing.T) {
	service, store, snapshot := rs05ProposeService(t)
	durable := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05ProposeFilterStep(filterSourceIdentity, rs05PublicOutputs(true)),
			rs05ProposeDeriveStep(2),
			rs05ProposePivotStep(filterSourceIdentity, rs05OldColumn),
		},
		SourceProjections: []ws.ConstructionSourceProjection{rs05RootProjection(rs05OldColumn, rs05PivotID)},
	}
	durable.Steps[2].Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: rs05DeriveID}}
	rs05ProposeSaveDraft(t, store, durable)
	acceptedBefore := string(store.created.DraftConfig)
	cleared := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05ProposeFilterStep(filterSourceIdentity, rs05PublicOutputs(true)),
			rs05ProposeDeriveStep(9),
			rs05ProposePivotStep(filterSourceIdentity),
		},
	}
	cleared.Steps[2].Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: rs05DeriveID}}
	proposal, err := service.ProposeConstruction(context.Background(),
		rs05ProposeRequest(store.created, snapshot, rs05DeriveID, cleared))
	if err != nil {
		t.Fatalf("propose derive edit with cleared Pivot source input: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" {
		t.Fatalf("proposal status = %q, want a previewable candidate", proposal.PreviewStatus)
	}
	if rs05HasProjection(proposal.CandidateConstruction.SourceProjections, rs05OldColumn, rs05PivotID) {
		t.Fatalf("REGRESSION: derive edit restored the cleared Pivot input: %#v", proposal.CandidateConstruction.SourceProjections)
	}
	if constructionStepIndex(proposal.CandidateConstruction.Steps, rs05DeriveID) < 0 ||
		constructionStepIndex(proposal.CandidateConstruction.Steps, rs05PivotID) < 0 ||
		constructionStepIndex(proposal.CandidateConstruction.Steps, rs05FilterID) < 0 {
		t.Fatalf("proposal lost a surviving step: %#v", proposal.CandidateConstruction.Steps)
	}
	if string(store.created.DraftConfig) != acceptedBefore {
		t.Fatal("proposal changed the accepted draft")
	}
}

const (
	rs05SetFilterID  = "patient-filter-active"
	rs05SetFilterCol = "patient_filter_active"
)

func rs05PatientSetFilterStep(prevStepID string) ws.ConstructionStep {
	return ws.ConstructionStep{
		ID:     rs05SetFilterID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: prevStepID}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
			ColumnID: rs05SetFilterCol, Operator: ws.ConstructionFilterEquals,
			Values: []ws.FilterValue{{Kind: ws.ConstructionFilterBoolean, Boolean: filterSourceBool(true)}},
		}},
		Outputs: []ws.StageColumn{
			{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"},
			{ID: rs05SetFilterCol, Name: ws.ConstructionSourceProjectionName(rs05SetFilterCol), Label: "Patient.active", Type: "boolean", Nullable: true},
		},
	}
}

func rs05PatientSetFilterProjection() ws.ConstructionSourceProjection {
	return ws.ConstructionSourceProjection{
		ColumnID: rs05SetFilterCol, OwnerStepID: rs05SetFilterID, OccurrenceID: ws.RootOccurrenceID,
		FieldPath: "active", FHIRType: "boolean", LogicalType: "boolean", Label: "Patient.active",
	}
}

func rs05PatientSetDerivedOutputs() []ws.StageColumn {
	return append(deriveSourcePatientIdentityColumns(), deriveSourcePatientProjectionColumn(),
		ws.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})
}

func TestXopPortDeriveSetBranchDoesNotReImportClearedFilterProjection(t *testing.T) {
	harness := newDeriveSourceHarness(t)
	snapshot, service := harness.snapshot, harness.service
	durable := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			deriveSourceAddStep(deriveSourceStep, "patient-id", deriveSourcePatientDerivedColumns()),
			rs05PatientSetFilterStep(deriveSourceStep),
		},
		SourceProjections: []ws.ConstructionSourceProjection{rs05PatientSetFilterProjection()},
	}
	base := deriveSourcePatientBase(snapshot, durable)

	candidate := rs05CloneConstruction(durable)
	candidate.SourceProjections = nil
	left := candidate.Steps[0].Operation.Derive.Left
	left.ColumnID = deriveSourceBirth
	candidate.Steps[0].Operation.Derive.Left = left
	candidate.Steps[0].Outputs = rs05PatientSetDerivedOutputs()

	prepared, err := service.rowSourceService.ConstructionCandidateWithDeriveSources(context.Background(), base, rowsourceProposalRequestForTest(deriveSourcePatientProposalRequest(deriveSourceStep, []rowsource.ConstructionDeriveSourceSelection{
		{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: deriveSourceBirth},
	})), candidate)
	if err != nil {
		t.Fatalf("derive SET while the foreign Filter source was cleared: %v", err)
	}
	if rs05HasProjection(prepared.SourceProjections, rs05SetFilterCol, rs05SetFilterID) {
		t.Fatalf("REGRESSION: DERIVE SET re-imported the cleared foreign FILTER-owned durable input from the base: %#v", prepared.SourceProjections)
	}
	if !rs05HasProjection(prepared.SourceProjections, deriveSourceBirth, deriveSourceStep) {
		t.Fatalf("DERIVE SET did not materialize its own signed projection: %#v", prepared.SourceProjections)
	}
}

func TestXopPortUnpivotSetBranchDoesNotReImportClearedFilterProjection(t *testing.T) {
	harness := newUnpivotSourceHarness(t)
	snapshot, service := harness.snapshot, harness.service
	carrierOutputs := append(deriveSourcePatientIdentityColumns(),
		ws.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})
	carrier := deriveSourceLiteralOnlyStep("derive-base", carrierOutputs)
	melt := unpivotMeltStep(unpivotSourceStep, []string{"derived_total"},
		unpivotMeltOutputs(ws.StageColumn{ID: "patient-id", Name: "patient_id", Label: "Patient ID"}))
	melt.Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "derive-base"}}
	durable := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			carrier, melt, rs05PatientSetFilterStep(unpivotSourceStep),
		},
		SourceProjections: []ws.ConstructionSourceProjection{rs05PatientSetFilterProjection()},
	}
	base := deriveSourcePatientBase(snapshot, durable)
	base.Stages = append(base.Stages, receiptmodel.ReceiptConstructionStage{
		ID: "derive-base", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "DERIVE",
		Columns: []receiptmodel.ReceiptConstructionStageColumn{
			{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"},
			{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"},
		},
	})

	candidate, err := decodeConstruction(durable)
	if err != nil {
		t.Fatal(err)
	}
	candidate.SourceProjections = nil
	candidate.Steps[1].Operation.Unpivot.Inputs = append(candidate.Steps[1].Operation.Unpivot.Inputs,
		ws.ConstructionUnpivotInput{ColumnID: unpivotPatientColumn, Key: unpivotKey("variant-birth")})
	candidate.Steps[1].Operation.Unpivot.ValueOutputColumnID = unpivotMeltValueColumn

	prepared, err := service.rowSourceService.ConstructionCandidateWithUnpivotSources(context.Background(), base, rowsourceProposalRequestForTest(unpivotPatientProposalRequest(unpivotSourceStep, []rowsource.ConstructionUnpivotSourceSelection{
		{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: unpivotPatientColumn},
	})), candidate)
	if err != nil {
		t.Fatalf("unpivot SET while the foreign Filter source was cleared: %v", err)
	}
	if rs05HasProjection(prepared.SourceProjections, rs05SetFilterCol, rs05SetFilterID) {
		t.Fatalf("REGRESSION: UNPIVOT SET re-imported the cleared foreign FILTER-owned durable input from the base: %#v", prepared.SourceProjections)
	}
	if !rs05HasProjection(prepared.SourceProjections, unpivotPatientColumn, unpivotSourceStep) {
		t.Fatalf("UNPIVOT SET did not materialize its own signed projection: %#v", prepared.SourceProjections)
	}
}

func TestXopPortDeriveSetBranchStillRepairsPartialPivotChain(t *testing.T) {
	harness := newDeriveSourceHarness(t)
	snapshot, service := harness.snapshot, harness.service
	durable := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			deriveSourceAddStep(deriveSourceStep, "patient-id", deriveSourcePatientDerivedColumns()),
			rs05HelperStep(), rs05SeedPivotStep(), rs05PatientSetFilterStep(rs05PivotID),
		},
		SourceProjections: []ws.ConstructionSourceProjection{
			rs05RootProjection(rs05HelperColumn, rs05PivotID), rs05PatientSetFilterProjection(),
		},
	}
	base := deriveSourcePatientBase(snapshot, durable)

	candidate := rs05CloneConstruction(durable)
	candidate.SourceProjections = []ws.ConstructionSourceProjection{rs05PatientSetFilterProjection()}
	left := candidate.Steps[0].Operation.Derive.Left
	left.ColumnID = deriveSourceBirth
	candidate.Steps[0].Operation.Derive.Left = left
	candidate.Steps[0].Outputs = rs05PatientSetDerivedOutputs()

	prepared, err := service.rowSourceService.ConstructionCandidateWithDeriveSources(context.Background(), base, rowsourceProposalRequestForTest(deriveSourcePatientProposalRequest(deriveSourceStep, []rowsource.ConstructionDeriveSourceSelection{
		{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: deriveSourceBirth},
	})), candidate)
	if err != nil {
		t.Fatalf("derive SET with a partially carried Pivot chain: %v", err)
	}
	if count := rs05CountOwned(prepared.SourceProjections, rs05PivotID); count != 1 {
		t.Fatalf("DERIVE SET dropped the partially omitted Pivot-owned input instead of repairing it: owned=%d %#v", count, prepared.SourceProjections)
	}
	if count := rs05CountOwned(prepared.SourceProjections, rs05SetFilterID); count != 1 {
		t.Fatalf("DERIVE SET over-stripped the foreign Filter-owned input the candidate kept: owned=%d %#v", count, prepared.SourceProjections)
	}
	if !rs05HasProjection(prepared.SourceProjections, deriveSourceBirth, deriveSourceStep) {
		t.Fatalf("DERIVE SET did not materialize its own signed projection: %#v", prepared.SourceProjections)
	}
}

func TestXopPortUnpivotSetBranchStillRepairsPartialPivotChain(t *testing.T) {
	harness := newUnpivotSourceHarness(t)
	snapshot, service := harness.snapshot, harness.service
	carrierOutputs := append(deriveSourcePatientIdentityColumns(),
		ws.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})
	carrier := deriveSourceLiteralOnlyStep("derive-base", carrierOutputs)
	melt := unpivotMeltStep(unpivotSourceStep, []string{"derived_total"},
		unpivotMeltOutputs(ws.StageColumn{ID: "patient-id", Name: "patient_id", Label: "Patient ID"}))
	melt.Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "derive-base"}}
	durable := ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			carrier, melt, rs05HelperStep(), rs05SeedPivotStep(), rs05PatientSetFilterStep(rs05PivotID),
		},
		SourceProjections: []ws.ConstructionSourceProjection{
			rs05RootProjection(rs05HelperColumn, rs05PivotID), rs05PatientSetFilterProjection(),
		},
	}
	base := deriveSourcePatientBase(snapshot, durable)
	base.Stages = append(base.Stages, receiptmodel.ReceiptConstructionStage{
		ID: "derive-base", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "DERIVE",
		Columns: []receiptmodel.ReceiptConstructionStageColumn{
			{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"},
			{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"},
		},
	})

	candidate, err := decodeConstruction(durable)
	if err != nil {
		t.Fatal(err)
	}
	candidate.SourceProjections = []ws.ConstructionSourceProjection{rs05PatientSetFilterProjection()}
	candidate.Steps[1].Operation.Unpivot.Inputs = append(candidate.Steps[1].Operation.Unpivot.Inputs,
		ws.ConstructionUnpivotInput{ColumnID: unpivotPatientColumn, Key: unpivotKey("variant-birth")})
	candidate.Steps[1].Operation.Unpivot.ValueOutputColumnID = unpivotMeltValueColumn

	prepared, err := service.rowSourceService.ConstructionCandidateWithUnpivotSources(context.Background(), base, rowsourceProposalRequestForTest(unpivotPatientProposalRequest(unpivotSourceStep, []rowsource.ConstructionUnpivotSourceSelection{
		{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: unpivotPatientColumn},
	})), candidate)
	if err != nil {
		t.Fatalf("unpivot SET with a partially carried Pivot chain: %v", err)
	}
	if count := rs05CountOwned(prepared.SourceProjections, rs05PivotID); count != 1 {
		t.Fatalf("UNPIVOT SET dropped the partially omitted Pivot-owned input instead of repairing it: owned=%d %#v", count, prepared.SourceProjections)
	}
	if count := rs05CountOwned(prepared.SourceProjections, rs05SetFilterID); count != 1 {
		t.Fatalf("UNPIVOT SET over-stripped the foreign Filter-owned input the candidate kept: owned=%d %#v", count, prepared.SourceProjections)
	}
	if !rs05HasProjection(prepared.SourceProjections, unpivotPatientColumn, unpivotSourceStep) {
		t.Fatalf("UNPIVOT SET did not materialize its own signed projection: %#v", prepared.SourceProjections)
	}
}

func fs1r6LaneCode(err error) string {
	if err == nil {
		return ""
	}
	var le *ports.Error
	if errors.As(err, &le) {
		return le.Code
	}
	return "untyped:" + err.Error()
}

func fs1r6Projection(columnID, ownerStepID, fieldPath, logicalType string) ws.ConstructionSourceProjection {
	return ws.ConstructionSourceProjection{
		ColumnID: columnID, OwnerStepID: ownerStepID, OccurrenceID: ws.RootOccurrenceID,
		FieldPath: fieldPath, FHIRType: logicalType, LogicalType: logicalType, Label: "BodyStructure." + fieldPath,
	}
}

func TestFS1ReverseOrderSignedSlotRefusedAtPivotBoundary(t *testing.T) {
	buildBase := func(t *testing.T, token string) (*Service, constructionBase, capability.Snapshot) {
		t.Helper()
		snapshot := filterSourceSnapshot(token)
		service := filterSourceService(t)
		baseConstruction := ws.Construction{
			Version: ws.ConstructionVersion,
			Steps:   []ws.ConstructionStep{rs05PivotStep(rs05PivotID, filterSourceIdentity, rs05OldColumn)},
			SourceProjections: []ws.ConstructionSourceProjection{
				fs1r6Projection(rs05OldColumn, rs05PivotID, filterSourceField, "boolean"),
			},
		}
		return service, filterSourceBase(snapshot, baseConstruction), snapshot
	}

	t.Run("filter lane", func(t *testing.T) {
		service, base, snapshot := buildBase(t, "fs1r6-filter-boundary")
		candidate := ws.Construction{
			Version: ws.ConstructionVersion,
			Steps: []ws.ConstructionStep{
				rs05PivotStep(rs05PivotID, filterSourceIdentity, rs05OldColumn),
				filterSourceFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn())),
			},
			SourceProjections: []ws.ConstructionSourceProjection{
				fs1r6Projection(rs05OldColumn, rs05PivotID, filterSourceField, "boolean"),
			},
		}
		request := filterSourceProposalRequest(rs05FilterID, []rowsource.ConstructionFilterSourceSelection{
			filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn),
		})
		_, err := service.rowSourceService.ConstructionCandidateWithFilterSources(context.Background(), base, rowsourceProposalRequestForTest(request), candidate)
		if code := fs1r6LaneCode(err); code != "FILTER_SOURCE_CANNOT_CROSS_STAGE" {
			t.Fatalf("pivot-first signed filter slot on the pivot's field: code=%q err=%v, want FILTER_SOURCE_CANNOT_CROSS_STAGE refusal (never silent accept, never catch-all 500)", code, err)
		}
	})

	t.Run("unpivot melt lane", func(t *testing.T) {
		service, base, snapshot := buildBase(t, "fs1r6-melt-boundary")
		melt := ws.ConstructionStep{
			ID:     "melt-step",
			Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationUnpivot, Unpivot: &ws.ConstructionUnpivot{
				ConstructionID:    "melt-step",
				Inputs:            []ws.ConstructionUnpivotInput{{ColumnID: "melt_slot", Key: unpivotKey("v")}},
				KeyOutputColumnID: "melt_key", ValueOutputColumnID: "melt_value",
				NullRowPolicy: ws.ConstructionUnpivotDrop,
			}},
			Outputs: []ws.StageColumn{
				{ID: "melt_slot", Name: ws.ConstructionSourceProjectionName("melt_slot"), Label: "Active", Type: "boolean"},
				{ID: "melt_key", Name: "melt_key", Label: "Melt key", Type: "string"},
				{ID: "melt_value", Name: "melt_value", Label: "Melt value", Type: "boolean"},
			},
		}
		candidate := ws.Construction{
			Version: ws.ConstructionVersion,
			Steps: []ws.ConstructionStep{
				rs05PivotStep(rs05PivotID, filterSourceIdentity, rs05OldColumn), melt,
			},
			SourceProjections: []ws.ConstructionSourceProjection{
				fs1r6Projection(rs05OldColumn, rs05PivotID, filterSourceField, "boolean"),
			},
		}
		request := construction.ConstructionProposalRequest{
			Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
			ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:filter-source-draft",
			OutputID: filterSourceOutput, ChangedStepID: "melt-step",
			UnpivotSources: []rowsource.ConstructionUnpivotSourceSelection{{ChoiceID: filterSourceRowChoice(t, snapshot, filterSourceField).ChoiceID, ColumnID: "melt_slot"}},
		}
		_, err := service.rowSourceService.ConstructionCandidateWithUnpivotSources(context.Background(), base, rowsourceProposalRequestForTest(request), candidate)
		if code := fs1r6LaneCode(err); code != "UNPIVOT_SOURCE_CANNOT_CROSS_STAGE" {
			t.Fatalf("pivot-first signed melt input on the pivot's field: code=%q err=%v, want UNPIVOT_SOURCE_CANNOT_CROSS_STAGE refusal", code, err)
		}
	})
}

func TestFS1ForwardAndReverseSignedCarrierCollisionRefusedNamed(t *testing.T) {
	for _, row := range []struct {
		order string
		first bool
	}{
		{"carrier-first", true},
		{"slot-first", false},
	} {
		t.Run(row.order, func(t *testing.T) {
			snapshot := filterSourceSnapshot("fs1r6-named-" + row.order)
			service := filterSourceService(t)
			carrier := ws.ConstructionStep{
				ID:        "derive-carrier",
				Inputs:    []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive},
				Outputs: []ws.StageColumn{
					{ID: "derive_slot_a", Name: ws.ConstructionSourceProjectionName("derive_slot_a"), Label: "Active", Type: "boolean"},
				},
			}
			filter := filterSourceFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn()))
			var steps []ws.ConstructionStep
			if row.first {
				steps = []ws.ConstructionStep{carrier, filter}
			} else {
				steps = []ws.ConstructionStep{filter, carrier}
			}
			projection := fs1r6Projection("derive_slot_a", "derive-carrier", filterSourceField, "boolean")
			base := filterSourceBase(snapshot, ws.Construction{Version: ws.ConstructionVersion, Steps: steps, SourceProjections: []ws.ConstructionSourceProjection{projection}})
			candidate := ws.Construction{Version: ws.ConstructionVersion, Steps: steps, SourceProjections: []ws.ConstructionSourceProjection{projection}}
			request := filterSourceProposalRequest(rs05FilterID, []rowsource.ConstructionFilterSourceSelection{
				filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn),
			})
			_, err := service.rowSourceService.ConstructionCandidateWithFilterSources(context.Background(), base, rowsourceProposalRequestForTest(request), candidate)
			if code := fs1r6LaneCode(err); code != "FILTER_SOURCE_FIELD_ALREADY_PROJECTED" {
				t.Fatalf("%s: code=%q err=%v, want the named FILTER_SOURCE_FIELD_ALREADY_PROJECTED refusal in both arrival directions", row.order, code, err)
			}
		})
	}
}

func TestFS1LegitNonCollisionReverseOrderStillAccepted(t *testing.T) {
	snapshot := filterSourceSnapshot("fs1r6-legit")
	service := filterSourceService(t)
	carrier := ws.ConstructionStep{
		ID:        "derive-carrier",
		Inputs:    []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive},
		Outputs: []ws.StageColumn{
			{ID: "derive_slot_a", Name: ws.ConstructionSourceProjectionName("derive_slot_a"), Label: "Description", Type: "string"},
		},
	}
	projection := fs1r6Projection("derive_slot_a", "derive-carrier", "description", "string")
	filter := filterSourceFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn()))
	steps := []ws.ConstructionStep{filter, carrier}
	base := filterSourceBase(snapshot, ws.Construction{Version: ws.ConstructionVersion, Steps: steps, SourceProjections: []ws.ConstructionSourceProjection{projection}})
	candidate := ws.Construction{Version: ws.ConstructionVersion, Steps: steps, SourceProjections: []ws.ConstructionSourceProjection{projection}}
	request := filterSourceProposalRequest(rs05FilterID, []rowsource.ConstructionFilterSourceSelection{
		filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn),
	})
	if _, err := service.rowSourceService.ConstructionCandidateWithFilterSources(context.Background(), base, rowsourceProposalRequestForTest(request), candidate); err != nil {
		t.Fatalf("distinct-field reverse-order signed slot must stay accepted, got %v", err)
	}
}

func fs1r6PivotPatientConstruction() (ws.Document, ws.Construction) {
	construction := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
		{
			ID:     "pivot",
			Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
				ConstructionID: "pivot", GroupKeyIDs: []string{"patient-id", "pv_slot"},
				CategoryColumnID: "category", ValueColumnID: "value",
				Categories:             []ws.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("A"), OutputColumnID: "d"}},
				DuplicatePolicy:        ws.ConstructionPivotDuplicateError,
				MissingCellPolicy:      ws.ConstructionPivotMissingNull,
				UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
			}},
			Outputs: []ws.StageColumn{
				{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"},
				{ID: "pv_slot", Name: ws.ConstructionSourceProjectionName("pv_slot"), Label: "Patient.multipleBirthInteger", Type: "integer"},
				{ID: "d", Name: "d", Label: "d", Type: "string"},
			},
		},
	}}
	construction.SourceProjections = []ws.ConstructionSourceProjection{{
		ColumnID: "pv_slot", OwnerStepID: "pivot", OccurrenceID: ws.RootOccurrenceID,
		FieldPath: "multipleBirthInteger", FHIRType: "integer", LogicalType: "integer", Label: "Patient.multipleBirthInteger",
	}}
	document := ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"},
		RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
		Rows: ws.RecordsRowDefinition(),
		Columns: []ws.Column{
			{ColumnID: "patient-id", Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}},
			{ColumnID: "category", Column: "category", Label: "Category", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceProjectID}},
			{ColumnID: "value", Column: "value", Label: "Value", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceProjectID}},
		},
		Construction: &construction,
	}
	return document, construction
}

func fs1r6SaveDraftWithPivot(t *testing.T, harness *filterSourceHarness, construction ws.Construction) {
	t.Helper()
	document, _ := fs1r6PivotPatientConstruction()
	document.Construction = &construction
	workspace, err := ws.DecodeWorkspace(harness.store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.SemanticsVersion = ws.CurrentSemanticsVersion
	workspace.Documents = []ws.Document{document}
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate fs1r6 pivot draft: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	harness.store.created.DraftConfig, harness.store.created.DraftDigest = draft, digest
}

func fs1r6SignedFilterStep() ws.ConstructionStep {
	return ws.ConstructionStep{
		ID:     "fs1r6_filter",
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
			ColumnID: "fs1r6_slot_b", Operator: ws.ConstructionFilterExists,
		}},
		Outputs: []ws.StageColumn{
			{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"},
			{ID: "category", Name: "category", Label: "Category", Type: "string"},
			{ID: "value", Name: "value", Label: "Value", Type: "string"},
			{ID: "fs1r6_slot_b", Name: ws.ConstructionSourceProjectionName("fs1r6_slot_b"), Label: "Patient.multipleBirthInteger", Type: "integer"},
			{ID: "pv_slot", Name: ws.ConstructionSourceProjectionName("pv_slot"), Label: "Patient.multipleBirthInteger", Type: "integer"},
		},
	}
}

func fs1r6SignedSlotRequest() rowsource.ConstructionFilterSourceSelection {
	return rowsource.ConstructionFilterSourceSelection{ColumnID: "fs1r6_slot_b"}
}

func TestFS1FullProposeRefusesSignedStepInsertedBeforeCommittedPivot(t *testing.T) {
	harness := newFilterSourceHarness(t)
	_, durable := fs1r6PivotPatientConstruction()
	fs1r6SaveDraftWithPivot(t, harness, durable)
	candidate := durable
	candidate.Steps = append([]ws.ConstructionStep{fs1r6SignedFilterStep()}, durable.Steps...)
	selection := fs1r6SignedSlotRequest()
	selection.ChoiceID = harness.choice(t, "multipleBirthInteger").ChoiceID
	_, err := harness.service.ProposeConstruction(context.Background(), construction.ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest,
		OutputID: "patients", ChangedStepID: "fs1r6_filter", CandidateConstruction: candidate,
		FilterSources: []rowsource.ConstructionFilterSourceSelection{selection},
	})
	if code := fs1r6LaneCode(err); code != "INVALID_CONSTRUCTION_CANDIDATE" {
		t.Fatalf("signed step inserted before the committed pivot: code=%q err=%v, want INVALID_CONSTRUCTION_CANDIDATE (reverse arrival unreachable by construction)", code, err)
	}
}

func TestFS1FullProposeReverseDirectionRefusedAtBoundary(t *testing.T) {
	harness := newFilterSourceHarness(t)
	_, durable := fs1r6PivotPatientConstruction()
	fs1r6SaveDraftWithPivot(t, harness, durable)
	candidate := durable
	candidate.Steps = append([]ws.ConstructionStep(nil), durable.Steps...)
	signed := fs1r6SignedFilterStep()
	signed.Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "pivot"}}
	candidate.Steps = append(candidate.Steps, signed)
	selection := fs1r6SignedSlotRequest()
	selection.ChoiceID = harness.choice(t, "multipleBirthInteger").ChoiceID
	proposal, err := harness.service.ProposeConstruction(context.Background(), construction.ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest,
		OutputID: "patients", ChangedStepID: "fs1r6_filter", CandidateConstruction: candidate,
		FilterSources: []rowsource.ConstructionFilterSourceSelection{selection},
	})
	if err == nil {
		t.Fatalf("pivot-first signed slot was accepted (status=%q, %d projections): the collision must be refused as FILTER_SOURCE_CANNOT_CROSS_STAGE at the lane boundary",
			proposal.PreviewStatus, len(proposal.CandidateConstruction.SourceProjections))
	}
	if code := fs1r6LaneCode(err); code != "FILTER_SOURCE_CANNOT_CROSS_STAGE" {
		t.Fatalf("pivot-first full-path refusal: code=%q err=%v, want FILTER_SOURCE_CANNOT_CROSS_STAGE", code, err)
	}
}
