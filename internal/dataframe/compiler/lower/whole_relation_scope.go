package lower

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func issueWholeRelationScopeEvidence(outputID string, plan ir.PhysicalPlan, schema []CompiledOutputColumn) (*ir.WholeRelationScopeEvidence, *ir.WholeRelationScopeIdentity, error) {
	if !wholeRelationScopeCandidate(plan) {
		return nil, nil, nil
	}
	schemaDigest, err := compiledOutputSchemaDigest(schema)
	if err != nil {
		return nil, nil, err
	}
	project, _ := plan.BindVars["project"].(string)
	generation, _ := plan.BindVars["dataset_generation"].(string)
	paths, _ := plan.BindVars["auth_resource_paths"].([]string)
	unrestricted, _ := plan.BindVars["auth_resource_paths_unrestricted"].(bool)
	mode := "restricted"
	if unrestricted {
		mode = "unrestricted"
	}
	identity := ir.WholeRelationScopeIdentity{
		OutputID: outputID, Project: project, DatasetGeneration: generation,
		AuthScopeMode: mode, AuthResourcePaths: append([]string(nil), paths...), SchemaDigest: schemaDigest,
	}
	evidence, err := ir.NewWholeRelationScopeEvidence(plan, identity)
	if err != nil {
		// A typed plan can remain valid for ordinary execution while falling
		// outside the stronger sealed-capture proof contract. Omit evidence so
		// private grouped capture fails closed without changing query compilation.
		return nil, nil, nil
	}
	return &evidence, &identity, nil
}

func wholeRelationScopeCandidate(plan ir.PhysicalPlan) bool {
	// Ordinary AQL outputs can be captured as complete source relations too.
	// Workspace ClickHouse plans receive evidence only after their ordered
	// resolver and child-evidence operands have been validated.
	return plan.Engine == "" || plan.Engine == ir.PhysicalEngineAQL
}

func compiledOutputSchemaDigest(schema []CompiledOutputColumn) (string, error) {
	encoded, err := json.Marshal(schema)
	if err != nil {
		return "", fmt.Errorf("marshal finalized output schema for whole-relation evidence: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
