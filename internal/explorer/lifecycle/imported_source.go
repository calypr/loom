package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func (s *Service) prepareImportedSource(ctx context.Context, project, explorerID string, authorized AuthorizedCapability, workspace authoringv2.Workspace, catalog authoringv2.CatalogSnapshot, commands []authoringv2.Command) ([]authoringv2.Command, error) {
	if s.config.EditImportedSource == nil {
		return nil, unavailable("commands", "IMPORTED_SOURCE_UNAVAILABLE", "source editing is unavailable", nil)
	}
	command := commands[0]
	var document *authoringv2.Document
	for i := range workspace.Documents {
		if workspace.Documents[i].Output.ID == command.OutputID {
			document = &workspace.Documents[i]
			break
		}
	}
	if document == nil || document.ImportedRecipe == nil {
		return nil, malformed("commands", "source editing requires an imported dataframe", nil)
	}
	updated, err := s.config.EditImportedSource(ctx, project, authorized, *document, command.SourceStep)
	if err != nil {
		return nil, unprocessable("commands", "INVALID_IMPORTED_SOURCE", err.Error(), err)
	}
	if err := command.ResolveImportedDocument(updated); err != nil {
		return nil, unprocessable("commands", "INVALID_IMPORTED_SOURCE", err.Error(), err)
	}
	prepared := []authoringv2.Command{command}
	candidate, _, err := authoringv2.ApplyCommands(workspace, catalog, "source-edit-validation", prepared)
	if err != nil {
		return nil, unprocessable("commands", "INVALID_IMPORTED_SOURCE", err.Error(), err)
	}
	// Compile the whole workspace: later operations and other tables may depend
	// on columns removed by this source change.
	if _, err := s.compile(ctx, compileRequest{Project: project, ExplorerID: explorerID, Workspace: candidate, SnapshotToken: authorized.Snapshot.Token, RequestID: "source-edit-validation"}); err != nil {
		return nil, err
	}
	return prepared, nil
}
