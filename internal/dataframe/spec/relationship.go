package spec

import (
	"fmt"
)

type TraversalMatchMode string

const (
	TraversalMatchOptional TraversalMatchMode = "OPTIONAL"
	TraversalMatchRequired TraversalMatchMode = "REQUIRED"
)

func (m TraversalMatchMode) Validate() error {
	switch m {
	case "", TraversalMatchOptional, TraversalMatchRequired:
		return nil
	default:
		return fmt.Errorf("unsupported traversal match mode %q", m)
	}
}

func (m TraversalMatchMode) required() bool {
	return m == TraversalMatchRequired
}

func (m TraversalMatchMode) Required() bool {
	return m.required()
}
