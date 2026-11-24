package aql

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func codedGroupIdentityExpression(coded *ir.PhysicalStageCodedGroup, system, version, code string) string {
	return fmt.Sprintf(
		"TO_STRING([\"construction\", @%s, \"operation\", \"CODED_GROUP\", \"occurrence\", @%s, \"path\", @%s, \"system\", %s, \"version\", %s, \"code\", %s])",
		coded.ConstructionIDBindKey, coded.OccurrenceIDBindKey, coded.CodingPathBindKey, system, version, code,
	)
}
