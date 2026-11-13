package dataset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
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

var ErrPublicationRecordMissing = errors.New("publication record is missing for selector")

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

type ActiveReleaseResolver interface {
	ReadActiveRelease(context.Context, string) (ActiveRelease, error)
}

type ReleaseService struct {
	Manifests ManifestReader
	Releases  ReleaseRepository
	Verifier  PublicationVerifier
	Required  []DataframeSelector
	Now       func() time.Time
}

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
	_ = previous
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
		if verifyErr != nil && !errors.Is(verifyErr, ErrPublicationRecordMissing) {
			return ProjectRelease{}, fmt.Errorf("publication verification store fault for required selector %s: %w", selector.Key(), verifyErr)
		}
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
	release.ID = releaseID(release)
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

const releaseIDDomain = "loom-release-id-v2|"

func releaseID(release ProjectRelease) string {
	digest := sha256.New()
	_, _ = digest.Write(releaseIDFraming(release))
	return "release_" + hex.EncodeToString(digest.Sum(nil))
}

func releaseIDFraming(release ProjectRelease) []byte {
	var frame strings.Builder
	frame.WriteString(releaseIDDomain)
	writeLengthPrefixed(&frame, release.Project)
	writeLengthPrefixed(&frame, release.Generation)
	writeLengthPrefixed(&frame, release.GitCommit)
	publications := canonicalPublications(release.Publications)
	writeLengthPrefixed(&frame, strconv.Itoa(len(publications)))
	for _, publication := range publications {
		frame.WriteString(publication.Selector.Key())
		writeLengthPrefixed(&frame, publication.ExecutionID)
		writeLengthPrefixed(&frame, publication.Generation)
		writeBool(&frame, publication.Required)
		writeBool(&frame, publication.Stale)
		writeLengthPrefixed(&frame, canonicalReleaseTime(publication.VerifiedAt))
	}
	verifications := canonicalVerifications(release.RequiredVerifications)
	writeLengthPrefixed(&frame, strconv.Itoa(len(verifications)))
	for _, verification := range verifications {
		frame.WriteString(verification.Selector.Key())
		writeLengthPrefixed(&frame, verification.ExecutionID)
		writeLengthPrefixed(&frame, verification.Generation)
		writeLengthPrefixed(&frame, verification.State)
		writeBool(&frame, verification.Queryable)
		writeLengthPrefixed(&frame, canonicalReleaseTime(verification.VerifiedAt))
		writeLengthPrefixed(&frame, verification.ErrorCode)
	}
	return []byte(frame.String())
}

func canonicalPublications(publications []ReleasePublication) []ReleasePublication {
	out := append(make([]ReleasePublication, 0, len(publications)), publications...)
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i].Selector.Key(), out[j].Selector.Key()
		if left != right {
			return left < right
		}
		return releasePublicationSignature(out[i]) < releasePublicationSignature(out[j])
	})
	return out
}

func canonicalVerifications(verifications []ContractVerification) []ContractVerification {
	if len(verifications) == 0 {
		return nil
	}
	out := append(make([]ContractVerification, 0, len(verifications)), verifications...)
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i].Selector.Key(), out[j].Selector.Key()
		if left != right {
			return left < right
		}
		return contractVerificationSignature(out[i]) < contractVerificationSignature(out[j])
	})
	return out
}

func releasePublicationSignature(binding ReleasePublication) string {
	var signature strings.Builder
	writeLengthPrefixed(&signature, binding.ExecutionID)
	writeLengthPrefixed(&signature, binding.Generation)
	writeBool(&signature, binding.Required)
	writeBool(&signature, binding.Stale)
	writeLengthPrefixed(&signature, canonicalReleaseTime(binding.VerifiedAt))
	return signature.String()
}

func contractVerificationSignature(verification ContractVerification) string {
	var signature strings.Builder
	writeLengthPrefixed(&signature, verification.ExecutionID)
	writeLengthPrefixed(&signature, verification.Generation)
	writeLengthPrefixed(&signature, verification.State)
	writeBool(&signature, verification.Queryable)
	writeLengthPrefixed(&signature, canonicalReleaseTime(verification.VerifiedAt))
	writeLengthPrefixed(&signature, verification.ErrorCode)
	return signature.String()
}

func canonicalReleaseTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func writeLengthPrefixed(frame *strings.Builder, field string) {
	frame.WriteString(strconv.Itoa(len(field)))
	frame.WriteByte(':')
	frame.WriteString(field)
}

func writeBool(frame *strings.Builder, value bool) {
	if value {
		frame.WriteByte('1')
		return
	}
	frame.WriteByte('0')
}
