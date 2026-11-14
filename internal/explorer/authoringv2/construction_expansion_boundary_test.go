package authoringv2

import (
	"github.com/calypr/loom/internal/explorer/capability"
	"strings"
	"testing"
)

func TestIndependentRelatedExpansionsRejectTwoByThreeProduct(t *testing.T) {
	makeStep := func(id, anchor, edge, target string) ConstructionStep {
		return ConstructionStep{ID: id, Operation: ConstructionOperation{Kind: ConstructionOperationRelatedExpand, RelatedExpand: &ConstructionRelatedExpand{
			AnchorColumnID: anchor, Route: []capability.ConstructionRouteStep{{EdgeID: edge, FromNodeID: "patient", ToNodeID: target, StorageDirection: "INBOUND"}},
		}}}
	}
	observations := makeStep("two-observations", "_key", "observations", "observation")
	conditions := makeStep("three-conditions", "_key", "conditions", "condition")
	// Multiplicity is independent fixture knowledge, never inferred from a
	// resource label or preview sample. The plan must fail before a six-row
	// product could execute, regardless of the actual observed counts.
	err := (&Construction{Version: ConstructionVersion, Steps: []ConstructionStep{observations, conditions}}).Validate(nil)
	if err == nil || !strings.Contains(err.Error(), "INDEPENDENT_RELATED_EXPANSIONS_UNSUPPORTED") {
		t.Fatalf("independent branches accepted: %v", err)
	}
	relatedAnchor := makeStep("onward", "observation-document-anchor", "specimens", "specimen")
	if err := validateIndependentRelatedExpansions([]ConstructionStep{observations, relatedAnchor}); err != nil {
		t.Fatalf("onward related-record route rejected: %v", err)
	}
	sameRoute := observations
	sameRoute.ID = "same-route"
	if err := validateIndependentRelatedExpansions([]ConstructionStep{observations, sameRoute}); err != nil {
		t.Fatalf("existing exact-route expansion rejected: %v", err)
	}
}
