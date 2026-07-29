package dataset

import (
	"encoding/json"
	"fmt"
)

// ActiveGeneration is the project-level reference selected for reads. It is
// intentionally only a DatasetRef: resolving it against persisted manifests
// must prove that exactly one matching manifest is still READY.
type ActiveGeneration struct {
	Dataset DatasetRef `json:"dataset"`
}

// ActiveGenerationFor returns a reference only for a validated READY
// manifest. Failed, loading, preflight, and superseded generations cannot be
// activated or reactivated through this API.
func ActiveGenerationFor(manifest Manifest) (ActiveGeneration, error) {
	if err := manifest.Validate(); err != nil {
		return ActiveGeneration{}, err
	}
	if manifest.State != ManifestStateReady {
		return ActiveGeneration{}, fmt.Errorf("%w: %s is %s", ErrGenerationNotReady, manifest.Dataset.Generation, manifest.State)
	}
	return ActiveGeneration{Dataset: manifest.Dataset}, nil
}

// Validate checks only the reference's key representation. Read adapters must
// verify readiness against their persisted manifest record.
func (a ActiveGeneration) Validate() error {
	if err := a.Dataset.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidActiveGeneration, err)
	}
	return nil
}

// ActivationPlan is a persistence-neutral description of an active-generation
// switch. A storage adapter must atomically persist Active and, when Previous
// is non-nil, supersede that previous manifest in its own transaction.
//
// This value does not claim to perform an atomic switch itself.
type ActivationPlan struct {
	Active   ActiveGeneration `json:"active"`
	Previous *DatasetRef      `json:"previous,omitempty"`
}

// PlanActivation validates a READY candidate and an optional currently active
// READY manifest. The existing manifest is not mutated. If a replacement is
// needed, Previous names the generation a persistence adapter must supersede
// atomically with writing the new active reference.

// Validate checks that an activation plan is internally coherent. It does not
// verify that a storage transaction was executed.
func (p ActivationPlan) Validate() error {
	if err := p.Active.Validate(); err != nil {
		return err
	}
	if p.Previous == nil {
		return nil
	}
	if err := p.Previous.Validate(); err != nil {
		return fmt.Errorf("%w: previous: %w", ErrInvalidActiveGeneration, err)
	}
	if p.Previous.Project != p.Active.Dataset.Project {
		return fmt.Errorf("%w: previous and active projects differ", ErrInvalidActiveGeneration)
	}
	if p.Previous.Equal(p.Active.Dataset) {
		return fmt.Errorf("%w: previous and active generations must differ", ErrInvalidActiveGeneration)
	}
	return nil
}

func (a ActiveGeneration) MarshalJSON() ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(activeGenerationWire{Dataset: a.Dataset})
}

func (a *ActiveGeneration) UnmarshalJSON(data []byte) error {
	if a == nil {
		return fmt.Errorf("%w: cannot unmarshal into nil ActiveGeneration", ErrInvalidActiveGeneration)
	}
	var decoded activeGenerationWire
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrInvalidActiveGeneration, err)
	}
	value := ActiveGeneration{Dataset: decoded.Dataset}
	if err := value.Validate(); err != nil {
		return err
	}
	*a = value
	return nil
}

func (p ActivationPlan) MarshalJSON() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(activationPlanWire{Active: p.Active, Previous: p.Previous})
}

func (p *ActivationPlan) UnmarshalJSON(data []byte) error {
	if p == nil {
		return fmt.Errorf("%w: cannot unmarshal into nil ActivationPlan", ErrInvalidActiveGeneration)
	}
	var decoded activationPlanWire
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrInvalidActiveGeneration, err)
	}
	plan := ActivationPlan{Active: decoded.Active, Previous: decoded.Previous}
	if err := plan.Validate(); err != nil {
		return err
	}
	*p = plan
	return nil
}

type activeGenerationWire struct {
	Dataset DatasetRef `json:"dataset"`
}

type activationPlanWire struct {
	Active   ActiveGeneration `json:"active"`
	Previous *DatasetRef      `json:"previous,omitempty"`
}
