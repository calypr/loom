package lifecycle

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	compilerprobe "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/explorer/capability"
)

// Generated identities bind a schema path to the authorized snapshot, rather
// than claiming that the path was observed in its population.
func schemaFieldCandidateID(snapshotToken, nodeID, path string) string {
	identity, _ := json.Marshal([3]string{snapshotToken, nodeID, path})
	return "schema_field." + base64.RawURLEncoding.EncodeToString(identity)
}

func resolveSchemaFieldCandidate(snapshot capability.Snapshot, candidateID string) (capability.Candidate, bool) {
	const prefix = "schema_field."
	if !strings.HasPrefix(candidateID, prefix) || len(candidateID) > 4096 {
		return capability.Candidate{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(candidateID, prefix))
	if err != nil {
		return capability.Candidate{}, false
	}
	var identity [3]string
	if json.Unmarshal(payload, &identity) != nil || identity[0] != snapshot.Token ||
		candidateID != schemaFieldCandidateID(identity[0], identity[1], identity[2]) {
		return capability.Candidate{}, false
	}
	node, ok := snapshot.Node(identity[1])
	if !ok || !node.Populated || !validSHA256Digest(snapshot.Identity.SchemaDigest) {
		return capability.Candidate{}, false
	}
	for _, field := range compilerprobe.SchemaFieldDescriptors(node.ResourceType) {
		if field.Path != identity[2] {
			continue
		}
		return capability.Candidate{
			ID: candidateID, NodeID: node.ID, ResourceType: node.ResourceType,
			FieldPath: field.Path, Label: node.ResourceType + "." + field.Path,
			LogicalType: field.PrimitiveType, Cardinality: field.Cardinality,
		}, true
	}
	return capability.Candidate{}, false
}
