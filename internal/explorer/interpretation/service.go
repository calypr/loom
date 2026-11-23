package interpretation

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"
)

type Repository interface {
	ListInterpretationLibraries(context.Context, string) ([]InterpretationLibrary, error)
	GetInterpretationRevision(context.Context, string, InterpretationRevisionID) (*InterpretationRevision, error)
	CreateInterpretationRevision(context.Context, InterpretationRevision, *InterpretationRevisionID) (*InterpretationRevision, error)
}

type ErrorFactory struct {
	Malformed          func(stage, message string, cause error) error
	Unavailable        func(stage, code, message string, cause error) error
	Conflict           func(stage, code, message string, details map[string]any, cause error) error
	NotFound           func(stage, code, message string, cause error) error
	Unprocessable      func(stage, code, message string, cause error) error
	Internal           func(stage, code, message string, cause error) error
	PersistenceFailure func(stage, message string, err error) error
}

type Config struct {
	Repository Repository
	Errors     ErrorFactory
	IsNotFound func(error) bool
}

type Service struct {
	repository Repository
	errors     ErrorFactory
	isNotFound func(error) bool
}

type InterpretationLibraryView struct {
	Library InterpretationLibrary
	Head    *InterpretationRevision
}

type ListInterpretationLibrariesResult struct {
	Project   string
	Libraries []InterpretationLibraryView
}

type GetInterpretationRevisionRequest struct {
	Project    string
	RevisionID string
}

type CreateInterpretationRevisionRequest struct {
	Project          string
	LibraryID        string
	ParentRevisionID string
	Applicability    InterpretationApplicability
	Rules            []InterpretationRule
	Explanation      string
	Author           string
}

func NewService(config Config) (*Service, error) {
	if config.IsNotFound == nil {
		config.IsNotFound = func(error) bool { return false }
	}
	if config.Errors.Malformed == nil {
		return nil, errors.New("interpretation error factory is required")
	}
	return &Service{repository: config.Repository, errors: config.Errors, isNotFound: config.IsNotFound}, nil
}

func (s *Service) repositoryOrError() (Repository, error) {
	if s == nil || s.repository == nil {
		return nil, s.errors.Unavailable("interpretations", "INTERPRETATION_UNAVAILABLE", "interpretation repository is not configured", nil)
	}
	return s.repository, nil
}

func (s *Service) ListInterpretationLibraries(ctx context.Context, rawProject string) (ListInterpretationLibrariesResult, error) {
	repository, err := s.repositoryOrError()
	if err != nil {
		return ListInterpretationLibrariesResult{}, err
	}
	project, err := CanonicalInterpretationProject(rawProject)
	if err != nil {
		return ListInterpretationLibrariesResult{}, s.errors.Malformed("interpretations", err.Error(), err)
	}
	libraries, err := repository.ListInterpretationLibraries(ctx, project)
	if err != nil {
		return ListInterpretationLibrariesResult{}, s.errors.PersistenceFailure("interpretations", "interpretation libraries could not be loaded", err)
	}
	result := ListInterpretationLibrariesResult{Project: project, Libraries: make([]InterpretationLibraryView, 0, len(libraries))}
	for _, library := range libraries {
		if library.Project != project {
			return ListInterpretationLibrariesResult{}, s.errors.Internal("interpretations", "INTERPRETATION_LIBRARY_INTEGRITY", "interpretation library crossed project scope", nil)
		}
		view := InterpretationLibraryView{Library: library}
		if library.HeadRevisionID != "" {
			head, getErr := repository.GetInterpretationRevision(ctx, project, library.HeadRevisionID)
			if getErr != nil {
				return ListInterpretationLibrariesResult{}, s.errors.PersistenceFailure("interpretations", "an interpretation library head could not be loaded", getErr)
			}
			if head == nil || head.Project != project || head.LibraryID != library.ID || head.ID != library.HeadRevisionID || head.ContentDigest != library.HeadDigest {
				return ListInterpretationLibrariesResult{}, s.errors.Internal("interpretations", "INTERPRETATION_HEAD_INTEGRITY", "interpretation library head does not match its immutable revision", nil)
			}
			view.Head = head
		}
		result.Libraries = append(result.Libraries, view)
	}
	return result, nil
}

func (s *Service) GetInterpretationRevision(ctx context.Context, request GetInterpretationRevisionRequest) (InterpretationRevision, error) {
	repository, err := s.repositoryOrError()
	if err != nil {
		return InterpretationRevision{}, err
	}
	project, err := CanonicalInterpretationProject(request.Project)
	if err != nil {
		return InterpretationRevision{}, s.errors.Malformed("interpretations", err.Error(), err)
	}
	revisionID, err := NewInterpretationRevisionID(strings.TrimSpace(request.RevisionID))
	if err != nil {
		return InterpretationRevision{}, s.errors.Malformed("interpretations", err.Error(), err)
	}
	revision, err := repository.GetInterpretationRevision(ctx, project, revisionID)
	if s.isNotFound(err) {
		return InterpretationRevision{}, s.errors.NotFound("interpretations", "INTERPRETATION_REVISION_NOT_FOUND", "interpretation revision was not found", err)
	}
	if err != nil {
		return InterpretationRevision{}, s.errors.PersistenceFailure("interpretations", "interpretation revision could not be loaded", err)
	}
	if revision == nil || revision.Project != project || revision.ID != revisionID {
		return InterpretationRevision{}, s.errors.Internal("interpretations", "INTERPRETATION_REVISION_INTEGRITY", "interpretation revision crossed project scope", nil)
	}
	return *revision, nil
}

func (s *Service) CreateInterpretationRevision(ctx context.Context, request CreateInterpretationRevisionRequest) (InterpretationRevision, error) {
	repository, err := s.repositoryOrError()
	if err != nil {
		return InterpretationRevision{}, err
	}
	project, err := CanonicalInterpretationProject(request.Project)
	if err != nil {
		return InterpretationRevision{}, s.errors.Malformed("interpretations", err.Error(), err)
	}
	libraryID, err := NewInterpretationLibraryID(strings.TrimSpace(request.LibraryID))
	if err != nil {
		return InterpretationRevision{}, s.errors.Malformed("interpretations", err.Error(), err)
	}
	if strings.TrimSpace(request.Author) == "" {
		return InterpretationRevision{}, s.errors.Malformed("interpretations", "author is required", nil)
	}
	if strings.TrimSpace(request.Explanation) == "" {
		return InterpretationRevision{}, s.errors.Malformed("interpretations", "explanation is required", nil)
	}
	parentID := strings.TrimSpace(request.ParentRevisionID)
	var expectedParent *InterpretationRevisionID
	var parentDigest *InterpretationContentDigest
	if parentID != "" {
		parsedParent, parseErr := NewInterpretationRevisionID(parentID)
		if parseErr != nil {
			return InterpretationRevision{}, s.errors.Malformed("interpretations", parseErr.Error(), parseErr)
		}
		parent, getErr := repository.GetInterpretationRevision(ctx, project, parsedParent)
		if s.isNotFound(getErr) {
			return InterpretationRevision{}, s.errors.Conflict("interpretations", "INTERPRETATION_PARENT_CONFLICT", "the requested parent revision is not available", nil, getErr)
		}
		if getErr != nil {
			return InterpretationRevision{}, s.errors.PersistenceFailure("interpretations", "an interpretation revision parent could not be loaded", getErr)
		}
		if parent == nil || parent.Project != project || parent.LibraryID != libraryID {
			return InterpretationRevision{}, s.errors.Conflict("interpretations", "INTERPRETATION_PARENT_CONFLICT", "the requested parent revision does not belong to this library", nil, nil)
		}
		expectedParent = &parsedParent
		digest := parent.ContentDigest
		parentDigest = &digest
	}
	prepared, err := PrepareInterpretationRevision(InterpretationRevision{
		Project: project, LibraryID: libraryID, ParentRevisionID: expectedParent, ParentDigest: parentDigest,
		Applicability: request.Applicability, Rules: append([]InterpretationRule(nil), request.Rules...),
		Author: request.Author, Explanation: request.Explanation,
	})
	if err != nil {
		return InterpretationRevision{}, s.errors.Unprocessable("interpretations", "INVALID_INTERPRETATION_REVISION", err.Error(), err)
	}
	created, err := repository.CreateInterpretationRevision(ctx, prepared, expectedParent)
	if errors.Is(err, ErrInterpretationParentConflict) {
		return InterpretationRevision{}, s.errors.Conflict("interpretations", "INTERPRETATION_PARENT_CONFLICT", "the interpretation library changed; reload before creating a revision", nil, err)
	}
	if err != nil {
		return InterpretationRevision{}, s.errors.PersistenceFailure("interpretations", "the interpretation revision could not be saved", err)
	}
	preparedForCompare := prepared
	createdForCompare := InterpretationRevision{}
	if created != nil {
		createdForCompare = *created
	}
	preparedForCompare.CreatedAt = time.Time{}
	createdForCompare.CreatedAt = time.Time{}
	if created == nil || created.Project != project || created.LibraryID != libraryID || !reflect.DeepEqual(createdForCompare, preparedForCompare) {
		return InterpretationRevision{}, s.errors.Internal("interpretations", "INTERPRETATION_REVISION_INTEGRITY", "interpretation repository returned content different from the prepared revision", nil)
	}
	return *created, nil
}
