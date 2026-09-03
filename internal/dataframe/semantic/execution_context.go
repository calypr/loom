package semantic

import "github.com/calypr/loom/internal/authscope"

// ExecutionContext carries request-scoped provenance into physical lowering.
// It is deliberately separate from OutputPlan so runtime authorization and
// dataset selection can never become part of a persisted recipe or semantic
// output digest.
type ExecutionContext struct {
	Project           string
	DatasetGeneration string
	AuthResourcePaths []string
	AuthScopeMode     authscope.ReadScopeMode
}
