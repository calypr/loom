package publication

import "fmt"

type State string

const (
	StateLoading State = "LOADING"
	StateReady   State = "READY"
	StateFailed  State = "FAILED"
)

type Manifest struct {
	Dataset        Ref            `json:"dataset"`
	State          State          `json:"state"`
	SchemaIdentity SchemaSnapshot `json:"schemaIdentity"`
}

func NewManifest(ref Ref, schema SchemaSnapshot) (Manifest, error) {
	manifest := Manifest{Dataset: ref, State: StateLoading, SchemaIdentity: schema}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) Validate() error {
	if err := m.Dataset.Validate(); err != nil {
		return fmt.Errorf("%w: dataset: %w", ErrInvalidManifest, err)
	}
	if m.State != StateLoading && m.State != StateReady && m.State != StateFailed {
		return fmt.Errorf("%w: state %q is not recognized", ErrInvalidManifest, m.State)
	}
	if err := m.SchemaIdentity.Validate(); err != nil {
		return fmt.Errorf("%w: schemaIdentity: %w", ErrInvalidManifest, err)
	}
	return nil
}

func (m Manifest) Transition(next State) (Manifest, error) {
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	if m.State != StateLoading || (next != StateReady && next != StateFailed) {
		return Manifest{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, m.State, next)
	}
	m.State = next
	return m, nil
}

func (m Manifest) IsReady() bool { return m.Validate() == nil && m.State == StateReady }
