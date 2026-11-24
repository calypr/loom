package dataset

import "fmt"

const (
	IdentityFormatLegacy  = "sha256-full-v1"
	IdentityFormatCompact = "sha256-truncated-128-v2"
)

type State string

const (
	StateLoading State = "LOADING"
	StateStaged  State = "STAGED"

	StateReady  State = "READY"
	StateFailed State = "FAILED"
)

type Manifest struct {
	Dataset        Ref            `json:"dataset"`
	State          State          `json:"state"`
	SchemaIdentity SchemaSnapshot `json:"schemaIdentity"`
	IdentityFormat string         `json:"identityFormat,omitempty"`
}

func NewManifest(ref Ref, schema SchemaSnapshot) (Manifest, error) {
	manifest := Manifest{Dataset: ref, State: StateLoading, SchemaIdentity: schema, IdentityFormat: IdentityFormatCompact}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) Validate() error {
	if err := m.Dataset.Validate(); err != nil {
		return fmt.Errorf("%w: dataset: %w", ErrInvalidManifest, err)
	}
	if m.State != StateLoading && m.State != StateStaged && m.State != StateReady && m.State != StateFailed {
		return fmt.Errorf("%w: state %q is not recognized", ErrInvalidManifest, m.State)
	}
	if err := m.SchemaIdentity.Validate(); err != nil {
		return fmt.Errorf("%w: schemaIdentity: %w", ErrInvalidManifest, err)
	}
	if m.IdentityFormat != "" && m.IdentityFormat != IdentityFormatLegacy && m.IdentityFormat != IdentityFormatCompact {
		return fmt.Errorf("%w: identity format %q is not recognized", ErrInvalidManifest, m.IdentityFormat)
	}
	return nil
}

func (m Manifest) Transition(next State) (Manifest, error) {
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	if m.State != StateLoading || (next != StateStaged && next != StateFailed) {
		return Manifest{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, m.State, next)
	}
	m.State = next
	return m, nil
}

func (m Manifest) IsStaged() bool {
	return m.Validate() == nil && (m.State == StateStaged || m.State == StateReady)
}
