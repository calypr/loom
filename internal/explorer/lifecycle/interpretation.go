package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/projectid"
)

// resolveWorkspaceInterpretations loads each distinct exact revision once and
// then delegates all matching and rule selection to the pure compiler
// resolver. No library head is consulted.
func (s *Service) resolveWorkspaceInterpretations(ctx context.Context, project string, workspace authoringv2.Workspace, snapshot capability.Snapshot) (explorercompilation.ResolvedInputs, error) {
	ids := make(map[explorer.InterpretationRevisionID]struct{})
	for _, document := range workspace.Documents {
		for _, column := range document.Columns {
			if column.Interpretation == nil || column.Interpretation.Kind != authoringv2.FeatureInterpretationPinned || column.Interpretation.Pinned == nil {
				continue
			}
			id, err := explorer.NewInterpretationRevisionID(strings.TrimSpace(column.Interpretation.Pinned.RevisionID))
			if err != nil {
				return explorercompilation.ResolvedInputs{}, fmt.Errorf("interpretation %s/%s: %w", document.Output.ID, column.Column, err)
			}
			ids[id] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return explorercompilation.ResolvedInputs{}, nil
	}
	if s.config.InterpretationRepository == nil {
		return explorercompilation.ResolvedInputs{}, fmt.Errorf("interpretation repository is required when workspace contains pinned revisions")
	}
	revisions := make(map[explorer.InterpretationRevisionID]explorer.InterpretationRevision, len(ids))
	for id := range ids {
		revision, err := s.config.InterpretationRepository.GetInterpretationRevision(ctx, projectid.Canonical(project), id)
		if err != nil {
			return explorercompilation.ResolvedInputs{}, fmt.Errorf("load interpretation revision %q: %w", id, err)
		}
		if revision == nil {
			return explorercompilation.ResolvedInputs{}, fmt.Errorf("load interpretation revision %q: repository returned nil", id)
		}
		revisions[id] = *revision
	}
	return explorercompilation.ResolveWorkspaceInterpretations(project, workspace, snapshot, revisions)
}
