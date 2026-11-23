package compilerfixtures

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func ReshapeOracleUnpivotOutput(policy recipe.UnpivotNullRowPolicy, constructionID string) recipe.Output {
	return ReshapeOracleOutput("unpivot", &OracleReshape{
		Kind: OracleReshapeUnpivot,
		Unpivot: &OracleUnpivot{
			ConstructionID: constructionID,
			Inputs: []OracleUnpivotInput{
				{Column: "group_number", Key: ReshapeOracleString("z-integer")},
				{Column: "numeric_value", Key: ReshapeOracleString("a-decimal")},
			},
			KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: policy,
		},
	})
}
