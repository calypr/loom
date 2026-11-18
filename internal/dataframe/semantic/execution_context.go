package semantic

import "github.com/calypr/loom/internal/authscope"

type ExecutionContext struct {
	Project                    string
	DatasetGeneration          string
	AuthResourcePaths          []string
	AuthScopeMode              authscope.ReadScopeMode
	SelectionProject           string
	SelectionMembersCollection string
}
