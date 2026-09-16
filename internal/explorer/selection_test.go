package explorer

import (
	"testing"
)

func TestMembershipDigestIsCanonicalAndDeduplicated(t *testing.T) {
	first := ResourceRef{Project: "HTAN_INT-demo", Generation: "g1", ResourceType: "DocumentReference", ID: "files001"}
	second := ResourceRef{Project: "HTAN_INT-demo", Generation: "g1", ResourceType: "DocumentReference", ID: "files003"}
	left := MembershipDigest([]SelectionMember{{Ref: first}, {Ref: second}, {Ref: first}})
	right := MembershipDigest([]SelectionMember{{Ref: second}, {Ref: first}})
	if left != right {
		t.Fatalf("digest changed with duplicate/order: %s != %s", left, right)
	}
	if first.Canonical().Project != "HTAN_INT/demo" {
		t.Fatalf("project was not canonicalized: %q", first.Canonical().Project)
	}
}

func TestSelectionRevisionRejectsUnaddressablePublishedSource(t *testing.T) {
	revision := SelectionRevision{
		ID: "selection-1", Project: "project", Generation: "g1", ResourceType: "DocumentReference",
		Rule:        SelectionRule{Kind: SelectionRuleAllMatching},
		Source:      SelectionSource{Kind: SelectionSourcePublished, RevisionID: "r1", ReceiptID: "receipt", ExecutionID: "e1", OutputID: "files", SchemaDigest: "schema"},
		ScopeDigest: "scope", RuleDigest: "rule",
	}
	if err := revision.Validate(); err == nil {
		t.Fatal("unaddressable source unexpectedly validated")
	}
}
