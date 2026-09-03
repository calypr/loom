package dataset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrNoActiveRelease           = errors.New("no active project release")
	ErrReleaseRequirementsUnmet  = errors.New("project release requirements are unmet")
	ErrReleaseActivationConflict = errors.New("project release activation compare-and-swap conflict")
)

type PublicationVerification struct {
	Selector      DataframeSelector `json:"selector"`
	ExecutionID   string            `json:"executionId"`
	Generation    string            `json:"generation"`
	State         string            `json:"state"`
	Queryable     bool              `json:"queryable"`
	VerifiedAt    time.Time         `json:"verifiedAt,omitempty"`
	PhysicalTable string            `json:"-"`
}

type ReleasePublication struct {
	Selector    DataframeSelector `json:"selector"`
	ExecutionID string            `json:"executionId"`
	Generation  string            `json:"generation"`
	Required    bool              `json:"required"`
	Stale       bool              `json:"stale"`
	VerifiedAt  time.Time         `json:"verifiedAt"`
}

type ContractVerification struct {
	Selector    DataframeSelector `json:"selector"`
	ExecutionID string            `json:"executionId,omitempty"`
	Generation  string            `json:"generation,omitempty"`
	State       string            `json:"state,omitempty"`
	Queryable   bool              `json:"queryable"`
	VerifiedAt  time.Time         `json:"verifiedAt,omitempty"`
	ErrorCode   string            `json:"errorCode,omitempty"`
}

// ProjectRelease is immutable. Active visibility is represented only by the
// release pointer revision stored alongside it.
type ProjectRelease struct {
	ID                    string                 `json:"id"`
	Project               string                 `json:"project"`
	GitCommit             string                 `json:"gitCommit"`
	Generation            string                 `json:"generation"`
	Publications          []ReleasePublication   `json:"publications"`
	RequiredVerifications []ContractVerification `json:"requiredVerifications"`
	CreatedAt             time.Time              `json:"createdAt"`
}

type ActiveRelease struct {
	Release  ProjectRelease `json:"release"`
	Revision int64          `json:"revision"`
}

type ActivationRequest struct {
	Project           string              `json:"-"`
	Generation        string              `json:"generation"`
	GitCommit         string              `json:"gitCommit"`
	ExpectedRevision  int64               `json:"expectedRevision"`
	OptionalSelectors []DataframeSelector `json:"optionalSelectors,omitempty"`
}

type ReleaseRequirementsError struct {
	Verifications []ContractVerification
}

func (e *ReleaseRequirementsError) Error() string {
	return fmt.Sprintf("%v: %d required dataframe selector(s) failed verification", ErrReleaseRequirementsUnmet, len(e.Verifications))
}
func (e *ReleaseRequirementsError) Unwrap() error { return ErrReleaseRequirementsUnmet }

type PublicationVerifier interface {
	VerifyPublication(context.Context, string, string, DataframeSelector) (PublicationVerification, error)
}

type ManifestReader interface {
	ReadManifest(context.Context, Ref) (Manifest, error)
}

type ReleaseRepository interface {
	SaveRelease(context.Context, ProjectRelease) (ProjectRelease, error)
	ReadActiveRelease(context.Context, string) (ActiveRelease, error)
	CompareAndSwapActivateRelease(context.Context, ProjectRelease, int64) (ActiveRelease, error)
}

type ReleaseService struct {
	Manifests ManifestReader
	Releases  ReleaseRepository
	Verifier  PublicationVerifier
	Required  []DataframeSelector
	Now       func() time.Time
}

// ValidateGeneration verifies the immutable graph-generation prerequisite used
// by release creation. Callers that perform expensive work before activating a
// release can use this as a preflight and avoid producing orphaned dataframe
// publications for a generation that cannot be activated.
func (s ReleaseService) ValidateGeneration(ctx context.Context, project, generation string) error {
	if s.Manifests == nil {
		return fmt.Errorf("manifest reader is required")
	}
	ref, err := NewRef(strings.TrimSpace(project), strings.TrimSpace(generation))
	if err != nil {
		return err
	}
	return s.validateGeneration(ctx, ref)
}

func (s ReleaseService) validateGeneration(ctx context.Context, ref Ref) error {
	manifest, err := s.Manifests.ReadManifest(ctx, ref)
	if err != nil {
		return err
	}
	if !manifest.IsStaged() {
		return fmt.Errorf("%w: generation %s manifest is %s", ErrReleaseRequirementsUnmet, ref.Generation, manifest.State)
	}
	return nil
}

func (s ReleaseService) Create(ctx context.Context, request ActivationRequest) (ProjectRelease, error) {
	if s.Manifests == nil || s.Releases == nil || s.Verifier == nil {
		return ProjectRelease{}, fmt.Errorf("release service dependencies are required")
	}
	ref, err := NewRef(strings.TrimSpace(request.Project), strings.TrimSpace(request.Generation))
	if err != nil {
		return ProjectRelease{}, err
	}
	if strings.TrimSpace(request.GitCommit) == "" {
		request.GitCommit = ref.Generation
	}
	if request.GitCommit != ref.Generation {
		return ProjectRelease{}, fmt.Errorf("gitCommit must match generation")
	}
	if err := s.validateGeneration(ctx, ref); err != nil {
		return ProjectRelease{}, err
	}

	previous, previousErr := s.Releases.ReadActiveRelease(ctx, ref.Project)
	if previousErr != nil && !errors.Is(previousErr, ErrNoActiveRelease) {
		return ProjectRelease{}, previousErr
	}
	currentRevision := int64(0)
	if previousErr == nil {
		currentRevision = previous.Revision
	}
	for _, selector := range s.Required {
		if err := selector.Validate(); err != nil {
			return ProjectRelease{}, fmt.Errorf("invalid required selector: %w", err)
		}
	}
	required := uniqueSelectors(s.Required)
	for _, selector := range request.OptionalSelectors {
		if err := selector.Validate(); err != nil {
			return ProjectRelease{}, fmt.Errorf("invalid optional selector: %w", err)
		}
	}
	optional := uniqueSelectors(request.OptionalSelectors)
	bindings := make(map[string]ReleasePublication, len(required)+len(optional))
	verifications := make([]ContractVerification, 0, len(required))
	failed := make([]ContractVerification, 0)
	for _, selector := range required {
		verification, verifyErr := s.Verifier.VerifyPublication(ctx, ref.Project, ref.Generation, selector)
		contract := contractVerification(selector, verification, verifyErr)
		verifications = append(verifications, contract)
		if verifyErr != nil || verification.Selector != selector || verification.State != "PUBLISHED" || !verification.Queryable || verification.Generation != ref.Generation {
			contract.ErrorCode = "RELEASE_REQUIREMENTS_UNMET"
			failed = append(failed, contract)
			continue
		}
		bindings[selector.Key()] = publicationBinding(verification, true, ref.Generation)
	}
	if len(failed) != 0 {
		return ProjectRelease{}, &ReleaseRequirementsError{Verifications: failed}
	}
	for _, selector := range optional {
		if _, requiredSelector := bindings[selector.Key()]; requiredSelector {
			continue
		}
		verification, verifyErr := s.Verifier.VerifyPublication(ctx, ref.Project, ref.Generation, selector)
		if verifyErr == nil && verification.Selector == selector && verification.State == "PUBLISHED" && verification.Queryable && verification.Generation == ref.Generation {
			bindings[selector.Key()] = publicationBinding(verification, false, ref.Generation)
		}
	}
	if previousErr == nil {
		for _, prior := range previous.Release.Publications {
			if _, replaced := bindings[prior.Selector.Key()]; replaced {
				continue
			}
			prior.Required = false
			prior.Stale = prior.Generation != ref.Generation
			bindings[prior.Selector.Key()] = prior
		}
	}
	publications := make([]ReleasePublication, 0, len(bindings))
	for _, binding := range bindings {
		publications = append(publications, binding)
	}
	sort.Slice(publications, func(i, j int) bool { return publications[i].Selector.Key() < publications[j].Selector.Key() })
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	release := ProjectRelease{
		Project: ref.Project, GitCommit: ref.Generation, Generation: ref.Generation,
		Publications: publications, RequiredVerifications: verifications, CreatedAt: now,
	}
	release.ID = releaseID(release, currentRevision+1)
	return s.Releases.SaveRelease(ctx, release)
}

func (s ReleaseService) Activate(ctx context.Context, request ActivationRequest) (ActiveRelease, error) {
	release, err := s.Create(ctx, request)
	if err != nil {
		return ActiveRelease{}, err
	}
	return s.Releases.CompareAndSwapActivateRelease(ctx, release, request.ExpectedRevision)
}

func (s ReleaseService) Active(ctx context.Context, project string) (ActiveRelease, error) {
	if s.Releases == nil {
		return ActiveRelease{}, fmt.Errorf("release repository is required")
	}
	return s.Releases.ReadActiveRelease(ctx, project)
}

func contractVerification(selector DataframeSelector, result PublicationVerification, err error) ContractVerification {
	verification := ContractVerification{Selector: selector, ExecutionID: result.ExecutionID, Generation: result.Generation, State: result.State, Queryable: result.Queryable, VerifiedAt: result.VerifiedAt}
	if err != nil {
		verification.ErrorCode = "PUBLICATION_FAILED"
	}
	return verification
}

func publicationBinding(result PublicationVerification, required bool, generation string) ReleasePublication {
	return ReleasePublication{Selector: result.Selector, ExecutionID: result.ExecutionID, Generation: result.Generation, Required: required, Stale: result.Generation != generation, VerifiedAt: result.VerifiedAt}
}

func uniqueSelectors(values []DataframeSelector) []DataframeSelector {
	byKey := make(map[string]DataframeSelector, len(values))
	for _, value := range values {
		if value.Validate() == nil {
			byKey[value.Key()] = value
		}
	}
	result := make([]DataframeSelector, 0, len(byKey))
	for _, value := range byKey {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key() < result[j].Key() })
	return result
}

func releaseID(release ProjectRelease, revision int64) string {
	digest := sha256.New()
	_, _ = fmt.Fprintf(digest, "%s\x00%s\x00%d", release.Project, release.Generation, revision)
	for _, publication := range release.Publications {
		_, _ = fmt.Fprintf(digest, "\x00%s\x00%s", publication.Selector.Key(), publication.ExecutionID)
	}
	return "release_" + hex.EncodeToString(digest.Sum(nil))
}
