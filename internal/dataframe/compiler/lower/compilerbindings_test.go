package lower_test

import (
	compiler "github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/testkit/compilerfixtures"
)

var CompileRecipeOutputWithPolicy = compiler.CompileRecipeOutputWithPolicy

var buildGenericPhysicalPlan = compilerfixtures.BuildGenericPhysicalPlan

var testSemanticField = compilerfixtures.TestSemanticField

var testSemanticFieldWithFallback = compilerfixtures.TestSemanticFieldWithFallback
