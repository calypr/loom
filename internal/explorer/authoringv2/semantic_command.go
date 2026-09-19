package authoringv2

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
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
	result := CommandResult{Type: CommandResultSemanticSelectionsAdded, OutputID: command.OutputID, SemanticSelections: make([]SemanticSelectionResult, 0, len(command.SemanticSelections))}
	for selectionIndex, selection := range command.SemanticSelections {
		entry := selection.ResolvedObservation
		if entry == nil || entry.ConceptID != selection.ConceptID || entry.BindingID != selection.BindingID {
			return CommandResult{}, fmt.Errorf("semantic selection %d has no matching server-resolved inventory row", selectionIndex)
		}
		observation := entry.Observation
		sourceType, binding, key, err := semanticSelectionSource(observation)
		if err != nil {
			return CommandResult{}, fmt.Errorf("semantic selection %d: %w", selectionIndex, err)
		}
		occurrenceID, err := ensureSemanticRoute(document, catalogSnapshot, commandID, commandIndex, selectionIndex, selection.RouteEdgeIDs, observation.Source.Type)
		if err != nil {
			return CommandResult{}, fmt.Errorf("semantic selection %d route: %w", selectionIndex, err)
		}
		source := ColumnSource{Kind: sourceType, Lookup: &LookupSource{
			Binding: &binding, Key: &key, ProjectionMode: strings.ToUpper(strings.TrimSpace(selection.ProjectionMode)),
		}}
		if err := source.validate("semantic selection source"); err != nil {
			return CommandResult{}, err
		}
		if err := validateEditableSource(*document, catalogSnapshot, occurrenceID, source); err != nil {
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
			if existing.OccurrenceID != occurrenceID || existing.LogicalType != observation.LogicalType || !sourceEqual(existing.Source, source) {
				return CommandResult{}, fmt.Errorf("semantic column identity %q conflicts with a different feature", columnID)
			}
			status = SemanticSelectionAlreadyPresent
			found = true
			break
		}
		if !found {
			label := strings.TrimSpace(selection.Title)
			if label == "" {
				label = strings.TrimSpace(observation.Key.Display)
			}
			if label == "" {
				label = strings.TrimSpace(observation.Key.Code)
			}
			column := Column{Column: columnID, Label: label, LogicalType: observation.LogicalType, OccurrenceID: occurrenceID, Source: source}
			applyInitialPresentation(&column, InitialPresentationTable, nextTableOrder(document.Columns))
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

func semanticSelectionSource(observation catalog.SemanticObservation) (string, fhirschema.CorrelatedBinding, fhirschema.CorrelatedKey, error) {
	if observation.SchemaVersion != catalog.SemanticObservationSchemaVersion || observation.RuleVersion != strconv.Itoa(catalog.SemanticObservationRuleVersion) {
		return "", fhirschema.CorrelatedBinding{}, fhirschema.CorrelatedKey{}, fmt.Errorf("inventory observation schema or rule version is unsupported")
	}
	if observation.Completeness != catalog.SemanticComplete || observation.Status != "SUPPORTED" {
		return "", fhirschema.CorrelatedBinding{}, fhirschema.CorrelatedKey{}, fmt.Errorf("inventory observation is incomplete or unsupported")
	}
	if observation.RuleHint != "OBSERVATION_CODE_VALUE" || observation.Source.Type != "Observation" || strings.TrimSpace(observation.Key.System) == "" || strings.TrimSpace(observation.Key.Code) == "" {
		return "", fhirschema.CorrelatedBinding{}, fhirschema.CorrelatedKey{}, fmt.Errorf("inventory observation is not a supported coded Observation value")
	}
	if strings.TrimSpace(observation.Key.Version) != "" {
		return "", fhirschema.CorrelatedBinding{}, fhirschema.CorrelatedKey{}, fmt.Errorf("version-specific coding selections are not supported by the current compiler")
	}
	var sourceType, keyPath string
	switch observation.OwningScope {
	case "":
		sourceType = SourceCodingBySystem
		keyPath = "code.coding[]"
	case "component[]":
		sourceType = SourceObservationComponentByCode
		keyPath = "component[].code.coding[]"
	default:
		return "", fhirschema.CorrelatedBinding{}, fhirschema.CorrelatedKey{}, fmt.Errorf("inventory owning scope %q is not supported", observation.OwningScope)
	}
	if observation.Key.Selector != "code.coding[]" || strings.TrimSpace(observation.Value.Selector) == "" || strings.TrimSpace(observation.LogicalType) == "" {
		return "", fhirschema.CorrelatedBinding{}, fhirschema.CorrelatedKey{}, fmt.Errorf("inventory observation does not contain a complete code/value binding")
	}
	binding := fhirschema.CorrelatedBinding{
		OwnerPath:   observation.OwningScope,
		KeyPath:     keyPath,
		SystemPath:  "system",
		CodePath:    "code",
		ValuePath:   observation.Value.Selector,
		LogicalType: observation.LogicalType,
	}
	if observation.ChoiceArm != "" {
		binding.ChoiceArms = []string{observation.ChoiceArm}
	}
	if _, err := fhirschema.ValidateCorrelatedBinding(observation.Source.Type, binding); err != nil {
		return "", fhirschema.CorrelatedBinding{}, fhirschema.CorrelatedKey{}, fmt.Errorf("inventory binding is not compiler-supported: %w", err)
	}
	return sourceType, binding, fhirschema.CorrelatedKey{System: observation.Key.System, Code: observation.Key.Code}, nil
}

func ensureSemanticRoute(document *Document, catalogSnapshot CatalogSnapshot, commandID string, commandIndex, selectionIndex int, edgeIDs []string, targetResource string) (string, error) {
	if document == nil || document.Route.OccurrenceID != RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return "", fmt.Errorf("table root route is invalid")
	}
	if catalogSnapshot.RoutePolicy.MaxHops != nil && len(edgeIDs) > *catalogSnapshot.RoutePolicy.MaxHops {
		return "", fmt.Errorf("ROUTE_TOO_LONG: route exceeds capability route policy (maxSteps=%d, steps=%d)", *catalogSnapshot.RoutePolicy.MaxHops, len(edgeIDs))
	}
	current := &document.Route
	for edgeIndex, edgeID := range edgeIDs {
		edge, found := catalogEdge(catalogSnapshot, edgeID)
		if !found {
			return "", fmt.Errorf("route edge %q was not found", edgeID)
		}
		from, fromFound := catalogNode(catalogSnapshot, edge.FromNodeID)
		to, toFound := catalogNode(catalogSnapshot, edge.ToNodeID)
		if !fromFound || !toFound || from.ResourceType != current.ResourceType {
			return "", fmt.Errorf("route edge %q does not extend resource %q", edgeID, current.ResourceType)
		}
		if from.ResourceType == to.ResourceType && !catalogSnapshot.RoutePolicy.AllowSelfLoops {
			return "", fmt.Errorf("route edge %q is a self-loop but self-loops are not allowed", edgeID)
		}
		if !catalogSnapshot.RoutePolicy.AllowRepeatedEdges && routePathUsesRelationship(&document.Route, current.OccurrenceID, from.ResourceType, to.ResourceType, edge.Label) {
			return "", fmt.Errorf("route edge %q is already used in this route", edgeID)
		}
		matchedIndex := -1
		for i := range current.Children {
			child := &current.Children[i]
			if child.ResourceType != to.ResourceType || child.Relationship != edge.Label {
				continue
			}
			if matchedIndex >= 0 {
				return "", fmt.Errorf("route edge %q matches multiple existing route occurrences", edgeID)
			}
			matchedIndex = i
		}
		if matchedIndex < 0 {
			occurrenceID := commandGeneratedID("occ_", "semantic-route/v1", document.Output.ID, commandID, commandIndex, selectionIndex, edgeIndex, edge.ID)
			current.Children = append(current.Children, RouteNode{OccurrenceID: occurrenceID, ResourceType: to.ResourceType, Relationship: edge.Label})
			matchedIndex = len(current.Children) - 1
		}
		current = &current.Children[matchedIndex]
	}
	if current.ResourceType != targetResource {
		return "", fmt.Errorf("selected route ends at %q, not the source resource %q", current.ResourceType, targetResource)
	}
	return current.OccurrenceID, nil
}
