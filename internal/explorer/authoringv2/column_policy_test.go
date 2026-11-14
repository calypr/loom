package authoringv2

import (
	"strings"
	"testing"
)

func TestColumnPolicyCannotApplyUnresolvedOrAmbiguousIdentity(t *testing.T) {
	workspace := Workspace{Documents: []Document{{Output: Output{ID: "out"}, Columns: []Column{{ColumnID: "same", Column: "first"}, {ColumnID: "same", Column: "second"}}}}}
	command := Command{Type: CommandApplyColumnPolicyProposal, OutputID: "out", ProposalID: "receipt"}
	if _, err := applyColumnPolicyProposal(&workspace, CatalogSnapshot{}, "id", 0, command); err == nil {
		t.Fatal("applied an unresolved client proposal")
	}
	if err := command.ResolveColumnPolicyProposal("same", "ALL"); err != nil {
		t.Fatal(err)
	}
	if _, err := applyColumnPolicyProposal(&workspace, CatalogSnapshot{}, "id", 0, command); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("arbitrarily chose a duplicate column identity: %v", err)
	}
}
