package ir

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type ArtifactAQLQuery struct {
	Query    string
	BindVars map[string]any
}

func ArtifactAQLPlanDigest(queries ...ArtifactAQLQuery) string {
	return executionPlanDigest("aql", queries)
}

func ClickHouseCombinePlanDigest(plan PhysicalClickHouseCombine) string {
	return executionPlanDigest("clickhouse-combine", plan)
}

func executionPlanDigest(domain string, plan any) string {
	const version = "v2"
	encoded, err := json.Marshal(plan)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(append([]byte(version+"\x00"+domain+"\x00"), encoded...))
	return version + ":" + hex.EncodeToString(digest[:])
}
