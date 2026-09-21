package lifecycle

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

const maxProposalComparisonExamples = 10

type proposalPreviewRows struct {
	Rows    map[string]map[string]any
	Summary dataframeexecution.PreviewSummary
}

type rowDefinitionPreviewRows = proposalPreviewRows

func (s *Service) compareRowDefinitionReceipts(ctx context.Context, request RowDefinitionProposalRequest, snapshot capability.Snapshot, base, candidate *explorer.CompilationReceipt, limit int) (RowDefinitionComparison, error) {
	if receiptHasUnsupportedGroupedRows(base, request.OutputID) || receiptHasUnsupportedGroupedRows(candidate, request.OutputID) {
		return unavailableRowDefinitionComparison("GROUPED_ROW_COMPILER_UNAVAILABLE", "FIELD_GROUP row-definition execution is unavailable in this workflow."), nil
	}
	if s.config.PreviewReceipt == nil || s.config.Capability.ForExecution == nil {
		return unavailableRowDefinitionComparison("PREVIEW_UNAVAILABLE", "Receipt preview execution is not configured."), nil
	}
	authorized, err := s.config.Capability.ForExecution(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil ||
		projectid.Canonical(authorized.Snapshot.Identity.Project) != projectid.Canonical(request.Project) ||
		authorized.Snapshot.Identity.Generation != snapshot.Identity.Generation || authorized.Snapshot.Identity.ShapeDigest != snapshot.Identity.ShapeDigest {
		return RowDefinitionComparison{}, conflict("row-definition-proposal", "RECEIPT_STALE", "the proposal receipts are no longer authorized for preview", nil, err)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return RowDefinitionComparison{}, conflict("row-definition-proposal", "RECEIPT_STALE", "the proposal receipts are no longer authorized for preview", nil, err)
	}
	if err := validateAuthorizedReceiptExecution(base, authorized); err != nil {
		return RowDefinitionComparison{}, conflict("row-definition-proposal", "RECEIPT_STALE", "the base receipt is no longer authorized for preview", nil, err)
	}
	if err := validateAuthorizedReceiptExecution(candidate, authorized); err != nil {
		return RowDefinitionComparison{}, conflict("row-definition-proposal", "RECEIPT_STALE", "the candidate receipt is no longer authorized for preview", nil, err)
	}
	if !receiptHasOutput(base.Bundle, request.OutputID) || validateReceiptOutputContract(base, request.OutputID) != nil ||
		!receiptHasOutput(candidate.Bundle, request.OutputID) || validateReceiptOutputContract(candidate, request.OutputID) != nil {
		return unavailableRowDefinitionComparison("OUTPUT_UNAVAILABLE", "A proposal receipt does not contain the requested output."), nil
	}
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(request.Project), SelectionProject: projectid.Canonical(request.Project),
		DatasetGeneration: snapshot.Identity.Generation, SelectionMembersCollection: s.config.SelectionMembersCollection,
		PreviewLimit: limit, OutputNames: []string{request.OutputID}, IncludeRowIdentity: true,
	}
	applyAuthorizedScope(&bindings, authorized, false)
	baseRows, err := s.previewReceiptRows(ctx, base, bindings, limit)
	if err != nil {
		comparison := unavailableRowDefinitionComparison("BASE_PREVIEW_UNAVAILABLE", "The base receipt could not be previewed with stable row identities.")
		comparison.Base = rowDefinitionPreviewSummary(baseRows.Summary)
		if err := ctx.Err(); err != nil {
			return RowDefinitionComparison{}, err
		}
		return comparison, nil
	}
	candidateRows, err := s.previewReceiptRows(ctx, candidate, bindings, limit)
	if err != nil {
		comparison := unavailableRowDefinitionComparison("CANDIDATE_PREVIEW_UNAVAILABLE", "The candidate receipt could not be previewed with stable row identities.")
		comparison.Base = rowDefinitionPreviewSummary(baseRows.Summary)
		comparison.Candidate = rowDefinitionPreviewSummary(candidateRows.Summary)
		if err := ctx.Err(); err != nil {
			return RowDefinitionComparison{}, err
		}
		return comparison, nil
	}
	return compareRowDefinitionPreviewRows(baseRows, candidateRows), nil
}

func (s *Service) previewUnavailableRowDefinitionComparison(ctx context.Context, request RowDefinitionProposalRequest, snapshot capability.Snapshot, base *explorer.CompilationReceipt, code, reason string, limit int) (RowDefinitionComparison, error) {
	comparison := unavailableRowDefinitionComparison(code, reason)
	if receiptHasUnsupportedGroupedRows(base, request.OutputID) || s.config.PreviewReceipt == nil || s.config.Capability.ForExecution == nil || base == nil {
		return comparison, nil
	}
	authorized, err := s.config.Capability.ForExecution(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil ||
		projectid.Canonical(authorized.Snapshot.Identity.Project) != projectid.Canonical(request.Project) ||
		authorized.Snapshot.Identity.Generation != snapshot.Identity.Generation ||
		validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest) != nil {
		return RowDefinitionComparison{}, conflict("row-definition-proposal", "RECEIPT_STALE", "the base receipt is no longer authorized for preview", nil, err)
	}
	if err := validateAuthorizedReceiptExecution(base, authorized); err != nil {
		return RowDefinitionComparison{}, conflict("row-definition-proposal", "RECEIPT_STALE", "the base receipt is no longer authorized for preview", nil, err)
	}
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(request.Project), SelectionProject: projectid.Canonical(request.Project),
		DatasetGeneration: snapshot.Identity.Generation, SelectionMembersCollection: s.config.SelectionMembersCollection,
		PreviewLimit: limit, OutputNames: []string{request.OutputID}, IncludeRowIdentity: true,
	}
	applyAuthorizedScope(&bindings, authorized, false)
	preview, err := s.previewReceiptRows(ctx, base, bindings, limit)
	if err == nil || preview.Summary.RowCount > 0 {
		comparison.Base = rowDefinitionPreviewSummary(preview.Summary)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return RowDefinitionComparison{}, contextErr
	}
	return comparison, nil
}

func (s *Service) previewReceiptRows(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, limit int) (proposalPreviewRows, error) {
	preview := proposalPreviewRows{Rows: make(map[string]map[string]any, limit)}
	summary, err := s.config.PreviewReceipt(ctx, receipt, bindings, func(row map[string]any) error {
		identity, ok := row["__loom_row_id"]
		if !ok || identity == nil || strings.TrimSpace(fmt.Sprint(identity)) == "" {
			return fmt.Errorf("preview row is missing a stable identity")
		}
		rowID := fmt.Sprint(identity)
		if _, exists := preview.Rows[rowID]; exists {
			return fmt.Errorf("preview emitted a duplicate stable row identity")
		}
		values := make(map[string]any, len(row)-1)
		for key, value := range row {
			if key != "__loom_row_id" {
				values[key] = value
			}
		}
		preview.Rows[rowID] = values
		return nil
	})
	preview.Summary = summary
	if err != nil {
		return preview, err
	}
	if len(preview.Rows) > limit {
		return preview, fmt.Errorf("preview exceeded the requested sample limit")
	}
	return preview, nil
}

func rowDefinitionPreviewSummary(summary dataframeexecution.PreviewSummary) *RowDefinitionPreviewSummary {
	return &RowDefinitionPreviewSummary{RowCount: summary.RowCount, Sampled: !summary.Complete || summary.Truncated}
}

func unavailableRowDefinitionComparison(code, reason string) RowDefinitionComparison {
	return RowDefinitionComparison{
		Status: RowDefinitionComparisonUnavailable, ReasonCode: code, Reason: reason,
		AffectedColumns: []string{}, Notices: []string{}, Examples: []RowDefinitionComparisonExample{},
	}
}

func compareRowDefinitionPreviewRows(base, candidate rowDefinitionPreviewRows) RowDefinitionComparison {
	columns := make(map[string]struct{})
	for _, column := range append(append([]string(nil), base.Summary.Columns...), candidate.Summary.Columns...) {
		columns[column] = struct{}{}
	}
	for _, row := range base.Rows {
		for column := range row {
			columns[column] = struct{}{}
		}
	}
	for _, row := range candidate.Rows {
		for column := range row {
			columns[column] = struct{}{}
		}
	}
	allColumns := make([]string, 0, len(columns))
	for column := range columns {
		allColumns = append(allColumns, column)
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
	changedColumns := make(map[string]struct{})
	for _, rowID := range rowIDs {
		before, beforeOK := base.Rows[rowID]
		after, afterOK := candidate.Rows[rowID]
		if !beforeOK || !afterOK {
			for _, column := range allColumns {
				changedColumns[column] = struct{}{}
			}
			continue
		}
		for _, column := range allColumns {
			beforeValue, beforeHasValue := before[column]
			afterValue, afterHasValue := after[column]
			if beforeHasValue != afterHasValue || !reflect.DeepEqual(beforeValue, afterValue) {
				changedColumns[column] = struct{}{}
			}
		}
	}
	affected := make([]string, 0, len(changedColumns))
	for column := range changedColumns {
		affected = append(affected, column)
	}
	sort.Strings(affected)
	comparison := RowDefinitionComparison{
		Status: RowDefinitionComparisonAvailable, Base: rowDefinitionPreviewSummary(base.Summary),
		Candidate: rowDefinitionPreviewSummary(candidate.Summary), AffectedColumns: affected,
		Notices: []string{}, Examples: make([]RowDefinitionComparisonExample, 0, min(maxProposalComparisonExamples, len(rowIDs))),
	}
	if comparison.Base.Sampled {
		comparison.Notices = append(comparison.Notices, "Base row count reflects a bounded sample.")
	}
	if comparison.Candidate.Sampled {
		comparison.Notices = append(comparison.Notices, "Candidate row count reflects a bounded sample.")
	}
	if len(rowIDs) > maxProposalComparisonExamples {
		comparison.Notices = append(comparison.Notices, "Example row identities are limited to 10.")
	}
	for _, rowID := range rowIDs[:min(maxProposalComparisonExamples, len(rowIDs))] {
		_, beforeOK := base.Rows[rowID]
		_, afterOK := candidate.Rows[rowID]
		comparison.Examples = append(comparison.Examples, RowDefinitionComparisonExample{
			RowIdentity: rowID, BasePresent: beforeOK, CandidatePresent: afterOK,
		})
	}
	return comparison
}

func receiptHasUnsupportedGroupedRows(receipt *explorer.CompilationReceipt, outputID string) bool {
	if receipt == nil {
		return false
	}
	workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
	if err != nil {
		return false
	}
	document := proposalDocument(workspace, outputID)
	return document != nil && document.Rows.Kind == authoringv2.RowDefinitionGroups && document.Rows.Groups != nil &&
		document.Rows.Groups.Source.Kind == authoringv2.GroupSourceField
}
