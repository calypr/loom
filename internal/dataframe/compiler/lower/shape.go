package lower

func genericPhysicalPlanUnavailableReason(node SemanticNode) string {
	return genericPhysicalNodeUnavailableReason(node)
}

func genericPhysicalNodeUnavailableReason(node SemanticNode) string {
	for _, child := range node.Children {
		if reason := genericPhysicalNodeUnavailableReason(child); reason != "" {
			return reason
		}
	}
	return ""
}
