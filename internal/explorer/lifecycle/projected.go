package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/explorer/authoringcore"
	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func (s *Service) prepareProjectedFieldRemoval(
	ctx context.Context,
	project string,
	explorerID string,
	request authoringv2.ApplyCommandsRequest,
	authorized ports.AuthorizedCapability,
	workspace ws.Workspace,
	catalog ws.CatalogSnapshot,
	commands []authoringv2.Command,
) ([]authoringv2.Command, error) {
	if len(commands) != 1 || commands[0].Type != authoringv2.CommandRemoveProjectedField {
		return nil, support.Malformed("commands", "REMOVE_PROJECTED_FIELD must be the only command in its atomic request", nil)
	}
	command := &commands[0]
	if s.config.AuthoringNetwork == nil {
		return nil, support.Unavailable("commands", "AUTHORING_NETWORK_UNAVAILABLE", "projected field ownership is unavailable", nil)
	}
	var document *ws.Document
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == command.OutputID {
			document = &workspace.Documents[index]
			break
		}
	}
	if document == nil {
		return nil, support.Unprocessable("commands", "OUTPUT_NOT_FOUND", "saved output is unavailable", nil)
	}
	network, err := s.config.AuthoringNetwork(ctx, project, authorized, *document)
	if err != nil {
		var typed *ports.Error
		if errors.As(err, &typed) {
			return nil, typed
		}
		return nil, support.Unprocessable("commands", "INVALID_AUTHORING_NETWORK", err.Error(), err)
	}
	var projected *authoringnetwork.ProjectedField
	for index := range network.ProjectedFields {
		field := &network.ProjectedFields[index]
		if field.ColumnID != command.ColumnID() {
			continue
		}
		if projected != nil {
			return nil, support.Unprocessable("commands", "AMBIGUOUS_PROJECTED_FIELD", "columnId is ambiguous in the saved output", nil)
		}
		projected = field
	}
	if projected == nil {
		return nil, support.Unprocessable("commands", "PROJECTED_FIELD_NOT_FOUND", "columnId is unavailable in the saved output", nil)
	}
	if projected.Status != authoringnetwork.ProjectedFieldStatusExact {
		return nil, support.Unprocessable("commands", "PROJECTED_FIELD_UNAVAILABLE", "the saved field has no exact stable identity and owner", nil)
	}
	switch {
	case projected.Owner == authoringnetwork.ProjectedFieldOwnerAuthored && projected.Removal == authoringnetwork.ProjectedFieldRemovalColumn:
		found := 0
		for _, column := range document.Columns {
			if column.ColumnID == projected.ColumnID && column.Source.Kind != ws.SourceImported {
				found++
			}
		}
		if found != 1 {
			return nil, support.Unprocessable("commands", "PROJECTED_FIELD_UNAVAILABLE", "the authored field identity is unavailable or ambiguous", nil)
		}
	case projected.Owner == authoringnetwork.ProjectedFieldOwnerPinned && projected.Removal == authoringnetwork.ProjectedFieldRemovalDisablePinned:
		if document.ImportedRecipe == nil || projected.SourceStepID == "" || projected.SourceFieldID == "" || s.config.RemoveImportedField == nil {
			return nil, support.Unavailable("commands", "IMPORTED_FIELD_REMOVAL_UNAVAILABLE", "the pinned field cannot be removed safely", nil)
		}
		updated, editErr := s.config.RemoveImportedField(ctx, project, authorized, *document, projected.SourceStepID, projected.SourceFieldID)
		if editErr != nil {
			var typed *ports.Error
			if errors.As(editErr, &typed) {
				return nil, typed
			}
			return nil, support.Unprocessable("commands", "INVALID_IMPORTED_FIELD_REMOVAL", editErr.Error(), editErr)
		}
		if err := command.ResolveProjectedFieldRemovalDocument(updated); err != nil {
			return nil, support.Unprocessable("commands", "INVALID_IMPORTED_FIELD_REMOVAL", err.Error(), err)
		}
	case projected.Owner == authoringnetwork.ProjectedFieldOwnerPinned && projected.Removal == authoringnetwork.ProjectedFieldRemovalDisablePinnedOutput:
		if document.ImportedRecipe == nil || projected.SourceStepID == "" || projected.SourceOutputKey == "" || s.config.RemoveImportedOutput == nil {
			return nil, support.Unavailable("commands", "IMPORTED_OUTPUT_REMOVAL_UNAVAILABLE", "the pinned output cannot be removed safely", nil)
		}
		updated, editErr := s.config.RemoveImportedOutput(ctx, project, authorized, *document, projected.SourceStepID, projected.SourceOutputKey)
		if editErr != nil {
			var typed *ports.Error
			if errors.As(editErr, &typed) {
				return nil, typed
			}
			return nil, support.Unprocessable("commands", "INVALID_IMPORTED_OUTPUT_REMOVAL", editErr.Error(), editErr)
		}
		if err := command.ResolveProjectedFieldRemovalDocument(updated); err != nil {
			return nil, support.Unprocessable("commands", "INVALID_IMPORTED_OUTPUT_REMOVAL", err.Error(), err)
		}
	default:
		return nil, support.Unprocessable("commands", "PROJECTED_FIELD_UNAVAILABLE", "the saved field does not support removal", nil)
	}
	candidate, _, err := authoringv2.ApplyCommands(workspace, catalog, "projected-field-removal-preview", commands)
	if err != nil {
		return nil, support.Unprocessable("commands", "INVALID_PROJECTED_FIELD_REMOVAL", err.Error(), err)
	}
	if _, err := s.core.Compile(ctx, authoringcore.CompileRequest{Project: project, ExplorerID: explorerID, Workspace: candidate, SnapshotToken: request.SnapshotToken, RequestID: "projected-field-removal-candidate"}); err != nil {
		return nil, fmt.Errorf("validate projected field removal candidate: %w", err)
	}
	return commands, nil
}
