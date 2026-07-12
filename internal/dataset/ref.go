package dataset

import (
	"encoding/json"
	"fmt"
)

// DatasetRef names one immutable dataset generation in one project.
// Generation is intentionally opaque: callers must not assume it is a UUID,
// timestamp, counter, or sortable value.
type DatasetRef struct {
	Project    string `json:"project"`
	Generation string `json:"generation"`
}

// NewDatasetRef returns a validated, canonical project/generation reference.
// It preserves valid identifiers exactly; it never invents or rewrites a
// generation value.
func NewDatasetRef(project, generation string) (DatasetRef, error) {
	ref := DatasetRef{Project: project, Generation: generation}
	if err := ref.Validate(); err != nil {
		return DatasetRef{}, err
	}
	return ref, nil
}

// Validate checks the stable key representation used by manifests, active
// references, and generation-bound reads.
func (r DatasetRef) Validate() error {
	if err := validateOpaqueIdentifier("project", r.Project, false); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDatasetRef, err)
	}
	if err := validateOpaqueIdentifier("generation", r.Generation, false); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDatasetRef, err)
	}
	return nil
}

// Equal reports whether two references name exactly the same generation.
func (r DatasetRef) Equal(other DatasetRef) bool {
	return r.Project == other.Project && r.Generation == other.Generation
}

func (r DatasetRef) MarshalJSON() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	type wire DatasetRef
	return json.Marshal(wire(r))
}

func (r *DatasetRef) UnmarshalJSON(data []byte) error {
	if r == nil {
		return fmt.Errorf("%w: cannot unmarshal into nil DatasetRef", ErrInvalidDatasetRef)
	}
	type wire DatasetRef
	var decoded wire
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrInvalidDatasetRef, err)
	}
	value := DatasetRef(decoded)
	if err := value.Validate(); err != nil {
		return err
	}
	*r = value
	return nil
}
