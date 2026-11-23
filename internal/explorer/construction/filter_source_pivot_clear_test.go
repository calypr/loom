package construction

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestFilterSourceClearNotRestoredByPivotPreparation(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-clear-mirror")
	service := filterSourceService(t)
	baseConstruction := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05SeedFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn())),
			rs05OtherHelperStep(),
			rs05PivotStep(rs05OtherPivotID, "public-key"),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{
			rs05FilterOwnedProjection(), rs05RootProjection(rs05OtherColumn, rs05OtherPivotID),
		},
	}
	candidate := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05SeedFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn())),
			rs05OtherHelperStep(),
			rs05PivotStep(rs05OtherPivotID, "public-key"),
		},
	}
	base := filterSourceBase(snapshot, baseConstruction)
	request := filterSourceProposalRequest(rs05FilterID, []ConstructionFilterSourceSelection{})

	afterPivot, _, err := service.constructionSources().constructionCandidateWithPivotSources(context.Background(), base, request, candidate)
	if err != nil {
		t.Fatalf("pivot preparation before a filter clear: %v", err)
	}
	if rs05HasProjection(afterPivot.SourceProjections, filterSourceColumn, rs05FilterID) {
		t.Fatalf("pivot preparation restored a removed Filter-owned projection: %#v", afterPivot.SourceProjections)
	}
	if !rs05HasProjection(afterPivot.SourceProjections, rs05OtherColumn, rs05OtherPivotID) {
		t.Fatalf("pivot preparation failed to repair the partial pivot-two omission carried by the client: %#v", afterPivot.SourceProjections)
	}

	afterFilter, err := service.testConstructionCandidateWithFilterSources(context.Background(), base, request, afterPivot)
	if err != nil {
		t.Fatalf("filter preparation for the sequence run: %v", err)
	}
	if rs05HasProjection(afterFilter.SourceProjections, filterSourceColumn, rs05FilterID) {
		t.Fatalf("the cleared Filter-owned projection did not stay cleared: %#v", afterFilter.SourceProjections)
	}
	for _, output := range afterFilter.Steps[0].Outputs {
		if output.ID == filterSourceColumn {
			t.Fatalf("filter clear left the projection column in the filter output: %#v", afterFilter.Steps[0].Outputs)
		}
	}
}

func TestPivotSourceClearSurvivesFilterPreparation(t *testing.T) {
	snapshot := filterSourceSnapshot("pivot-clear-survives-filter")
	service := filterSourceService(t)
	baseConstruction := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05PivotStep(rs05PivotID, filterSourceIdentity, rs05OldColumn),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{rs05RootProjection(rs05OldColumn, rs05PivotID)},
	}
	candidate := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps:   []authoringv2.ConstructionStep{rs05PivotStep(rs05PivotID, filterSourceIdentity)},
	}
	base := filterSourceBase(snapshot, baseConstruction)
	request := ConstructionProposalRequest{
		Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
		ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:filter-source-draft", OutputID: filterSourceOutput,
		ChangedStepID: rs05PivotID, PivotSources: []ConstructionPivotSourceSelection{},
	}

	cleared, _, err := service.constructionSources().constructionCandidateWithPivotSources(context.Background(), base, request, candidate)
	if err != nil {
		t.Fatalf("pivot preparation for the clear: %v", err)
	}
	if len(cleared.SourceProjections) != 0 {
		t.Fatalf("pivot preparation did not clear the edited Pivot-owned projections: %#v", cleared.SourceProjections)
	}
	afterFilter, err := service.testConstructionCandidateWithFilterSources(context.Background(), base, request, cleared)
	if err != nil {
		t.Fatalf("filter preparation after the pivot clear: %v", err)
	}
	if len(afterFilter.SourceProjections) != 0 {
		t.Fatalf("REGRESSION: filter preparation restored the intentionally cleared Pivot input: %#v", afterFilter.SourceProjections)
	}
}

func TestPivotSourceClearWithoutFilterPreparationUnaffected(t *testing.T) {
	snapshot := filterSourceSnapshot("pivot-clear-alone")
	service := filterSourceService(t)
	baseConstruction := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05PivotStep(rs05PivotID, filterSourceIdentity, rs05OldColumn),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{rs05RootProjection(rs05OldColumn, rs05PivotID)},
	}
	candidate := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps:   []authoringv2.ConstructionStep{rs05PivotStep(rs05PivotID, filterSourceIdentity)},
	}
	cleared, _, err := service.constructionSources().constructionCandidateWithPivotSources(context.Background(),
		filterSourceBase(snapshot, baseConstruction),
		ConstructionProposalRequest{
			Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
			ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:filter-source-draft", OutputID: filterSourceOutput,
			ChangedStepID: rs05PivotID, PivotSources: []ConstructionPivotSourceSelection{},
		}, candidate)
	if err != nil {
		t.Fatalf("pivot preparation for the clear: %v", err)
	}
	if len(cleared.SourceProjections) != 0 {
		t.Fatalf("pivot-only clear kept owned projections: %#v", cleared.SourceProjections)
	}
}

func TestPivotSourceReplaceSurvivesFilterPreparation(t *testing.T) {
	snapshot := filterSourceSnapshot("pivot-replace-survives-filter")
	service := filterSourceService(t)
	baseConstruction := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05PivotStep(rs05PivotID, filterSourceIdentity, rs05OldColumn),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{rs05RootProjection(rs05OldColumn, rs05PivotID)},
	}
	candidate := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps:   []authoringv2.ConstructionStep{rs05PivotStep(rs05PivotID, filterSourceIdentity, rs05NewColumn)},
	}
	base := filterSourceBase(snapshot, baseConstruction)
	request := ConstructionProposalRequest{
		Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
		ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:filter-source-draft", OutputID: filterSourceOutput,
		ChangedStepID: rs05PivotID,
		PivotSources: []ConstructionPivotSourceSelection{{
			ChoiceID: filterSourceRowChoice(t, snapshot, filterSourceField).ChoiceID,
			ColumnID: rs05NewColumn,
		}},
	}

	prepared, _, err := service.constructionSources().constructionCandidateWithPivotSources(context.Background(), base, request, candidate)
	if err != nil {
		t.Fatalf("pivot preparation for the replacement: %v", err)
	}
	afterFilter, err := service.testConstructionCandidateWithFilterSources(context.Background(), base, request, prepared)
	if err != nil {
		t.Fatalf("filter preparation after the replacement: %v", err)
	}
	if len(afterFilter.SourceProjections) != 1 {
		t.Fatalf("prepared projections = %#v, want only the replacement Pivot-owned projection", afterFilter.SourceProjections)
	}
	got := afterFilter.SourceProjections[0]
	if got.ColumnID != rs05NewColumn || got.OwnerStepID != rs05PivotID || got.FieldPath != filterSourceField ||
		got.FHIRType != "boolean" || got.LogicalType != "boolean" || got.OccurrenceID != authoringv2.RootOccurrenceID {
		t.Fatalf("replacement projection = %#v, want server-derived facts for the new Pivot input", got)
	}
}

func TestXopPortClearPivotPathEmptySliceStrips(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-clear-empty")
	service := filterSourceService(t)
	durable := rs05FilterOwnedDurable()
	base := filterSourceBase(snapshot, durable)
	if !rs05HasOwnedHelper(durable.Steps, rs05PivotID) || !rs05HasProjection(durable.SourceProjections, rs05HelperColumn, rs05PivotID) {
		t.Fatal("fixture precondition broken: durable must start with pivot inputs")
	}
	request := ConstructionProposalRequest{
		Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
		ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:xop", OutputID: filterSourceOutput,
		ChangedStepID: rs05PivotID, PivotSources: []ConstructionPivotSourceSelection{},
	}
	cleared, _, err := service.constructionSources().constructionCandidateWithPivotSources(context.Background(), base, request, rs05CloneConstruction(durable))
	if err != nil {
		t.Fatalf("clear pivot with empty slice failed: %v", err)
	}
	rs05AssertionPivotCleared(t, "[] clear", cleared)
	if !rs05HasProjection(cleared.SourceProjections, filterSourceColumn, rs05FilterID) {
		t.Errorf("over-clear: pivot [] clear wiped the foreign filter-owned projection")
	}
}

func TestXopPortPivotLaneReaddsDuringFilterChange(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-pivot-lane-readd")
	service := filterSourceService(t)
	base := filterSourceBase(snapshot, rs05SeedDurable())
	request := ConstructionProposalRequest{
		Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
		ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:xop", OutputID: filterSourceOutput,
		ChangedStepID: rs05FilterID,
	}
	prepared, _, err := service.constructionSources().constructionCandidateWithPivotSources(context.Background(), base, request, rs05CloneConstruction(rs05SeedCleared()))
	if err != nil {
		t.Fatalf("pivot lane during filter change failed: %v", err)
	}
	rs05AssertionPivotCleared(t, "pivot lane during filter change", prepared)
}

func TestXopPortPivotSetBranchDoesNotReImportClearedDeriveProjection(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-pivot-set-derive-clear")
	service := filterSourceService(t)
	durable := rs05FilterOwnedDurable()
	durable.Steps = append(durable.Steps, rs05DeriveLaneStep(2))
	durable.SourceProjections = append(durable.SourceProjections, rs05DeriveOwnedProjection())
	base := rs05BaseWithHelperStage(filterSourceBase(snapshot, durable))

	candidate := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05SeedFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
			rs05HelperStep(),
			rs05SeedPivotStep(),
			rs05DeriveLaneStep(2),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{rs05RootProjection(rs05HelperColumn, rs05PivotID)},
	}
	candidate.Steps[2].Operation.Pivot.GroupKeyIDs = []string{filterSourceIdentity, rs05NewColumn}
	request := ConstructionProposalRequest{
		Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
		ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:xop", OutputID: filterSourceOutput,
		ChangedStepID: rs05PivotID,
		PivotSources: []ConstructionPivotSourceSelection{{
			ChoiceID: filterSourceRowChoice(t, snapshot, filterSourceField).ChoiceID,
			ColumnID: rs05NewColumn,
		}},
	}
	prepared, _, err := service.constructionSources().constructionCandidateWithPivotSources(context.Background(), base, request, candidate)
	if err != nil {
		t.Fatalf("pivot SET while the derive source was cleared: %v", err)
	}
	if rs05HasProjection(prepared.SourceProjections, rs05DeriveColumn, rs05DeriveID) {
		t.Fatalf("pivot SET re-imported the cleared DERIVE-owned durable input: %#v", prepared.SourceProjections)
	}
	if rs05HasProjection(prepared.SourceProjections, filterSourceColumn, rs05FilterID) {
		t.Fatalf("pivot SET re-imported the cleared filter-owned projection: %#v", prepared.SourceProjections)
	}
	if !rs05HasProjection(prepared.SourceProjections, rs05NewColumn, rs05PivotID) {
		t.Fatalf("pivot SET did not materialize its own signed projection: %#v", prepared.SourceProjections)
	}
}

func TestXopPortPivotSetBranchDoesNotReImportClearedFilterProjection(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-pivot-set-filter-clear")
	service := filterSourceService(t)
	durable := rs05FilterOwnedDurable()
	base := filterSourceBase(snapshot, durable)

	base = rs05BaseWithHelperStage(base)
	candidate := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05SeedFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
			rs05HelperStep(),
			rs05SeedPivotStep(),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{rs05RootProjection(rs05HelperColumn, rs05PivotID)},
	}
	candidate.Steps[2].Operation.Pivot.GroupKeyIDs = []string{filterSourceIdentity, rs05NewColumn}
	request := ConstructionProposalRequest{
		Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
		ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:xop", OutputID: filterSourceOutput,
		ChangedStepID: rs05PivotID,
		PivotSources: []ConstructionPivotSourceSelection{{
			ChoiceID: filterSourceRowChoice(t, snapshot, filterSourceField).ChoiceID,
			ColumnID: rs05NewColumn,
		}},
	}
	prepared, _, err := service.constructionSources().constructionCandidateWithPivotSources(context.Background(), base, request, candidate)
	if err != nil {
		t.Fatalf("pivot SET while the filter source was cleared: %v", err)
	}
	if rs05HasProjection(prepared.SourceProjections, filterSourceColumn, rs05FilterID) {
		t.Fatalf("pivot SET re-imported the cleared filter-owned projection from the base: %#v", prepared.SourceProjections)
	}
	if !rs05HasProjection(prepared.SourceProjections, rs05NewColumn, rs05PivotID) {
		t.Fatalf("pivot SET did not materialize its own signed projection: %#v", prepared.SourceProjections)
	}
}

func rs05AssertionPivotCleared(t *testing.T, name string, prepared authoringv2.Construction) {
	t.Helper()
	if rs05HasOwnedHelper(prepared.Steps, rs05PivotID) {
		t.Errorf("REGRESSION [%s]: preparation RE-ADDED the cleared pivot-owned RELATED_FIELD helper step %s", name, rs05HelperID)
	}
	if rs05HasProjection(prepared.SourceProjections, rs05HelperColumn, rs05PivotID) {
		t.Errorf("REGRESSION [%s]: preparation RE-ADDED the cleared pivot-owned SourceProjection %s", name, rs05HelperColumn)
	}
}

func rs05BaseWithHelperStage(base constructionBase) constructionBase {
	base.Stages = append(append([]explorer.ReceiptConstructionStage(nil), base.Stages...), explorer.ReceiptConstructionStage{
		ID: rs05HelperID, InputStageID: recipe.ConstructionSourceProjectionID, Operation: "RELATED_FIELD",
		Columns: []explorer.ReceiptConstructionStageColumn{
			{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			{ID: rs05HelperColumn, Name: "pv_col", Label: "BodyStructure.active", Type: "boolean"},
		},
	})
	return base
}

func rs05CloneConstruction(c authoringv2.Construction) authoringv2.Construction {
	out := c
	out.Steps = append([]authoringv2.ConstructionStep(nil), c.Steps...)
	for index := range out.Steps {
		step := out.Steps[index]
		if step.Inputs != nil {
			step.Inputs = append([]authoringv2.ConstructionInputRef(nil), step.Inputs...)
		}
		if step.Outputs != nil {
			step.Outputs = append([]authoringv2.StageColumn(nil), step.Outputs...)
		}
		out.Steps[index] = step
	}
	if c.SourceProjections != nil {
		out.SourceProjections = append([]authoringv2.ConstructionSourceProjection(nil), c.SourceProjections...)
	} else {
		out.SourceProjections = nil
	}
	return out
}

func rs05DeriveLaneStep(literal int64) authoringv2.ConstructionStep {
	value := literal
	return authoringv2.ConstructionStep{
		ID:     rs05DeriveID,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive, Derive: &authoringv2.ConstructionDerive{
			ConstructionID:     rs05DeriveID,
			OutputColumnID:     "derived_total",
			Operation:          authoringv2.ConstructionDerivedAdd,
			Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: &value}},
			Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: func() *int64 { three := int64(3); return &three }()}},
			MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"},
		},
	}
}

func rs05DeriveOwnedProjection() authoringv2.ConstructionSourceProjection {
	return authoringv2.ConstructionSourceProjection{
		ColumnID: rs05DeriveColumn, OwnerStepID: rs05DeriveID, OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: filterSourceField, FHIRType: "integer", LogicalType: "integer", Label: "BodyStructure.precedence",
	}
}

func rs05FilterOwnedDurable() authoringv2.Construction {
	durable := rs05SeedDurable()
	durable.Steps[0] = rs05SeedFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn()))
	durable.SourceProjections = append(durable.SourceProjections, rs05FilterOwnedProjection())
	return durable
}

func rs05FilterOwnedProjection() authoringv2.ConstructionSourceProjection {
	return rs05RootProjection(filterSourceColumn, rs05FilterID)
}

func rs05HasOwnedHelper(steps []authoringv2.ConstructionStep, ownerID string) bool {
	for _, step := range steps {
		if step.ID == rs05HelperID && step.OwnerStepID == ownerID &&
			step.Operation.Kind == authoringv2.ConstructionOperationRelatedField {
			return true
		}
	}
	return false
}

func rs05HasProjection(projections []authoringv2.ConstructionSourceProjection, columnID, ownerStepID string) bool {
	for _, projection := range projections {
		if projection.ColumnID == columnID && projection.OwnerStepID == ownerStepID {
			return true
		}
	}
	return false
}

func rs05HelperStep() authoringv2.ConstructionStep {
	return authoringv2.ConstructionStep{
		ID: rs05HelperID, OwnerStepID: rs05PivotID,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedField, RelatedField: &authoringv2.ConstructionRelatedField{
			ChoiceID: "seed-choice", OutputColumnID: rs05HelperColumn,
			Source: authoringv2.ConstructionRelatedFieldSource{
				Kind: capability.ConstructionChoiceSourceField, CandidateID: "seed-cand", NodeID: filterSourceRootNode,
				ResourceType: filterSourceRootType, Path: filterSourceField,
				Cardinality: "optional_one", LogicalType: "boolean",
			},
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			{ID: rs05HelperColumn, Name: "pv_col", Label: "BodyStructure.active", Type: "boolean", Nullable: true},
		},
	}
}

func rs05OtherHelperStep() authoringv2.ConstructionStep {
	return authoringv2.ConstructionStep{
		ID: rs05Helper2ID, OwnerStepID: rs05OtherPivotID,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedField, RelatedField: &authoringv2.ConstructionRelatedField{
			ChoiceID: "seed-choice-two", OutputColumnID: rs05OtherColumn,
			Source: authoringv2.ConstructionRelatedFieldSource{
				Kind: capability.ConstructionChoiceSourceField, CandidateID: "seed-cand", NodeID: filterSourceRootNode,
				ResourceType: filterSourceRootType, Path: filterSourceField,
				Cardinality: "optional_one", LogicalType: "boolean",
			},
		}},
		Outputs: []authoringv2.StageColumn{{ID: "public-key", Name: "public_key", Label: "Public key", Type: "string"},
			{ID: rs05OtherColumn, Name: "pv_two_col", Label: "BodyStructure.active", Type: "boolean", Nullable: true}},
	}
}

func rs05PivotStep(id string, groupKeys ...string) authoringv2.ConstructionStep {
	return authoringv2.ConstructionStep{
		ID:     id,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: &authoringv2.ConstructionPivot{
			ConstructionID: id, GroupKeyIDs: groupKeys,
			CategoryColumnID: rs05CategoryCol, ValueColumnID: rs05ValueCol,
			Categories:             []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("t"), OutputColumnID: "pval"}},
			DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateError,
			MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
			UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
		}},
		Outputs: []authoringv2.StageColumn{{ID: "pval", Name: "pval", Label: "pval", Type: "string"}},
	}
}

func rs05RootProjection(columnID, ownerStepID string) authoringv2.ConstructionSourceProjection {
	return authoringv2.ConstructionSourceProjection{
		ColumnID: columnID, OwnerStepID: ownerStepID, OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: filterSourceField, FHIRType: "boolean", LogicalType: "boolean", Label: "BodyStructure.active",
	}
}

func rs05RunBothPreparations(t *testing.T, service *Service, base constructionBase, request ConstructionProposalRequest, candidate authoringv2.Construction) authoringv2.Construction {
	t.Helper()
	prepared, _, err := service.constructionSources().constructionCandidateWithPivotSources(context.Background(), base, request, candidate)
	if err != nil {
		t.Fatalf("pivot preparation: %v", err)
	}
	prepared, err = service.testConstructionCandidateWithFilterSources(context.Background(), base, request, prepared)
	if err != nil {
		t.Fatalf("filter preparation: %v", err)
	}
	return prepared
}

func rs05SeedCleared() authoringv2.Construction {
	return authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05SeedFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
			rs05SeedPivotStep(),
		},
	}
}

func rs05SeedDurable() authoringv2.Construction {
	return authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05SeedFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
			rs05HelperStep(),
			rs05SeedPivotStep(),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{rs05RootProjection(rs05HelperColumn, rs05PivotID)},
	}
}

func rs05SeedFilterStep(columnID string, outputs []authoringv2.StageColumn) authoringv2.ConstructionStep {
	return authoringv2.ConstructionStep{
		ID:     rs05FilterID,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
			ColumnID: columnID, Operator: authoringv2.ConstructionFilterEquals,
			Values: []authoringv2.FilterValue{{Kind: authoringv2.ConstructionFilterBoolean, Boolean: filterSourceBool(true)}},
		}},
		Outputs: outputs,
	}
}

func rs05SeedPivotStep() authoringv2.ConstructionStep {
	return authoringv2.ConstructionStep{
		ID:     rs05PivotID,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: rs05HelperID}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: &authoringv2.ConstructionPivot{
			ConstructionID: rs05PivotID, GroupKeyIDs: []string{filterSourceIdentity},
			CategoryColumnID: rs05HelperColumn, ValueColumnID: "pval",
			Categories:             []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("t"), OutputColumnID: "pval"}},
			DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateError,
			MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
			UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			{ID: "pval", Name: "pval", Label: "pval", Type: "integer"},
		},
	}
}

func TestFilterSourceCreateAndRestoreSurvivePivotPreparation(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-create-and-restore-sequence")
	service := filterSourceService(t)
	selection := filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)

	baseConstruction := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05SeedFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn())),
			rs05PivotStep(rs05OtherPivotID, "public-key-two"),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{rs05RootProjection(rs05OtherColumn, rs05OtherPivotID)},
	}
	candidate := rs05CloneConstruction(baseConstruction)
	created := rs05RunBothPreparations(t, service, filterSourceBase(snapshot, baseConstruction),
		filterSourceProposalRequest(rs05FilterID, []ConstructionFilterSourceSelection{selection}), candidate)
	if count := rs05CountOwned(created.SourceProjections, rs05FilterID); count != 1 {
		t.Fatalf("created filter-owned projections = %d: %#v", count, created.SourceProjections)
	}
	if !rs05HasProjection(created.SourceProjections, filterSourceColumn, rs05FilterID) ||
		created.SourceProjections[rs05IndexOwned(created.SourceProjections, rs05FilterID)].FieldPath != filterSourceField {
		t.Fatalf("created projections = %#v, want the signed durable filter projection", created.SourceProjections)
	}
	if !rs05HasProjection(created.SourceProjections, rs05OtherColumn, rs05OtherPivotID) {
		t.Fatalf("creating a filter source dropped the carried pivot-two chain: %#v", created.SourceProjections)
	}

	saved := rs05CloneConstruction(baseConstruction)
	restored := rs05RunBothPreparations(t, service, filterSourceBase(snapshot, saved),
		filterSourceProposalRequest(rs05FilterID, []ConstructionFilterSourceSelection{selection}), saved)
	if count := rs05CountOwned(restored.SourceProjections, rs05FilterID); count != 1 {
		t.Fatalf("restored filter-owned projections = %d: %#v", count, restored.SourceProjections)
	}
	if !rs05HasProjection(restored.SourceProjections, rs05OtherColumn, rs05OtherPivotID) {
		t.Fatalf("restoring a filter source dropped the carried pivot-two chain: %#v", restored.SourceProjections)
	}
}

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
	rs05DeriveID     = "derive-total"
	rs05DeriveColumn = "derive-private-input"
)

func rs05CountOwned(projections []authoringv2.ConstructionSourceProjection, ownerStepID string) int {
	count := 0
	for _, projection := range projections {
		if projection.OwnerStepID == ownerStepID {
			count++
		}
	}
	return count
}

func rs05IndexOwned(projections []authoringv2.ConstructionSourceProjection, ownerStepID string) int {
	for index, projection := range projections {
		if projection.OwnerStepID == ownerStepID {
			return index
		}
	}
	return -1
}
