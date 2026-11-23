package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func (s *Service) prepareProjectedFieldRemoval(
	ctx context.Context,
	project string,
	explorerID string,
	request authoringv2.ApplyCommandsRequest,
	authorized ports.AuthorizedCapability,
	workspace authoringv2.Workspace,
	catalog authoringv2.CatalogSnapshot,
	commands []authoringv2.Command,
) ([]authoringv2.Command, error) {
	if len(commands) != 1 || commands[0].Type != authoringv2.CommandRemoveProjectedField {
		return nil, malformed("commands", "REMOVE_PROJECTED_FIELD must be the only command in its atomic request", nil)
	}
	command := &commands[0]
	if s.config.AuthoringNetwork == nil {
		return nil, unavailable("commands", "AUTHORING_NETWORK_UNAVAILABLE", "projected field ownership is unavailable", nil)
	}
	var document *authoringv2.Document
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == command.OutputID {
			document = &workspace.Documents[index]
			break
		}
	}
	if document == nil {
		return nil, unprocessable("commands", "OUTPUT_NOT_FOUND", "saved output is unavailable", nil)
	}
	network, err := s.config.AuthoringNetwork(ctx, project, authorized, *document)
	if err != nil {
		var typed *ports.Error
		if errors.As(err, &typed) {
			return nil, typed
		}
		return nil, unprocessable("commands", "INVALID_AUTHORING_NETWORK", err.Error(), err)
	}
	var projected *authoringv2.ProjectedField
	for index := range network.ProjectedFields {
		field := &network.ProjectedFields[index]
		if field.ColumnID != command.ColumnID() {
			continue
		}
		if projected != nil {
			return nil, unprocessable("commands", "AMBIGUOUS_PROJECTED_FIELD", "columnId is ambiguous in the saved output", nil)
		}
		projected = field
	}
	if projected == nil {
		return nil, unprocessable("commands", "PROJECTED_FIELD_NOT_FOUND", "columnId is unavailable in the saved output", nil)
	}
	if projected.Status != authoringv2.ProjectedFieldStatusExact {
		return nil, unprocessable("commands", "PROJECTED_FIELD_UNAVAILABLE", "the saved field has no exact stable identity and owner", nil)
	}
	switch {
	case projected.Owner == authoringv2.ProjectedFieldOwnerAuthored && projected.Removal == authoringv2.ProjectedFieldRemovalColumn:
		found := 0
		for _, column := range document.Columns {
			if column.ColumnID == projected.ColumnID && column.Source.Kind != authoringv2.SourceImported {
				found++
			}
		}
		if found != 1 {
			return nil, unprocessable("commands", "PROJECTED_FIELD_UNAVAILABLE", "the authored field identity is unavailable or ambiguous", nil)
		}
	case projected.Owner == authoringv2.ProjectedFieldOwnerPinned && projected.Removal == authoringv2.ProjectedFieldRemovalDisablePinned:
		if document.ImportedRecipe == nil || projected.SourceStepID == "" || projected.SourceFieldID == "" || s.config.RemoveImportedField == nil {
			return nil, unavailable("commands", "IMPORTED_FIELD_REMOVAL_UNAVAILABLE", "the pinned field cannot be removed safely", nil)
		}
		updated, editErr := s.config.RemoveImportedField(ctx, project, authorized, *document, projected.SourceStepID, projected.SourceFieldID)
		if editErr != nil {
			var typed *ports.Error
			if errors.As(editErr, &typed) {
				return nil, typed
			}
			return nil, unprocessable("commands", "INVALID_IMPORTED_FIELD_REMOVAL", editErr.Error(), editErr)
		}
		if err := command.ResolveProjectedFieldRemovalDocument(updated); err != nil {
			return nil, unprocessable("commands", "INVALID_IMPORTED_FIELD_REMOVAL", err.Error(), err)
		}
	case projected.Owner == authoringv2.ProjectedFieldOwnerPinned && projected.Removal == authoringv2.ProjectedFieldRemovalDisablePinnedOutput:
		if document.ImportedRecipe == nil || projected.SourceStepID == "" || projected.SourceOutputKey == "" || s.config.RemoveImportedOutput == nil {
			return nil, unavailable("commands", "IMPORTED_OUTPUT_REMOVAL_UNAVAILABLE", "the pinned output cannot be removed safely", nil)
		}
		updated, editErr := s.config.RemoveImportedOutput(ctx, project, authorized, *document, projected.SourceStepID, projected.SourceOutputKey)
		if editErr != nil {
			var typed *ports.Error
			if errors.As(editErr, &typed) {
				return nil, typed
			}
			return nil, unprocessable("commands", "INVALID_IMPORTED_OUTPUT_REMOVAL", editErr.Error(), editErr)
		}
		if err := command.ResolveProjectedFieldRemovalDocument(updated); err != nil {
			return nil, unprocessable("commands", "INVALID_IMPORTED_OUTPUT_REMOVAL", err.Error(), err)
		}
	default:
		return nil, unprocessable("commands", "PROJECTED_FIELD_UNAVAILABLE", "the saved field does not support removal", nil)
	}
	candidate, _, err := authoringv2.ApplyCommands(workspace, catalog, "projected-field-removal-preview", commands)
	if err != nil {
		return nil, unprocessable("commands", "INVALID_PROJECTED_FIELD_REMOVAL", err.Error(), err)
	}
	if _, err := s.compile(ctx, compileRequest{Project: project, ExplorerID: explorerID, Workspace: candidate, SnapshotToken: request.SnapshotToken, RequestID: "projected-field-removal-candidate"}); err != nil {
		return nil, fmt.Errorf("validate projected field removal candidate: %w", err)
	}
	return commands, nil
}
