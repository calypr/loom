package compilerfixtures

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/testkit/recipefixtures"
)

func ReshapeOracleUnpivotOutput(policy recipe.UnpivotNullRowPolicy, constructionID string) recipe.Output {
	return ReshapeOracleOutput("unpivot", &recipefixtures.OracleReshape{
		Kind: recipefixtures.OracleReshapeUnpivot,
		Unpivot: &recipefixtures.OracleUnpivot{
			ConstructionID: constructionID,
			Inputs: []recipefixtures.OracleUnpivotInput{
				{Column: "group_number", Key: ReshapeOracleString("z-integer")},
				{Column: "numeric_value", Key: ReshapeOracleString("a-decimal")},
			},
			KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: policy,
		},
	})
}
