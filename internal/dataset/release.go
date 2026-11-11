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

// ProjectRelease is immutable. Active visibility is represented only by the
// release pointer revision stored alongside it. Inactive candidates remain
// retained for audit; this package does not delete release history.
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

// ErrPublicationRecordMissing marks a deterministic absence of the durable
// publication record for a selector. The publication store maps its not-found
// sentinel to this error so release activation can separate permanent contract
// misses (typed requirements failures, non-retryable) from transient
// verification-store faults (plain errors, retryable by clients).
var ErrPublicationRecordMissing = errors.New("publication record is missing for selector")

type PublicationVerifier interface {
	VerifyPublication(context.Context, string, string, DataframeSelector) (PublicationVerification, error)
}

type ManifestReader interface {
	ReadManifest(context.Context, Ref) (Manifest, error)
}

type ReleaseRepository interface {
	// SaveRelease retains the immutable candidate even when a later activation
	// compare-and-swap loses a revision race.
	SaveRelease(context.Context, ProjectRelease) (ProjectRelease, error)
	ReadActiveRelease(context.Context, string) (ActiveRelease, error)
	CompareAndSwapActivateRelease(context.Context, ProjectRelease, int64) (ActiveRelease, error)
}

// ActiveReleaseResolver exposes the immutable publication bindings selected by
// the active project release without granting readers mutation access.
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
	_ = previous // kept as fail-fast validation of the active pointer; the ID no longer folds the revision (F-B15).
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
			// A verification-store fault is not a contract verdict: keep it a
			// plain error so callers classify it retryable/unavailable instead
			// of telling the client to fix data that is already correct.
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
	// F-B15 repair: the ID is CONTENT-ADDRESSED (revision excluded). The
	// activation CAS's alreadyActive guard needs the SAME releaseId to make an
	// identical re-activation a no-op; folding the live revision into the ID
	// made every authoring click mint a new immutable document. Create stays
	// deterministic for byte-identical requests regardless of revision churn.
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

// releaseIDDomain namespaces every release identity so a later framing change
// can never collide with a digest an older binary derived from a narrower
// field set (the pre-widening encode hashed only Project, Generation and each
// publication's Selector.Key() plus ExecutionID).
const releaseIDDomain = "loom-release-id-v2|"

// releaseID fingerprints the content-addressed identity of a ProjectRelease.
// An identical re-submission reproduces the same id regardless of revision
// churn (F-B15), and a DISTINCT immutable snapshot always produces a distinct
// id.
//
// FIELD SCOPE (why every persisted immutable category must feed the digest).
// The stores key the immutable document on this id
// (internal/backends/arango/internal/dataset/release_store.go: releaseDocument["_key"] =
// releaseDocumentKey(release.ID)) and refuse to overwrite an existing key whose
// content differs, so the digest must separate exactly the fields the document
// persists. A framing over Project/Generation/(Selector.Key(), ExecutionID)
// only — the pre-widening shape — folded two genuinely different candidates:
// reclassifying the same selector under the same execution (Required true ->
// false, or a Stale carry-forward) kept every hashed field, so SaveRelease
// derived the EXISTING key and rejected the new candidate with "release already
// exists with different content" and the policy change could never be saved or
// activated. The digest now covers Project, GitCommit, Generation, every
// ReleasePublication field (Selector, ExecutionID, Generation, Required, Stale,
// VerifiedAt) and every ContractVerification field (Selector, ExecutionID,
// Generation, State, Queryable, VerifiedAt, ErrorCode). Only the derived ID and
// the creation timestamp stay excluded.
//
// Exclusions and canonicalization:
//   - CreatedAt is the first-creation timestamp and is excluded: both stores
//     keep the original document untouched on a re-save, so it cannot drift,
//     and folding it would make an identical snapshot retry mint a new identity.
//     Per-execution VerifiedAt values are NOT excluded — they are immutable
//     evidence carried inside the document.
//   - Publications and RequiredVerifications are hashed as SETS in canonical
//     Selector.Key() order (the order Create itself emits, release.go:229), so
//     equivalent submissions in different array order derive one id.
//   - Timestamps canonicalize to their UTC instant, so the same instant in
//     another zone (or carrying a monotonic reading) hashes alike while a
//     genuinely different instant does not.
//   - A nil and an empty RequiredVerifications slice are one service form.
//
// Injectivity (hardening, defense-in-depth). Every field is written
// length-prefixed, exactly like Selector.Key() (dataframe_selector.go:34) and
// BundleIdentity.PointerName() (F-B21,
// internal/dataframe/publication/bundle.go:100-115): a scalar becomes
// len:content, Selector.Key() is emitted verbatim because it already
// self-delimits, each repeated section is count-prefixed, and a bool is one
// fixed byte. No field content — NUL, ':', digits, or otherwise — can shift a
// boundary, so the encoder is injective by construction and cannot be broken
// even if an upstream validator is weakened. The archived fold classes (a NUL
// blurred across a bare join, an un-delimited ExecutionID impersonating the
// next publication group) are replayed against the pinned historical framing in
// release_id_framing_test.go, which asserts they still collapse there and are
// separated here.
//
// OUTPUT-STABILITY ANALYSIS (derived from the store code). Changing this
// function changes every digest, and releaseId IS persisted and re-compared
// across binary versions: internal/backends/arango/internal/dataset/release_store.go keys the
// document on it ("releaseDocument[\"_key\"] = releaseDocumentKey(release.ID)"
// at L17/L73), stores the id field, and patches it onto the pointers
// ("releaseKey: @release_key, releaseId: @release_id" at L176-177). It is
// re-compared against a freshly derived id in activateReleaseAQL:
//
// L162  LET alreadyActive = releasePointer != null AND releasePointer.releaseId == @release_id
// L165  FILTER ... storedRelease.id == @release_id ...
//
// Yet the only genuine CROSS-VERSION re-derivation is L162's alreadyActive
// idempotency guard, and readActiveReleaseAQL never re-derives the id — it
// follows the stored key ("LET release = DOCUMENT(@@lifecycle_collection,
// pointer.releaseKey)" at L151) — so every reader treats releaseId as an
// opaque immutable content label. Therefore a digest change cannot yield a
// wrong serve, orphan active content, or produce an incorrect verdict: at the
// upgrade boundary the sole effect is that an already-active release's first
// content-identical re-activation misses the L162 no-op and mints one
// duplicate, content-identical immutable document plus a single revision bump
// under the new id, after which alreadyActive hits again (SaveRelease keeps
// the old document untouched via its same-version `compatible` guard at
// L139-141). That bounded, self-converging, security-neutral churn is the
// whole cross-version cost, and no correctness or security guarantee depends
// on historical digests staying bit-stable, so — matching the F-B21 precedent
// which likewise made a persisted content-derived identity injective by
// construction — we adopt the widened length-prefixed encode.
func releaseID(release ProjectRelease) string {
	digest := sha256.New()
	_, _ = digest.Write(releaseIDFraming(release))
	return "release_" + hex.EncodeToString(digest.Sum(nil))
}

// releaseIDFraming renders the exact byte string hashed by releaseID so the
// injectivity controls can decode and round-trip it without duplicating the
// framing in the test. Length-prefixing every field and count-prefixing every
// repeated section makes the string uniquely decodable: read len digits, a ':',
// then len bytes, and repeat — Selector.Key() consumes three such prefixed
// sub-fields, so it needs no separator of its own.
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

// canonicalPublications returns the publication SET in canonical order,
// mirroring the ordering ReleaseService.Create applies, so equivalent
// submissions derive one digest. The tie-break on the whole binding keeps the
// order total when two bindings share a Selector.Key() (Create's map makes
// that unreachable, but a total order removes any reliance on sort stability).
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

// canonicalVerifications returns the verification SET in canonical order; nil
// and empty are one service form.
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

// canonicalReleaseTime renders one instant in exactly one way: the UTC
// RFC3339Nano form, with the zero instant as the empty string. Location and
// monotonic readings are transport detail, not immutable content.
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
