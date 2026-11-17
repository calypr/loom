package ir

import "testing"

func TestPhysicalPlanFingerprintRetainsSetKind(t *testing.T) {
	plan := PhysicalPlan{Operations: []PhysicalOperation{{Kind: PhysicalSetOp, Set: &PhysicalSet{Variable: "members", Kind: "NODE_SET"}}}}
	if got, want := physicalPlanFingerprint(plan), "27e8342eee24874b5a80994c07efb39b012f14cd85782bd6ffce77cb4b1cec5e"; got != want {
		t.Fatalf("physical plan fingerprint = %s, want %s", got, want)
	}
}
