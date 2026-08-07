package dataset

import (
	"fmt"
	"strings"
)

// Ref names one immutable generation in one project.
type Ref struct {
	Project    string `json:"project"`
	Generation string `json:"generation"`
}

func NewRef(project, generation string) (Ref, error) {
	// Clone transport-provided strings before persisting them. Fiber/fasthttp
	// path parameter strings may otherwise alias a request buffer that is reused
	// after the handler returns.
	ref := Ref{Project: strings.Clone(project), Generation: strings.Clone(generation)}
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
