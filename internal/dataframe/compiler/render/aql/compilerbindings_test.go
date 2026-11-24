package aql_test

import (
	compiler "github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/testkit/compilerfixtures"
)

var buildGenericPhysicalPlanWithContext = compilerfixtures.BuildGenericPhysicalPlanWithContext

var executeReshapeOracleQuery = compilerfixtures.ExecuteReshapeOracleQuery

var openConstructionReshapeArango = compilerfixtures.OpenConstructionReshapeArango

var extensionStringPointer = compilerfixtures.ExtensionStringPointer

var buildGenericPhysicalPlan = compilerfixtures.BuildGenericPhysicalPlan

var testSemanticField = compilerfixtures.TestSemanticField

var stringPtr = compilerfixtures.StringPtr

var mustPhysicalSelector = compilerfixtures.MustPhysicalSelector

var compilePopulationMappingOutput = compilerfixtures.CompilePopulationMappingOutput

var compileCurrentConstructionOracle = compilerfixtures.OutputCompiler(compiler.CompileRecipeOutputWithPolicy).CompileCurrentConstructionOracle

var currentOracleDerivedPivotOutput = compilerfixtures.CurrentOracleDerivedPivotOutput

var compileReshapeOracle = compilerfixtures.OutputCompiler(compiler.CompileRecipeOutputWithPolicy).CompileReshapeOracle

var reshapeOracleUnpivotOutput = compilerfixtures.ReshapeOracleUnpivotOutput

var insertConstructionReshapeRows = compilerfixtures.InsertConstructionReshapeRows
