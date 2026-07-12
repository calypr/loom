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
// call ResolveActive against their persisted manifest set to verify readiness.
func (a ActiveGeneration) Validate() error {
	if err := a.Dataset.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidActiveGeneration, err)
	}
	return nil
}

// ResolveActive returns the single READY manifest named by active. It rejects
// missing, duplicate, failed, loading, and superseded matches, which prevents
// a caller from silently reading a different generation.
func ResolveActive(active ActiveGeneration, manifests []Manifest) (Manifest, error) {
	if err := active.Validate(); err != nil {
		return Manifest{}, err
	}

	var matched *Manifest
	for index, manifest := range manifests {
		if !manifest.Dataset.Equal(active.Dataset) {
			continue
		}
		if err := manifest.Validate(); err != nil {
			return Manifest{}, fmt.Errorf("%w: matching manifest at index %d: %w", ErrInvalidActiveGeneration, index, err)
		}
		if matched != nil {
			return Manifest{}, fmt.Errorf("%w: multiple manifests match %s/%s", ErrInvalidActiveGeneration, active.Dataset.Project, active.Dataset.Generation)
		}
		copy := manifest.Clone()
		matched = &copy
	}
	if matched == nil {
		return Manifest{}, fmt.Errorf("%w: %s/%s was not found", ErrInvalidActiveGeneration, active.Dataset.Project, active.Dataset.Generation)
	}
	if matched.State != ManifestStateReady {
		return Manifest{}, fmt.Errorf("%w: %s/%s is %s", ErrGenerationNotReady, active.Dataset.Project, active.Dataset.Generation, matched.State)
	}
	return matched.Clone(), nil
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
func PlanActivation(current *Manifest, candidate Manifest) (ActivationPlan, error) {
	active, err := ActiveGenerationFor(candidate)
	if err != nil {
		return ActivationPlan{}, err
	}
	plan := ActivationPlan{Active: active}
	if current == nil {
		return plan, nil
	}
	if err := current.Validate(); err != nil {
		return ActivationPlan{}, err
	}
	if current.State != ManifestStateReady {
		return ActivationPlan{}, fmt.Errorf("%w: current generation %s is %s", ErrGenerationNotReady, current.Dataset.Generation, current.State)
	}
	if current.Dataset.Project != candidate.Dataset.Project {
		return ActivationPlan{}, fmt.Errorf("%w: current and candidate projects differ", ErrInvalidActiveGeneration)
	}
	if current.Dataset.Equal(candidate.Dataset) {
		return plan, nil
	}
	previous := current.Dataset
	plan.Previous = &previous
	return plan, nil
}

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
