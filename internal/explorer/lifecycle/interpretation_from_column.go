package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/projectid"
)

type CreateInterpretationRevisionFromColumnRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	Column               string
	LibraryID            string
	ParentRevisionID     string
	Explanation          string
	Author               string
}

func (s *Service) CreateInterpretationRevisionFromColumn(ctx context.Context, request CreateInterpretationRevisionFromColumnRequest) (explorer.InterpretationRevision, error) {
	if err := validateCreateInterpretationFromColumnRequest(request); err != nil {
		return explorer.InterpretationRevision{}, malformed("interpretation-create", err.Error(), err)
	}
	if s.config.Capability.ForCompilation == nil {
		return explorer.InterpretationRevision{}, unavailable("interpretation-create", "CAPABILITY_UNAVAILABLE", "authorized capability resolution is not configured", nil)
	}
	project, err := explorer.CanonicalInterpretationProject(request.Project)
	if err != nil {
		return explorer.InterpretationRevision{}, malformed("interpretation-create", err.Error(), err)
	}
	owner, err := s.store.Get(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return explorer.InterpretationRevision{}, err
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return explorer.InterpretationRevision{}, conflict("interpretation-create", "DRAFT_CONFLICT", "the Explorer draft changed; reload before creating a mapping", nil, explorer.ErrDraftConflict)
	}
	workspace, err := previewWorkspace(ctx, s.store, owner)
	if err != nil {
		return explorer.InterpretationRevision{}, conflict("interpretation-create", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be resolved", nil, err)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(authorized.Snapshot.Identity.Project) != project {
		return explorer.InterpretationRevision{}, conflict("interpretation-create", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return explorer.InterpretationRevision{}, conflict("interpretation-create", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	document := findSemanticOutput(workspace, request.OutputID)
	if document == nil {
		return explorer.InterpretationRevision{}, notFound("interpretation-create", "COLUMN_NOT_FOUND", "the requested table does not exist", nil)
	}
	column := configuredColumn(document, request.Column)
	if column == nil {
		return explorer.InterpretationRevision{}, notFound("interpretation-create", "COLUMN_NOT_FOUND", "the requested column does not exist in this table", nil)
	}
	if column.Interpretation != nil && column.Interpretation.Kind == authoringv2.FeatureInterpretationPinned {
		return explorer.InterpretationRevision{}, conflict("interpretation-create", "INTERPRETATION_ALREADY_PINNED", "create a mapping from an unpinned saved column", nil, nil)
	}
	resolution, err := explorercompilation.ResolveInterpretationCandidate(*document, *column, authorized.Snapshot)
	if err != nil {
		return explorer.InterpretationRevision{}, conflict("interpretation-create", "STALE_COLUMN_ROUTE", "the configured column route no longer resolves in this authorized snapshot", nil, err)
	}
	match, ready := resolution.Match()
	if !ready {
		return explorer.InterpretationRevision{}, unprocessable("interpretation-create", "INTERPRETATION_SOURCE_UNAVAILABLE", fmt.Sprintf("the configured column source is %s: %s", resolution.State(), resolution.Reason()), nil)
	}

	definition := explorer.InterpretationFeatureDefinition{Source: column.Source.Normalized()}
	if column.Contributor != nil {
		contributor := column.Contributor.Normalized()
		definition.Contributor = &contributor
	}
	candidate := match.StructuralCandidate
	return s.CreateInterpretationRevision(ctx, CreateInterpretationRevisionRequest{
		Project: request.Project, LibraryID: request.LibraryID, ParentRevisionID: request.ParentRevisionID,
		Applicability: explorer.InterpretationApplicability{
			ResourceTypes: singletonInterpretationDimension(candidate.ResourceType),
			LogicalTypes:  singletonInterpretationDimension(candidate.LogicalType),
			Cardinalities: singletonInterpretationDimension(candidate.Cardinality),
		},
		Rules: []explorer.InterpretationRule{{
			ID: "rule",
			Match: explorer.InterpretationStructuralMatch{
				ResourceType: candidate.ResourceType, SourceProfile: candidate.SourceProfile, SourceCanonical: candidate.SourceCanonical,
				OwningScope: candidate.OwningScope, System: candidate.System, Code: candidate.Code,
				ExtensionURLPath: append([]string(nil), candidate.ExtensionURLPath...), LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality,
			},
			Definition: definition,
		}},
		Explanation: request.Explanation, Author: request.Author,
	})
}

func validateCreateInterpretationFromColumnRequest(request CreateInterpretationRevisionFromColumnRequest) error {
	values := map[string]string{
		"project": request.Project, "explorerId": request.ExplorerID, "snapshotToken": request.SnapshotToken,
		"expectedDraftDigest": request.ExpectedDraftDigest, "outputId": request.OutputID, "column": request.Column,
		"libraryId": request.LibraryID, "explanation": request.Explanation, "author": request.Author,
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

func configuredColumn(document *authoringv2.Document, name string) *authoringv2.Column {
	if document == nil {
		return nil
	}
	for index := range document.Columns {
		if document.Columns[index].Column == name {
			return &document.Columns[index]
		}
	}
	return nil
}

func singletonInterpretationDimension(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return []string{value}
}
