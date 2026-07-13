package compiler

// genericPhysicalPlanUnavailableReason is semantic-shape validation that
// remains in the compiler facade until the lowerer is extracted.
func genericPhysicalPlanUnavailableReason(node SemanticNode) string {
	return genericPhysicalNodeUnavailableReason(node, true)
}

func genericPhysicalNodeUnavailableReason(node SemanticNode, root bool) string {
	for _, child := range node.Children {
		if reason := genericPhysicalNodeUnavailableReason(child, false); reason != "" {
			return reason
		}
	}
	return ""
}
