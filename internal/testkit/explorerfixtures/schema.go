package explorerfixtures

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/capability"
)

func SchemaFieldsSnapshot(scope authscope.ReadScope, candidates []capability.Candidate) capability.Snapshot {
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	scopeHash := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	identity := capability.SnapshotIdentity{
		Project: "loom_dev_cda_fhir", Generation: "cda-fhir-v1",
		AuthorizationScopeDigest: hex.EncodeToString(scopeHash[:]), SchemaDigest: strings.Repeat("a", 64),
		ResourceInventoryDigest: "inventory", RelationshipDigest: "relationships", FieldDigest: "fields",
		ProtocolVersion: "protocol", CompilerVersion: "compiler", TraversalPolicyVersion: "route", ProjectionPolicyVersion: "projection",
	}
	return capability.NewSnapshot(identity, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{
		{ID: "patient-node", ResourceType: "Patient", Populated: true},
		{ID: "observation-node", ResourceType: "Observation", Populated: true},
	}, nil, candidates, nil)
}
