package lifecycle

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

// ROW-SOURCE-05 durable regressions.
//
// ProposeConstruction composes the candidate through the Pivot lane first and
// the Filter lane second (construction_proposal.go). Both lanes preserved
// durable Pivot-owned inputs out of the accepted base, and both set branches
// bulk-copied every base projection except the edited owner's. The 01 wire
// contract ("nil never re-attaches; [] clears") makes the submitted candidate
// a full replacement construction: an owner whose complete base-owned chain
// is gone from the candidate is intentionally cleared, and no lane may
// re-import it from the base (constructionOwnerInputsClearedFromCandidate).
// A partial chain remains a client omission that preservation must repair.

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

func rs05RootProjection(columnID, ownerStepID string) authoringv2.ConstructionSourceProjection {
	return authoringv2.ConstructionSourceProjection{
		ColumnID: columnID, OwnerStepID: ownerStepID, OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: filterSourceField, FHIRType: "boolean", LogicalType: "boolean", Label: "BodyStructure.active",
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

func rs05CountOwned(projections []authoringv2.ConstructionSourceProjection, ownerStepID string) int {
	count := 0
	for _, projection := range projections {
		if projection.OwnerStepID == ownerStepID {
			count++
		}
	}
	return count
}

func rs05HasProjection(projections []authoringv2.ConstructionSourceProjection, columnID, ownerStepID string) bool {
	for _, projection := range projections {
		if projection.ColumnID == columnID && projection.OwnerStepID == ownerStepID {
			return true
		}
	}
	return false
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

// ---------------------------------------------------------------------------
// Lane-level: the Pivot clear must survive the Filter preparation even when
// the accepted base still holds the inputs (the not-yet-applied clear window).
// ---------------------------------------------------------------------------

// Reproducer-equivalent from the ROW-SOURCE-05 contract: an edited Pivot that
// submits PivotSources: [] clears its owned private projections; the
// subsequent Filter preparation on that result must leave them cleared.
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

	cleared, _, err := service.constructionCandidateWithPivotSources(context.Background(), base, request, candidate)
	if err != nil {
		t.Fatalf("pivot preparation for the clear: %v", err)
	}
	if len(cleared.SourceProjections) != 0 {
		t.Fatalf("pivot preparation did not clear the edited Pivot-owned projections: %#v", cleared.SourceProjections)
	}
	afterFilter, err := service.constructionCandidateWithFilterSources(context.Background(), base, request, cleared)
	if err != nil {
		t.Fatalf("filter preparation after the pivot clear: %v", err)
	}
	if len(afterFilter.SourceProjections) != 0 {
		t.Fatalf("REGRESSION: filter preparation restored the intentionally cleared Pivot input: %#v", afterFilter.SourceProjections)
	}
}

// The same sequence with a replacement selection: the pivot preparation swaps
// the owned projection, and the filter preparation must neither drop the
// replacement nor restore the replaced-away projection.
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

	prepared, _, err := service.constructionCandidateWithPivotSources(context.Background(), base, request, candidate)
	if err != nil {
		t.Fatalf("pivot preparation for the replacement: %v", err)
	}
	afterFilter, err := service.constructionCandidateWithFilterSources(context.Background(), base, request, prepared)
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

// A Pivot clear with no Filter preparation at all is unaffected.
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
	cleared, _, err := service.constructionCandidateWithPivotSources(context.Background(),
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

// ---------------------------------------------------------------------------
// Ports of the operator-accepted reproduction seeds
// (/tmp/opencode/xop-repro/internal/explorer/lifecycle/repro_filter_clears_pivot_test.go).
// ---------------------------------------------------------------------------

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

// rs05HelperStep is the helper-form pivot chain used by the xop seeds.
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

func rs05SeedCleared() authoringv2.Construction {
	return authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05SeedFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
			rs05SeedPivotStep(),
		},
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

func rs05AssertionPivotCleared(t *testing.T, name string, prepared authoringv2.Construction) {
	t.Helper()
	if rs05HasOwnedHelper(prepared.Steps, rs05PivotID) {
		t.Errorf("REGRESSION [%s]: preparation RE-ADDED the cleared pivot-owned RELATED_FIELD helper step %s", name, rs05HelperID)
	}
	if rs05HasProjection(prepared.SourceProjections, rs05HelperColumn, rs05PivotID) {
		t.Errorf("REGRESSION [%s]: preparation RE-ADDED the cleared pivot-owned SourceProjection %s", name, rs05HelperColumn)
	}
}

// TestXopPortFilterPreparationRestoresClearedPivotInputs: after the Pivot
// source inputs were cleared (no helper, no projection), composing a FILTER
// candidate must not re-attach them from the durable base, both for a plain
// filter edit and for a filter preparation with a signed filter source.
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
			[]ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}), clearedB)
	if err != nil {
		t.Fatalf("prepare filter with signed filterSource failed: %v", err)
	}
	rs05AssertionPivotCleared(t, "signed filterSource", preparedB)
	if count := rs05CountOwned(preparedB.SourceProjections, rs05FilterID); count != 1 {
		t.Fatalf("filter-owned projection count = %d, want the one signed projection: %#v", count, preparedB.SourceProjections)
	}
}

// TestXopPortPivotLaneReaddsDuringFilterChange: the pivot lane, when NOT the
// applied operation (any non-Pivot edit runs it first with skipOwnerID=""),
// must already refuse to re-add the cleared pivot inputs.
func TestXopPortPivotLaneReaddsDuringFilterChange(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-pivot-lane-readd")
	service := filterSourceService(t)
	base := filterSourceBase(snapshot, rs05SeedDurable())
	request := ConstructionProposalRequest{
		Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
		ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:xop", OutputID: filterSourceOutput,
		ChangedStepID: rs05FilterID,
	}
	prepared, _, err := service.constructionCandidateWithPivotSources(context.Background(), base, request, rs05CloneConstruction(rs05SeedCleared()))
	if err != nil {
		t.Fatalf("pivot lane during filter change failed: %v", err)
	}
	rs05AssertionPivotCleared(t, "pivot lane during filter change", prepared)
}

// TestXopPortPerOwnerIsolationOnlyEditedOwnerPreserves: the durable
// re-attach must never resurrect the owner the candidate intentionally
// cleared, while foreign owners the candidate keeps must survive untouched.
func TestXopPortPerOwnerIsolationOnlyEditedOwnerPreserves(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-owner-isolation")
	service := filterSourceService(t)
	durable := rs05SeedDurable()
	durable.Steps = append(durable.Steps, rs05PivotStep(rs05OtherPivotID, "public-key"))
	base := filterSourceBase(snapshot, durable)

	candidate := rs05SeedCleared()
	candidate.Steps = append(candidate.Steps, rs05PivotStep(rs05OtherPivotID, "public-key"))
	request := ConstructionProposalRequest{
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

// TestXopPortControlDurableAlreadyClearedStaysCleared pins the trigger:
// restore only fires from the accepted base; once the durable is cleared,
// nothing is re-added and the lane's own signed materialization still works.
func TestXopPortControlDurableAlreadyClearedStaysCleared(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-cleared-durable")
	service := filterSourceService(t)
	cleared := rs05SeedCleared()
	base := filterSourceBase(snapshot, rs05CloneConstruction(cleared))

	clearedForFilter := rs05CloneConstruction(cleared)
	clearedForFilter.Steps[0] = rs05SeedFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn()))
	prepared, err := service.constructionCandidateWithFilterSources(context.Background(), base,
		filterSourceProposalRequest(rs05FilterID,
			[]ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}), clearedForFilter)
	if err != nil {
		t.Fatalf("prepare filter against cleared durable: %v", err)
	}
	rs05AssertionPivotCleared(t, "control cleared durable", prepared)
	if !rs05HasProjection(prepared.SourceProjections, filterSourceColumn, rs05FilterID) {
		t.Errorf("control: filter's own signed projection did not materialize; harness broken")
	}
}

// TestXopPortClearPivotPathEmptySliceStrips: the 01 contract holds on the
// pivot lane — an explicit empty (non-nil) PivotSources strips the helper
// and projection while a foreign filter-owned projection survives (scope).
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
	cleared, _, err := service.constructionCandidateWithPivotSources(context.Background(), base, request, rs05CloneConstruction(durable))
	if err != nil {
		t.Fatalf("clear pivot with empty slice failed: %v", err)
	}
	rs05AssertionPivotCleared(t, "[] clear", cleared)
	if !rs05HasProjection(cleared.SourceProjections, filterSourceColumn, rs05FilterID) {
		t.Errorf("over-clear: pivot [] clear wiped the foreign filter-owned projection")
	}
}

// TestXopPortPivotSetBranchDoesNotReImportClearedFilterProjection pins the
// pivot lane SET branch (construction_pivot_source.go) foreign-owner bulk
// copy: a candidate that cleared the FILTER's durable input while the Pivot
// SET re-materializes its own must not have the filter-owned projection
// re-imported from the base.
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
	prepared, _, err := service.constructionCandidateWithPivotSources(context.Background(), base, request, candidate)
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

// TestXopPortFilterSetBranchDoesNotReImportClearedPivotProjection pins the
// filter lane SET branch (filter_source.go) foreign-owner bulk copy: a
// candidate that cleared another Pivot's durable input while the Filter SET
// materializes its own must not have the pivot-owned projection re-imported.
func TestXopPortFilterSetBranchDoesNotReImportClearedPivotProjection(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-filter-set-pivot-clear")
	service := filterSourceService(t)
	baseConstruction := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05SeedFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
			rs05PivotStep(rs05OtherPivotID, "public-key"),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{rs05RootProjection(rs05OtherColumn, rs05OtherPivotID)},
	}
	candidate := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05SeedFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn())),
			rs05PivotStep(rs05OtherPivotID, "public-key"),
		},
	}
	prepared, err := service.constructionCandidateWithFilterSources(context.Background(), filterSourceBase(snapshot, baseConstruction),
		filterSourceProposalRequest(rs05FilterID,
			[]ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}), candidate)
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

// The mirror hazard direction: a candidate whose Filter-owned projection is
// already gone and whose second Pivot carried only part of its chain. Pivot
// preparation must still repair the partial client omission yet leave the
// fully cleared Filter input cleared, and the following Filter preparation
// must keep it that way.
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

	afterPivot, _, err := service.constructionCandidateWithPivotSources(context.Background(), base, request, candidate)
	if err != nil {
		t.Fatalf("pivot preparation before a filter clear: %v", err)
	}
	if rs05HasProjection(afterPivot.SourceProjections, filterSourceColumn, rs05FilterID) {
		t.Fatalf("pivot preparation restored a removed Filter-owned projection: %#v", afterPivot.SourceProjections)
	}
	if !rs05HasProjection(afterPivot.SourceProjections, rs05OtherColumn, rs05OtherPivotID) {
		t.Fatalf("pivot preparation failed to repair the partial pivot-two omission carried by the client: %#v", afterPivot.SourceProjections)
	}

	afterFilter, err := service.constructionCandidateWithFilterSources(context.Background(), base, request, afterPivot)
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

// Contract preservation: ordinary Filter source creation and restoration
// still materialize exactly the signed durable projection through the
// complete preparation sequence, alongside the other Pivot's carried chain.
func TestFilterSourceCreateAndRestoreSurvivePivotPreparation(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-create-and-restore-sequence")
	service := filterSourceService(t)
	selection := filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)

	// Create: the candidate carries the other Pivot's chain (full-replacement
	// contract) and adds a new source-backed Filter ahead of it.
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

	// Restore: reselecting a saved source-backed Filter keeps its one durable
	// projection through both preparations.
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

func rs05IndexOwned(projections []authoringv2.ConstructionSourceProjection, ownerStepID string) int {
	for index, projection := range projections {
		if projection.OwnerStepID == ownerStepID {
			return index
		}
	}
	return -1
}

func rs05RunBothPreparations(t *testing.T, service *Service, base constructionBase, request ConstructionProposalRequest, candidate authoringv2.Construction) authoringv2.Construction {
	t.Helper()
	prepared, _, err := service.constructionCandidateWithPivotSources(context.Background(), base, request, candidate)
	if err != nil {
		t.Fatalf("pivot preparation: %v", err)
	}
	prepared, err = service.constructionCandidateWithFilterSources(context.Background(), base, request, prepared)
	if err != nil {
		t.Fatalf("filter preparation: %v", err)
	}
	return prepared
}

// ---------------------------------------------------------------------------
// FULL ProposeConstruction regressions (both ChangedStepID directions) over
// the pending-clear window: the accepted base still holds the inputs while
// the submitted full-replacement candidate has cleared them.
// ---------------------------------------------------------------------------

func rs05ProposeService(t *testing.T) (*Service, *fakeStore, capability.Snapshot) {
	t.Helper()
	service, store, snapshot := constructionProposalService(t)
	donor := filterSourceSnapshot("rs05-propose")
	snapshot.Nodes = donor.Nodes
	snapshot.Candidates = donor.Candidates
	resolver := filterSourceResolver(t)
	service.config.RowChoicePlanner, service.config.RowChoiceResolver = resolver, resolver
	authorized := AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}
	service.config.Capability.Token = func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil }
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) { return authorized, nil }
	service.config.Capability.ForExecution = func(context.Context, string, string) (AuthorizedCapability, error) { return authorized, nil }
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := lifecycleTableShapeReceipt(snapshot, request.Workspace)
		receipt.ConstructionProposal = cloneConstructionProposalBinding(request.ConstructionProposal)
		receipt.ConstructionStages = map[string][]explorer.ReceiptConstructionStage{
			"patients": testConstructionStageDescriptors(request.Workspace, "patients"),
		}
		var err error
		receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = explorer.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil {
			return dataframeexecution.PreviewSummary{}, context.Canceled
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"body_structure_id"}, Complete: true}, nil
	}
	return service, store, snapshot
}

func rs05ProposeSaveDraft(t *testing.T, store *fakeStore, construction authoringv2.Construction) {
	t.Helper()
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.SemanticsVersion = authoringv2.CurrentSemanticsVersion
	document := authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: "patients", Title: "Body structures"},
		RootResourceType: filterSourceRootType,
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: filterSourceRootType},
		Rows:             authoringv2.RecordsRowDefinition(),
		Columns: []authoringv2.Column{
			{
				ColumnID: filterSourceIdentity, Column: "body_structure_id", Label: "Body structure ID",
				LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
				Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
			},
			{
				ColumnID: rs05CategoryCol, Column: "public_category", Label: "Public category",
				LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
				Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
			},
			{
				ColumnID: rs05ValueCol, Column: "public_value", Label: "Public value",
				LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
				Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "description", ProjectionMode: "VALUE"}},
			},
		},
		Construction: &construction,
	}
	workspace.Documents = []authoringv2.Document{document}
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

func rs05PublicOutputs(withPcol bool) []authoringv2.StageColumn {
	outputs := []authoringv2.StageColumn{
		{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
		{ID: rs05CategoryCol, Name: "public_category", Label: "Public category", Type: "string"},
		{ID: rs05ValueCol, Name: "public_value", Label: "Public value", Type: "string"},
	}
	if withPcol {
		outputs = append(outputs, authoringv2.StageColumn{
			ID: rs05OldColumn, Name: authoringv2.ConstructionSourceProjectionName(rs05OldColumn), Label: "BodyStructure.active", Type: "boolean"})
	}
	return outputs
}

func rs05ProposeFilterStep(columnID string, outputs []authoringv2.StageColumn) authoringv2.ConstructionStep {
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

func rs05ProposePivotStep(groupKeys ...string) authoringv2.ConstructionStep {
	outputs := make([]authoringv2.StageColumn, 0, len(groupKeys)+1)
	for _, key := range groupKeys {
		keyType, keyName := "string", key
		switch key {
		case rs05OldColumn:
			keyType = "boolean"
			keyName = authoringv2.ConstructionSourceProjectionName(rs05OldColumn)
		case filterSourceIdentity:
			keyName = "body_structure_id"
		}
		outputs = append(outputs, authoringv2.StageColumn{ID: key, Name: keyName, Label: key, Type: keyType})
	}
	outputs = append(outputs, authoringv2.StageColumn{ID: "pval", Name: "pval", Label: "pval", Type: "string"})
	return authoringv2.ConstructionStep{
		ID:     rs05PivotID,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: rs05FilterID}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: &authoringv2.ConstructionPivot{
			ConstructionID: rs05PivotID, GroupKeyIDs: groupKeys, CategoryColumnID: rs05CategoryCol, ValueColumnID: rs05ValueCol,
			Categories:             []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("t"), OutputColumnID: "pval"}},
			DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateError,
			MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
			UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
		}},
		Outputs: outputs,
	}
}

func rs05ProposeRequest(store *explorer.Explorer, snapshot capability.Snapshot, changedStepID string, candidate authoringv2.Construction) ConstructionProposalRequest {
	return ConstructionProposalRequest{
		Project: store.Project, ExplorerID: store.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.DraftVersion, ExpectedDraftDigest: store.DraftDigest,
		OutputID: "patients", ChangedStepID: changedStepID, CandidateConstruction: candidate,
	}
}

// Direction 1 (ChangedStepID = FILTER): the pending user clear of the Pivot
// source inputs must survive the complete ProposeConstruction sequence;
// pre-fix the pivot lane (then the filter lane) re-added the inputs from the
// base, which the authoring validation rejected as a projection the filter
// no longer carries — the proposal could not even be previewed.
func TestProposeConstructionFilterEditDoesNotRestoreClearedPivotSourceInput(t *testing.T) {
	service, store, snapshot := rs05ProposeService(t)
	durable := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05ProposeFilterStep(filterSourceIdentity, rs05PublicOutputs(true)),
			rs05ProposePivotStep(filterSourceIdentity, rs05OldColumn),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{rs05RootProjection(rs05OldColumn, rs05PivotID)},
	}
	rs05ProposeSaveDraft(t, store, durable)
	acceptedBefore := string(store.created.DraftConfig)
	cleared := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
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

// Direction 1 with a signed Filter source selection (the ROW-SOURCE-05
// contract's own repro sequence): the filter lane's own materialization must
// still happen while the cleared Pivot input stays cleared.
func TestProposeConstructionFilterSourceSetDoesNotRestoreClearedPivotSourceInput(t *testing.T) {
	service, store, snapshot := rs05ProposeService(t)
	durable := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05ProposeFilterStep(filterSourceIdentity, rs05PublicOutputs(true)),
			rs05ProposePivotStep(filterSourceIdentity, rs05OldColumn),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{rs05RootProjection(rs05OldColumn, rs05PivotID)},
	}
	rs05ProposeSaveDraft(t, store, durable)
	cleared := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05ProposeFilterStep(filterSourceColumn, append(rs05PublicOutputs(false), filterSourceProjectionColumn())),
			rs05ProposePivotStep(filterSourceIdentity),
		},
	}
	request := rs05ProposeRequest(store.created, snapshot, rs05FilterID, cleared)
	request.FilterSources = []ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}
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

// Direction 2 (ChangedStepID = PIVOT): the owning lane's [] clear strips the
// Pivot inputs while a foreign filter-owned durable input must survive the
// same proposal (no over-clear) — the per-operator ownership acceptance.
func TestProposeConstructionPivotClearKeepsForeignFilterSourceInput(t *testing.T) {
	service, store, snapshot := rs05ProposeService(t)
	durable := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05ProposeFilterStep(filterSourceColumn, append(rs05PublicOutputs(true), filterSourceProjectionColumn())),
			rs05ProposePivotStep(filterSourceIdentity, rs05OldColumn),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{
			rs05RootProjection(rs05OldColumn, rs05PivotID), rs05FilterOwnedProjection(),
		},
	}
	rs05ProposeSaveDraft(t, store, durable)
	cleared := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05ProposeFilterStep(filterSourceColumn, append(rs05PublicOutputs(false), filterSourceProjectionColumn())),
			rs05ProposePivotStep(filterSourceIdentity),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{rs05FilterOwnedProjection()},
	}
	request := rs05ProposeRequest(store.created, snapshot, rs05PivotID, cleared)
	request.PivotSources = []ConstructionPivotSourceSelection{}
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

// Control pin for the trigger: the restore only fires while the accepted
// base still holds the inputs. With the durable itself cleared, a filter
// edit stays clean and pre-views normally.
func TestProposeConstructionFilterEditAgainstAlreadyClearedDurable(t *testing.T) {
	service, store, snapshot := rs05ProposeService(t)
	clearedDurable := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
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
	if reflect.DeepEqual(proposal.CandidateConstruction.Steps, []authoringv2.ConstructionStep(nil)) {
		t.Fatal("control lost the candidate steps")
	}
}

func rs05BaseWithHelperStage(base constructionBase) constructionBase {
	base.stages = append(append([]explorer.ReceiptConstructionStage(nil), base.stages...), explorer.ReceiptConstructionStage{
		ID: rs05HelperID, InputStageID: recipe.ConstructionSourceProjectionID, Operation: "RELATED_FIELD",
		Columns: []explorer.ReceiptConstructionStageColumn{
			{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			{ID: rs05HelperColumn, Name: "pv_col", Label: "BodyStructure.active", Type: "boolean"},
		},
	})
	return base
}

// ---------------------------------------------------------------------------
// b92548c7 third-lane coverage (b9-review Finding 1, mutE-proven):
// constructionCandidateWithDeriveSources shares preserveConstructionOwnedInputs
// with the two original lanes; the shared-helper repair must make the Derive
// lane inherit the candidate-cleared semantics, and the :383/:244 foreign-owner
// copies must honor a cleared DERIVE-owned durable input symmetrically.
// ---------------------------------------------------------------------------

const (
	rs05DeriveID     = "derive-total"
	rs05DeriveColumn = "derive-private-input"
)

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

func rs05DeriveRequest(changedStepID string) ConstructionProposalRequest {
	return ConstructionProposalRequest{
		Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
		ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:xop", OutputID: filterSourceOutput,
		ChangedStepID: changedStepID,
	}
}

// TestXopPortDerivePreparationDoesNotRestoreClearedPivotInput is the durable
// port of the b9 reviewer's mutE-proved Finding 1: with the filter lane fixed,
// the DERIVE preparation re-imported the intentionally cleared Pivot chain
// from the base (derive_source.go:230). The shared-helper predicate must close
// this third lane without editing the frozen derive_source.go.
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

// TestXopPortPivotSetBranchDoesNotReImportClearedDeriveProjection is the
// mirror through the Pivot SET-branch foreign copy (:383): with the Pivot SET
// materializing its own signed selection, a fully client-cleared DERIVE-owned
// durable input must not be re-imported from the base.
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
	prepared, _, err := service.constructionCandidateWithPivotSources(context.Background(), base, request, candidate)
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

// TestXopPortFilterSetBranchDoesNotReImportClearedDeriveProjection is the
// mirror through the Filter SET-branch foreign copy (:244).
func TestXopPortFilterSetBranchDoesNotReImportClearedDeriveProjection(t *testing.T) {
	snapshot := filterSourceSnapshot("xop-port-filter-set-derive-clear")
	service := filterSourceService(t)
	baseConstruction := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05SeedFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
			rs05DeriveLaneStep(2),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{rs05DeriveOwnedProjection()},
	}
	candidate := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05SeedFilterStep(filterSourceColumn, append(filterSourceIdentityColumns(), filterSourceProjectionColumn())),
			rs05DeriveLaneStep(2),
		},
	}
	prepared, err := service.constructionCandidateWithFilterSources(context.Background(), filterSourceBase(snapshot, baseConstruction),
		filterSourceProposalRequest(rs05FilterID,
			[]ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}), candidate)
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

func rs05ProposeDeriveStep(literal int64) authoringv2.ConstructionStep {
	value := literal
	return authoringv2.ConstructionStep{
		ID:     rs05DeriveID,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: rs05FilterID}},
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
			{ID: rs05CategoryCol, Name: "public_category", Label: "Public category", Type: "string"},
			{ID: rs05ValueCol, Name: "public_value", Label: "Public value", Type: "string"},
			{ID: rs05OldColumn, Name: authoringv2.ConstructionSourceProjectionName(rs05OldColumn), Label: "BodyStructure.active", Type: "boolean"},
			{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"},
		},
	}
}

// TestProposeConstructionDeriveEditDoesNotRestoreClearedPivotSourceInput runs
// the complete preparation sequence (pivot -> filter -> derive) with
// ChangedStepID = DERIVE and a client-cleared Pivot chain: the proposal must
// stay previewable with the cleared input still cleared.
func TestProposeConstructionDeriveEditDoesNotRestoreClearedPivotSourceInput(t *testing.T) {
	service, store, snapshot := rs05ProposeService(t)
	durable := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05ProposeFilterStep(filterSourceIdentity, rs05PublicOutputs(true)),
			rs05ProposeDeriveStep(2),
			rs05ProposePivotStep(filterSourceIdentity, rs05OldColumn),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{rs05RootProjection(rs05OldColumn, rs05PivotID)},
	}
	durable.Steps[2].Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: rs05DeriveID}}
	rs05ProposeSaveDraft(t, store, durable)
	acceptedBefore := string(store.created.DraftConfig)
	cleared := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			rs05ProposeFilterStep(filterSourceIdentity, rs05PublicOutputs(true)),
			rs05ProposeDeriveStep(9),
			rs05ProposePivotStep(filterSourceIdentity),
		},
	}
	cleared.Steps[2].Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: rs05DeriveID}}
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
