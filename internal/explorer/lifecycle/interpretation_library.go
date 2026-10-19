package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/calypr/loom/internal/explorer"
)

func (s *Service) interpretationRepository() (explorer.InterpretationRepository, error) {
	if s == nil || s.config.InterpretationRepository == nil {
		return nil, unavailable("interpretations", "INTERPRETATION_UNAVAILABLE", "interpretation repository is not configured", nil)
	}
	return s.config.InterpretationRepository, nil
}

// ListInterpretationLibraries returns project-scoped libraries with their exact
// current head revision expanded. The head is a browsing affordance only; no
// compiler or workspace resolver consumes it as a mutable reference.
func (s *Service) ListInterpretationLibraries(ctx context.Context, rawProject string) (ListInterpretationLibrariesResult, error) {
	repository, err := s.interpretationRepository()
	if err != nil {
		return ListInterpretationLibrariesResult{}, err
	}
	project, err := explorer.CanonicalInterpretationProject(rawProject)
	if err != nil {
		return ListInterpretationLibrariesResult{}, malformed("interpretations", err.Error(), err)
	}
	libraries, err := repository.ListInterpretationLibraries(ctx, project)
	if err != nil {
		return ListInterpretationLibrariesResult{}, internal("interpretations", "INTERPRETATION_LIBRARY_READ_FAILED", err.Error(), err)
	}
	result := ListInterpretationLibrariesResult{Project: project, Libraries: make([]InterpretationLibraryView, 0, len(libraries))}
	for _, library := range libraries {
		if library.Project != project {
			return ListInterpretationLibrariesResult{}, internal("interpretations", "INTERPRETATION_LIBRARY_INTEGRITY", "interpretation library crossed project scope", nil)
		}
		view := InterpretationLibraryView{Library: library}
		if library.HeadRevisionID != "" {
			head, getErr := repository.GetInterpretationRevision(ctx, project, library.HeadRevisionID)
			if getErr != nil {
				return ListInterpretationLibrariesResult{}, internal("interpretations", "INTERPRETATION_HEAD_READ_FAILED", getErr.Error(), getErr)
			}
			if head == nil || head.Project != project || head.LibraryID != library.ID || head.ID != library.HeadRevisionID || head.ContentDigest != library.HeadDigest {
				return ListInterpretationLibrariesResult{}, internal("interpretations", "INTERPRETATION_HEAD_INTEGRITY", "interpretation library head does not match its immutable revision", nil)
			}
			view.Head = head
		}
		result.Libraries = append(result.Libraries, view)
	}
	return result, nil
}

func (s *Service) GetInterpretationRevision(ctx context.Context, request GetInterpretationRevisionRequest) (explorer.InterpretationRevision, error) {
	repository, err := s.interpretationRepository()
	if err != nil {
		return explorer.InterpretationRevision{}, err
	}
	project, err := explorer.CanonicalInterpretationProject(request.Project)
	if err != nil {
		return explorer.InterpretationRevision{}, malformed("interpretations", err.Error(), err)
	}
	revisionID, err := explorer.NewInterpretationRevisionID(strings.TrimSpace(request.RevisionID))
	if err != nil {
		return explorer.InterpretationRevision{}, malformed("interpretations", err.Error(), err)
	}
	revision, err := repository.GetInterpretationRevision(ctx, project, revisionID)
	if errors.Is(err, explorer.ErrNotFound) {
		return explorer.InterpretationRevision{}, notFound("interpretations", "INTERPRETATION_REVISION_NOT_FOUND", "interpretation revision was not found", err)
	}
	if err != nil {
		return explorer.InterpretationRevision{}, internal("interpretations", "INTERPRETATION_REVISION_READ_FAILED", err.Error(), err)
	}
	if revision == nil || revision.Project != project || revision.ID != revisionID {
		return explorer.InterpretationRevision{}, internal("interpretations", "INTERPRETATION_REVISION_INTEGRITY", "interpretation revision crossed project scope", nil)
	}
	return *revision, nil
}

// CreateInterpretationRevision prepares closed typed content and advances the
// repository head only if its expected parent still matches. A missing parent
// request explicitly means a root revision; callers revising an existing
// library must send its exact current head as the parent.
func (s *Service) CreateInterpretationRevision(ctx context.Context, request CreateInterpretationRevisionRequest) (explorer.InterpretationRevision, error) {
	repository, err := s.interpretationRepository()
	if err != nil {
		return explorer.InterpretationRevision{}, err
	}
	project, err := explorer.CanonicalInterpretationProject(request.Project)
	if err != nil {
		return explorer.InterpretationRevision{}, malformed("interpretations", err.Error(), err)
	}
	libraryID, err := explorer.NewInterpretationLibraryID(strings.TrimSpace(request.LibraryID))
	if err != nil {
		return explorer.InterpretationRevision{}, malformed("interpretations", err.Error(), err)
	}
	if strings.TrimSpace(request.Author) == "" {
		return explorer.InterpretationRevision{}, malformed("interpretations", "author is required", nil)
	}
	if strings.TrimSpace(request.Explanation) == "" {
		return explorer.InterpretationRevision{}, malformed("interpretations", "explanation is required", nil)
	}

	parentID := strings.TrimSpace(request.ParentRevisionID)
	var expectedParent *explorer.InterpretationRevisionID
	var parentDigest *explorer.InterpretationContentDigest
	if parentID != "" {
		parsedParent, parseErr := explorer.NewInterpretationRevisionID(parentID)
		if parseErr != nil {
			return explorer.InterpretationRevision{}, malformed("interpretations", parseErr.Error(), parseErr)
		}
		parent, getErr := repository.GetInterpretationRevision(ctx, project, parsedParent)
		if errors.Is(getErr, explorer.ErrNotFound) {
			return explorer.InterpretationRevision{}, conflict("interpretations", "INTERPRETATION_PARENT_CONFLICT", "the requested parent revision is not available", nil, getErr)
		}
		if getErr != nil {
			return explorer.InterpretationRevision{}, internal("interpretations", "INTERPRETATION_PARENT_READ_FAILED", getErr.Error(), getErr)
		}
		if parent == nil || parent.Project != project || parent.LibraryID != libraryID {
			return explorer.InterpretationRevision{}, conflict("interpretations", "INTERPRETATION_PARENT_CONFLICT", "the requested parent revision does not belong to this library", nil, nil)
		}
		expectedParent = &parsedParent
		digest := parent.ContentDigest
		parentDigest = &digest
	}
	prepared, err := explorer.PrepareInterpretationRevision(explorer.InterpretationRevision{
		Project: project, LibraryID: libraryID, ParentRevisionID: expectedParent, ParentDigest: parentDigest,
		Applicability: request.Applicability, Rules: append([]explorer.InterpretationRule(nil), request.Rules...),
		Author: request.Author, Explanation: request.Explanation,
	})
	if err != nil {
		return explorer.InterpretationRevision{}, unprocessable("interpretations", "INVALID_INTERPRETATION_REVISION", err.Error(), err)
	}
	created, err := repository.CreateInterpretationRevision(ctx, prepared, expectedParent)
	if errors.Is(err, explorer.ErrInterpretationParentConflict) {
		return explorer.InterpretationRevision{}, conflict("interpretations", "INTERPRETATION_PARENT_CONFLICT", "the interpretation library changed; reload before creating a revision", nil, err)
	}
	if err != nil {
		return explorer.InterpretationRevision{}, internal("interpretations", "INTERPRETATION_REVISION_CREATE_FAILED", "the interpretation revision could not be saved", err)
	}
	preparedForCompare := prepared
	createdForCompare := explorer.InterpretationRevision{}
	if created != nil {
		createdForCompare = *created
	}
	preparedForCompare.CreatedAt = time.Time{}
	createdForCompare.CreatedAt = time.Time{}
	if created == nil || created.Project != project || created.LibraryID != libraryID || !reflect.DeepEqual(createdForCompare, preparedForCompare) {
		return explorer.InterpretationRevision{}, internal("interpretations", "INTERPRETATION_REVISION_INTEGRITY", "interpretation repository returned content different from the prepared revision", nil)
	}
	return *created, nil
}
