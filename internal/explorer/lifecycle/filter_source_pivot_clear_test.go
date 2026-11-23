package lifecycle

import (
	"context"
	"reflect"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/rowsource"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
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
	return ws.ConstructionSourceProjection{
		ColumnID: columnID, OwnerStepID: ownerStepID, OccurrenceID: ws.RootOccurrenceID,
		FieldPath: filterSourceField, FHIRType: "boolean", LogicalType: "boolean", Label: "BodyStructure.active",
	}
}

func rs05PivotStep(id string, groupKeys ...string) ws.ConstructionStep {
	return ws.ConstructionStep{
		ID:     id,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
			ConstructionID: id, GroupKeyIDs: groupKeys,
			CategoryColumnID: rs05CategoryCol, ValueColumnID: rs05ValueCol,
			Categories:             []ws.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("t"), OutputColumnID: "pval"}},
			DuplicatePolicy:        ws.ConstructionPivotDuplicateError,
			MissingCellPolicy:      ws.ConstructionPivotMissingNull,
			UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
		}},
		Outputs: []ws.StageColumn{{ID: "pval", Name: "pval", Label: "pval", Type: "string"}},
	}
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

func rs05CloneConstruction(c ws.Construction) ws.Construction {
	out := c
	out.Steps = append([]ws.ConstructionStep(nil), c.Steps...)
	for index := range out.Steps {
		step := out.Steps[index]
		if step.Inputs != nil {
			step.Inputs = append([]ws.ConstructionInputRef(nil), step.Inputs...)
		}
		if step.Outputs != nil {
			step.Outputs = append([]ws.StageColumn(nil), step.Outputs...)
		}
		out.Steps[index] = step
	}
	if c.SourceProjections != nil {
		out.SourceProjections = append([]ws.ConstructionSourceProjection(nil), c.SourceProjections...)
	} else {
		out.SourceProjections = nil
	}
	return out
}

func rs05OtherHelperStep() ws.ConstructionStep {
	return ws.ConstructionStep{
		ID: rs05Helper2ID, OwnerStepID: rs05OtherPivotID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedField, RelatedField: &ws.ConstructionRelatedField{
			ChoiceID: "seed-choice-two", OutputColumnID: rs05OtherColumn,
			Source: ws.ConstructionRelatedFieldSource{
				Kind: capability.ConstructionChoiceSourceField, CandidateID: "seed-cand", NodeID: filterSourceRootNode,
				ResourceType: filterSourceRootType, Path: filterSourceField,
				Cardinality: "optional_one", LogicalType: "boolean",
			},
		}},
		Outputs: []ws.StageColumn{{ID: "public-key", Name: "public_key", Label: "Public key", Type: "string"},
			{ID: rs05OtherColumn, Name: "pv_two_col", Label: "BodyStructure.active", Type: "boolean", Nullable: true}},
	}
}

func rs05HelperStep() ws.ConstructionStep {
	return ws.ConstructionStep{
		ID: rs05HelperID, OwnerStepID: rs05PivotID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedField, RelatedField: &ws.ConstructionRelatedField{
			ChoiceID: "seed-choice", OutputColumnID: rs05HelperColumn,
			Source: ws.ConstructionRelatedFieldSource{
				Kind: capability.ConstructionChoiceSourceField, CandidateID: "seed-cand", NodeID: filterSourceRootNode,
				ResourceType: filterSourceRootType, Path: filterSourceField,
				Cardinality: "optional_one", LogicalType: "boolean",
			},
		}},
		Outputs: []ws.StageColumn{
			{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			{ID: rs05HelperColumn, Name: "pv_col", Label: "BodyStructure.active", Type: "boolean", Nullable: true},
		},
	}
}

func rs05SeedPivotStep() ws.ConstructionStep {
	return ws.ConstructionStep{
		ID:     rs05PivotID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: rs05HelperID}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
			ConstructionID: rs05PivotID, GroupKeyIDs: []string{filterSourceIdentity},
			CategoryColumnID: rs05HelperColumn, ValueColumnID: "pval",
			Categories:             []ws.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("t"), OutputColumnID: "pval"}},
			DuplicatePolicy:        ws.ConstructionPivotDuplicateError,
			MissingCellPolicy:      ws.ConstructionPivotMissingNull,
			UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
		}},
		Outputs: []ws.StageColumn{
			{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			{ID: "pval", Name: "pval", Label: "pval", Type: "integer"},
		},
	}
}

func rs05SeedFilterStep(columnID string, outputs []ws.StageColumn) ws.ConstructionStep {
	return ws.ConstructionStep{
		ID:     rs05FilterID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
			ColumnID: columnID, Operator: ws.ConstructionFilterEquals,
			Values: []ws.FilterValue{{Kind: ws.ConstructionFilterBoolean, Boolean: filterSourceBool(true)}},
		}},
		Outputs: outputs,
	}
}

func rs05SeedDurable() ws.Construction {
	return ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			rs05SeedFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
			rs05HelperStep(),
			rs05SeedPivotStep(),
		},
		SourceProjections: []ws.ConstructionSourceProjection{rs05RootProjection(rs05HelperColumn, rs05PivotID)},
	}
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

func rs05FilterOwnedDurable() ws.Construction {
	durable := rs05SeedDurable()
	durable.Steps[0] = rs05SeedFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn()))
	durable.SourceProjections = append(durable.SourceProjections, rs05FilterOwnedProjection())
	return durable
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

	preparedA, err := service.constructionCandidateWithFilterSources(context.Background(), base,
		filterSourceProposalRequest(rs05FilterID, nil), rs05CloneConstruction(cleared))
	if err != nil {
		t.Fatalf("prepare filter (no filterSources) failed: %v", err)
	}
	rs05AssertionPivotCleared(t, "no filterSources", preparedA)

	clearedB := rs05CloneConstruction(cleared)
	clearedB.Steps[0] = rs05SeedFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn()))
	preparedB, err := service.constructionCandidateWithFilterSources(context.Background(), base,
		filterSourceProposalRequest(rs05FilterID,
			[]rowsource.ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}), clearedB)
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
	prepared, err := service.constructionCandidateWithFilterSources(context.Background(), base, request, candidate)
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
	prepared, err := service.constructionCandidateWithFilterSources(context.Background(), base,
		filterSourceProposalRequest(rs05FilterID,
			[]rowsource.ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}), clearedForFilter)
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
	prepared, err := service.constructionCandidateWithFilterSources(context.Background(), filterSourceBase(snapshot, baseConstruction),
		filterSourceProposalRequest(rs05FilterID,
			[]rowsource.ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}), candidate)
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

func rs05IndexOwned(projections []ws.ConstructionSourceProjection, ownerStepID string) int {
	for index, projection := range projections {
		if projection.OwnerStepID == ownerStepID {
			return index
		}
	}
	return -1
}

func rs05ProposeService(t *testing.T) (*Service, *fakeStore, capability.Snapshot) {
	t.Helper()
	service, store, snapshot := constructionProposalService(t)
	donor := filterSourceSnapshot("rs05-propose")
	snapshot.Nodes = donor.Nodes
	snapshot.Candidates = donor.Candidates
	resolver := filterSourceResolver(t)
	service.config.RowChoicePlanner, service.config.RowChoiceResolver = resolver, resolver
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}
	service.config.Capability.Token = func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil }
	service.config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) { return authorized, nil }
	service.config.Capability.ForExecution = func(context.Context, string, string) (ports.AuthorizedCapability, error) { return authorized, nil }
	service.config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		receipt := lifecycleConstructionReceipt(snapshot, request.Workspace)
		receipt.ConstructionProposal = cloneConstructionProposalBinding(request.ConstructionProposal)
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
	service.config.PreviewReceipt = func(_ context.Context, receipt *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil {
			return dataframeexecution.PreviewSummary{}, context.Canceled
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"body_structure_id"}, Complete: true}, nil
	}
	return service, store, snapshot
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
	return ws.ConstructionStep{
		ID:     rs05FilterID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
			ColumnID: columnID, Operator: ws.ConstructionFilterEquals,
			Values: []ws.FilterValue{{Kind: ws.ConstructionFilterBoolean, Boolean: filterSourceBool(true)}},
		}},
		Outputs: outputs,
	}
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

func rs05BaseWithHelperStage(base constructionBase) constructionBase {
	base.Stages = append(append([]receiptmodel.ReceiptConstructionStage(nil), base.Stages...), receiptmodel.ReceiptConstructionStage{
		ID: rs05HelperID, InputStageID: recipe.ConstructionSourceProjectionID, Operation: "RELATED_FIELD",
		Columns: []receiptmodel.ReceiptConstructionStageColumn{
			{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			{ID: rs05HelperColumn, Name: "pv_col", Label: "BodyStructure.active", Type: "boolean"},
		},
	})
	return base
}

const (
	rs05DeriveID     = "derive-total"
	rs05DeriveColumn = "derive-private-input"
)

func rs05DeriveLaneStep(literal int64) ws.ConstructionStep {
	value := literal
	return ws.ConstructionStep{
		ID:     rs05DeriveID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
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
			{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"},
		},
	}
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

	prepared, err := service.constructionCandidateWithDeriveSources(context.Background(), base,
		rs05DeriveRequest(rs05DeriveID), rs05CloneConstruction(cleared))
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
	prepared, err := service.constructionCandidateWithFilterSources(context.Background(), filterSourceBase(snapshot, baseConstruction),
		filterSourceProposalRequest(rs05FilterID,
			[]rowsource.ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}), candidate)
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

	prepared, err := service.constructionCandidateWithDeriveSources(context.Background(), base,
		deriveSourcePatientProposalRequest(deriveSourceStep, []rowsource.ConstructionDeriveSourceSelection{
			{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: deriveSourceBirth},
		}), candidate)
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

	prepared, err := service.constructionCandidateWithUnpivotSources(context.Background(), base,
		unpivotPatientProposalRequest(unpivotSourceStep, []rowsource.ConstructionUnpivotSourceSelection{
			{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: unpivotPatientColumn},
		}), candidate)
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

	prepared, err := service.constructionCandidateWithDeriveSources(context.Background(), base,
		deriveSourcePatientProposalRequest(deriveSourceStep, []rowsource.ConstructionDeriveSourceSelection{
			{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: deriveSourceBirth},
		}), candidate)
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

	prepared, err := service.constructionCandidateWithUnpivotSources(context.Background(), base,
		unpivotPatientProposalRequest(unpivotSourceStep, []rowsource.ConstructionUnpivotSourceSelection{
			{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: unpivotPatientColumn},
		}), candidate)
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
