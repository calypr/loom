package authoringv2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/explorer/capability"
)

type FrameZeroPolicy string

const (
	FrameZeroNull      FrameZeroPolicy = "NULL"
	FrameZeroEmptyList FrameZeroPolicy = "EMPTY_LIST"
)

type FrameManyPolicy string

const (
	FrameManyInvalidMultipleValues FrameManyPolicy = "INVALID_MULTIPLE_VALUES"
	FrameManyFirst                 FrameManyPolicy = "FIRST"
	FrameManyAll                   FrameManyPolicy = "ALL"
	FrameManyDistinct              FrameManyPolicy = "DISTINCT"
)

func validFrameForm(form capability.ConstructionChoiceForm) bool {
	switch form {
	case capability.ConstructionChoiceValue, capability.ConstructionChoiceFirst,
		capability.ConstructionChoiceAll, capability.ConstructionChoiceDistinct:
		return true
	default:
		return false
	}
}

// FrameDefinition selects one metadata-backed correlated code/value family
// and an exact route. It is source guidance until matching categories are
// added; it does not change the row grain or compile an evaluator of its own.
type FrameDefinition struct {
	ID          string                             `json:"id"`
	Title       string                             `json:"title"`
	Description string                             `json:"description"`
	Source      capability.SemanticFrameFamily     `json:"source"`
	Route       []capability.ConstructionRouteStep `json:"route"`
	Form        capability.ConstructionChoiceForm  `json:"form"`
	ZeroPolicy  FrameZeroPolicy                    `json:"zeroPolicy"`
	ManyPolicy  FrameManyPolicy                    `json:"manyPolicy"`
}

func NewFrameDefinition(title, description string, source capability.SemanticFrameFamily, route []capability.ConstructionRouteStep, form capability.ConstructionChoiceForm) (FrameDefinition, error) {
	frame := FrameDefinition{Title: strings.TrimSpace(title), Description: strings.TrimSpace(description), Source: source, Route: cloneFrameRoute(route), Form: form}
	switch form {
	case capability.ConstructionChoiceValue:
		frame.ZeroPolicy, frame.ManyPolicy = FrameZeroNull, FrameManyInvalidMultipleValues
	case capability.ConstructionChoiceFirst:
		frame.ZeroPolicy, frame.ManyPolicy = FrameZeroNull, FrameManyFirst
	case capability.ConstructionChoiceAll:
		frame.ZeroPolicy, frame.ManyPolicy = FrameZeroEmptyList, FrameManyAll
	case capability.ConstructionChoiceDistinct:
		frame.ZeroPolicy, frame.ManyPolicy = FrameZeroEmptyList, FrameManyDistinct
	default:
		return FrameDefinition{}, fmt.Errorf("frame form %q is unsupported", form)
	}
	frame.ID = frameIdentityID(frame.Source, frame.Route)
	if err := frame.Validate(); err != nil {
		return FrameDefinition{}, err
	}
	return frame, nil
}

func (f FrameDefinition) Validate() error {
	if strings.TrimSpace(f.ID) == "" || f.ID != strings.TrimSpace(f.ID) || f.ID != frameIdentityID(f.Source, f.Route) {
		return fmt.Errorf("frame id does not match its source and exact route")
	}
	if strings.TrimSpace(f.Title) == "" || f.Title != strings.TrimSpace(f.Title) || strings.TrimSpace(f.Description) == "" || f.Description != strings.TrimSpace(f.Description) {
		return fmt.Errorf("frame title and description are required and must be trimmed")
	}
	if strings.TrimSpace(f.Source.BindingID) == "" || strings.TrimSpace(f.Source.ResourceType) == "" ||
		strings.TrimSpace(f.Source.SourcePath) == "" || f.Source.OwningScope != strings.TrimSpace(f.Source.OwningScope) ||
		strings.TrimSpace(f.Source.KeyPath) == "" || strings.TrimSpace(f.Source.ValuePath) == "" ||
		strings.TrimSpace(f.Source.LogicalType) == "" || strings.TrimSpace(f.Source.RuleVersion) == "" || f.Source.SchemaVersion <= 0 {
		return fmt.Errorf("frame source identity is incomplete")
	}
	if f.ZeroPolicy == FrameZeroNull && f.ManyPolicy == FrameManyInvalidMultipleValues && f.Form == capability.ConstructionChoiceValue ||
		f.ZeroPolicy == FrameZeroNull && f.ManyPolicy == FrameManyFirst && f.Form == capability.ConstructionChoiceFirst ||
		f.ZeroPolicy == FrameZeroEmptyList && f.ManyPolicy == FrameManyAll && f.Form == capability.ConstructionChoiceAll ||
		f.ZeroPolicy == FrameZeroEmptyList && f.ManyPolicy == FrameManyDistinct && f.Form == capability.ConstructionChoiceDistinct {
		// The selected form and its zero/many behavior are a closed policy pair.
	} else {
		return fmt.Errorf("frame zero/many policy does not match form %q", f.Form)
	}
	previous := ""
	for index, step := range f.Route {
		if strings.TrimSpace(step.EdgeID) == "" || strings.TrimSpace(step.FromNodeID) == "" || strings.TrimSpace(step.ToNodeID) == "" ||
			strings.TrimSpace(step.FromResourceType) == "" || strings.TrimSpace(step.ToResourceType) == "" ||
			strings.TrimSpace(step.Relationship) == "" || strings.TrimSpace(step.StorageDirection) == "" {
			return fmt.Errorf("frame route[%d] is incomplete", index)
		}
		if index > 0 && step.FromNodeID != previous {
			return fmt.Errorf("frame route[%d] is not contiguous", index)
		}
		previous = step.ToNodeID
	}
	if len(f.Route) > 0 && f.Route[len(f.Route)-1].ToResourceType != f.Source.ResourceType {
		return fmt.Errorf("frame route does not end at its source resource type")
	}
	return nil
}

func validateDocumentFrames(document Document) error {
	frames := make(map[string]FrameDefinition, len(document.Frames))
	for index, frame := range document.Frames {
		if err := frame.Validate(); err != nil {
			return fmt.Errorf("frames[%d]: %w", index, err)
		}
		if len(frame.Route) == 0 {
			if frame.Source.ResourceType != document.RootResourceType {
				return fmt.Errorf("frames[%d] direct source does not match the row root", index)
			}
		} else {
			if frame.Route[0].FromResourceType != document.RootResourceType {
				return fmt.Errorf("frames[%d] route does not start at the row root", index)
			}
			for stepIndex := 1; stepIndex < len(frame.Route); stepIndex++ {
				if frame.Route[stepIndex-1].ToNodeID != frame.Route[stepIndex].FromNodeID {
					return fmt.Errorf("frames[%d] route is not contiguous", index)
				}
			}
		}
		if _, exists := frames[frame.ID]; exists {
			return fmt.Errorf("DUPLICATE_FRAME_ID: frames[%d].id", index)
		}
		frames[frame.ID] = frame
	}
	for index, column := range document.Columns {
		if column.FrameID == "" {
			continue
		}
		frame, exists := frames[column.FrameID]
		if !exists {
			return fmt.Errorf("columns[%d].frameId references an unknown frame", index)
		}
		if column.Source.Kind != SourceCodedValue || column.Source.Lookup == nil || column.Source.Lookup.Binding == nil || column.Source.Lookup.Key == nil {
			return fmt.Errorf("columns[%d].frameId requires a correlated coded-value source", index)
		}
		binding := column.Source.Lookup.Binding
		if binding.OwnerPath != frame.Source.OwningScope || binding.KeyPath != frame.Source.KeyPath ||
			binding.ValuePath != frame.Source.ValuePath || !reflect.DeepEqual(binding.ChoiceArms, frame.Source.ChoiceArms) ||
			binding.LogicalType != frame.Source.LogicalType {
			return fmt.Errorf("columns[%d].frameId does not match its coded-value family", index)
		}
		columnRoute, ok := documentRouteForOccurrence(document.Route, column.OccurrenceID)
		if !ok || !routeMatchesDocument(frame.Route, columnRoute) {
			return fmt.Errorf("columns[%d].frameId does not match its exact source route", index)
		}
	}
	return nil
}

func documentRouteForOccurrence(root RouteNode, occurrenceID string) ([]RouteNode, bool) {
	var walk func(RouteNode, []RouteNode) ([]RouteNode, bool)
	walk = func(node RouteNode, prefix []RouteNode) ([]RouteNode, bool) {
		path := append(append([]RouteNode(nil), prefix...), node)
		if node.OccurrenceID == occurrenceID {
			return path, true
		}
		for _, child := range node.Children {
			if found, ok := walk(child, path); ok {
				return found, true
			}
		}
		return nil, false
	}
	return walk(root, nil)
}

func routeMatchesDocument(frameRoute []capability.ConstructionRouteStep, documentRoute []RouteNode) bool {
	if len(frameRoute) != len(documentRoute)-1 {
		return false
	}
	for index, step := range frameRoute {
		parent, child := documentRoute[index], documentRoute[index+1]
		if step.EdgeID != child.CatalogEdgeID || step.FromResourceType != parent.ResourceType ||
			step.ToResourceType != child.ResourceType || step.Relationship != child.Relationship ||
			RouteMatchMode(strings.ToUpper(step.MatchMode)).Normalized() != child.MatchMode.Normalized() {
			return false
		}
	}
	return true
}

func frameIdentityID(source capability.SemanticFrameFamily, route []capability.ConstructionRouteStep) string {
	identity, _ := json.Marshal(struct {
		Source capability.SemanticFrameFamily     `json:"source"`
		Route  []capability.ConstructionRouteStep `json:"route"`
	}{Source: source, Route: route})
	sum := sha256.Sum256(identity)
	return "frame_" + hex.EncodeToString(sum[:])
}

func cloneFrameRoute(route []capability.ConstructionRouteStep) []capability.ConstructionRouteStep {
	if route == nil {
		return []capability.ConstructionRouteStep{}
	}
	return append([]capability.ConstructionRouteStep(nil), route...)
}

// ResolveFrameSource attaches a frame choice already authorized by lifecycle.
func (c *Command) ResolveFrameSource(frame FrameDefinition) error {
	if c == nil || c.Type != CommandSetFrameSource && c.Type != CommandReplaceFrameSource {
		return fmt.Errorf("a frame source can only be resolved for SET_FRAME_SOURCE or REPLACE_FRAME_SOURCE")
	}
	if err := frame.Validate(); err != nil {
		return fmt.Errorf("resolved frame source: %w", err)
	}
	cloned := frame
	cloned.Route = cloneFrameRoute(frame.Route)
	cloned.Source.ChoiceArms = append([]string(nil), frame.Source.ChoiceArms...)
	c.resolvedFrame = &cloned
	return nil
}

func applyFrameSourceCommand(workspace *Workspace, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	documentIndexValue := documentIndex(workspace, command.OutputID)
	if documentIndexValue < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	document := &workspace.Documents[documentIndexValue]
	switch command.Type {
	case CommandRemoveFrameSource:
		return removeFrameSource(document, workspace, command.OutputID, command.FrameID)
	case CommandSetFrameSource:
		if command.resolvedFrame == nil {
			return result, fmt.Errorf("SET_FRAME_SOURCE has no lifecycle-resolved source")
		}
		for _, existing := range document.Frames {
			if existing.ID == command.resolvedFrame.ID {
				return result, fmt.Errorf("frame source %q already exists", existing.ID)
			}
		}
	case CommandReplaceFrameSource:
		if command.resolvedFrame == nil {
			return result, fmt.Errorf("REPLACE_FRAME_SOURCE has no lifecycle-resolved source")
		}
		if !frameExists(document.Frames, command.FrameID) {
			return result, fmt.Errorf("frame source %q was not found", command.FrameID)
		}
	default:
		return result, fmt.Errorf("unsupported frame source command %q", command.Type)
	}
	if command.Type == CommandReplaceFrameSource {
		var err error
		result, err = removeFrameSource(document, workspace, command.OutputID, command.FrameID)
		if err != nil {
			return result, err
		}
		documentIndexValue = documentIndex(workspace, command.OutputID)
		document = &workspace.Documents[documentIndexValue]
		for _, existing := range document.Frames {
			if existing.ID == command.resolvedFrame.ID {
				return result, fmt.Errorf("replacement frame source %q already exists", existing.ID)
			}
		}
	}
	frame := *command.resolvedFrame
	frame.Route = cloneFrameRoute(frame.Route)
	frame.Source.ChoiceArms = append([]string(nil), frame.Source.ChoiceArms...)
	document.Frames = append(document.Frames, frame)
	result.Type, result.FrameID = CommandResultTableChanged, frame.ID
	return result, nil
}

func removeFrameSource(document *Document, workspace *Workspace, outputID, frameID string) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: outputID}
	frameIndex := -1
	for index := range document.Frames {
		if document.Frames[index].ID == frameID {
			frameIndex = index
			break
		}
	}
	if frameIndex < 0 {
		return result, fmt.Errorf("frame source %q was not found", frameID)
	}
	document.Frames = append(document.Frames[:frameIndex], document.Frames[frameIndex+1:]...)
	columns := document.Columns[:0]
	for _, column := range document.Columns {
		if column.FrameID == frameID {
			result.RemovedColumns = append(result.RemovedColumns, column.Column)
			continue
		}
		columns = append(columns, column)
	}
	document.Columns = columns
	result.FrameID = frameID
	cleanupDocumentReferences(document)
	cleanupWorkspaceBindings(workspace)
	return result, nil
}

func frameExists(frames []FrameDefinition, frameID string) bool {
	for _, frame := range frames {
		if frame.ID == frameID {
			return true
		}
	}
	return false
}
