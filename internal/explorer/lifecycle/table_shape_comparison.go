package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) compareTableShapeReceipts(ctx context.Context, request TableShapeProposalRequest, snapshot capability.Snapshot, base, candidate *explorer.CompilationReceipt, limit int) (TableShapeComparison, error) {
	if s.config.PreviewReceipt == nil || s.config.Capability.ForExecution == nil {
		return unavailableTableShapeComparison("PREVIEW_UNAVAILABLE", "Receipt preview execution is not configured."), nil
	}
	authorized, err := s.config.Capability.ForExecution(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil ||
		projectid.Canonical(authorized.Snapshot.Identity.Project) != projectid.Canonical(request.Project) ||
		authorized.Snapshot.Identity.Generation != snapshot.Identity.Generation ||
		authorized.Snapshot.Identity.ShapeDigest != snapshot.Identity.ShapeDigest {
		return TableShapeComparison{}, conflict("table-shape-proposal", "RECEIPT_STALE", "the proposal receipts are no longer authorized for preview", nil, err)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return TableShapeComparison{}, conflict("table-shape-proposal", "RECEIPT_STALE", "the proposal receipts are no longer authorized for preview", nil, err)
	}
	if err := validateAuthorizedReceiptExecution(base, authorized); err != nil {
		return TableShapeComparison{}, conflict("table-shape-proposal", "RECEIPT_STALE", "the base receipt is no longer authorized for preview", nil, err)
	}
	if err := validateAuthorizedReceiptExecution(candidate, authorized); err != nil {
		return TableShapeComparison{}, conflict("table-shape-proposal", "RECEIPT_STALE", "the candidate receipt is no longer authorized for preview", nil, err)
	}
	if !receiptHasOutput(base.Bundle, request.OutputID) || validateReceiptOutputContract(base, request.OutputID) != nil ||
		!receiptHasOutput(candidate.Bundle, request.OutputID) || validateReceiptOutputContract(candidate, request.OutputID) != nil {
		return unavailableTableShapeComparison("OUTPUT_UNAVAILABLE", "A proposal receipt does not contain the requested output."), nil
	}
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(request.Project), SelectionProject: projectid.Canonical(request.Project),
		DatasetGeneration: snapshot.Identity.Generation, SelectionMembersCollection: s.config.SelectionMembersCollection,
		PreviewLimit: limit, OutputNames: []string{request.OutputID}, IncludeRowIdentity: true,
	}
	applyAuthorizedScope(&bindings, authorized, false)
	baseRows, err := s.previewReceiptRows(ctx, base, bindings, limit)
	if err != nil {
		comparison := unavailableTableShapeComparison("BASE_PREVIEW_UNAVAILABLE", "The base receipt could not be previewed with stable row identities.")
		comparison.Base = tableShapePreviewSummary(baseRows.Summary)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return TableShapeComparison{}, ctxErr
		}
		return comparison, nil
	}
	candidateRows, err := s.previewReceiptRows(ctx, candidate, bindings, limit)
	if err != nil {
		comparison := unavailableTableShapeComparison("CANDIDATE_PREVIEW_UNAVAILABLE", "The candidate receipt could not be previewed with stable row identities.")
		comparison.Base = tableShapePreviewSummary(baseRows.Summary)
		comparison.Candidate = tableShapePreviewSummary(candidateRows.Summary)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return TableShapeComparison{}, ctxErr
		}
		return comparison, nil
	}
	comparison := compareTableShapePreviewRows(baseRows, candidateRows)
	if err := s.attachTableShapeCellTraceEvidence(ctx, candidate, bindings, request.OutputID, &comparison); err != nil {
		return TableShapeComparison{}, err
	}
	return comparison, nil
}

func unavailableTableShapeComparison(code, reason string) TableShapeComparison {
	return TableShapeComparison{
		Status: TableShapeComparisonUnavailable, ReasonCode: code, Reason: reason,
		ChangedColumns: []string{}, ChangedRows: []TableShapeChangedRow{}, Contributors: []TableShapeContributor{},
		EvidenceLimitations: tableShapeEvidenceLimitations(), Notices: []string{},
	}
}

const (
	maxProposalComparisonTraceColumns = 6
	maxProposalComparisonContributors = 100
)

func tableShapeEvidenceLimitations() []string {
	return []string{
		"Excluded records are not enumerated by this comparison.",
		"Information loss caused by reshape operations is not measured by this comparison.",
	}
}

func tableShapePreviewSummary(summary dataframeexecution.PreviewSummary) *TableShapePreviewSummary {
	return &TableShapePreviewSummary{RowCount: summary.RowCount, Sampled: !summary.Complete || summary.Truncated}
}

func compareTableShapePreviewRows(base, candidate proposalPreviewRows) TableShapeComparison {
	baseColumns := make(map[string]struct{})
	candidateColumns := make(map[string]struct{})
	columns := make(map[string]struct{})
	for _, column := range base.Summary.Columns {
		if !isTableShapeComparisonColumn(column) {
			continue
		}
		baseColumns[column] = struct{}{}
		columns[column] = struct{}{}
	}
	for _, column := range candidate.Summary.Columns {
		if !isTableShapeComparisonColumn(column) {
			continue
		}
		candidateColumns[column] = struct{}{}
		columns[column] = struct{}{}
	}
	for _, rows := range []map[string]map[string]any{base.Rows, candidate.Rows} {
		for _, row := range rows {
			for column := range row {
				if !isTableShapeComparisonColumn(column) {
					continue
				}
				columns[column] = struct{}{}
			}
		}
	}
	allColumns := make([]string, 0, len(columns))
	changedColumns := make(map[string]struct{})
	for column := range columns {
		allColumns = append(allColumns, column)
		_, baseHasColumn := baseColumns[column]
		_, candidateHasColumn := candidateColumns[column]
		if baseHasColumn != candidateHasColumn {
			changedColumns[column] = struct{}{}
		}
	}
	sort.Strings(allColumns)
	identities := make(map[string]struct{}, len(base.Rows)+len(candidate.Rows))
	for identity := range base.Rows {
		identities[identity] = struct{}{}
	}
	for identity := range candidate.Rows {
		identities[identity] = struct{}{}
	}
	rowIDs := make([]string, 0, len(identities))
	for identity := range identities {
		rowIDs = append(rowIDs, identity)
	}
	sort.Strings(rowIDs)
	changedRows := make([]TableShapeChangedRow, 0, min(maxProposalComparisonExamples, len(rowIDs)))
	changedRowCount := 0
	for _, rowID := range rowIDs {
		before, beforeOK := base.Rows[rowID]
		after, afterOK := candidate.Rows[rowID]
		rowChangedColumns := make([]string, 0)
		if !beforeOK || !afterOK {
			rowChangedColumns = append(rowChangedColumns, allColumns...)
		} else {
			for _, column := range allColumns {
				beforeValue, beforeHasValue := before[column]
				afterValue, afterHasValue := after[column]
				if beforeHasValue != afterHasValue || !reflect.DeepEqual(beforeValue, afterValue) {
					rowChangedColumns = append(rowChangedColumns, column)
				}
			}
		}
		if len(rowChangedColumns) == 0 {
			continue
		}
		changedRowCount++
		for _, column := range rowChangedColumns {
			changedColumns[column] = struct{}{}
		}
		if len(changedRows) < maxProposalComparisonExamples {
			changedRow := TableShapeChangedRow{
				RowIdentity: rowID, BasePresent: beforeOK, CandidatePresent: afterOK,
				ChangedColumns: rowChangedColumns, ChangedCells: make([]TableShapeChangedCell, 0, len(rowChangedColumns)),
			}
			for _, column := range rowChangedColumns {
				beforeValue := tableShapeComparisonCellValue(before, beforeOK, column)
				afterValue := tableShapeComparisonCellValue(after, afterOK, column)
				trace := TableShapeCellTraceEvidence{
					State: TableShapeCellTraceNotApplicable, Contributors: []TableShapeCellContributor{},
				}
				if afterValue.Present {
					trace.State = TableShapeCellTraceNotRequested
				}
				changedRow.ChangedCells = append(changedRow.ChangedCells, TableShapeChangedCell{
					Column: column, Before: beforeValue, After: afterValue, Trace: trace,
				})
			}
			changedRows = append(changedRows, changedRow)
		}
	}
	orderedChangedColumns := make([]string, 0, len(changedColumns))
	for column := range changedColumns {
		orderedChangedColumns = append(orderedChangedColumns, column)
	}
	sort.Strings(orderedChangedColumns)
	comparison := TableShapeComparison{
		Status: TableShapeComparisonAvailable, Base: tableShapePreviewSummary(base.Summary),
		Candidate: tableShapePreviewSummary(candidate.Summary), ChangedColumns: orderedChangedColumns,
		ChangedRowCount: changedRowCount,
		ChangedRowsSampled: changedRowCount > maxProposalComparisonExamples ||
			!base.Summary.Complete || base.Summary.Truncated || !candidate.Summary.Complete || candidate.Summary.Truncated,
		ChangedRows: changedRows, Contributors: []TableShapeContributor{},
		EvidenceLimitations: tableShapeEvidenceLimitations(), Notices: []string{},
	}
	if comparison.Base.Sampled {
		comparison.Notices = append(comparison.Notices, "Base row count reflects a bounded sample.")
	}
	if comparison.Candidate.Sampled {
		comparison.Notices = append(comparison.Notices, "Candidate row count reflects a bounded sample.")
	}
	if changedRowCount > maxProposalComparisonExamples {
		comparison.Notices = append(comparison.Notices, "Changed row examples are limited to 10.")
	}
	if comparison.ChangedRowsSampled {
		comparison.Notices = append(comparison.Notices, "Changed row examples reflect only the compared preview rows.")
	}
	return comparison
}

func isTableShapeComparisonColumn(column string) bool {
	return !strings.HasPrefix(column, "__loom_")
}

func tableShapeComparisonCellValue(row map[string]any, rowPresent bool, column string) TableShapeCellValue {
	if !rowPresent {
		return TableShapeCellValue{Present: false, Value: nil}
	}
	value, present := row[column]
	return TableShapeCellValue{Present: present, Value: value}
}

func (s *Service) attachTableShapeCellTraceEvidence(ctx context.Context, candidate *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, outputID string, comparison *TableShapeComparison) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	contributors := make(map[string]TableShapeContributor)
	sampled := comparison.ChangedRowsSampled || comparison.ChangedRowCount > len(comparison.ChangedRows)
	traceColumnLimitReached := false
	for rowIndex := range comparison.ChangedRows {
		row := &comparison.ChangedRows[rowIndex]
		traceCalls := 0
		for cellIndex := range row.ChangedCells {
			if err := ctx.Err(); err != nil {
				return err
			}
			cell := &row.ChangedCells[cellIndex]
			if !row.CandidatePresent || !cell.After.Present {
				continue
			}
			if s.config.CellTrace == nil {
				cell.Trace = TableShapeCellTraceEvidence{
					State: TableShapeCellTraceUnavailable, Contributors: []TableShapeCellContributor{},
					FailureCode: "CELL_TRACE_UNAVAILABLE",
				}
				sampled = true
				continue
			}
			if traceCalls >= maxProposalComparisonTraceColumns {
				cell.Trace = TableShapeCellTraceEvidence{
					State: TableShapeCellTraceNotRequested, Contributors: []TableShapeCellContributor{},
					Sampled: true, OmissionCode: "TABLE_SHAPE_TRACE_COLUMN_LIMIT",
				}
				sampled = true
				traceColumnLimitReached = true
				continue
			}
			traceCalls++
			trace, err := s.config.CellTrace(ctx, candidate, bindings, dataframeexecution.CellTraceRequest{
				Output: outputID, RowID: row.RowIdentity, Column: cell.Column,
				Offset: 0, Limit: compiler.MaxCellTraceContributions,
			})
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || tableShapeTraceErrorIsStale(err) {
					return err
				}
				cell.Trace = TableShapeCellTraceEvidence{
					State: TableShapeCellTraceFailed, Contributors: []TableShapeCellContributor{},
					Sampled: true, FailureCode: "CELL_TRACE_FAILED",
				}
				sampled = true
				continue
			}
			cellContributors, incompleteEvidence := tableShapeTraceContributors(trace.Contributions)
			cell.Trace = TableShapeCellTraceEvidence{
				State: TableShapeCellTraceAvailable, CellStatus: string(trace.Status),
				Contributors: cellContributors,
				Complete:     trace.Complete && !trace.HasMore && trace.OmissionCode == "" && !incompleteEvidence,
				Sampled:      trace.HasMore || !trace.Complete || trace.OmissionCode != "" || incompleteEvidence,
				OmissionCode: trace.OmissionCode,
			}
			if incompleteEvidence && cell.Trace.OmissionCode == "" {
				cell.Trace.OmissionCode = "CELL_TRACE_CONTRIBUTION_INCOMPLETE"
			}
			for _, contributor := range cellContributors {
				identity := TableShapeContributor{ResourceType: contributor.ResourceType, ResourceID: contributor.ResourceID}
				contributors[tableShapeContributorKey(identity)] = identity
			}
			if cell.Trace.Sampled {
				sampled = true
			}
		}
	}
	comparison.Contributors, comparison.ContributorsSampled = boundedTableShapeContributors(contributors, maxProposalComparisonContributors)
	globalContributorsSampled := comparison.ContributorsSampled
	comparison.ContributorsSampled = comparison.ContributorsSampled || sampled
	if traceColumnLimitReached {
		comparison.Notices = append(comparison.Notices, "Cell contributor evidence is limited to 6 changed candidate columns per example.")
	}
	if comparison.ContributorsSampled {
		comparison.Notices = append(comparison.Notices, "Contributor identities reflect only available traces from the displayed examples.")
	}
	if globalContributorsSampled {
		comparison.Notices = append(comparison.Notices, "The global contributor list is limited to 100 unique resource identities.")
	}
	return nil
}

func tableShapeTraceErrorIsStale(err error) bool {
	var lifecycleErr *Error
	return errors.As(err, &lifecycleErr) && lifecycleErr.Class == ClassConflict &&
		(lifecycleErr.Code == "RECEIPT_STALE" || lifecycleErr.Code == "STALE_AUTHORIZATION_SCOPE")
}

func tableShapeTraceContributors(values []dataframeexecution.CellTraceContribution) ([]TableShapeCellContributor, bool) {
	unique := make(map[string]TableShapeCellContributor, len(values))
	incompleteEvidence := false
	for _, value := range values {
		if strings.TrimSpace(value.ResourceType) == "" || strings.TrimSpace(value.ResourceID) == "" {
			incompleteEvidence = true
			continue
		}
		contributor := TableShapeCellContributor{ResourceType: value.ResourceType, ResourceID: value.ResourceID, Value: value.Value}
		key, err := tableShapeCellContributorKey(contributor)
		if err != nil {
			incompleteEvidence = true
			continue
		}
		unique[key] = contributor
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	contributors := make([]TableShapeCellContributor, 0, len(keys))
	for _, key := range keys {
		contributors = append(contributors, unique[key])
	}
	return contributors, incompleteEvidence
}

func tableShapeCellContributorKey(contributor TableShapeCellContributor) (string, error) {
	encodedValue, err := json.Marshal(contributor.Value)
	if err != nil {
		return "", err
	}
	return contributor.ResourceType + "\x00" + contributor.ResourceID + "\x00" + string(encodedValue), nil
}

func boundedTableShapeContributors(values map[string]TableShapeContributor, limit int) ([]TableShapeContributor, bool) {
	if limit < 0 {
		limit = 0
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sampled := len(keys) > limit
	if len(keys) > limit {
		keys = keys[:limit]
	}
	result := make([]TableShapeContributor, 0, len(keys))
	for _, key := range keys {
		result = append(result, values[key])
	}
	return result, sampled
}

func tableShapeContributorKey(contributor TableShapeContributor) string {
	return contributor.ResourceType + "\x00" + contributor.ResourceID
}
