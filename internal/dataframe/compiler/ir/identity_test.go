package ir

import (
	"strings"
	"testing"
)

func TestArtifactPlanDigestVersionsAndBindsExecutionInputs(t *testing.T) {
	query := ArtifactAQLQuery{
		Query:    "FOR row IN data LIMIT @limit RETURN row",
		BindVars: map[string]any{"limit": 1},
	}
	digest := ArtifactAQLPlanDigest(query)
	if !strings.HasPrefix(digest, "v2:") {
		t.Fatalf("unexpected artifact plan digest %q", digest)
	}
	query.BindVars["limit"] = 2
	if changed := ArtifactAQLPlanDigest(query); changed == digest {
		t.Fatal("changing a limit bind reused the artifact plan digest")
	}
}
