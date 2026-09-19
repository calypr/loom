package explorer

import "testing"

func TestArtifactIDIsStableAndRequiresExactIdentity(t *testing.T) {
	record := ArtifactRecord{Project: "study/project", ExplorerID: "explorer", RevisionID: "revision", OutputID: "patients", ReceiptID: "receipt", ExecutionID: "execution", IdempotencyKey: "request-1"}
	first, err := ArtifactID(record)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ArtifactID(record)
	if err != nil || second != first || len(first) != len("artifact_")+64 {
		t.Fatalf("artifact ids = %q/%q err=%v", first, second, err)
	}
	record.OutputID = ""
	if _, err := ArtifactID(record); err == nil {
		t.Fatal("incomplete artifact identity was accepted")
	}
}
