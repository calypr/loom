package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/projectid"
)

const (
	maxInterpretationContextColumns   = 512
	maxInterpretationContextLibraries = 256
)

type ConfiguredColumnContextRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
}

type InterpretationRevisionSummary struct {
	ID            string    `json:"id"`
	LibraryID     string    `json:"libraryId"`
	ContentDigest string    `json:"contentDigest"`
	Author        string    `json:"author"`
	Explanation   string    `json:"explanation"`
	CreatedAt     time.Time `json:"createdAt"`
}

type InterpretationLibrarySummary struct {
	ID             string                         `json:"id"`
	HeadRevisionID string                         `json:"headRevisionId,omitempty"`
	HeadDigest     string                         `json:"headDigest,omitempty"`
	Head           *InterpretationRevisionSummary `json:"head,omitempty"`
	UpdatedAt      time.Time                      `json:"updatedAt"`
}

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

func (s *Service) ConfiguredColumnContext(ctx context.Context, request ConfiguredColumnContextRequest) (ConfiguredColumnContextResult, error) {
	result := ConfiguredColumnContextResult{
		SnapshotToken: request.SnapshotToken,
		Libraries:     []InterpretationLibrarySummary{}, PinnedRevisions: []InterpretationRevisionSummary{}, Columns: []ConfiguredColumnContext{},
	}
	if err := validateConfiguredColumnContextRequest(request); err != nil {
		return result, malformed("interpretation-context", err.Error(), err)
	}
	if s.config.Capability.ForCompilation == nil {
		return result, unavailable("interpretation-context", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	repository, err := s.interpretationRepository()
	if err != nil {
		return result, err
	}
	project, err := explorer.CanonicalInterpretationProject(request.Project)
	if err != nil {
		return result, malformed("interpretation-context", err.Error(), err)
	}
	owner, err := s.store.Get(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return result, err
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return result, conflict("interpretation-context", "DRAFT_CONFLICT", "the Explorer draft changed; reload before resolving column mappings", nil, explorer.ErrDraftConflict)
	}
	workspace, err := previewWorkspace(ctx, s.store, owner)
	if err != nil {
		return result, conflict("interpretation-context", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be resolved", nil, err)
	}
	columnCount := 0
	for _, document := range workspace.Documents {
		columnCount += len(document.Columns)
	}
	if columnCount > maxInterpretationContextColumns {
		return result, unprocessable("interpretation-context", "INTERPRETATION_CONTEXT_LIMIT", fmt.Sprintf("the Explorer has %d configured columns; the limit is %d", columnCount, maxInterpretationContextColumns), nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(authorized.Snapshot.Identity.Project) != project {
		return result, conflict("interpretation-context", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return result, conflict("interpretation-context", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}

	libraries, err := repository.ListInterpretationLibraries(ctx, project)
	if err != nil {
		return result, internal("interpretation-context", "INTERPRETATION_LIBRARY_READ_FAILED", err.Error(), err)
	}
	if len(libraries) > maxInterpretationContextLibraries {
		return result, unprocessable("interpretation-context", "INTERPRETATION_CONTEXT_LIMIT", fmt.Sprintf("the project has %d interpretation libraries; the limit is %d", len(libraries), maxInterpretationContextLibraries), nil)
	}

	pinnedIDs, err := configuredPinnedRevisionIDs(workspace)
	if err != nil {
		return result, conflict("interpretation-context", "AUTHORING_STATE_MISSING", "the saved Explorer draft contains an invalid pinned interpretation", nil, err)
	}
	revisionIDs := make(map[explorer.InterpretationRevisionID]struct{}, len(pinnedIDs)+len(libraries))
	for id := range pinnedIDs {
		revisionIDs[id] = struct{}{}
	}
	for _, library := range libraries {
		if library.Project != project {
			return result, internal("interpretation-context", "INTERPRETATION_LIBRARY_INTEGRITY", "interpretation library crossed project scope", nil)
		}
		if library.HeadRevisionID != "" {
			revisionIDs[library.HeadRevisionID] = struct{}{}
		}
	}
	revisions, err := loadInterpretationContextRevisions(ctx, repository, project, revisionIDs)
	if err != nil {
		return result, err
	}

	result.DraftVersion = owner.DraftVersion
	result.DraftDigest = owner.DraftDigest
	result.Libraries, err = interpretationLibrarySummaries(libraries, revisions)
	if err != nil {
		return result, err
	}
	result.PinnedRevisions = pinnedInterpretationSummaries(pinnedIDs, revisions)
	headRevisions := interpretationHeadRevisions(libraries, revisions)
	for _, document := range workspace.Documents {
		for _, column := range document.Columns {
			resolution, resolveErr := explorercompilation.ResolveInterpretationCandidate(document, column, authorized.Snapshot)
			if resolveErr != nil {
				return result, conflict("interpretation-context", "STALE_COLUMN_ROUTE", "a configured column route no longer resolves in this authorized snapshot", nil, resolveErr)
			}
			entry := ConfiguredColumnContext{OutputID: document.Output.ID, Column: column.Column, OccurrenceID: column.OccurrenceID}
			match, ready := resolution.Match()
			if !ready {
				entry.Resolution = configuredColumnUnavailable{state: resolution.State(), reason: resolution.Reason()}
			} else {
				entry.Resolution = ConfiguredColumnReady{
					CapabilityCandidateIDs: append([]string(nil), match.CapabilityCandidateIDs...),
					ApplicableRevisionIDs:  applicableInterpretationRevisionIDs(headRevisions, match.StructuralCandidate),
				}
			}
			result.Columns = append(result.Columns, entry)
		}
	}
	return result, nil
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

func configuredPinnedRevisionIDs(workspace authoringv2.Workspace) (map[explorer.InterpretationRevisionID]struct{}, error) {
	ids := make(map[explorer.InterpretationRevisionID]struct{})
	for _, document := range workspace.Documents {
		for _, column := range document.Columns {
			if column.Interpretation == nil || column.Interpretation.Kind != authoringv2.FeatureInterpretationPinned {
				continue
			}
			if column.Interpretation.Pinned == nil {
				return nil, fmt.Errorf("%s/%s has a pinned interpretation without a revision", document.Output.ID, column.Column)
			}
			id, err := explorer.NewInterpretationRevisionID(strings.TrimSpace(column.Interpretation.Pinned.RevisionID))
			if err != nil {
				return nil, err
			}
			ids[id] = struct{}{}
		}
	}
	return ids, nil
}

func loadInterpretationContextRevisions(ctx context.Context, repository explorer.InterpretationRepository, project string, ids map[explorer.InterpretationRevisionID]struct{}) (map[explorer.InterpretationRevisionID]explorer.InterpretationRevision, error) {
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, string(id))
	}
	sort.Strings(ordered)
	result := make(map[explorer.InterpretationRevisionID]explorer.InterpretationRevision, len(ids))
	for _, rawID := range ordered {
		id := explorer.InterpretationRevisionID(rawID)
		revision, err := repository.GetInterpretationRevision(ctx, project, id)
		if errors.Is(err, explorer.ErrNotFound) {
			return nil, conflict("interpretation-context", "INTERPRETATION_REVISION_MISSING", "a referenced interpretation revision is no longer available", nil, err)
		}
		if err != nil {
			return nil, internal("interpretation-context", "INTERPRETATION_REVISION_READ_FAILED", err.Error(), err)
		}
		if revision == nil || revision.ID != id || revision.Project != project {
			return nil, internal("interpretation-context", "INTERPRETATION_REVISION_INTEGRITY", "interpretation revision crossed project scope", nil)
		}
		if err := revision.Validate(); err != nil {
			return nil, internal("interpretation-context", "INTERPRETATION_REVISION_INTEGRITY", "interpretation revision is invalid", err)
		}
		result[id] = *revision
	}
	return result, nil
}

func interpretationLibrarySummaries(libraries []explorer.InterpretationLibrary, revisions map[explorer.InterpretationRevisionID]explorer.InterpretationRevision) ([]InterpretationLibrarySummary, error) {
	result := make([]InterpretationLibrarySummary, 0, len(libraries))
	for _, library := range libraries {
		summary := InterpretationLibrarySummary{ID: string(library.ID), HeadRevisionID: string(library.HeadRevisionID), HeadDigest: string(library.HeadDigest), UpdatedAt: library.UpdatedAt}
		if library.HeadRevisionID != "" {
			revision, ok := revisions[library.HeadRevisionID]
			if !ok || revision.LibraryID != library.ID || revision.ContentDigest != library.HeadDigest {
				return nil, internal("interpretation-context", "INTERPRETATION_HEAD_INTEGRITY", "interpretation library head does not match its immutable revision", nil)
			}
			value := interpretationRevisionSummary(revision)
			summary.Head = &value
		}
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func pinnedInterpretationSummaries(ids map[explorer.InterpretationRevisionID]struct{}, revisions map[explorer.InterpretationRevisionID]explorer.InterpretationRevision) []InterpretationRevisionSummary {
	result := make([]InterpretationRevisionSummary, 0, len(ids))
	for id := range ids {
		result = append(result, interpretationRevisionSummary(revisions[id]))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func interpretationRevisionSummary(revision explorer.InterpretationRevision) InterpretationRevisionSummary {
	return InterpretationRevisionSummary{
		ID: string(revision.ID), LibraryID: string(revision.LibraryID), ContentDigest: string(revision.ContentDigest),
		Author: revision.Author, Explanation: revision.Explanation, CreatedAt: revision.CreatedAt,
	}
}

func interpretationHeadRevisions(libraries []explorer.InterpretationLibrary, revisions map[explorer.InterpretationRevisionID]explorer.InterpretationRevision) []explorer.InterpretationRevision {
	result := make([]explorer.InterpretationRevision, 0, len(libraries))
	for _, library := range libraries {
		if library.HeadRevisionID != "" {
			result = append(result, revisions[library.HeadRevisionID])
		}
	}
	return result
}

func applicableInterpretationRevisionIDs(revisions []explorer.InterpretationRevision, candidate explorer.InterpretationStructuralCandidate) []string {
	result := make([]string, 0, len(revisions))
	for _, revision := range revisions {
		if !revision.Applicability.Matches(candidate) {
			continue
		}
		if _, err := revision.SelectRule(candidate); err != nil {
			continue
		}
		result = append(result, string(revision.ID))
	}
	sort.Strings(result)
	return result
}
