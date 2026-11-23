package proposal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func ProposalDocument(workspace ws.Workspace, outputID string) *ws.Document {
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == outputID {
			return &workspace.Documents[index]
		}
	}
	return nil
}

func DocumentDigest(document ws.Document) (string, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode authoring document digest: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func RowIdentityForNetwork(document ws.Document, network authoringnetwork.AuthoringNetwork) ws.RowIdentityPlan {
	effective := document
	effective.Rows = network.SourceRows
	var route func(string) ws.RouteNode
	route = func(id string) ws.RouteNode {
		var result ws.RouteNode
		for _, occurrence := range network.Occurrences {
			if occurrence.OccurrenceID == id {
				result = ws.RouteNode{OccurrenceID: id, ResourceType: occurrence.ResourceType, CatalogEdgeID: occurrence.CatalogEdgeID, Relationship: occurrence.Relationship, MatchMode: occurrence.MatchMode}
				break
			}
		}
		for _, occurrence := range network.Occurrences {
			if occurrence.ParentOccurrenceID == id && occurrence.Enabled {
				result.Children = append(result.Children, route(occurrence.OccurrenceID))
			}
		}
		return result
	}
	effective.Route = route(ws.RootOccurrenceID)
	return ws.DescribeRowIdentity(effective)
}
