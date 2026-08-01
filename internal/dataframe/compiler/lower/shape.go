package lower

import "github.com/calypr/loom/internal/dataframe/semantic"

func genericPhysicalPlanUnavailableReason(node semantic.SemanticNode) string {
	return genericPhysicalNodeUnavailableReason(node)
}

func genericPhysicalNodeUnavailableReason(node semantic.SemanticNode) string {
	for _, child := range node.Children {
		if reason := genericPhysicalNodeUnavailableReason(child); reason != "" {
			return reason
		}
	}
	return ""
}
