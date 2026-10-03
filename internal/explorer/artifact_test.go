package explorer

import "testing"

func TestArtifactIDIsStableAndRequiresExactIdentity(t *testing.T) {
	record := ArtifactRecord{Project: "study/project", ExplorerID: "explorer", RevisionID: "revision", OutputID: "patients", ReceiptID: "receipt", ExecutionID: "execution", DatasetGeneration: "generation-a", SchemaDigest: "schema-a", AuthorizationScopeDigest: "scope-a", IdempotencyKey: "request-1"}
	first, err := ArtifactID(record)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ArtifactID(record)
	if err != nil || second != first || len(first) != len("artifact_")+64 {
		t.Fatalf("artifact ids = %q/%q err=%v", first, second, err)
	}
	for _, changed := range []ArtifactRecord{
		{DatasetGeneration: "generation-b"},
		{SchemaDigest: "schema-b"},
		{AuthorizationScopeDigest: "scope-b"},
	} {
		variant := record
		if changed.DatasetGeneration != "" {
			variant.DatasetGeneration = changed.DatasetGeneration
		}
		if changed.SchemaDigest != "" {
			variant.SchemaDigest = changed.SchemaDigest
		}
		if changed.AuthorizationScopeDigest != "" {
			variant.AuthorizationScopeDigest = changed.AuthorizationScopeDigest
		}
		got, err := ArtifactID(variant)
		if err != nil || got == first {
			t.Fatalf("immutable identity change did not change artifact id: %q err=%v", got, err)
		}
	}
	record.OutputID = ""
	if _, err := ArtifactID(record); err == nil {
		t.Fatal("incomplete artifact identity was accepted")
	}
}
