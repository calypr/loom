package lower

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func relatedExpandIdentityColumnNames(stepID string) (string, string) {
	digest := sha256.Sum256([]byte(stepID))
	suffix := hex.EncodeToString(digest[:])
	return "__loom_related_parent_row_id_" + suffix, "__loom_related_terminal_id_" + suffix
}

func physicalValueExpression(value ir.PhysicalValue) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Value: &value,
	}
}

func constructionPhysicalRelatedRoute(route []recipe.ConstructionRelatedRouteStep) []ir.PhysicalStageRelatedRouteStep {
	result := make([]ir.PhysicalStageRelatedRouteStep, 0, len(route))
	for _, hop := range route {
		result = append(result, ir.PhysicalStageRelatedRouteStep{
			EdgeID: hop.EdgeID, FromNodeID: hop.FromNodeID, ToNodeID: hop.ToNodeID,
			FromResourceType: hop.FromResourceType, ToResourceType: hop.ToResourceType,
			Relationship: hop.Relationship, StorageDirection: hop.StorageDirection, MatchMode: hop.MatchMode,
		})
	}
	return result
}
