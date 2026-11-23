package proposal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func ProposalDocument(workspace authoringv2.Workspace, outputID string) *authoringv2.Document {
	for index := range workspace.Documents {
		if workspace.Documents[index].Output.ID == outputID {
			return &workspace.Documents[index]
		}
	}
	return nil
}

func DocumentDigest(document authoringv2.Document) (string, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode authoring document digest: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func RowIdentityForNetwork(document authoringv2.Document, network authoringv2.AuthoringNetwork) authoringv2.RowIdentityPlan {
	effective := document
	effective.Rows = network.SourceRows
	var route func(string) authoringv2.RouteNode
	route = func(id string) authoringv2.RouteNode {
		var result authoringv2.RouteNode
		for _, occurrence := range network.Occurrences {
			if occurrence.OccurrenceID == id {
				result = authoringv2.RouteNode{OccurrenceID: id, ResourceType: occurrence.ResourceType, CatalogEdgeID: occurrence.CatalogEdgeID, Relationship: occurrence.Relationship, MatchMode: occurrence.MatchMode}
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
	effective.Route = route(authoringv2.RootOccurrenceID)
	return authoringv2.DescribeRowIdentity(effective)
}
