package authoringv2

import (
	"fmt"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func ResolveFrameSource(c Command, frame ws.FrameDefinition) (FrameSourceResolution, error) {
	if c.Type != CommandSetFrameSource && c.Type != CommandReplaceFrameSource {
		return FrameSourceResolution{}, fmt.Errorf("a frame source can only be resolved for SET_FRAME_SOURCE or REPLACE_FRAME_SOURCE")
	}
	if err := frame.Validate(); err != nil {
		return FrameSourceResolution{}, fmt.Errorf("resolved frame source: %w", err)
	}
	cloned := frame
	cloned.Route = ws.CloneFrameRoute(frame.Route)
	cloned.Source.ChoiceArms = append([]string(nil), frame.Source.ChoiceArms...)
	cloned.Source.ValueFallbacks = append([]string(nil), frame.Source.ValueFallbacks...)
	return FrameSourceResolution{Frame: cloned}, nil
}

func applyFrameSourceCommand(workspace *ws.Workspace, command Command, resolution CommandResolution) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	frameResolution, hasFrame := resolution.(FrameSourceResolution)
	documentIndexValue := documentIndex(workspace, command.OutputID)
	if documentIndexValue < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	document := &workspace.Documents[documentIndexValue]
	switch command.Type {
	case CommandRemoveFrameSource:
		return removeFrameSource(document, workspace, command.OutputID, command.FrameID())
	case CommandSetFrameSource:
		if !hasFrame {
			return result, fmt.Errorf("SET_FRAME_SOURCE has no lifecycle-resolved source")
		}
		for _, existing := range document.Frames {
			if existing.ID == frameResolution.Frame.ID {
				return result, fmt.Errorf("frame source %q already exists", existing.ID)
			}
		}
	case CommandReplaceFrameSource:
		if !hasFrame {
			return result, fmt.Errorf("REPLACE_FRAME_SOURCE has no lifecycle-resolved source")
		}
		if !frameExists(document.Frames, command.FrameID()) {
			return result, fmt.Errorf("frame source %q was not found", command.FrameID())
		}
	default:
		return result, fmt.Errorf("unsupported frame source command %q", command.Type)
	}
	if command.Type == CommandReplaceFrameSource {
		var err error
		result, err = removeFrameSource(document, workspace, command.OutputID, command.FrameID())
		if err != nil {
			return result, err
		}
		documentIndexValue = documentIndex(workspace, command.OutputID)
		document = &workspace.Documents[documentIndexValue]
		for _, existing := range document.Frames {
			if existing.ID == frameResolution.Frame.ID {
				return result, fmt.Errorf("replacement frame source %q already exists", existing.ID)
			}
		}
	}
	frame := frameResolution.Frame
	frame.Route = ws.CloneFrameRoute(frame.Route)
	frame.Source.ChoiceArms = append([]string(nil), frame.Source.ChoiceArms...)
	frame.Source.ValueFallbacks = append([]string(nil), frame.Source.ValueFallbacks...)
	document.Frames = append(document.Frames, frame)
	result.Type, result.FrameID = CommandResultTableChanged, frame.ID
	return result, nil
}

func removeFrameSource(document *ws.Document, workspace *ws.Workspace, outputID, frameID string) (CommandResult, error) {
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

func frameExists(frames []ws.FrameDefinition, frameID string) bool {
	for _, frame := range frames {
		if frame.ID == frameID {
			return true
		}
	}
	return false
}
