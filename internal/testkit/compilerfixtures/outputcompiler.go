package compilerfixtures

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type OutputCompiler func(lower.CompiledRecipeOutput, recipe.RuntimeBindings, int, ir.PhysicalOptimizationPolicy) (model.CompiledQuery, error)
