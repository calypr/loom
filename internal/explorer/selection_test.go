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

func TestSelectionSourceVariantsRejectForeignIdentity(t *testing.T) {
	for name, source := range map[string]SelectionSource{
		"explicit with revision":    {Kind: SelectionSourceExplicit, RevisionID: "selection-1"},
		"published with membership": {Kind: SelectionSourcePublished, RevisionID: "revision-1", ReceiptID: "receipt-1", ExecutionID: "execution-1", OutputID: "files", SchemaDigest: "schema-1", Generation: "generation-1", ResourceType: "DocumentReference", SourceIDColumn: "id", MembershipDigest: "members-1"},
		"revision with execution":   {Kind: SelectionSourceRevision, RevisionID: "selection-1", Generation: "generation-1", ResourceType: "DocumentReference", MembershipDigest: "members-1", ExecutionID: "execution-1"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := source.Validate("project-1", "generation-1", "DocumentReference"); err == nil {
				t.Fatal("source accepted identity fields from another source variant")
			}
		})
	}
}
