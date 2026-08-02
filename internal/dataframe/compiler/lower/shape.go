package lower

import "github.com/calypr/loom/internal/dataframe/semantic"

func genericPhysicalNodeUnavailableReason(node semantic.SemanticNode) string {
	for _, child := range node.Children {
		if reason := genericPhysicalNodeUnavailableReason(child); reason != "" {
			return reason
		}
	}
	return ""
}
