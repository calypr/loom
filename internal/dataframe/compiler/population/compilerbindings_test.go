package population_test

import (
	compiler "github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/testkit/compilerfixtures"
)

var executeReshapeOracleQuery = compilerfixtures.ExecuteReshapeOracleQuery

var openConstructionReshapeArango = compilerfixtures.OpenConstructionReshapeArango

var CompileRecipeOutputWithPolicy = compiler.CompileRecipeOutputWithPolicy

var compilePopulationMappingOutput = compilerfixtures.CompilePopulationMappingOutput

var populationMappingTestBindings = compilerfixtures.PopulationMappingTestBindings

var groupedRelatedSummaryOutput = compilerfixtures.GroupedRelatedSummaryOutput
