package dataframe

import (
	"fmt"
)

// TraversalMatchMode controls whether a relationship contributes values to a
// dataframe row only, or must exist for that root row to be included at all.
//
// OPTIONAL is the legacy behavior: a missing child yields an empty child
// projection but does not remove the root row. REQUIRED is intentionally
// opt-in and lowers to a root-scoped semi-join. The empty value is OPTIONAL so
// existing Builder callers retain their current behavior.
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
