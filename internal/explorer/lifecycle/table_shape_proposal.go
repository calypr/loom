package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/tableshapecap"
)

type TableShapeProposalRequest struct {
	Project              string                 `json:"project"`
	ExplorerID           string                 `json:"explorerId"`
	SnapshotToken        string                 `json:"snapshotToken"`
	ExpectedDraftVersion int64                  `json:"expectedDraftVersion"`
	ExpectedDraftDigest  string                 `json:"expectedDraftDigest"`
	OutputID             string                 `json:"outputId"`
	Mode                 TableShapeProposalMode `json:"mode"`
	CatalogID            string                 `json:"catalogId"`
	ReshapeResolutionID  string                 `json:"reshapeResolutionId,omitempty"`
	DerivedResolutionIDs []string               `json:"derivedResolutionIds,omitempty"`
	Limit                int                    `json:"limit,omitempty"`
}

func (r TableShapeProposalRequest) Validate() error {
	if err := r.catalogRequest().Validate(); err != nil {
		return err
	}
	if err := requireExactIdentity(r.CatalogID, "catalogId"); err != nil {
		return err
	}
	switch r.Mode {
	case TableShapeProposalAdd, TableShapeProposalReplace, TableShapeProposalRemove:
	default:
		return fmt.Errorf("mode must be ADD, REPLACE, or REMOVE")
	}
	if r.ReshapeResolutionID != "" {
		if err := requireExactIdentity(r.ReshapeResolutionID, "reshapeResolutionId"); err != nil {
			return err
		}
	}
	if len(r.DerivedResolutionIDs) > maxTableShapeResolutionReferences {
		return fmt.Errorf("derivedResolutionIds exceeds the supported maximum")
	}
	seen := make(map[string]struct{}, len(r.DerivedResolutionIDs)+1)
	if r.ReshapeResolutionID != "" {
		seen[r.ReshapeResolutionID] = struct{}{}
	}
	for index, id := range r.DerivedResolutionIDs {
		if err := requireExactIdentity(id, fmt.Sprintf("derivedResolutionIds[%d]", index)); err != nil {
			return err
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("resolution IDs must be unique")
		}
		seen[id] = struct{}{}
	}
	if r.Mode == TableShapeProposalRemove && (r.ReshapeResolutionID != "" || len(r.DerivedResolutionIDs) != 0) {
		return fmt.Errorf("REMOVE does not accept resolution IDs")
	}
	if r.Limit < 0 || r.Limit > dataframeexecution.MaxPreviewLimit {
		return fmt.Errorf("limit is outside the supported range")
	}
	return nil
}

func (r TableShapeProposalRequest) catalogRequest() TableShapeCatalogRequest {
	return TableShapeCatalogRequest{
		Project: r.Project, ExplorerID: r.ExplorerID, SnapshotToken: r.SnapshotToken,
		ExpectedDraftVersion: r.ExpectedDraftVersion, ExpectedDraftDigest: r.ExpectedDraftDigest,
		OutputID: r.OutputID,
	}
}

func (r *TableShapeProposalRequest) UnmarshalJSON(data []byte) error {
	type wire TableShapeProposalRequest
	value, err := tableshapecap.DecodeStrict[wire](data)
	if err != nil {
		return err
	}
	decoded := TableShapeProposalRequest(value)
	if err := decoded.Validate(); err != nil {
		return err
	}
	*r = decoded
	return nil
}

type TableShapeProposalMode string

const (
	TableShapeProposalAdd     TableShapeProposalMode = "ADD"
	TableShapeProposalReplace TableShapeProposalMode = "REPLACE"
	TableShapeProposalRemove  TableShapeProposalMode = "REMOVE"
)

type TableShapeComparisonStatus string

const (
	TableShapeComparisonAvailable   TableShapeComparisonStatus = "AVAILABLE"
	TableShapeComparisonUnavailable TableShapeComparisonStatus = "UNAVAILABLE"
)

type TableShapeExclusionEvidenceStatus string

const (
	TableShapeExclusionsComplete    TableShapeExclusionEvidenceStatus = "COMPLETE"
	TableShapeExclusionsIncomplete  TableShapeExclusionEvidenceStatus = "INCOMPLETE"
	TableShapeExclusionsUnavailable TableShapeExclusionEvidenceStatus = "UNAVAILABLE"
)

type TableShapeInformationLossStatus string

const (
	TableShapeInformationLossComplete    TableShapeInformationLossStatus = "COMPLETE"
	TableShapeInformationLossUnavailable TableShapeInformationLossStatus = "UNAVAILABLE"
)

type TableShapeSourceIdentity struct {
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
}

type TableShapeCategoryValue struct {
	Present bool `json:"present"`
	Value   any  `json:"value"`
}

type TableShapeExcludedRecord struct {
	SourceIdentity *TableShapeSourceIdentity `json:"sourceIdentity,omitempty"`
	Category       TableShapeCategoryValue   `json:"category"`
	CategoryType   string                    `json:"categoryType"`
	OutputRowID    string                    `json:"outputRowId"`
	Reason         string                    `json:"reason"`
	OmissionCode   string                    `json:"omissionCode,omitempty"`
}

type TableShapeExclusionEvidence struct {
	Status      TableShapeExclusionEvidenceStatus `json:"status"`
	Records     []TableShapeExcludedRecord        `json:"records"`
	Complete    bool                              `json:"complete"`
	Sampled     bool                              `json:"sampled"`
	FailureCode string                            `json:"failureCode,omitempty"`
}

type TableShapeInformationLoss struct {
	Code            string   `json:"code"`
	Label           string   `json:"label"`
	Detail          string   `json:"detail"`
	AffectedColumns []string `json:"affectedColumns"`
}

type TableShapeDeclaredInformationLoss struct {
	Status      TableShapeInformationLossStatus `json:"status"`
	Items       []TableShapeInformationLoss     `json:"items"`
	FailureCode string                          `json:"failureCode,omitempty"`
}

type TableShapeEvidenceLimitation struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type TableShapePreviewSummary struct {
	RowCount int  `json:"rowCount"`
	Sampled  bool `json:"sampled"`
}

type TableShapeCellValue struct {
	Present bool `json:"present"`
	Value   any  `json:"value"`
}

type TableShapeContributor struct {
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
}

type TableShapeCellContributor struct {
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
	Value        any    `json:"value"`
}

type TableShapeCellTraceState string

const (
	TableShapeCellTraceNotApplicable TableShapeCellTraceState = "NOT_APPLICABLE"
	TableShapeCellTraceNotRequested  TableShapeCellTraceState = "NOT_REQUESTED"
	TableShapeCellTraceUnavailable   TableShapeCellTraceState = "UNAVAILABLE"
	TableShapeCellTraceFailed        TableShapeCellTraceState = "FAILED"
	TableShapeCellTraceAvailable     TableShapeCellTraceState = "AVAILABLE"
)

type TableShapeCellTraceEvidence struct {
	State        TableShapeCellTraceState    `json:"state"`
	CellStatus   string                      `json:"cellStatus,omitempty"`
	Contributors []TableShapeCellContributor `json:"contributors"`
	Complete     bool                        `json:"complete"`
	Sampled      bool                        `json:"sampled"`
	OmissionCode string                      `json:"omissionCode,omitempty"`
	FailureCode  string                      `json:"failureCode,omitempty"`
}

type TableShapeChangedCell struct {
	Column string                      `json:"column"`
	Before TableShapeCellValue         `json:"before"`
	After  TableShapeCellValue         `json:"after"`
	Trace  TableShapeCellTraceEvidence `json:"trace"`
}

type TableShapeChangedRow struct {
	RowIdentity      string                  `json:"rowIdentity"`
	BasePresent      bool                    `json:"basePresent"`
	CandidatePresent bool                    `json:"candidatePresent"`
	ChangedColumns   []string                `json:"changedColumns"`
	ChangedCells     []TableShapeChangedCell `json:"changedCells"`
}

type TableShapeComparison struct {
	Status                  TableShapeComparisonStatus        `json:"status"`
	ReasonCode              string                            `json:"reasonCode,omitempty"`
	Reason                  string                            `json:"reason,omitempty"`
	Base                    *TableShapePreviewSummary         `json:"base,omitempty"`
	Candidate               *TableShapePreviewSummary         `json:"candidate,omitempty"`
	ChangedColumns          []string                          `json:"changedColumns"`
	ChangedRowCount         int                               `json:"changedRowCount"`
	ChangedRowsSampled      bool                              `json:"changedRowsSampled"`
	ChangedRows             []TableShapeChangedRow            `json:"changedRows"`
	Contributors            []TableShapeContributor           `json:"contributors"`
	ContributorsSampled     bool                              `json:"contributorsSampled"`
	Exclusions              TableShapeExclusionEvidence       `json:"exclusions"`
	DeclaredInformationLoss TableShapeDeclaredInformationLoss `json:"declaredInformationLoss"`
	EvidenceLimitations     []TableShapeEvidenceLimitation    `json:"evidenceLimitations"`
	Notices                 []string                          `json:"notices"`
}

type TableShapeProposal struct {
	ProposalID               string                 `json:"proposalId,omitempty"`
	BaseReceiptID            string                 `json:"baseReceiptId"`
	OutputID                 string                 `json:"outputId"`
	SnapshotToken            string                 `json:"snapshotToken"`
	DraftVersion             int64                  `json:"draftVersion"`
	DraftDigest              string                 `json:"draftDigest"`
	BaseDocumentDigest       string                 `json:"baseDocumentDigest"`
	CandidateWorkspaceDigest string                 `json:"candidateWorkspaceDigest"`
	Mode                     TableShapeProposalMode `json:"mode"`
	Comparison               TableShapeComparison   `json:"comparison"`
	phaseTimings             tableShapeProposalTimings
}

type tableShapeProposalTimings struct {
	candidateCompile time.Duration
	comparison       time.Duration
	basePreview      time.Duration
	candidatePreview time.Duration
	rowDiff          time.Duration
	cellTrace        time.Duration
	receiptEvidence  time.Duration
}

// ServerTiming returns the proposal work breakdown for the HTTP response.
// These measurements are deliberately excluded from the proposal JSON body.
func (p TableShapeProposal) ServerTiming() string {
	metrics := []struct {
		name     string
		duration time.Duration
	}{
		{name: "candidate-compile", duration: p.phaseTimings.candidateCompile},
		{name: "base-preview", duration: p.phaseTimings.basePreview},
		{name: "candidate-preview", duration: p.phaseTimings.candidatePreview},
		{name: "row-diff", duration: p.phaseTimings.rowDiff},
		{name: "cell-trace", duration: p.phaseTimings.cellTrace},
		{name: "receipt-evidence", duration: p.phaseTimings.receiptEvidence},
		{name: "comparison", duration: p.phaseTimings.comparison},
	}
	parts := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		if metric.duration <= 0 {
			continue
		}
		parts = append(parts, metric.name+";dur="+strconv.FormatFloat(float64(metric.duration)/float64(time.Millisecond), 'f', 3, 64))
	}
	return strings.Join(parts, ", ")
}

func (s *Service) ProposeTableShape(ctx context.Context, request TableShapeProposalRequest) (TableShapeProposal, error) {
	if err := request.Validate(); err != nil {
		return TableShapeProposal{}, malformed("table-shape-proposal", err.Error(), err)
	}
	if s.config.TableShapeCapabilities == nil || s.config.Capability.Catalog == nil || s.config.CompileReceipt == nil {
		return TableShapeProposal{}, unavailable("table-shape-proposal", "PROPOSAL_UNAVAILABLE", "table shape proposal compilation is not configured", nil)
	}
	if s.config.PreviewReceipt == nil || s.config.Capability.ForExecution == nil {
		return TableShapeProposal{}, unavailable("table-shape-proposal", "PREVIEW_UNAVAILABLE", "table shape preview execution is not configured", nil)
	}
	base, catalog, err := s.tableShapeCatalogForRequest(ctx, request.catalogRequest(), request.CatalogID)
	if err != nil {
		return TableShapeProposal{}, err
	}
	if base.finalReceipt == nil {
		return TableShapeProposal{}, unavailable("table-shape-proposal", "PROPOSAL_UNAVAILABLE", "the current table compilation receipt is unavailable", nil)
	}
	if err := tableShapeProposalModeMatches(request.Mode, base.document.TableShape); err != nil {
		return TableShapeProposal{}, unprocessable("table-shape-proposal", "INVALID_TABLE_SHAPE", err.Error(), err)
	}
	shape, err := s.composeTableShapeProposal(ctx, request, catalog)
	if err != nil {
		var lifecycleErr *Error
		if errors.As(err, &lifecycleErr) {
			return TableShapeProposal{}, err
		}
		return TableShapeProposal{}, unprocessable("table-shape-proposal", "INVALID_TABLE_SHAPE", err.Error(), err)
	}
	mode, err := tableShapeProposalMode(base.document.TableShape, shape)
	if err != nil {
		return TableShapeProposal{}, unprocessable("table-shape-proposal", "INVALID_TABLE_SHAPE", err.Error(), err)
	}
	if mode != request.Mode {
		return TableShapeProposal{}, unprocessable("table-shape-proposal", "INVALID_TABLE_SHAPE", "the selected mode does not match the reconstructed table shape", nil)
	}
	baseDocumentDigest, err := documentDigest(base.document)
	if err != nil {
		return TableShapeProposal{}, err
	}
	command := authoringv2.Command{Type: authoringv2.CommandApplyTableShapeProposal, OutputID: request.OutputID, ProposalID: "proposal-preview"}
	if err := command.ResolveTableShapeProposal(shape); err != nil {
		return TableShapeProposal{}, unprocessable("table-shape-proposal", "INVALID_TABLE_SHAPE", err.Error(), err)
	}
	candidateWorkspace, _, err := authoringv2.ApplyCommands(base.workspace, s.config.Capability.Catalog(base.snapshot, request.ExplorerID), "table-shape-proposal-preview", []authoringv2.Command{command})
	if err != nil {
		return TableShapeProposal{}, unprocessable("table-shape-proposal", "INVALID_TABLE_SHAPE", err.Error(), err)
	}
	comparisonBase, err := authoringv2.PrepareWorkspaceForCompilation(base.workspace, s.catalog(base.snapshot, request.ExplorerID))
	if err != nil {
		return TableShapeProposal{}, unprocessable("table-shape-proposal", "INVALID_TABLE_SHAPE", err.Error(), err)
	}
	if _, err := tableShapeWorkspaceChange(comparisonBase, candidateWorkspace, request.OutputID); err != nil {
		return TableShapeProposal{}, unprocessable("table-shape-proposal", "INVALID_TABLE_SHAPE", err.Error(), err)
	}
	candidateDigest, err := candidateWorkspace.Digest()
	if err != nil {
		return TableShapeProposal{}, err
	}
	limit := request.Limit
	if limit == 0 {
		limit = dataframeexecution.DefaultPreviewLimit
	}
	binding := &explorer.TableShapeProposalBinding{
		DraftVersion: base.owner.DraftVersion, DraftDigest: base.owner.DraftDigest, OutputID: request.OutputID,
		BaseDocumentDigest: baseDocumentDigest, CandidateWorkspaceDigest: candidateDigest,
		SnapshotToken: request.SnapshotToken,
	}
	compileStarted := time.Now()
	candidateReceipt, err := s.compile(ctx, compileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidateWorkspace,
		SnapshotToken: request.SnapshotToken, RequestID: "table-shape-proposal-candidate",
		TableShapeProposal: binding,
	})
	if err != nil {
		return TableShapeProposal{}, err
	}
	if _, err := s.verifyProposalReceipt(ctx, "table-shape-proposal", candidateReceipt, request.Project, request.ExplorerID, request.SnapshotToken, base.snapshot, &candidateWorkspace); err != nil {
		return TableShapeProposal{}, err
	}
	phaseTimings := tableShapeProposalTimings{candidateCompile: time.Since(compileStarted)}
	comparisonStarted := time.Now()
	comparison, err := s.compareTableShapeReceipts(ctx, request, base.snapshot, base.finalReceipt, candidateReceipt, limit, &phaseTimings)
	if err != nil {
		return TableShapeProposal{}, err
	}
	phaseTimings.comparison = time.Since(comparisonStarted)
	return TableShapeProposal{
		ProposalID: candidateReceipt.ID, BaseReceiptID: base.finalReceipt.ID, OutputID: request.OutputID,
		SnapshotToken: request.SnapshotToken, DraftVersion: base.owner.DraftVersion, DraftDigest: base.owner.DraftDigest,
		BaseDocumentDigest: baseDocumentDigest, CandidateWorkspaceDigest: candidateDigest,
		Mode: mode, Comparison: comparison, phaseTimings: phaseTimings,
	}, nil
}

func tableShapeProposalModeMatches(mode TableShapeProposalMode, saved *authoringv2.TableShape) error {
	switch mode {
	case TableShapeProposalAdd:
		if saved != nil {
			return fmt.Errorf("ADD requires a table without a saved shape")
		}
	case TableShapeProposalReplace, TableShapeProposalRemove:
		if saved == nil {
			return fmt.Errorf("%s requires a saved table shape", mode)
		}
	default:
		return fmt.Errorf("mode must be ADD, REPLACE, or REMOVE")
	}
	return nil
}

func tableShapeProposalMode(base, candidate *authoringv2.TableShape) (TableShapeProposalMode, error) {
	if sameTableShapeDefinition(base, candidate) {
		return "", fmt.Errorf("candidate table shape does not change the saved table")
	}
	if candidate == nil {
		return TableShapeProposalRemove, nil
	}
	if base == nil {
		return TableShapeProposalAdd, nil
	}
	return TableShapeProposalReplace, nil
}

func sameTableShapeDefinition(left, right *authoringv2.TableShape) bool {
	return reflect.DeepEqual(tableShapeWithoutConstructionIDs(left), tableShapeWithoutConstructionIDs(right))
}

func tableShapeWithoutConstructionIDs(shape *authoringv2.TableShape) *authoringv2.TableShape {
	if shape == nil {
		return nil
	}
	copy := *shape
	copy.Derived = append([]authoringv2.DerivedConstruction(nil), shape.Derived...)
	for index := range copy.Derived {
		copy.Derived[index].ConstructionID = ""
	}
	if shape.Reshape == nil {
		return &copy
	}
	reshape := *shape.Reshape
	copy.Reshape = &reshape
	if shape.Reshape.Pivot != nil {
		pivot := *shape.Reshape.Pivot
		pivot.ConstructionID = ""
		pivot.GroupKeys = append([]string(nil), pivot.GroupKeys...)
		pivot.Categories = append([]authoringv2.PivotCategory(nil), pivot.Categories...)
		reshape.Pivot = &pivot
	}
	if shape.Reshape.Unpivot != nil {
		unpivot := *shape.Reshape.Unpivot
		unpivot.ConstructionID = ""
		unpivot.Inputs = append([]authoringv2.UnpivotInput(nil), unpivot.Inputs...)
		reshape.Unpivot = &unpivot
	}
	return &copy
}

func (s *Service) prepareTableShapeProposal(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, snapshot capability.Snapshot, current authoringv2.Workspace, commands []authoringv2.Command) ([]authoringv2.Command, *explorer.CompilationReceipt, error) {
	if len(commands) != 1 || commands[0].Type != authoringv2.CommandApplyTableShapeProposal {
		return nil, nil, malformed("commands", "APPLY_TABLE_SHAPE_PROPOSAL must be the only command in its atomic request", nil)
	}
	command := &commands[0]
	receipt, err := s.lookupReceipt(ctx, project, explorerID, command.ProposalID)
	if err != nil {
		return nil, nil, err
	}
	if receipt == nil || receipt.ID != command.ProposalID {
		return nil, nil, conflict("commands", "INVALID_TABLE_SHAPE_PROPOSAL", "the proposal ID does not match a candidate receipt", nil, nil)
	}
	binding := receipt.TableShapeProposal
	if binding == nil || binding.DraftVersion != request.ExpectedDraftVersion || binding.DraftDigest != request.ExpectedDraftDigest ||
		binding.OutputID != command.OutputID || binding.SnapshotToken != request.SnapshotToken {
		return nil, nil, conflict("commands", "STALE_TABLE_SHAPE_PROPOSAL", "the proposal is not bound to this exact draft, output, and snapshot", nil, nil)
	}
	currentDigest, err := current.Digest()
	if err != nil {
		return nil, nil, err
	}
	if currentDigest != binding.DraftDigest {
		return nil, nil, conflict("commands", "STALE_TABLE_SHAPE_PROPOSAL", "the saved draft no longer matches the proposal base", nil, nil)
	}
	baseDocument := proposalDocument(current, command.OutputID)
	if baseDocument == nil {
		return nil, nil, conflict("commands", "STALE_TABLE_SHAPE_PROPOSAL", "the proposal output is missing from the saved draft", nil, nil)
	}
	baseDocumentDigest, err := documentDigest(*baseDocument)
	if err != nil {
		return nil, nil, err
	}
	if baseDocumentDigest != binding.BaseDocumentDigest {
		return nil, nil, conflict("commands", "STALE_TABLE_SHAPE_PROPOSAL", "the target table changed after the proposal was compiled", nil, nil)
	}
	candidate, err := s.verifyProposalReceipt(ctx, "table-shape-proposal", receipt, project, explorerID, request.SnapshotToken, snapshot, nil)
	if err != nil {
		return nil, nil, err
	}
	candidateDocument := proposalDocument(candidate, command.OutputID)
	if candidateDocument == nil {
		return nil, nil, conflict("commands", "INVALID_TABLE_SHAPE_PROPOSAL", "the candidate receipt does not contain the requested output", nil, nil)
	}
	comparisonBase, err := authoringv2.PrepareWorkspaceForCompilation(current, s.catalog(snapshot, explorerID))
	if err != nil {
		return nil, nil, conflict("commands", "STALE_TABLE_SHAPE_PROPOSAL", "the saved draft can no longer be normalized for the proposal", nil, err)
	}
	if _, err := tableShapeWorkspaceChange(comparisonBase, candidate, command.OutputID); err != nil {
		return nil, nil, conflict("commands", "STALE_TABLE_SHAPE_PROPOSAL", "the candidate receipt no longer changes only the requested table shape", nil, err)
	}
	if err := command.ResolveTableShapeProposal(candidateDocument.TableShape); err != nil {
		return nil, nil, conflict("commands", "INVALID_TABLE_SHAPE_PROPOSAL", "the candidate table shape is invalid", nil, err)
	}
	return commands, receipt, nil
}

func checkTableShapeProposalResult(workspace authoringv2.Workspace, receipt *explorer.CompilationReceipt) error {
	if receipt == nil {
		return conflict("commands", "INVALID_TABLE_SHAPE_PROPOSAL", "the candidate receipt was not prepared", nil, nil)
	}
	digest, err := workspace.Digest()
	if err != nil {
		return fmt.Errorf("digest applied table-shape workspace: %w", err)
	}
	if digest != receipt.IntentDigest {
		return conflict("commands", "TABLE_SHAPE_PROPOSAL_MISMATCH", "the applied table shape does not match the candidate receipt workspace", nil, nil)
	}
	return nil
}

func tableShapeWorkspaceChange(base, candidate authoringv2.Workspace, outputID string) (authoringv2.Document, error) {
	baseWithoutShape := base
	candidateWithoutShape := candidate
	baseWithoutShape.Documents = append([]authoringv2.Document(nil), base.Documents...)
	candidateWithoutShape.Documents = append([]authoringv2.Document(nil), candidate.Documents...)
	baseDocument, candidateDocument := -1, -1
	for index := range base.Documents {
		if base.Documents[index].Output.ID == outputID {
			if baseDocument >= 0 {
				return authoringv2.Document{}, fmt.Errorf("output %q is duplicated in the base workspace", outputID)
			}
			baseDocument = index
		}
	}
	for index := range candidate.Documents {
		if candidate.Documents[index].Output.ID == outputID {
			if candidateDocument >= 0 {
				return authoringv2.Document{}, fmt.Errorf("output %q is duplicated in the candidate workspace", outputID)
			}
			candidateDocument = index
		}
	}
	if baseDocument < 0 || candidateDocument < 0 || baseDocument != candidateDocument {
		return authoringv2.Document{}, fmt.Errorf("output %q is missing or moved in the candidate workspace", outputID)
	}
	if reflect.DeepEqual(base.Documents[baseDocument].TableShape, candidate.Documents[candidateDocument].TableShape) {
		return authoringv2.Document{}, fmt.Errorf("candidate table shape does not change output %q", outputID)
	}
	baseWithoutShape.Documents[baseDocument].TableShape = nil
	candidateWithoutShape.Documents[candidateDocument].TableShape = nil
	if !reflect.DeepEqual(baseWithoutShape, candidateWithoutShape) {
		return authoringv2.Document{}, fmt.Errorf("candidate changes authoring fields outside Document.TableShape")
	}
	return candidate.Documents[candidateDocument], nil
}
