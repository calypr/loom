package capability

import "strings"

func (s Snapshot) MatchPopulationRouteEdge(edge Edge, currentID, currentType, targetType, relationship, storageDirection string) (Node, bool) {
	from, fromOK := s.Node(edge.FromNodeID)
	to, toOK := s.Node(edge.ToNodeID)
	direction := strings.ToUpper(strings.TrimSpace(edge.StorageDirection))
	return to, edge.ID != "" && edge.BlockedReason == "" && edge.FromNodeID == currentID &&
		fromOK && toOK && from.ResourceType == currentType && (edge.SourceResourceType == "" || edge.SourceResourceType == currentType) &&
		to.ResourceType == targetType && (edge.TargetResourceType == "" || edge.TargetResourceType == targetType) && edge.Label == relationship &&
		(direction == "" || direction == "INBOUND" || direction == "OUTBOUND") && (storageDirection == "" || direction == storageDirection)
}
