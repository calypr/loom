package dataset

import (
	"encoding/json"
	"fmt"
)

// ManifestState is the lifecycle state of one immutable dataset generation.
type ManifestState string

const (
	ManifestStatePreflight  ManifestState = "PREFLIGHT"
	ManifestStateLoading    ManifestState = "LOADING"
	ManifestStateAnalyzing  ManifestState = "ANALYZING"
	ManifestStateReady      ManifestState = "READY"
	ManifestStateFailed     ManifestState = "FAILED"
	ManifestStateSuperseded ManifestState = "SUPERSEDED"
)

// AnalysisVersion is an opaque version emitted by the future analysis owner.
// The empty value is the intentional C1 placeholder: it means no finalized
// analysis version has been attached and must never be auto-filled by dataset
// lifecycle code.
type AnalysisVersion string

// NewAnalysisVersion validates a non-empty opaque analysis-version value.
func NewAnalysisVersion(value string) (AnalysisVersion, error) {
	version := AnalysisVersion(value)
	if !version.IsSet() {
		return "", fmt.Errorf("%w: value is required", ErrInvalidAnalysisVersion)
	}
	if err := version.Validate(); err != nil {
		return "", err
	}
	return version, nil
}

// IsSet reports whether a finalized analysis version is present.
func (v AnalysisVersion) IsSet() bool { return v != "" }

// Validate accepts the explicit empty placeholder or one canonical opaque
// version. It never creates a synthetic analysis version.
func (v AnalysisVersion) Validate() error {
	if err := validateOpaqueIdentifier("analysisVersion", string(v), true); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAnalysisVersion, err)
	}
	return nil
}

func (v AnalysisVersion) MarshalJSON() ([]byte, error) {
	if err := v.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(string(v))
}

func (v *AnalysisVersion) UnmarshalJSON(data []byte) error {
	if v == nil {
		return fmt.Errorf("%w: cannot unmarshal into nil AnalysisVersion", ErrInvalidAnalysisVersion)
	}
	if isJSONNull(data) {
		return fmt.Errorf("%w: JSON null is not an analysis version", ErrInvalidAnalysisVersion)
	}
	var value string
	if err := decodeStrictJSON(data, &value); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrInvalidAnalysisVersion, err)
	}
	version := AnalysisVersion(value)
	if err := version.Validate(); err != nil {
		return err
	}
	*v = version
	return nil
}

// Manifest records the lifecycle state and immutable schema metadata of one
// dataset generation. It is a value contract only; callers persist it and
// attach load/error metrics in their storage layer.
type Manifest struct {
	Dataset         DatasetRef             `json:"dataset"`
	State           ManifestState          `json:"state"`
	SchemaIdentity  SchemaIdentitySnapshot `json:"schemaIdentity"`
	AnalysisVersion AnalysisVersion        `json:"analysisVersion"`
}

// NewManifest starts a new generation in PREFLIGHT. Once a caller's external
// preflight succeeds, it must use Transition(ManifestStateLoading) before
// writing data or catalog records.
func NewManifest(ref DatasetRef, schema SchemaIdentitySnapshot) (Manifest, error) {
	manifest := Manifest{
		Dataset:        ref,
		State:          ManifestStatePreflight,
		SchemaIdentity: schema.Clone(),
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Clone returns an independent copy. SchemaIdentity's slice is copied even
// though it is not exposed directly, so future internal changes cannot alias
// a manifest's stored metadata.
func (m Manifest) Clone() Manifest {
	return Manifest{
		Dataset:         m.Dataset,
		State:           m.State,
		SchemaIdentity:  m.SchemaIdentity.Clone(),
		AnalysisVersion: m.AnalysisVersion,
	}
}

// Validate checks a manifest without making persistence or transaction claims.
func (m Manifest) Validate() error {
	if err := m.Dataset.Validate(); err != nil {
		return fmt.Errorf("%w: dataset: %w", ErrInvalidManifest, err)
	}
	if !m.State.valid() {
		return fmt.Errorf("%w: state %q is not recognized", ErrInvalidManifest, m.State)
	}
	if err := m.SchemaIdentity.Validate(); err != nil {
		return fmt.Errorf("%w: schemaIdentity: %w", ErrInvalidManifest, err)
	}
	if err := m.AnalysisVersion.Validate(); err != nil {
		return fmt.Errorf("%w: analysisVersion: %w", ErrInvalidManifest, err)
	}
	return nil
}

// Transition returns a new manifest in the requested state. It never mutates
// the source value and disallows terminal-state reactivation.
//
// Allowed transitions are:
//
//   - PREFLIGHT -> LOADING or FAILED
//   - LOADING -> ANALYZING or FAILED
//   - ANALYZING -> READY or FAILED
//   - READY -> SUPERSEDED
//
// FAILED and SUPERSEDED are terminal. A failed or superseded generation can
// never be transitioned back to LOADING, ANALYZING, or READY.
func (m Manifest) Transition(next ManifestState) (Manifest, error) {
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	if !canTransition(m.State, next) {
		return Manifest{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, m.State, next)
	}
	result := m.Clone()
	result.State = next
	if err := result.Validate(); err != nil {
		return Manifest{}, err
	}
	return result, nil
}

// WithAnalysisVersion attaches a finalized analysis version while a generation
// is in ANALYZING. The lifecycle package neither generates that value nor
// allows changing it after READY.
func (m Manifest) WithAnalysisVersion(version AnalysisVersion) (Manifest, error) {
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	if m.State != ManifestStateAnalyzing {
		return Manifest{}, fmt.Errorf("%w: analysisVersion can only be attached while state is %s", ErrInvalidTransition, ManifestStateAnalyzing)
	}
	if !version.IsSet() {
		return Manifest{}, fmt.Errorf("%w: value is required while attaching an analysis version", ErrInvalidAnalysisVersion)
	}
	if err := version.Validate(); err != nil {
		return Manifest{}, err
	}
	result := m.Clone()
	result.AnalysisVersion = version
	return result, nil
}

// IsReady reports whether this manifest is eligible to become the active
// generation. A caller that needs analysis facts must separately require a set
// AnalysisVersion; READY intentionally permits the C1 empty placeholder.
func (m Manifest) IsReady() bool {
	return m.Validate() == nil && m.State == ManifestStateReady
}

func (s ManifestState) valid() bool {
	switch s {
	case ManifestStatePreflight, ManifestStateLoading, ManifestStateAnalyzing,
		ManifestStateReady, ManifestStateFailed, ManifestStateSuperseded:
		return true
	default:
		return false
	}
}

func canTransition(from, to ManifestState) bool {
	switch from {
	case ManifestStatePreflight:
		return to == ManifestStateLoading || to == ManifestStateFailed
	case ManifestStateLoading:
		return to == ManifestStateAnalyzing || to == ManifestStateFailed
	case ManifestStateAnalyzing:
		return to == ManifestStateReady || to == ManifestStateFailed
	case ManifestStateReady:
		return to == ManifestStateSuperseded
	default:
		return false
	}
}

func (m Manifest) MarshalJSON() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(manifestWire{
		Dataset:         m.Dataset,
		State:           m.State,
		SchemaIdentity:  m.SchemaIdentity,
		AnalysisVersion: m.AnalysisVersion,
	})
}

func (m *Manifest) UnmarshalJSON(data []byte) error {
	if m == nil {
		return fmt.Errorf("%w: cannot unmarshal into nil Manifest", ErrInvalidManifest)
	}
	var decoded manifestWire
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrInvalidManifest, err)
	}
	manifest := Manifest{
		Dataset:         decoded.Dataset,
		State:           decoded.State,
		SchemaIdentity:  decoded.SchemaIdentity,
		AnalysisVersion: decoded.AnalysisVersion,
	}
	if err := manifest.Validate(); err != nil {
		return err
	}
	*m = manifest.Clone()
	return nil
}

type manifestWire struct {
	Dataset         DatasetRef             `json:"dataset"`
	State           ManifestState          `json:"state"`
	SchemaIdentity  SchemaIdentitySnapshot `json:"schemaIdentity"`
	AnalysisVersion AnalysisVersion        `json:"analysisVersion"`
}
