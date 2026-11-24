package population

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/projectid"
)

const (
	DefaultPopulationMappingLimit = 100
	MaxPopulationMappingLimit     = 1000
)

type PopulationMappingRequest struct {
	Project    string
	ExplorerID string
	ReceiptID  string
	OutputID   string
	Cursor     string
	Limit      int
}

type PopulationMappingBinding struct {
	ReceiptID           string
	OutputID            string
	Project             string
	ExplorerID          string
	Generation          string
	ScopeDigest         string
	SelectionRevisionID string
	MembershipDigest    string
	ResourceType        string
}

type PopulationMappingCounts struct {
	Selected    int64
	Mapped      int64
	Unmapped    int64
	EmittedRows int64
}

type PopulationMappingDiagnostic struct {
	Code    string
	Message string
}

type PopulationMappingReport struct {
	Binding     PopulationMappingBinding
	Status      dataframeexecution.PopulationMappingStatus
	Counts      *PopulationMappingCounts
	Unmapped    []selection.ResourceRef
	NextCursor  string
	Diagnostics []PopulationMappingDiagnostic
}

type PopulationMappingResult struct {
	Report PopulationMappingReport
}

func (s *Service) PopulationMapping(ctx context.Context, request PopulationMappingRequest) (PopulationMappingResult, error) {
	if strings.TrimSpace(request.ReceiptID) == "" || strings.TrimSpace(request.OutputID) == "" {
		return PopulationMappingResult{}, support.Malformed("populationMapping", "receiptId and outputId are required", nil)
	}
	if s.config.Capability.ForExecution == nil || s.config.PopulationMapping == nil || s.config.PopulationMappingCursorCodec == nil {
		return PopulationMappingResult{}, support.Unavailable("populationMapping", "POPULATION_MAPPING_UNAVAILABLE", "population mapping is not configured", nil)
	}
	limit := request.Limit
	if limit <= 0 {
		limit = DefaultPopulationMappingLimit
	}
	if limit > MaxPopulationMappingLimit {
		return PopulationMappingResult{}, support.Unprocessable("populationMapping", "INVALID_POPULATION_MAPPING_LIMIT", "limit must be between 1 and 1000", nil)
	}
	var cursor *PopulationMappingCursor
	if strings.TrimSpace(request.Cursor) != "" {
		if s.config.PopulationMappingCursorCodec == nil {
			return PopulationMappingResult{}, support.Unavailable("populationMapping", "POPULATION_MAPPING_UNAVAILABLE", "population mapping cursor signing is not configured", nil)
		}
		decoded, err := s.config.PopulationMappingCursorCodec.Decode(request.Cursor)
		if errors.Is(err, ErrPopulationMappingCursorUnavailable) {
			return PopulationMappingResult{}, support.Unavailable("populationMapping", "POPULATION_MAPPING_UNAVAILABLE", "population mapping cursor signing is not configured", err)
		}
		if err != nil {
			return PopulationMappingResult{}, support.Malformed("populationMapping", "cursor is invalid", err)
		}
		cursor = &decoded
	}
	if cursor != nil && (cursor.ReceiptID != strings.TrimSpace(request.ReceiptID) || cursor.OutputID != strings.TrimSpace(request.OutputID)) {
		return PopulationMappingResult{}, support.Conflict("populationMapping", "POPULATION_MAPPING_CURSOR_STALE", "population mapping cursor is stale", nil, nil)
	}

	prepared, err := support.PrepareReceiptOutput(ctx, s.config.Core, s.config.Capability.ForExecution, support.ReceiptOutputRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, ReceiptID: request.ReceiptID, OutputID: request.OutputID, Stage: "populationMapping",
	})
	if err != nil {
		return PopulationMappingResult{}, err
	}
	receipt := prepared.Receipt
	output := populationOutput(receipt, prepared.OutputID)
	if output == nil || output.Population == nil {
		return PopulationMappingResult{}, support.Unprocessable("populationMapping", "POPULATION_NOT_ATTACHED", "the output has no selected-resource population", nil)
	}
	constraint := output.Population
	selected, err := s.config.Store.GetSelection(ctx, projectid.Canonical(receipt.Project), constraint.SelectionRevisionID)
	if err != nil {
		return PopulationMappingResult{}, support.Unavailable("populationMapping", "POPULATION_MAPPING_UNAVAILABLE", "the selected-resource membership could not be loaded", err)
	}
	if selected == nil || !selected.Complete {
		return PopulationMappingResult{}, support.Conflict("populationMapping", "SELECTION_NOT_COMPLETE", "the selected-resource membership is not complete", nil, selection.ErrSelectionIncomplete)
	}
	if err := validatePopulationMappingSelection(receipt, prepared.Capability, selected, *constraint); err != nil {
		return PopulationMappingResult{}, support.Conflict("populationMapping", "POPULATION_MAPPING_STALE", "the receipt's selected-resource identity is stale", nil, err)
	}
	if cursor != nil && !populationCursorMatches(*cursor, receipt, selected) {
		return PopulationMappingResult{}, support.Conflict("populationMapping", "POPULATION_MAPPING_CURSOR_STALE", "population mapping cursor is stale", nil, nil)
	}

	refs, ids, err := s.readPopulationMembers(ctx, selected, request.Project)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return PopulationMappingResult{Report: incompletePopulationReport(receipt, prepared.OutputID, selected)}, nil
		}
		var typed *ports.Error
		if errors.As(err, &typed) {
			return PopulationMappingResult{}, typed
		}
		return PopulationMappingResult{}, support.Unavailable("populationMapping", "POPULATION_MAPPING_UNAVAILABLE", "selection members could not be read", err)
	}
	bindings := prepared.Bindings(s.config.SelectionMembersCollection)
	after := ""
	if cursor != nil {
		after = cursor.MemberKey
	}
	executed, err := s.config.PopulationMapping(ctx, receipt, bindings, prepared.OutputID, ids, after, limit)
	if err != nil {
		if s.config.ClassifyMaterializationError != nil {
			if classified := s.config.ClassifyMaterializationError("populationMapping", err); classified != nil {
				return PopulationMappingResult{}, classified
			}
		}
		return PopulationMappingResult{}, support.Unavailable("populationMapping", "POPULATION_MAPPING_UNAVAILABLE", "population mapping execution is unavailable", err)
	}
	if executed.Status != dataframeexecution.PopulationMappingComplete {
		return PopulationMappingResult{Report: incompletePopulationReport(receipt, prepared.OutputID, selected)}, nil
	}
	result := PopulationMappingReport{
		Binding:  populationMappingBinding(receipt, prepared.OutputID, selected),
		Status:   dataframeexecution.PopulationMappingComplete,
		Counts:   &PopulationMappingCounts{Selected: executed.SelectedCount, Mapped: executed.MappedCount, Unmapped: executed.UnmappedCount, EmittedRows: executed.EmittedRows},
		Unmapped: make([]selection.ResourceRef, 0, len(executed.UnmappedMemberIDs)),
	}
	for _, id := range executed.UnmappedMemberIDs {
		ref, ok := refs[id]
		if !ok {
			return PopulationMappingResult{}, support.Internal("populationMapping", "POPULATION_MAPPING_IDENTITY", "mapping returned an unknown selected-resource identity", nil)
		}
		result.Unmapped = append(result.Unmapped, ref)
	}
	if executed.HasMoreUnmapped && len(result.Unmapped) > 0 {
		last := result.Unmapped[len(result.Unmapped)-1].ID
		next, encodeErr := s.config.PopulationMappingCursorCodec.Encode(PopulationMappingCursor{Version: 1, ReceiptID: receipt.ID, OutputID: prepared.OutputID, Project: projectid.Canonical(receipt.Project), ExplorerID: receipt.ExplorerID, Generation: selected.Generation, ScopeDigest: selected.ScopeDigest, SelectionRevisionID: selected.ID, MembershipDigest: selected.MembershipDigest, ResourceType: selected.ResourceType, MemberKey: last})
		if encodeErr != nil {
			if errors.Is(encodeErr, ErrPopulationMappingCursorUnavailable) {
				return PopulationMappingResult{}, support.Unavailable("populationMapping", "POPULATION_MAPPING_UNAVAILABLE", "population mapping cursor signing is not configured", encodeErr)
			}
			return PopulationMappingResult{}, support.Internal("populationMapping", "POPULATION_MAPPING_CURSOR", "population mapping cursor could not be created", encodeErr)
		}
		result.NextCursor = next
	}
	return PopulationMappingResult{Report: result}, nil
}

func populationOutput(receipt *receiptmodel.CompilationReceipt, outputID string) *recipe.Output {
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

func validatePopulationMappingSelection(receipt *receiptmodel.CompilationReceipt, authorized ports.AuthorizedCapability, selected *selection.SelectionRevision, constraint recipe.PopulationConstraint) error {
	if selected.ID != constraint.SelectionRevisionID || selected.MembershipDigest != constraint.MembershipDigest || selected.MemberCount != constraint.MemberCount {
		return fmt.Errorf("selection identity does not match receipt population")
	}
	if projectid.Canonical(selected.Project) != projectid.Canonical(receipt.Project) || selected.Generation != receipt.SourceGeneration || selected.ScopeDigest != receipt.AuthorizationScopeDigest {
		return fmt.Errorf("selection project, generation, or scope changed")
	}
	if selected.Generation != authorized.Snapshot.Identity.Generation || selected.ScopeDigest != authorized.Snapshot.Identity.AuthorizationScopeDigest || selected.ResourceType != constraint.ResourceType {
		return fmt.Errorf("selection authorization or resource type changed")
	}
	return nil
}

func (s *Service) readPopulationMembers(ctx context.Context, selected *selection.SelectionRevision, project string) (map[string]selection.ResourceRef, []string, error) {
	return selection.CollectMembers(ctx, s.config.Store, *selected, projectid.Canonical(project))
}

func populationMappingBinding(receipt *receiptmodel.CompilationReceipt, outputID string, selected *selection.SelectionRevision) PopulationMappingBinding {
	return PopulationMappingBinding{ReceiptID: receipt.ID, OutputID: outputID, Project: projectid.Canonical(receipt.Project), ExplorerID: receipt.ExplorerID, Generation: receipt.SourceGeneration, ScopeDigest: receipt.AuthorizationScopeDigest, SelectionRevisionID: selected.ID, MembershipDigest: selected.MembershipDigest, ResourceType: selected.ResourceType}
}

func incompletePopulationReport(receipt *receiptmodel.CompilationReceipt, outputID string, selected *selection.SelectionRevision) PopulationMappingReport {
	return PopulationMappingReport{Binding: populationMappingBinding(receipt, outputID, selected), Status: dataframeexecution.PopulationMappingIncomplete, Diagnostics: []PopulationMappingDiagnostic{{Code: "INCOMPLETE", Message: "population mapping did not finish within the request limits"}}}
}

func populationCursorMatches(cursor PopulationMappingCursor, receipt *receiptmodel.CompilationReceipt, selected *selection.SelectionRevision) bool {
	return cursor.ReceiptID == receipt.ID && cursor.OutputID != "" && cursor.Project == projectid.Canonical(receipt.Project) && cursor.ExplorerID == receipt.ExplorerID && cursor.MembershipDigest == selected.MembershipDigest && cursor.SelectionRevisionID == selected.ID && cursor.Generation == selected.Generation && cursor.ScopeDigest == selected.ScopeDigest && cursor.ResourceType == selected.ResourceType
}
