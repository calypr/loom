package authoringv2

import (
	"fmt"
	"strings"

	semanticauthoring "github.com/calypr/loom/internal/explorer/semanticauthoring"
	"github.com/calypr/loom/internal/semantic"
)

func (s SemanticSelection) validate() error {
	if strings.TrimSpace(s.ConceptID) == "" || strings.TrimSpace(s.BindingID) == "" {
		return fmt.Errorf("conceptId and bindingId are required")
	}
	if s.RouteEdgeIDs == nil {
		return fmt.Errorf("routeEdgeIds must be explicit, including an empty root route")
	}
	if !contains([]string{"VALUE", "INDEXED", "FIRST", "ALL", "DISTINCT"}, strings.ToUpper(strings.TrimSpace(s.ProjectionMode))) {
		return fmt.Errorf("projectionMode must be an explicit supported value policy")
	}
	return nil
}

func applySemanticSelections(workspace *Workspace, catalogSnapshot CatalogSnapshot, commandID string, commandIndex int, command Command) (CommandResult, error) {
	documentIndex := documentIndex(workspace, command.OutputID)
	if documentIndex < 0 {
		return CommandResult{}, fmt.Errorf("output %q was not found", command.OutputID)
	}
	document := &workspace.Documents[documentIndex]
	result := CommandResult{Type: CommandResultSemanticSelectionsAdded, OutputID: command.OutputID, SemanticSelections: make([]SemanticSelectionResult, 0, len(command.SemanticSelections()))}
	for selectionIndex, selection := range command.SemanticSelections() {
		entry := selection.ResolvedObservation
		if entry == nil || entry.ConceptID != selection.ConceptID || entry.BindingID != selection.BindingID {
			return CommandResult{}, fmt.Errorf("semantic selection %d has no matching server-resolved inventory row", selectionIndex)
		}
		observation := entry.Observation
		plan := semanticauthoring.ResolveSemanticSelectionPlan(observation)
		if !plan.Readiness.Addable() {
			return CommandResult{}, fmt.Errorf("semantic selection %d: %s: %s", selectionIndex, plan.Readiness.Code, plan.Readiness.Message)
		}
		if plan.Source == nil {
			return CommandResult{}, fmt.Errorf("semantic selection %d: %s: %s", selectionIndex, "SELECTION_PLAN_INCOMPLETE", "the selected semantic field has no complete compiler source")
		}
		occurrenceID, err := semanticauthoring.EnsureSemanticRoute(document, catalogSnapshot, commandID, commandIndex, selectionIndex, selection.RouteEdgeIDs, observation.Source.Type)
		if err != nil {
			return CommandResult{}, fmt.Errorf("semantic selection %d route: %w", selectionIndex, err)
		}
		source := plan.Source.Normalized()
		source.Lookup.ProjectionMode = strings.ToUpper(strings.TrimSpace(selection.ProjectionMode))
		if err := source.ValidateSource("semantic selection source"); err != nil {
			return CommandResult{}, err
		}
		if err := validateEditableSourcePolicy(*document, catalogSnapshot, occurrenceID, source, true); err != nil {
			return CommandResult{}, err
		}
		columnID := commandGeneratedID("col_", "semantic-selection/v1", command.OutputID, selection.ConceptID, selection.BindingID, strings.Join(selection.RouteEdgeIDs, "\x00"), source.Lookup.ProjectionMode)
		status := SemanticSelectionAdded
		found := false
		for i := range document.Columns {
			existing := &document.Columns[i]
			if existing.Column != columnID {
				continue
			}
			if existing.OccurrenceID != occurrenceID || existing.LogicalType != plan.LogicalType || !sourceEqual(existing.Source, source) {
				return CommandResult{}, fmt.Errorf("semantic column identity %q conflicts with a different feature", columnID)
			}
			status = SemanticSelectionAlreadyPresent
			found = true
			break
		}
		if !found {
			label := strings.TrimSpace(selection.Title)
			if label == "" && observation.RuleHint == semantic.RuleHintCodedCategoryV1 {
				label = semanticauthoring.SemanticCategoryDefaultTitle(observation.Source.Type, observation.Source.Path, observation.Key.System)
			}
			if label == "" {
				label = strings.TrimSpace(observation.Key.Display)
			}
			if label == "" {
				label = strings.TrimSpace(observation.Key.Code)
			}
			column := Column{Column: columnID, Label: label, LogicalType: plan.LogicalType, OccurrenceID: occurrenceID, Source: source}
			column.ColumnID = stagedSourceColumnID(document.Output.ID, commandID, commandIndex, command.Type, selectionIndex, selection.ConceptID, selection.BindingID)
			applyInitialPresentation(&column, InitialPresentationTable, nextTableOrder(*document))
			document.Columns = append(document.Columns, column)
		}
		result.SemanticSelections = append(result.SemanticSelections, SemanticSelectionResult{
			ConceptID: selection.ConceptID,
			BindingID: selection.BindingID,
			ColumnID:  columnID,
			Status:    status,
		})
	}
	return result, nil
}
