package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
)

const (
	DefaultPopulationMappingLimit = 100
	MaxPopulationMappingLimit     = 1000
)

// PopulationMapping performs one synchronous, receipt-bound coverage check.
// It reruns the request-scoped mapping query for later pages; no report state
// is persisted on a receipt, selection, or workspace.
func (s *Service) PopulationMapping(ctx context.Context, request PopulationMappingRequest) (PopulationMappingResult, error) {
	if strings.TrimSpace(request.ReceiptID) == "" || strings.TrimSpace(request.OutputID) == "" {
		return PopulationMappingResult{}, malformed("populationMapping", "receiptId and outputId are required", nil)
	}
	if s.config.Capability.ForExecution == nil || s.config.PopulationMapping == nil || s.config.PopulationMappingCursorCodec == nil {
		return PopulationMappingResult{}, unavailable("populationMapping", "POPULATION_MAPPING_UNAVAILABLE", "population mapping is not configured", nil)
	}
	limit := request.Limit
	if limit <= 0 {
		limit = DefaultPopulationMappingLimit
	}
	if limit > MaxPopulationMappingLimit {
		return PopulationMappingResult{}, unprocessable("populationMapping", "INVALID_POPULATION_MAPPING_LIMIT", "limit must be between 1 and 1000", nil)
	}
	var cursor *PopulationMappingCursor
	if strings.TrimSpace(request.Cursor) != "" {
		if s.config.PopulationMappingCursorCodec == nil {
			return PopulationMappingResult{}, unavailable("populationMapping", "POPULATION_MAPPING_UNAVAILABLE", "population mapping cursor signing is not configured", nil)
		}
		decoded, err := s.config.PopulationMappingCursorCodec.Decode(request.Cursor)
		if err != nil {
			return PopulationMappingResult{}, malformed("populationMapping", "cursor is invalid", err)
		}
		cursor = &decoded
	}
	if cursor != nil && (cursor.ReceiptID != strings.TrimSpace(request.ReceiptID) || cursor.OutputID != strings.TrimSpace(request.OutputID)) {
		return PopulationMappingResult{}, conflict("populationMapping", "POPULATION_MAPPING_CURSOR_STALE", "population mapping cursor is stale", nil, nil)
	}

	receipt, err := s.lookupReceipt(ctx, request.Project, request.ExplorerID, request.ReceiptID)
	if err != nil {
		return PopulationMappingResult{}, err
	}
	if err := s.validateReceiptRoute(receipt, request.Project, request.ExplorerID); err != nil {
		return PopulationMappingResult{}, err
	}
	authorized, err := s.config.Capability.ForExecution(ctx, receipt.Project, receipt.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(receipt.SnapshotToken) != nil || strings.TrimSpace(authorized.Snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {
		return PopulationMappingResult{}, conflict("populationMapping", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, err)
	}
	if err := validateAuthorizedReceiptExecution(receipt, authorized); err != nil {
		return PopulationMappingResult{}, conflict("populationMapping", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, err)
	}
	if !receiptHasOutput(receipt.Bundle, request.OutputID) || validateReceiptOutputContract(receipt, request.OutputID) != nil {
		return PopulationMappingResult{}, unprocessable("populationMapping", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil)
	}
	output := populationOutput(receipt, request.OutputID)
	if output == nil || output.Population == nil {
		return PopulationMappingResult{}, unprocessable("populationMapping", "POPULATION_NOT_ATTACHED", "the output has no selected-resource population", nil)
	}
	population := output.Population
	selection, err := s.store.GetSelection(ctx, projectid.Canonical(receipt.Project), population.SelectionRevisionID)
	if err != nil {
		return PopulationMappingResult{}, err
	}
	if selection == nil || !selection.Complete {
		return PopulationMappingResult{}, conflict("populationMapping", "SELECTION_NOT_COMPLETE", "the selected-resource membership is not complete", nil, explorer.ErrSelectionIncomplete)
	}
	if err := validatePopulationMappingSelection(receipt, authorized, selection, *population); err != nil {
		return PopulationMappingResult{}, conflict("populationMapping", "POPULATION_MAPPING_STALE", "the receipt's selected-resource identity is stale", nil, err)
	}
	if cursor != nil && !populationCursorMatches(*cursor, receipt, selection) {
		return PopulationMappingResult{}, conflict("populationMapping", "POPULATION_MAPPING_CURSOR_STALE", "population mapping cursor is stale", nil, nil)
	}

	refs, ids, err := s.readPopulationMembers(ctx, selection, request.Project)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return PopulationMappingResult{Report: incompletePopulationReport(receipt, request.OutputID, selection)}, nil
		}
		return PopulationMappingResult{}, err
	}
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(receipt.Project), SelectionProject: projectid.Canonical(receipt.Project),
		DatasetGeneration: receipt.SourceGeneration, SelectionMembersCollection: s.config.SelectionMembersCollection,
	}
	applyAuthorizedScope(&bindings, authorized, false)
	after := ""
	if cursor != nil {
		after = cursor.MemberKey
	}
	executed, err := s.config.PopulationMapping(ctx, receipt, bindings, request.OutputID, ids, after, limit)
	if err != nil {
		return PopulationMappingResult{}, err
	}
	if executed.Status != dataframeexecution.PopulationMappingComplete {
		return PopulationMappingResult{Report: incompletePopulationReport(receipt, request.OutputID, selection)}, nil
	}
	result := PopulationMappingReport{
		Binding:  populationMappingBinding(receipt, request.OutputID, selection),
		Status:   dataframeexecution.PopulationMappingComplete,
		Counts:   &PopulationMappingCounts{Selected: executed.SelectedCount, Mapped: executed.MappedCount, Unmapped: executed.UnmappedCount, EmittedRows: executed.EmittedRows},
		Unmapped: make([]explorer.ResourceRef, 0, len(executed.UnmappedMemberIDs)),
	}
	for _, id := range executed.UnmappedMemberIDs {
		ref, ok := refs[id]
		if !ok {
			return PopulationMappingResult{}, internal("populationMapping", "POPULATION_MAPPING_IDENTITY", "mapping returned an unknown selected-resource identity", nil)
		}
		result.Unmapped = append(result.Unmapped, ref)
	}
	if executed.HasMoreUnmapped && len(result.Unmapped) > 0 {
		last := result.Unmapped[len(result.Unmapped)-1].ID
		next, encodeErr := s.config.PopulationMappingCursorCodec.Encode(PopulationMappingCursor{Version: 1, ReceiptID: receipt.ID, OutputID: request.OutputID, Project: projectid.Canonical(receipt.Project), ExplorerID: receipt.ExplorerID, Generation: selection.Generation, ScopeDigest: selection.ScopeDigest, SelectionRevisionID: selection.ID, MembershipDigest: selection.MembershipDigest, ResourceType: selection.ResourceType, MemberKey: last})
		if encodeErr != nil {
			return PopulationMappingResult{}, internal("populationMapping", "POPULATION_MAPPING_CURSOR", "population mapping cursor could not be created", encodeErr)
		}
		result.NextCursor = next
	}
	return PopulationMappingResult{Report: result}, nil
}

func populationOutput(receipt *explorer.CompilationReceipt, outputID string) *recipe.Output {
	if receipt == nil {
		return nil
	}
	for index := range receipt.Bundle.Outputs {
		if receipt.Bundle.Outputs[index].Name == outputID {
			return &receipt.Bundle.Outputs[index]
		}
	}
	return nil
}

func validatePopulationMappingSelection(receipt *explorer.CompilationReceipt, authorized AuthorizedCapability, selection *explorer.SelectionRevision, population recipe.PopulationConstraint) error {
	if selection.ID != population.SelectionRevisionID || selection.MembershipDigest != population.MembershipDigest || selection.MemberCount != population.MemberCount {
		return fmt.Errorf("selection identity does not match receipt population")
	}
	if projectid.Canonical(selection.Project) != projectid.Canonical(receipt.Project) || selection.Generation != receipt.SourceGeneration || selection.ScopeDigest != receipt.AuthorizationScopeDigest {
		return fmt.Errorf("selection project, generation, or scope changed")
	}
	if selection.Generation != authorized.Snapshot.Identity.Generation || selection.ScopeDigest != authorized.Snapshot.Identity.AuthorizationScopeDigest || selection.ResourceType != population.ResourceType {
		return fmt.Errorf("selection authorization or resource type changed")
	}
	return nil
}

func (s *Service) readPopulationMembers(ctx context.Context, selection *explorer.SelectionRevision, project string) (map[string]explorer.ResourceRef, []string, error) {
	refs := make(map[string]explorer.ResourceRef, selection.MemberCount)
	after := ""
	for {
		pageCount := 0
		next, err := s.store.VisitSelectionMembers(ctx, projectid.Canonical(project), selection.ID, after, selectionBatchSize, func(member explorer.SelectionMember) error {
			member = member.Canonical()
			if err := member.Validate(selection.Project, selection.Generation, selection.ResourceType); err != nil {
				return err
			}
			pageCount++
			id := member.Ref.ID
			if prior, exists := refs[id]; exists && prior != member.Ref {
				return fmt.Errorf("selection contains conflicting resource identities")
			}
			refs[id] = member.Ref
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
		if pageCount == 0 || pageCount < selectionBatchSize || next == after {
			break
		}
		after = next
	}
	if int64(len(refs)) != selection.MemberCount {
		return nil, nil, fmt.Errorf("selection member count changed")
	}
	ids := make([]string, 0, len(refs))
	for id := range refs {
		ids = append(ids, id)
	}
	return refs, ids, nil
}

func populationMappingBinding(receipt *explorer.CompilationReceipt, outputID string, selection *explorer.SelectionRevision) PopulationMappingBinding {
	return PopulationMappingBinding{ReceiptID: receipt.ID, OutputID: outputID, Project: projectid.Canonical(receipt.Project), ExplorerID: receipt.ExplorerID, Generation: receipt.SourceGeneration, ScopeDigest: receipt.AuthorizationScopeDigest, SelectionRevisionID: selection.ID, MembershipDigest: selection.MembershipDigest, ResourceType: selection.ResourceType}
}

func incompletePopulationReport(receipt *explorer.CompilationReceipt, outputID string, selection *explorer.SelectionRevision) PopulationMappingReport {
	return PopulationMappingReport{Binding: populationMappingBinding(receipt, outputID, selection), Status: dataframeexecution.PopulationMappingIncomplete, Diagnostics: []PopulationMappingDiagnostic{{Code: "INCOMPLETE", Message: "population mapping did not finish within the request limits"}}}
}

func populationCursorMatches(cursor PopulationMappingCursor, receipt *explorer.CompilationReceipt, selection *explorer.SelectionRevision) bool {
	return cursor.ReceiptID == receipt.ID && cursor.OutputID != "" && cursor.Project == projectid.Canonical(receipt.Project) && cursor.ExplorerID == receipt.ExplorerID && cursor.MembershipDigest == selection.MembershipDigest && cursor.SelectionRevisionID == selection.ID && cursor.Generation == selection.Generation && cursor.ScopeDigest == selection.ScopeDigest && cursor.ResourceType == selection.ResourceType
}
