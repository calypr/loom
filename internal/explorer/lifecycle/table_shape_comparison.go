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
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) compareTableShapeReceipts(ctx context.Context, request TableShapeProposalRequest, snapshot capability.Snapshot, base, candidate *explorer.CompilationReceipt, limit int) (TableShapeComparison, error) {
	if s.config.PreviewReceipt == nil || s.config.Capability.ForExecution == nil {
		comparison := unavailableTableShapeComparison("PREVIEW_UNAVAILABLE", "Receipt preview execution is not configured.")
		comparison.DeclaredInformationLoss = declaredTableShapeInformationLoss(base, candidate, request.OutputID)
		comparison.EvidenceLimitations = tableShapeEvidenceLimitations(comparison.Exclusions, comparison.DeclaredInformationLoss)
		return comparison, nil
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
		comparison := unavailableTableShapeComparison("OUTPUT_UNAVAILABLE", "A proposal receipt does not contain the requested output.")
		comparison.Exclusions = unavailableTableShapeExclusions("TABLE_SHAPE_OUTPUT_UNAVAILABLE")
		comparison.DeclaredInformationLoss = unavailableTableShapeInformationLoss()
		comparison.EvidenceLimitations = tableShapeEvidenceLimitations(comparison.Exclusions, comparison.DeclaredInformationLoss)
		return comparison, nil
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
		if err := s.attachTableShapeReceiptEvidence(ctx, base, candidate, bindings, request.OutputID, &comparison); err != nil {
			return TableShapeComparison{}, err
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
		if err := s.attachTableShapeReceiptEvidence(ctx, base, candidate, bindings, request.OutputID, &comparison); err != nil {
			return TableShapeComparison{}, err
		}
		return comparison, nil
	}
	comparison := compareTableShapePreviewRows(baseRows, candidateRows)
	if err := s.attachTableShapeCellTraceEvidence(ctx, candidate, bindings, request.OutputID, &comparison); err != nil {
		return TableShapeComparison{}, err
	}
	if err := s.attachTableShapeReceiptEvidence(ctx, base, candidate, bindings, request.OutputID, &comparison); err != nil {
		return TableShapeComparison{}, err
	}
	return comparison, nil
}

func unavailableTableShapeComparison(code, reason string) TableShapeComparison {
	return TableShapeComparison{
		Status: TableShapeComparisonUnavailable, ReasonCode: code, Reason: reason,
		ChangedColumns: []string{}, ChangedRows: []TableShapeChangedRow{}, Contributors: []TableShapeContributor{},
		Exclusions: TableShapeExclusionEvidence{
			Status: TableShapeExclusionsUnavailable, Records: []TableShapeExcludedRecord{}, Complete: false,
			FailureCode: "TABLE_SHAPE_EXCLUSION_EXECUTOR_UNAVAILABLE",
		},
		DeclaredInformationLoss: TableShapeDeclaredInformationLoss{
			Status: TableShapeInformationLossUnavailable, Items: []TableShapeInformationLoss{},
			FailureCode: "TABLE_SHAPE_INFORMATION_LOSS_UNAVAILABLE",
		},
		EvidenceLimitations: []TableShapeEvidenceLimitation{}, Notices: []string{},
	}
}

const (
	maxProposalComparisonTraceColumns = 6
	maxProposalComparisonContributors = 100
	maxProposalComparisonExclusions   = 25
)

func (s *Service) attachTableShapeReceiptEvidence(ctx context.Context, base, candidate *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, outputID string, comparison *TableShapeComparison) error {
	comparison.DeclaredInformationLoss = declaredTableShapeInformationLoss(base, candidate, outputID)
	if s.config.TableShapeExclusions == nil {
		comparison.Exclusions = unavailableTableShapeExclusions("TABLE_SHAPE_EXCLUSION_EXECUTOR_UNAVAILABLE")
		comparison.EvidenceLimitations = tableShapeEvidenceLimitations(comparison.Exclusions, comparison.DeclaredInformationLoss)
		return nil
	}
	result, err := s.config.TableShapeExclusions(ctx, candidate, bindings, dataframeexecution.TableShapeExclusionRequest{
		Output: outputID, Offset: 0, Limit: maxProposalComparisonExclusions,
	})
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if err != nil {
		if tableShapeTraceErrorIsStale(err) {
			return err
		}
		comparison.Exclusions = unavailableTableShapeExclusions("TABLE_SHAPE_EXCLUSION_EXECUTION_FAILED")
	} else {
		if len(result.Exclusions) > maxProposalComparisonExclusions &&
			(result.Status == dataframeexecution.TableShapeExclusionComplete || result.Status == dataframeexecution.TableShapeExclusionIncomplete) {
			result.Exclusions = result.Exclusions[:maxProposalComparisonExclusions]
			result.Status = dataframeexecution.TableShapeExclusionIncomplete
			result.Complete = false
			result.HasMore = true
		}
		comparison.Exclusions = tableShapeExclusionsFromExecution(candidate, outputID, result)
	}
	comparison.EvidenceLimitations = tableShapeEvidenceLimitations(comparison.Exclusions, comparison.DeclaredInformationLoss)
	return nil
}

func unavailableTableShapeExclusions(code string) TableShapeExclusionEvidence {
	return TableShapeExclusionEvidence{
		Status: TableShapeExclusionsUnavailable, Records: []TableShapeExcludedRecord{}, Complete: false,
		FailureCode: code,
	}
}

func tableShapeExclusionsFromExecution(candidate *explorer.CompilationReceipt, outputID string, result dataframeexecution.TableShapeExclusionResult) TableShapeExclusionEvidence {
	switch result.Status {
	case dataframeexecution.TableShapeExclusionComplete, dataframeexecution.TableShapeExclusionIncomplete:
		records := make([]TableShapeExcludedRecord, 0, len(result.Exclusions))
		complete := result.Status == dataframeexecution.TableShapeExclusionComplete && result.Complete && !result.HasMore
		for _, exclusion := range result.Exclusions {
			record := TableShapeExcludedRecord{
				Category:     TableShapeCategoryValue{Present: exclusion.Category.Present, Value: exclusion.Category.Value},
				CategoryType: exclusion.CategoryType, OutputRowID: exclusion.OutputRowID,
				Reason: exclusion.Reason, OmissionCode: exclusion.OmissionCode,
			}
			if exclusion.SourceIdentity != nil {
				record.SourceIdentity = &TableShapeSourceIdentity{
					ResourceType: exclusion.SourceIdentity.ResourceType,
					ResourceID:   exclusion.SourceIdentity.ResourceID,
				}
			}
			if exclusion.OmissionCode != "" {
				complete = false
			}
			records = append(records, record)
		}
		status := TableShapeExclusionsComplete
		if !complete {
			status = TableShapeExclusionsIncomplete
		}
		return TableShapeExclusionEvidence{
			Status: status, Records: records, Complete: complete, Sampled: result.HasMore,
		}
	case dataframeexecution.TableShapeExclusionUnsupported:
		if tableShapePolicyProvesNoExpectedExclusions(candidate, outputID) {
			return completeEmptyTableShapeExclusions()
		}
		return unavailableTableShapeExclusions("TABLE_SHAPE_EXCLUSION_UNSUPPORTED")
	case dataframeexecution.TableShapeExclusionNoExclusionPolicy:
		if tableShapePolicyProvesNoExpectedExclusions(candidate, outputID) {
			return completeEmptyTableShapeExclusions()
		}
		return unavailableTableShapeExclusions("TABLE_SHAPE_EXCLUSION_POLICY_UNAVAILABLE")
	default:
		return unavailableTableShapeExclusions("TABLE_SHAPE_EXCLUSION_RESULT_INVALID")
	}
}

func completeEmptyTableShapeExclusions() TableShapeExclusionEvidence {
	return TableShapeExclusionEvidence{
		Status: TableShapeExclusionsComplete, Records: []TableShapeExcludedRecord{}, Complete: true,
	}
}

func tableShapePolicyProvesNoExpectedExclusions(candidate *explorer.CompilationReceipt, outputID string) bool {
	semantics, found := tableShapeSemantics(candidate, outputID)
	if !found {
		return false
	}
	switch semantics.kind {
	case "":
		return true
	case "PIVOT":
		return semantics.hasPayload && semantics.pivotPolicy == string(recipe.PivotUnlistedCategoryError)
	case "UNPIVOT":
		return semantics.hasPayload && semantics.unpivotNullPolicy == string(recipe.UnpivotNullPreserve)
	default:
		return false
	}
}

func declaredTableShapeInformationLoss(base, candidate *explorer.CompilationReceipt, outputID string) TableShapeDeclaredInformationLoss {
	semantics, found := tableShapeSemantics(candidate, outputID)
	if !found {
		return unavailableTableShapeInformationLoss()
	}
	result := TableShapeDeclaredInformationLoss{Status: TableShapeInformationLossComplete, Items: []TableShapeInformationLoss{}}
	switch semantics.kind {
	case "":
		return result
	case "PIVOT":
		if !semantics.hasPayload {
			return unavailableTableShapeInformationLoss()
		}
		baseColumns, baseColumnsAvailable := receiptOutputColumns(base, outputID)
		candidateColumns, candidateColumnsAvailable := receiptOutputColumns(candidate, outputID)
		if !baseColumnsAvailable || !candidateColumnsAvailable {
			return unavailableTableShapeInformationLoss()
		}
		retained := make(map[string]struct{}, len(candidateColumns))
		for _, column := range candidateColumns {
			retained[column] = struct{}{}
		}
		dropped := make([]string, 0, len(baseColumns))
		for _, column := range baseColumns {
			if _, present := retained[column]; !present {
				dropped = append(dropped, column)
			}
		}
		sort.Strings(dropped)
		result.Items = append(result.Items, TableShapeInformationLoss{
			Code:            "GROUPED_PIVOT_DROPS_NON_GROUP_OUTPUT_COLUMNS",
			Label:           "Grouped pivot drops non-output base columns",
			Detail:          "The grouped pivot keeps its group keys and selected category outputs. All other base columns, including the category and value inputs, are discarded.",
			AffectedColumns: dropped,
		})
	case "UNPIVOT":
		if !semantics.hasPayload {
			return unavailableTableShapeInformationLoss()
		}
		result.Items = append(result.Items, TableShapeInformationLoss{
			Code:            "UNPIVOT_REMOVES_SELECTED_INPUT_COLUMNS",
			Label:           "Unpivot removes selected input columns",
			Detail:          "Selected source columns are removed and their values are represented by the configured key and value outputs.",
			AffectedColumns: semantics.unpivotInputs,
		})
		if semantics.unpivotNullPolicy == string(recipe.UnpivotNullDrop) {
			result.Items = append(result.Items, TableShapeInformationLoss{
				Code:            "UNPIVOT_DROP_NULL_ROWS",
				Label:           "Unpivot may discard null rows",
				Detail:          "The DROP policy discards rows when a selected input value is null or missing.",
				AffectedColumns: []string{},
			})
		}
	default:
		return unavailableTableShapeInformationLoss()
	}
	return result
}

type tableShapeReceiptSemantics struct {
	kind              string
	hasPayload        bool
	pivotPolicy       string
	unpivotNullPolicy string
	unpivotInputs     []string
}

func tableShapeSemantics(receipt *explorer.CompilationReceipt, outputID string) (tableShapeReceiptSemantics, bool) {
	if receipt == nil {
		return tableShapeReceiptSemantics{}, false
	}
	if len(receipt.NormalizedBundle) != 0 {
		workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
		if err != nil {
			return tableShapeReceiptSemantics{}, false
		}
		for _, document := range workspace.Documents {
			if document.Output.ID != outputID {
				continue
			}
			if document.TableShape == nil || document.TableShape.Reshape == nil {
				return tableShapeReceiptSemantics{}, true
			}
			reshape := document.TableShape.Reshape
			semantics := tableShapeReceiptSemantics{kind: reshape.Kind}
			switch reshape.Kind {
			case "PIVOT":
				if reshape.Pivot != nil {
					semantics.hasPayload = true
					semantics.pivotPolicy = reshape.Pivot.UnlistedCategoryPolicy
				}
			case "UNPIVOT":
				if reshape.Unpivot != nil {
					semantics.hasPayload = true
					semantics.unpivotNullPolicy = reshape.Unpivot.NullRowPolicy
					semantics.unpivotInputs = make([]string, 0, len(reshape.Unpivot.Inputs))
					for _, input := range reshape.Unpivot.Inputs {
						semantics.unpivotInputs = append(semantics.unpivotInputs, input.Column)
					}
				}
			}
			return semantics, true
		}
		return tableShapeReceiptSemantics{}, false
	}
	for _, output := range receipt.Bundle.Outputs {
		if output.Name != outputID {
			continue
		}
		if output.TableReshape == nil {
			return tableShapeReceiptSemantics{}, true
		}
		reshape := output.TableReshape
		semantics := tableShapeReceiptSemantics{hasPayload: true}
		switch reshape.Kind {
		case recipe.TableReshapeGroupedPivot:
			semantics.kind = "PIVOT"
			if reshape.GroupedPivot != nil {
				semantics.pivotPolicy = string(reshape.GroupedPivot.UnlistedCategoryPolicy)
			} else {
				semantics.hasPayload = false
			}
		case recipe.TableReshapeUnpivot:
			semantics.kind = "UNPIVOT"
			if reshape.Unpivot != nil {
				semantics.unpivotNullPolicy = string(reshape.Unpivot.NullRowPolicy)
				semantics.unpivotInputs = make([]string, 0, len(reshape.Unpivot.Inputs))
				for _, input := range reshape.Unpivot.Inputs {
					semantics.unpivotInputs = append(semantics.unpivotInputs, input.Column)
				}
			} else {
				semantics.hasPayload = false
			}
		default:
			semantics.kind = string(reshape.Kind)
		}
		return semantics, true
	}
	return tableShapeReceiptSemantics{}, false
}

func unavailableTableShapeInformationLoss() TableShapeDeclaredInformationLoss {
	return TableShapeDeclaredInformationLoss{
		Status: TableShapeInformationLossUnavailable, Items: []TableShapeInformationLoss{},
		FailureCode: "TABLE_SHAPE_OUTPUT_COLUMNS_UNAVAILABLE",
	}
}

func receiptOutputColumns(receipt *explorer.CompilationReceipt, outputID string) ([]string, bool) {
	if receipt == nil {
		return nil, false
	}
	columns := make([]string, 0)
	for _, column := range receipt.EmittedColumns {
		if column.OutputID == outputID {
			columns = append(columns, column.PublicColumn)
		}
	}
	if len(columns) == 0 {
		return nil, false
	}
	sort.Strings(columns)
	return columns, true
}

func tableShapeEvidenceLimitations(exclusions TableShapeExclusionEvidence, informationLoss TableShapeDeclaredInformationLoss) []TableShapeEvidenceLimitation {
	limitations := make([]TableShapeEvidenceLimitation, 0, 3)
	if !exclusions.Complete {
		if exclusions.FailureCode != "" {
			limitations = append(limitations, TableShapeEvidenceLimitation{
				Code: exclusions.FailureCode, Message: tableShapeExclusionFailureMessage(exclusions.FailureCode),
			})
		} else {
			if exclusions.Sampled {
				limitations = append(limitations, TableShapeEvidenceLimitation{
					Code: "TABLE_SHAPE_EXCLUSIONS_SAMPLED", Message: "Only the first bounded page of candidate exclusions is included.",
				})
			}
			omissionCodes := make(map[string]struct{})
			for _, record := range exclusions.Records {
				if record.OmissionCode == "" {
					continue
				}
				if _, exists := omissionCodes[record.OmissionCode]; exists {
					continue
				}
				omissionCodes[record.OmissionCode] = struct{}{}
				limitations = append(limitations, TableShapeEvidenceLimitation{
					Code: record.OmissionCode, Message: tableShapeExclusionOmissionMessage(record.OmissionCode),
				})
			}
			if !exclusions.Sampled && len(omissionCodes) == 0 {
				limitations = append(limitations, TableShapeEvidenceLimitation{
					Code: "TABLE_SHAPE_EXCLUSIONS_INCOMPLETE", Message: "Candidate exclusion evidence is incomplete.",
				})
			}
		}
	}
	if informationLoss.Status != TableShapeInformationLossComplete {
		limitations = append(limitations, TableShapeEvidenceLimitation{
			Code: informationLoss.FailureCode, Message: tableShapeInformationLossFailureMessage(informationLoss.FailureCode),
		})
	}
	return limitations
}

func tableShapeInformationLossFailureMessage(code string) string {
	if code == "TABLE_SHAPE_OUTPUT_COLUMNS_UNAVAILABLE" {
		return "Exact base columns removed by the grouped pivot could not be derived from receipt metadata."
	}
	return "Declared information loss could not be derived from the candidate recipe."
}

func tableShapeExclusionFailureMessage(code string) string {
	switch code {
	case "TABLE_SHAPE_EXCLUSION_EXECUTOR_UNAVAILABLE":
		return "Candidate table-shape exclusions could not be enumerated because execution is not configured."
	case "TABLE_SHAPE_OUTPUT_UNAVAILABLE":
		return "Candidate table-shape exclusions could not be enumerated because the requested output is unavailable."
	case "TABLE_SHAPE_EXCLUSION_EXECUTION_FAILED":
		return "The candidate table-shape exclusion query could not be completed."
	case "TABLE_SHAPE_EXCLUSION_UNSUPPORTED":
		return "The candidate policy may exclude records, but exact exclusion evidence is unsupported."
	case "TABLE_SHAPE_EXCLUSION_POLICY_UNAVAILABLE":
		return "The candidate policy does not prove whether excluded records are expected."
	default:
		return "Candidate table-shape exclusion evidence is unavailable."
	}
}

func tableShapeExclusionOmissionMessage(code string) string {
	if code == "TABLE_SHAPE_SOURCE_IDENTITY_UNAVAILABLE" {
		return "Some excluded records do not have an exact source identity."
	}
	return "Some excluded records contain omitted evidence."
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
		Exclusions: TableShapeExclusionEvidence{
			Status: TableShapeExclusionsUnavailable, Records: []TableShapeExcludedRecord{}, Complete: false,
			FailureCode: "TABLE_SHAPE_EXCLUSION_EXECUTOR_UNAVAILABLE",
		},
		DeclaredInformationLoss: TableShapeDeclaredInformationLoss{
			Status: TableShapeInformationLossUnavailable, Items: []TableShapeInformationLoss{},
			FailureCode: "TABLE_SHAPE_INFORMATION_LOSS_UNAVAILABLE",
		},
		EvidenceLimitations: []TableShapeEvidenceLimitation{}, Notices: []string{},
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
