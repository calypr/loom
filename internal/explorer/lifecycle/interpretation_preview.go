package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/projectid"
)

// PreviewInterpretationCandidate compiles the current draft and an immutable
// candidate workspace, then executes both receipts with one authorization
// binding. It deliberately has no persistence side effect other than the
// immutable compilation receipts returned by the configured compiler.
func (s *Service) PreviewInterpretationCandidate(ctx context.Context, request PreviewInterpretationCandidateRequest) (PreviewInterpretationCandidateResult, error) {
	if err := validateInterpretationCandidatePreviewRequest(request); err != nil {
		return PreviewInterpretationCandidateResult{}, malformed("interpretation-preview", err.Error(), err)
	}
	limit := request.Limit
	if limit == 0 {
		limit = dataframeexecution.DefaultPreviewLimit
	}
	if limit < 1 || limit > dataframeexecution.MaxPreviewLimit {
		return PreviewInterpretationCandidateResult{}, unprocessable("interpretation-preview", "INVALID_PREVIEW_LIMIT", "limit is outside the supported range", nil)
	}
	if s.config.Capability.Token == nil || s.config.Capability.Catalog == nil {
		return PreviewInterpretationCandidateResult{}, unavailable("interpretation-preview", "PREVIEW_UNAVAILABLE", "Explorer capability lookup is not configured", nil)
	}
	if s.config.CompileReceipt == nil || s.config.PreviewReceipt == nil {
		return PreviewInterpretationCandidateResult{}, unavailable("interpretation-preview", "PREVIEW_UNAVAILABLE", "candidate receipt preview is not configured", nil)
	}
	if s.config.Capability.ForExecution == nil {
		return PreviewInterpretationCandidateResult{}, unavailable("interpretation-preview", "PREVIEW_UNAVAILABLE", "authorized receipt execution is not configured", nil)
	}

	snapshot, err := s.config.Capability.Token(ctx, request.Project, request.SnapshotToken)
	if err != nil || snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(request.Project) {
		return PreviewInterpretationCandidateResult{}, conflict("interpretation-preview", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	owner, err := s.store.Get(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return PreviewInterpretationCandidateResult{}, err
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return PreviewInterpretationCandidateResult{}, conflict("interpretation-preview", "DRAFT_CONFLICT", "the Explorer draft changed; reload before previewing", nil, explorer.ErrDraftConflict)
	}
	workspace, err := previewWorkspace(ctx, s.store, owner)
	if err != nil {
		return PreviewInterpretationCandidateResult{}, conflict("interpretation-preview", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be previewed", nil, err)
	}

	catalog := s.catalog(snapshot, request.ExplorerID)
	candidateWorkspace, _, err := authoringv2.ApplyCommands(workspace, catalog, "interpretation-preview", []authoringv2.Command{{
		Type:                    authoringv2.CommandApplyInterpretationCandidate,
		OutputID:                request.OutputID,
		Column:                  request.Column,
		InterpretationCandidate: &authoringv2.ApplyInterpretationCandidate{CandidateReceiptID: "preview", RevisionID: request.RevisionID},
	}})
	if err != nil {
		return PreviewInterpretationCandidateResult{}, unprocessable("interpretation-preview", "INVALID_INTERPRETATION_CANDIDATE", err.Error(), err)
	}

	baseReceipt, err := s.compile(ctx, compileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: workspace, SnapshotToken: request.SnapshotToken, RequestID: "interpretation-preview-base"})
	if err != nil {
		return PreviewInterpretationCandidateResult{}, err
	}
	candidateReceipt, err := s.compile(ctx, compileRequest{Project: request.Project, ExplorerID: request.ExplorerID, Workspace: candidateWorkspace, SnapshotToken: request.SnapshotToken, RequestID: "interpretation-preview-candidate"})
	if err != nil {
		return PreviewInterpretationCandidateResult{}, err
	}
	if err := verifyPreviewReceiptIntent(baseReceipt, workspace); err != nil {
		return PreviewInterpretationCandidateResult{}, conflict("interpretation-preview", "INVALID_COMPILATION_RECEIPT", "the base receipt does not represent the current draft", nil, err)
	}
	if err := verifyPreviewReceiptIntent(candidateReceipt, candidateWorkspace); err != nil {
		return PreviewInterpretationCandidateResult{}, conflict("interpretation-preview", "INVALID_COMPILATION_RECEIPT", "the candidate receipt does not represent the proposed workspace", nil, err)
	}
	authorized, err := s.config.Capability.ForExecution(ctx, request.Project, request.SnapshotToken)
	if err != nil {
		return PreviewInterpretationCandidateResult{}, conflict("interpretation-preview", "RECEIPT_STALE", "the candidate receipt's capability snapshot is no longer authorized or retained", nil, err)
	}
	if authorized.Snapshot.Token != request.SnapshotToken || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(authorized.Snapshot.Identity.Project) != projectid.Canonical(request.Project) || authorized.Snapshot.Identity.Generation != snapshot.Identity.Generation || authorized.Snapshot.Identity.ShapeDigest != snapshot.Identity.ShapeDigest {
		return PreviewInterpretationCandidateResult{}, conflict("interpretation-preview", "RECEIPT_STALE", "the candidate receipt's capability snapshot is no longer authorized or retained", nil, nil)
	}
	if err := validateAuthorizedReceiptExecution(baseReceipt, authorized); err != nil {
		return PreviewInterpretationCandidateResult{}, conflict("interpretation-preview", "RECEIPT_STALE", "the base receipt's capability snapshot is no longer authorized or retained", nil, err)
	}
	if err := validateAuthorizedReceiptExecution(candidateReceipt, authorized); err != nil {
		return PreviewInterpretationCandidateResult{}, conflict("interpretation-preview", "RECEIPT_STALE", "the candidate receipt's capability snapshot is no longer authorized or retained", nil, err)
	}

	baseEmissions := authoredInterpretationEmissions(baseReceipt, request.OutputID, request.Column)
	candidateEmissions := authoredInterpretationEmissions(candidateReceipt, request.OutputID, request.Column)
	if len(baseEmissions) == 0 || len(candidateEmissions) == 0 {
		return PreviewInterpretationCandidateResult{}, unprocessable("interpretation-preview", "INTERPRETATION_OUTPUT_MISSING", "the target authored feature has no physical receipt emissions", nil)
	}
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(request.Project), SelectionProject: projectid.Canonical(request.Project),
		DatasetGeneration: snapshot.Identity.Generation, SelectionMembersCollection: s.config.SelectionMembersCollection,
		PreviewLimit: limit, OutputNames: []string{request.OutputID}, IncludeRowIdentity: true,
	}
	applyAuthorizedScope(&bindings, authorized, false)
	baseRows, baseSummary, err := s.previewInterpretationReceipt(ctx, baseReceipt, bindings)
	if err != nil {
		return PreviewInterpretationCandidateResult{}, err
	}
	candidateRows, candidateSummary, err := s.previewInterpretationReceipt(ctx, candidateReceipt, bindings)
	if err != nil {
		return PreviewInterpretationCandidateResult{}, err
	}

	result := compareInterpretationCandidateRows(baseRows, candidateRows, baseEmissions, candidateEmissions, request, limit)
	if !previewSummaryComplete(baseSummary) || !previewSummaryComplete(candidateSummary) {
		result.Completeness = CandidatePreviewIncomplete
	}
	return result, nil
}

func validateInterpretationCandidatePreviewRequest(request PreviewInterpretationCandidateRequest) error {
	values := map[string]string{
		"project": request.Project, "explorerId": request.ExplorerID, "snapshotToken": request.SnapshotToken,
		"expectedDraftDigest": request.ExpectedDraftDigest, "outputId": request.OutputID, "column": request.Column,
		"revisionId": request.RevisionID,
	}
	for name, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if request.ExpectedDraftVersion < 0 {
		return fmt.Errorf("expectedDraftVersion must not be negative")
	}
	return nil
}

func previewWorkspace(ctx context.Context, store *explorer.Service, owner *explorer.Explorer) (authoringv2.Workspace, error) {
	if owner == nil {
		return authoringv2.Workspace{}, fmt.Errorf("Explorer owner is required")
	}
	if len(owner.DraftConfig) != 0 {
		return authoringv2.DecodeWorkspace(owner.DraftConfig)
	}
	if owner.ActiveRevisionID != "" {
		revision, err := store.ActiveRevision(ctx, owner.Project, owner.ExplorerID)
		if err != nil {
			return authoringv2.Workspace{}, err
		}
		return authoringv2.DecodeWorkspace(revision.AuthoringBundle)
	}
	return authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: owner.Title}, Documents: []authoringv2.Document{}, Tabs: []authoringv2.Tab{}}, nil
}

type interpretationPreviewRows struct {
	ReceiptID string
	Rows      map[string]map[string]any
}

func (s *Service) previewInterpretationReceipt(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings) (interpretationPreviewRows, dataframeexecution.PreviewSummary, error) {
	rows := interpretationPreviewRows{ReceiptID: receipt.ID, Rows: make(map[string]map[string]any)}
	summary, err := s.config.PreviewReceipt(ctx, receipt, bindings, func(row map[string]any) error {
		value, ok := row["__loom_row_id"]
		if !ok || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
			return fmt.Errorf("candidate preview row is missing stable __loom_row_id")
		}
		rowID := fmt.Sprint(value)
		if _, exists := rows.Rows[rowID]; exists {
			return fmt.Errorf("candidate preview emitted duplicate stable __loom_row_id %q", rowID)
		}
		copy := make(map[string]any, len(row))
		for key, item := range row {
			// Preview rows are already transport-safe values. Keep their native
			// numeric representation instead of a JSON round trip that could
			// turn int64/uint64 values into float64.
			copy[key] = item
		}
		rows.Rows[rowID] = copy
		return nil
	})
	if err != nil {
		return interpretationPreviewRows{}, summary, err
	}
	return rows, summary, nil
}

func authoredInterpretationEmissions(receipt *explorer.CompilationReceipt, outputID, column string) []explorer.EmittedColumn {
	if receipt == nil {
		return nil
	}
	emissions := make([]explorer.EmittedColumn, 0)
	for _, emission := range receipt.EmittedColumns {
		// Preview rows expose public column names. An internal emission ID is
		// not a row key and must never leak into the comparison sample.
		if emission.OutputID != outputID || emission.PublicColumn == "" || !containsString(emission.AuthoredColumns, column) {
			continue
		}
		emissions = append(emissions, emission)
	}
	sort.SliceStable(emissions, func(i, j int) bool {
		if emissions[i].EmissionID != emissions[j].EmissionID {
			return emissions[i].EmissionID < emissions[j].EmissionID
		}
		return emissions[i].PublicColumn < emissions[j].PublicColumn
	})
	return emissions
}

func compareInterpretationCandidateRows(base, candidate interpretationPreviewRows, baseEmissions, candidateEmissions []explorer.EmittedColumn, request PreviewInterpretationCandidateRequest, limit int) PreviewInterpretationCandidateResult {
	emissions := make(map[string]struct{})
	for _, emission := range append(append([]explorer.EmittedColumn(nil), baseEmissions...), candidateEmissions...) {
		emissions[emission.PublicColumn] = struct{}{}
	}
	emissionKeys := make([]string, 0, len(emissions))
	for key := range emissions {
		emissionKeys = append(emissionKeys, key)
	}
	sort.Strings(emissionKeys)
	baseByID := emissionByID(baseEmissions)
	candidateByID := emissionByID(candidateEmissions)
	rowIDs := make(map[string]struct{}, len(base.Rows)+len(candidate.Rows))
	for rowID := range base.Rows {
		rowIDs[rowID] = struct{}{}
	}
	for rowID := range candidate.Rows {
		rowIDs[rowID] = struct{}{}
	}
	orderedRows := make([]string, 0, len(rowIDs))
	for rowID := range rowIDs {
		orderedRows = append(orderedRows, rowID)
	}
	sort.Strings(orderedRows)
	rowsTruncated := len(orderedRows) > limit
	if len(orderedRows) > limit {
		orderedRows = orderedRows[:limit]
	}

	result := PreviewInterpretationCandidateResult{
		BaseReceiptID: base.ReceiptID, CandidateReceiptID: candidate.ReceiptID,
		OutputID: request.OutputID, Column: request.Column, RevisionID: request.RevisionID,
		Completeness: CandidatePreviewComplete, Samples: make([]InterpretationCandidatePreviewSample, 0, len(orderedRows)),
	}
	if rowsTruncated {
		result.Completeness = CandidatePreviewIncomplete
	}
	for _, rowID := range orderedRows {
		beforeRow, beforeOK := base.Rows[rowID]
		afterRow, afterOK := candidate.Rows[rowID]
		before := make(map[string]any, len(emissionKeys))
		after := make(map[string]any, len(emissionKeys))
		state := CandidatePreviewUnchanged
		rowUnresolved, rowResolved, rowChanged, rowDifferent := false, false, false, false
		for _, key := range emissionKeys {
			beforeEmission := baseByID[key]
			afterEmission := candidateByID[key]
			beforeValue, beforeValueOK := previewEmissionValue(beforeRow, beforeOK, beforeEmission)
			afterValue, afterValueOK := previewEmissionValue(afterRow, afterOK, afterEmission)
			if beforeValueOK {
				before[key] = beforeValue
			}
			if afterValueOK {
				after[key] = afterValue
			}
			beforeUnresolved := !beforeValueOK || beforeValue == nil
			afterUnresolved := !afterValueOK || afterValue == nil
			if beforeUnresolved != afterUnresolved || (!beforeUnresolved && !afterUnresolved && !previewValuesEqual(beforeValue, afterValue)) {
				rowDifferent = true
			}
			if afterUnresolved {
				rowUnresolved = true
			} else if beforeUnresolved {
				rowResolved = true
			} else if !previewValuesEqual(beforeValue, afterValue) {
				rowChanged = true
			}
		}
		switch {
		case rowUnresolved:
			state = CandidatePreviewUnresolved
		case rowResolved:
			state = CandidatePreviewResolved
		case rowChanged:
			state = CandidatePreviewChanged
		}
		result.Samples = append(result.Samples, InterpretationCandidatePreviewSample{RowID: rowID, Before: before, After: after, State: state})
		result.Counts.Compared++
		if rowDifferent {
			result.Counts.Changed++
		}
		switch state {
		case CandidatePreviewResolved:
			result.Counts.Resolved++
		case CandidatePreviewUnresolved:
			result.Counts.Unresolved++
		}
	}
	return result
}

func emissionByID(values []explorer.EmittedColumn) map[string]explorer.EmittedColumn {
	result := make(map[string]explorer.EmittedColumn, len(values))
	for _, value := range values {
		if value.PublicColumn != "" {
			result[value.PublicColumn] = value
		}
	}
	return result
}

func previewEmissionValue(row map[string]any, rowOK bool, emission explorer.EmittedColumn) (any, bool) {
	if !rowOK {
		return nil, false
	}
	if value, ok := row[emission.PublicColumn]; ok {
		return value, true
	}
	return nil, false
}

func previewValuesEqual(left, right any) bool {
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftRaw) == string(rightRaw)
}

func previewSummaryComplete(summary dataframeexecution.PreviewSummary) bool {
	return summary.Complete && !summary.Truncated
}

func verifyPreviewReceiptIntent(receipt *explorer.CompilationReceipt, workspace authoringv2.Workspace) error {
	if receipt == nil {
		return fmt.Errorf("compilation receipt is required")
	}
	digest, err := workspace.Digest()
	if err != nil {
		return err
	}
	if receipt.IntentDigest != digest {
		return fmt.Errorf("receipt intent digest %q does not match workspace digest %q", receipt.IntentDigest, digest)
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
