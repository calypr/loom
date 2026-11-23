package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/projectid"
)

const (
	maxInterpretationContextColumns   = 512
	maxInterpretationContextLibraries = 256
)

type ConfiguredColumnResolution interface {
	State() explorercompilation.InterpretationCandidateResolutionState
	Reason() string
	Ready() (ConfiguredColumnReady, bool)
	configuredColumnResolution()
}

type ConfiguredColumnReady struct {
	CapabilityCandidateIDs []string `json:"capabilityCandidateIds"`
	ApplicableRevisionIDs  []string `json:"applicableRevisionIds"`
}

func (ConfiguredColumnReady) State() explorercompilation.InterpretationCandidateResolutionState {
	return explorercompilation.InterpretationCandidateReady
}

func (ConfiguredColumnReady) Reason() string { return "" }

func (value ConfiguredColumnReady) Ready() (ConfiguredColumnReady, bool) {
	copy := value
	copy.CapabilityCandidateIDs = append([]string(nil), value.CapabilityCandidateIDs...)
	copy.ApplicableRevisionIDs = append([]string(nil), value.ApplicableRevisionIDs...)
	return copy, true
}

func (ConfiguredColumnReady) configuredColumnResolution() {}

type configuredColumnUnavailable struct {
	state  explorercompilation.InterpretationCandidateResolutionState
	reason string
}

func (value configuredColumnUnavailable) State() explorercompilation.InterpretationCandidateResolutionState {
	return value.state
}

func (value configuredColumnUnavailable) Reason() string { return value.reason }

func (configuredColumnUnavailable) Ready() (ConfiguredColumnReady, bool) {
	return ConfiguredColumnReady{}, false
}

func (configuredColumnUnavailable) configuredColumnResolution() {}

type ConfiguredColumnContext struct {
	OutputID     string                     `json:"outputId"`
	Column       string                     `json:"column"`
	OccurrenceID string                     `json:"occurrenceId"`
	Resolution   ConfiguredColumnResolution `json:"resolution"`
}

type ConfiguredColumnContextResult struct {
	SnapshotToken   string                          `json:"snapshotToken"`
	DraftVersion    int64                           `json:"draftVersion"`
	DraftDigest     string                          `json:"draftDigest"`
	Libraries       []InterpretationLibrarySummary  `json:"libraries"`
	PinnedRevisions []InterpretationRevisionSummary `json:"pinnedRevisions"`
	Columns         []ConfiguredColumnContext       `json:"columns"`
}

func validateConfiguredColumnContextRequest(request ConfiguredColumnContextRequest) error {
	values := map[string]string{
		"project": request.Project, "explorerId": request.ExplorerID, "snapshotToken": request.SnapshotToken,
		"expectedDraftDigest": request.ExpectedDraftDigest,
	}
	for name, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if request.ExpectedDraftVersion < 1 {
		return fmt.Errorf("expectedDraftVersion must be positive")
	}
	return nil
}

func (s *Service) ConfiguredColumnContext(ctx context.Context, request ConfiguredColumnContextRequest) (ConfiguredColumnContextResult, error) {
	result := ConfiguredColumnContextResult{
		SnapshotToken: request.SnapshotToken,
		Libraries:     []InterpretationLibrarySummary{}, PinnedRevisions: []InterpretationRevisionSummary{}, Columns: []ConfiguredColumnContext{},
	}
	if err := validateConfiguredColumnContextRequest(request); err != nil {
		return result, support.Malformed("interpretation-context", err.Error(), err)
	}
	if s.config.Capability.ForCompilation == nil {
		return result, support.Unavailable("interpretation-context", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	repository, err := s.interpretationRepository()
	if err != nil {
		return result, err
	}
	project, err := explorer.CanonicalInterpretationProject(request.Project)
	if err != nil {
		return result, support.Malformed("interpretation-context", err.Error(), err)
	}
	owner, err := s.getOwner(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return result, err
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return result, support.Conflict("interpretation-context", "DRAFT_CONFLICT", "the Explorer draft changed; reload before resolving column mappings", nil, explorer.ErrDraftConflict)
	}
	workspace, err := previewWorkspace(ctx, s.store, owner)
	if err != nil {
		return result, support.Conflict("interpretation-context", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be resolved", nil, err)
	}
	columnCount := 0
	for _, document := range workspace.Documents {
		for _, column := range document.Columns {
			if column.Source.Kind != authoringv2.SourceImported {
				columnCount++
			}
		}
	}
	if columnCount > maxInterpretationContextColumns {
		return result, support.Unprocessable("interpretation-context", "INTERPRETATION_CONTEXT_LIMIT", fmt.Sprintf("the Explorer has %d configured columns; the limit is %d", columnCount, maxInterpretationContextColumns), nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(authorized.Snapshot.Identity.Project) != project {
		return result, support.Conflict("interpretation-context", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	if err := support.ValidateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, support.Conflict("interpretation-context", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}

	libraries, err := repository.ListInterpretationLibraries(ctx, project)
	if err != nil {
		return result, support.PersistenceFailure("interpretation-context", "interpretation libraries could not be loaded", err)
	}
	if len(libraries) > maxInterpretationContextLibraries {
		return result, support.Unprocessable("interpretation-context", "INTERPRETATION_CONTEXT_LIMIT", fmt.Sprintf("the project has %d interpretation libraries; the limit is %d", len(libraries), maxInterpretationContextLibraries), nil)
	}

	pinnedIDs, err := interpretation.ConfiguredPinnedRevisionIDs(workspace)
	if err != nil {
		return result, support.Conflict("interpretation-context", "AUTHORING_STATE_MISSING", "the saved Explorer draft contains an invalid pinned interpretation", nil, err)
	}
	revisionIDs := make(map[explorer.InterpretationRevisionID]struct{}, len(pinnedIDs)+len(libraries))
	for id := range pinnedIDs {
		revisionIDs[id] = struct{}{}
	}
	for _, library := range libraries {
		if library.Project != project {
			return result, support.Internal("interpretation-context", "INTERPRETATION_LIBRARY_INTEGRITY", "interpretation library crossed project scope", nil)
		}
		if library.HeadRevisionID != "" {
			revisionIDs[library.HeadRevisionID] = struct{}{}
		}
	}
	revisions, err := s.interpretationService.LoadRevisions(ctx, project, revisionIDs)
	if err != nil {
		return result, err
	}

	result.DraftVersion = owner.DraftVersion
	result.DraftDigest = owner.DraftDigest
	result.Libraries, err = s.interpretationService.LibrarySummaries(libraries, revisions)
	if err != nil {
		return result, err
	}
	result.PinnedRevisions = s.interpretationService.PinnedSummaries(pinnedIDs, revisions)
	headRevisions := s.interpretationService.HeadRevisions(libraries, revisions)
	for _, document := range workspace.Documents {
		for _, column := range document.Columns {
			if column.Source.Kind == authoringv2.SourceImported {
				continue
			}
			resolution, resolveErr := explorercompilation.ResolveInterpretationCandidate(document, column, authorized.Snapshot)
			if resolveErr != nil {
				return result, support.Conflict("interpretation-context", "STALE_COLUMN_ROUTE", "a configured column route no longer resolves in this authorized snapshot", nil, resolveErr)
			}
			entry := ConfiguredColumnContext{OutputID: document.Output.ID, Column: column.Column, OccurrenceID: column.OccurrenceID}
			match, ready := resolution.Match()
			if !ready {
				entry.Resolution = configuredColumnUnavailable{state: resolution.State(), reason: resolution.Reason()}
			} else {
				entry.Resolution = ConfiguredColumnReady{
					CapabilityCandidateIDs: append([]string(nil), match.CapabilityCandidateIDs...),
					ApplicableRevisionIDs:  s.interpretationService.ApplicableRevisionIDs(headRevisions, match.StructuralCandidate),
				}
			}
			result.Columns = append(result.Columns, entry)
		}
	}
	return result, nil
}
