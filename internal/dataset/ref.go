package dataset

import "fmt"

// Ref names one immutable generation in one project.
type Ref struct {
	Project    string `json:"project"`
	Generation string `json:"generation"`
}

func NewRef(project, generation string) (Ref, error) {
	ref := Ref{Project: project, Generation: generation}
	if err := ref.Validate(); err != nil {
		return Ref{}, err
	}
	return ref, nil
}

func (r Ref) Validate() error {
	if err := validateOpaqueIdentifier("project", r.Project); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDatasetRef, err)
	}
	if err := validateOpaqueIdentifier("generation", r.Generation); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDatasetRef, err)
	}
	return nil
}
