package benchmark

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Ruling-B conditions: the authoring publish must consume its materialization
// evidence (ii), activation-style refusals are fatal everywhere (iii), and
// the two arms must prove matched state before any timing (i).

func TestPublishReceiptVerifiesMaterializationIdentity(t *testing.T) {
	f := newFakeArm(BackendArango)
	defer f.close()
	arm := f.arm(BackendArango)
	pub, err := arm.PublishReceipt(context.Background(), "Bearer x", "proj", "exp-1", "rcpt-out")
	if err != nil {
		t.Fatal(err)
	}
	if pub.ReceiptID != "rcpt-out" || pub.RevisionID == "" || len(pub.Outputs) != 1 || pub.Outputs[0].MaterializationID == "" {
		t.Fatalf("verified publication lost identity: %+v", pub)
	}
	calls := f.calls
	found := false
	for _, c := range calls {
		if c == BackendArango+":publish:rcpt-out" {
			found = true
		}
	}
	if !found {
		t.Fatalf("publish route not called: %v", calls)
	}

	// A READY-less output is a hard failure: materialization evidence must
	// be consumed, never accepted decoratively.
	f.mu.Lock()
	f.publishOutputs = []PublicationOutput{{OutputID: "out", State: "PENDING", MaterializationID: "mat-1"}}
	f.mu.Unlock()
	if _, err := arm.PublishReceipt(context.Background(), "Bearer x", "proj", "exp-1", "rcpt-out"); err == nil || !strings.Contains(err.Error(), "PENDING") {
		t.Fatalf("non-READY output must fail: %v", err)
	}
	f.mu.Lock()
	f.publishOutputs = []PublicationOutput{{OutputID: "out", State: "READY"}}
	f.mu.Unlock()
	if _, err := arm.PublishReceipt(context.Background(), "Bearer x", "proj", "exp-1", "rcpt-out"); err == nil || !strings.Contains(err.Error(), "materialization identity") {
		t.Fatalf("output without materialization id must fail: %v", err)
	}
	f.mu.Lock()
	f.publishOutputs = nil
	f.publishEchoBad = true
	f.mu.Unlock()
	if _, err := arm.PublishReceipt(context.Background(), "Bearer x", "proj", "exp-1", "rcpt-out"); err == nil || !strings.Contains(err.Error(), "for reconcile") {
		t.Fatalf("receipt identity mismatch must fail: %v", err)
	}
	f.mu.Lock()
	f.publishEchoBad = false
	f.publishNoOutputs = true
	f.mu.Unlock()
	if _, err := arm.PublishReceipt(context.Background(), "Bearer x", "proj", "exp-1", "rcpt-out"); err == nil || !strings.Contains(err.Error(), "no materialized outputs") {
		t.Fatalf("zero outputs must fail: %v", err)
	}
}

func TestActivationRefusalIsFatalOnEveryRoute(t *testing.T) {
	activationBody := `{"error":{"code":"GENERATION_ACTIVATION_FAILED","message":"activate ExplorerConfigV2 failed; the prior Explorer revision was retained"}}`
	f := newFakeArm(BackendPostgres)
	defer f.close()
	f.mu.Lock()
	f.activationBody = activationBody
	f.mu.Unlock()
	arm := f.arm(BackendPostgres)

	_, err := arm.PublishReceipt(context.Background(), "Bearer x", "proj", "exp-1", "rcpt-1")
	if !errors.Is(err, ErrActivationRefusal) {
		t.Fatalf("authoring publish must fail closed on an activation-style refusal: %v", err)
	}
	// the repository workspace route wraps it too: no lane can tolerate one
	workspace := []byte(`{"kind":"explorer-builder"}`)
	if _, err := arm.PublishWorkspace(context.Background(), "Bearer x", "proj", "gen-1", workspace, "a"); !errors.Is(err, ErrActivationRefusal) {
		t.Fatalf("repository publish must fail closed on the same refusal: %v", err)
	}
	// a plain scripted failure (no activation marker) must NOT be labeled as
	// an activation refusal: the detection is content-based, not status-based
	f.mu.Lock()
	f.activationBody = ""
	f.failWith["anyout"] = 503
	f.mu.Unlock()
	if _, err := arm.ExecuteOutput(context.Background(), "Bearer x", "proj", "exp-1", "rcpt-1", "anyout", 10); errors.Is(err, ErrActivationRefusal) {
		t.Fatal("generic 503 must not be mislabeled as an activation refusal")
	}
}

func TestMatchedSetupProofCatchesDivergence(t *testing.T) {
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	fa.rows["out"] = []map[string]any{{"patient_id": "p1"}}
	fp.rows["out"] = []map[string]any{{"patient_id": "p1"}}

	proof := func(f *fakeArm, backend string) SetupProof {
		t.Helper()
		p, err := f.arm(backend).SetupProofEvidence(context.Background(), "Bearer x", "proj", "exp-1", "rcpt-out", "out")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	pa, pp := proof(fa, BackendArango), proof(fp, BackendPostgres)
	if err := MatchedSetupProof("w", pa, pp); err != nil {
		t.Fatalf("matched arms must prove: %v", err)
	}
	if pa.PreviewRows != 1 || len(pa.OutputIDs) != 1 || pa.DraftDigest == "" {
		t.Fatalf("proof lost content: %+v", pa)
	}

	// draft digest divergence (the content-equivalence proof)
	bad := pp
	bad.DraftDigest = "sha256:other"
	if err := MatchedSetupProof("w", pa, bad); err == nil || !strings.Contains(err.Error(), "draft digest") {
		t.Fatalf("digest divergence must fail: %v", err)
	}
	// row divergence before timing
	bad = pp
	bad.PreviewRows = 2
	if err := MatchedSetupProof("w", pa, bad); err == nil || !strings.Contains(err.Error(), "preview rows") {
		t.Fatalf("row divergence must fail: %v", err)
	}
	// output-set divergence
	bad = pp
	bad.OutputIDs = []string{"out-other"}
	bad.Materializations = 1
	if err := MatchedSetupProof("w", pa, bad); err == nil || !strings.Contains(err.Error(), "output set") {
		t.Fatalf("output set divergence must fail: %v", err)
	}
	// materialization count must match the output set (decorative evidence
	// cannot pass)
	bad = pp
	bad.Materializations = 0
	if err := MatchedSetupProof("w", pa, bad); err == nil || !strings.Contains(err.Error(), "output/materialization counts") {
		t.Fatalf("decorative materialization count must fail: %v", err)
	}
	// a real cross-arm readback divergence surfaces through SetupProofEvidence
	fp.mu.Lock()
	fp.activeDigest = ""
	fp.mu.Unlock()
	if _, err := fp.arm(BackendPostgres).SetupProofEvidence(context.Background(), "Bearer x", "proj", "exp-1", "rcpt-out", "out"); err != nil {
		t.Fatalf("default digest script must answer: %v", err)
	}
}
