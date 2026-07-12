package dataset

import (
	"encoding/json"
	"fmt"
)

// ReadBinding is the immutable identity envelope that a future analysis,
// discovery, dataframe, cache, or export adapter can carry after it has
// resolved a READY active generation. It does not itself query data or prove a
// persistence transaction occurred.
type ReadBinding struct {
	dataset              DatasetRef
	analysisVersion      AnalysisVersion
	authScopeFingerprint AuthScopeFingerprint
}

// BindActive constructs a generation-pinned, authorization-scoped read value.
// The supplied manifest must be the READY manifest named by active. The empty
// AnalysisVersion placeholder remains valid, but consumers that require an
// analysis snapshot must require binding.AnalysisVersion().IsSet().
func BindActive(active ActiveGeneration, manifest Manifest, scope AuthScopeFingerprint) (ReadBinding, error) {
	if err := active.Validate(); err != nil {
		return ReadBinding{}, err
	}
	if err := manifest.Validate(); err != nil {
		return ReadBinding{}, err
	}
	if !active.Dataset.Equal(manifest.Dataset) {
		return ReadBinding{}, fmt.Errorf("%w: active generation does not match manifest", ErrInvalidActiveGeneration)
	}
	if manifest.State != ManifestStateReady {
		return ReadBinding{}, fmt.Errorf("%w: %s/%s is %s", ErrGenerationNotReady, manifest.Dataset.Project, manifest.Dataset.Generation, manifest.State)
	}
	if err := scope.Validate(); err != nil {
		return ReadBinding{}, err
	}
	binding := ReadBinding{
		dataset:              manifest.Dataset,
		analysisVersion:      manifest.AnalysisVersion,
		authScopeFingerprint: scope,
	}
	if err := binding.Validate(); err != nil {
		return ReadBinding{}, err
	}
	return binding, nil
}

// Dataset returns the immutable generation selected for this binding.
func (b ReadBinding) Dataset() DatasetRef { return b.dataset }

// AnalysisVersion returns the opaque attached analysis version, or the empty
// C1 placeholder when the generation has no finalized analysis snapshot yet.
func (b ReadBinding) AnalysisVersion() AnalysisVersion { return b.analysisVersion }

// AuthScopeFingerprint returns the authorization-scope cache key without
// exposing raw authorization paths.
func (b ReadBinding) AuthScopeFingerprint() AuthScopeFingerprint {
	return b.authScopeFingerprint
}

// Validate checks a serialized binding's fields. It cannot prove that its
// DatasetRef is active without resolving it through the manifest store.
func (b ReadBinding) Validate() error {
	if err := b.dataset.Validate(); err != nil {
		return fmt.Errorf("%w: dataset: %w", ErrInvalidActiveGeneration, err)
	}
	if err := b.analysisVersion.Validate(); err != nil {
		return fmt.Errorf("%w: analysisVersion: %w", ErrInvalidActiveGeneration, err)
	}
	if err := b.authScopeFingerprint.Validate(); err != nil {
		return fmt.Errorf("%w: authScopeFingerprint: %w", ErrInvalidActiveGeneration, err)
	}
	return nil
}

func (b ReadBinding) MarshalJSON() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(readBindingWire{
		Dataset:              b.dataset,
		AnalysisVersion:      b.analysisVersion,
		AuthScopeFingerprint: b.authScopeFingerprint,
	})
}

func (b *ReadBinding) UnmarshalJSON(data []byte) error {
	if b == nil {
		return fmt.Errorf("%w: cannot unmarshal into nil ReadBinding", ErrInvalidActiveGeneration)
	}
	var decoded readBindingWire
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrInvalidActiveGeneration, err)
	}
	binding := ReadBinding{
		dataset:              decoded.Dataset,
		analysisVersion:      decoded.AnalysisVersion,
		authScopeFingerprint: decoded.AuthScopeFingerprint,
	}
	if err := binding.Validate(); err != nil {
		return err
	}
	*b = binding
	return nil
}

type readBindingWire struct {
	Dataset              DatasetRef           `json:"dataset"`
	AnalysisVersion      AnalysisVersion      `json:"analysisVersion"`
	AuthScopeFingerprint AuthScopeFingerprint `json:"authScopeFingerprint"`
}
