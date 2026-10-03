package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
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
	if s.config.PreviewReceipt == nil || s.config.Capability.ForExecution == nil {
		return RowDefinitionComparison{}, unavailable("row-definition-proposal", "PREVIEW_UNAVAILABLE", "row definition preview execution is not configured", nil)
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
	if err := validateReceiptOutputContract(base, request.OutputID); err != nil {
		return RowDefinitionComparison{}, internal("row-definition-proposal", "PREVIEW_OUTPUT_INVALID", "the base proposal receipt is missing the requested output contract", err)
	}
	if err := validateReceiptOutputContract(candidate, request.OutputID); err != nil {
		return RowDefinitionComparison{}, internal("row-definition-proposal", "PREVIEW_OUTPUT_INVALID", "the candidate proposal receipt is missing the requested output contract", err)
	}
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(request.Project), SelectionProject: projectid.Canonical(request.Project),
		DatasetGeneration: snapshot.Identity.Generation, SelectionMembersCollection: s.config.SelectionMembersCollection,
		PreviewLimit: limit, OutputNames: []string{request.OutputID}, IncludeRowIdentity: true,
	}
	applyAuthorizedScope(&bindings, authorized, false)
	baseRows, err := s.previewReceiptRows(ctx, base, bindings, limit)
	if err != nil {
		if err := ctx.Err(); err != nil {
			return RowDefinitionComparison{}, err
		}
		return RowDefinitionComparison{}, internal("row-definition-proposal", "BASE_PREVIEW_FAILED", "the current row definition could not be previewed", err)
	}
	candidateRows, err := s.previewReceiptRows(ctx, candidate, bindings, limit)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return RowDefinitionComparison{}, ctxErr
		}
		if request.Selection.Kind == RowDefinitionSelectionExpanded &&
			request.Selection.Expanded.EmptyCollectionPolicy == authoringv2.EmptyCollectionError &&
			isEmptyCollectionExpansionError(err) {
			return RowDefinitionComparison{}, unprocessable(
				"row-definition-proposal", "EMPTY_COLLECTION_ERROR",
				"Some records have no values for this field. Choose \"Leave out records with no values\" or \"Keep records with no values as one empty row\", or choose another field.", err,
			)
		}
		if request.Selection.Kind == RowDefinitionSelectionExplicitGroup &&
			request.Selection.ExplicitGroup.UnassignedMemberPolicy == authoringv2.UnassignedMemberError &&
			isUnassignedExplicitGroupError(err) {
			return RowDefinitionComparison{}, unprocessable(
				"row-definition-proposal", "EXPLICIT_GROUP_UNASSIGNED_MEMBER",
				"Some records do not belong to a group. Choose \"Leave out records without a group\" or \"Put records without a group in their own group\", or assign them to a group.", err,
			)
		}
		if featureErr, ok := featureResolutionError(err); ok {
			return RowDefinitionComparison{}, unprocessable("row-definition-proposal", featureErr.Code(), dataframeerrors.PublicMessage(featureErr), err)
		}
		return RowDefinitionComparison{}, internal("row-definition-proposal", "CANDIDATE_PREVIEW_FAILED", "the candidate row definition could not be previewed", err)
	}
	return compareRowDefinitionPreviewRows(baseRows, candidateRows), nil
}

func isEmptyCollectionExpansionError(err error) bool {
	return errorChainHasCode(err, dataframeerrors.CodeConstructionExpansionEmpty)
}

func isUnassignedExplicitGroupError(err error) bool {
	return errorChainHasCode(err, dataframeerrors.CodeExplicitGroupUnassignedMember)
}

func errorChainHasCode(err error, code dataframeerrors.ErrorCode) bool {
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if typed, ok := cause.(dataframeerrors.UserError); ok && typed.Code() == string(code) {
			return true
		}
	}
	return false
}

func featureResolutionError(err error) (dataframeerrors.UserError, bool) {
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if typed, ok := cause.(dataframeerrors.UserError); ok && dataframeerrors.IsFeatureResolutionCode(typed.Code()) {
			return typed, true
		}
	}
	return nil, false
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
