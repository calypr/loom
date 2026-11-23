package recipe

import (
	"github.com/calypr/loom/internal/authscope"
)

type RuntimeBindings struct {
	Project string

	RecipeDigest      string
	DatasetGeneration string
	AuthResourcePaths []string
	AuthScopeMode     authscope.ReadScopeMode

	SelectionProject string

	SelectionMembersCollection string
	PreviewLimit               int

	AllowSampledAggregatePreview bool

	IncludeAuthResourcePath bool

	OutputNames []string

	IncludeRowIdentity bool

	IncludeSourceIdentity bool
}

func (b RuntimeBindings) Clone() RuntimeBindings {
	b.AuthResourcePaths = append([]string(nil), b.AuthResourcePaths...)
	b.OutputNames = append([]string(nil), b.OutputNames...)
	return b
}
