package celltrace_test

import (
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

type oraclePivot = compilerfixtures.OraclePivot

type oraclePivotCategory = compilerfixtures.OraclePivotCategory

type oracleReshape = compilerfixtures.OracleReshape

const oracleReshapePivot = compilerfixtures.OracleReshapePivot

var reshapeOracleOutput = compilerfixtures.ReshapeOracleOutput

var reshapeOracleString = compilerfixtures.ReshapeOracleString

var reshapeOracleUnpivotOutput = compilerfixtures.ReshapeOracleUnpivotOutput

var constructionRelatedSourceCellTraceRecipeOutput = compilerfixtures.ConstructionRelatedSourceCellTraceRecipeOutput

var constructionOracleOutput = compilerfixtures.ConstructionOracleOutput
