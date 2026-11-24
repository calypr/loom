package celltrace_test

import (
	"github.com/calypr/loom/internal/testkit/recipefixtures"

	compiler "github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/testkit/compilerfixtures"
)

var compileCurrentConstructionOracle = compilerfixtures.OutputCompiler(compiler.CompileRecipeOutputWithPolicy).CompileCurrentConstructionOracle

var currentOracleSource = compilerfixtures.CurrentOracleSource

var executeReshapeOracleQuery = compilerfixtures.ExecuteReshapeOracleQuery

var insertReshapeAgreementFixture = compilerfixtures.InsertReshapeAgreementFixture

var currentOracleDerivedPivotOutput = compilerfixtures.CurrentOracleDerivedPivotOutput

var currentOraclePivotOutput = compilerfixtures.CurrentOraclePivotOutput

var currentOracleUnpivotOutput = compilerfixtures.CurrentOracleUnpivotOutput

var compilePopulationMappingOutput = compilerfixtures.CompilePopulationMappingOutput

var compileReshapeOracle = compilerfixtures.OutputCompiler(compiler.CompileRecipeOutputWithPolicy).CompileReshapeOracle

type oraclePivot = recipefixtures.OraclePivot

type oraclePivotCategory = recipefixtures.OraclePivotCategory

type oracleReshape = recipefixtures.OracleReshape

const oracleReshapePivot = recipefixtures.OracleReshapePivot

var reshapeOracleOutput = compilerfixtures.ReshapeOracleOutput

var reshapeOracleString = compilerfixtures.ReshapeOracleString

var reshapeOracleUnpivotOutput = compilerfixtures.ReshapeOracleUnpivotOutput

var constructionRelatedSourceCellTraceRecipeOutput = compilerfixtures.ConstructionRelatedSourceCellTraceRecipeOutput

var constructionOracleOutput = compilerfixtures.ConstructionOracleOutput
