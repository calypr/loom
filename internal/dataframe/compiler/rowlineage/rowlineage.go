package rowlineage

import "github.com/calypr/loom/internal/dataframe/compiler"

type RowLineageCapability = compiler.RowLineageCapability
type RowLineageUnsupportedError = compiler.RowLineageUnsupportedError
type CompiledRowLineageQuery = compiler.CompiledRowLineageQuery

var (
	CompileRowLineageOutput       = compiler.CompileRowLineageOutput
	RowLineageCapabilityForOutput = compiler.RowLineageCapabilityForOutput
)
