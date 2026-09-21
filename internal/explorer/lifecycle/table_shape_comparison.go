package lifecycle

import (
	"context"
	"reflect"
	"sort"

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
	return compareTableShapePreviewRows(baseRows, candidateRows), nil
}

func unavailableTableShapeComparison(code, reason string) TableShapeComparison {
	return TableShapeComparison{
		Status: TableShapeComparisonUnavailable, ReasonCode: code, Reason: reason,
		ChangedColumns: []string{}, ChangedRowCount: 0, ChangedRows: []TableShapeChangedRow{}, Notices: []string{},
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
		baseColumns[column] = struct{}{}
		columns[column] = struct{}{}
	}
	for _, column := range candidate.Summary.Columns {
		candidateColumns[column] = struct{}{}
		columns[column] = struct{}{}
	}
	for _, rows := range []map[string]map[string]any{base.Rows, candidate.Rows} {
		for _, row := range rows {
			for column := range row {
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
			changedRows = append(changedRows, TableShapeChangedRow{
				RowIdentity: rowID, BasePresent: beforeOK, CandidatePresent: afterOK,
				ChangedColumns: rowChangedColumns,
			})
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
		ChangedRowCount: changedRowCount, ChangedRows: changedRows, Notices: []string{},
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
	return comparison
}
