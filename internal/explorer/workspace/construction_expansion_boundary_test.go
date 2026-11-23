package workspace

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
)

func TestIndependentRelatedExpansionsRejectTwoByThreeProduct(t *testing.T) {
	makeStep := func(id, anchor, edge, target string) ConstructionStep {
		return ConstructionStep{ID: id, Operation: ConstructionOperation{Kind: ConstructionOperationRelatedExpand, RelatedExpand: &ConstructionRelatedExpand{
			AnchorColumnID: anchor, Route: []capability.ConstructionRouteStep{{EdgeID: edge, FromNodeID: "patient", ToNodeID: target, StorageDirection: "INBOUND"}},
		}}}
	}
	observations := makeStep("two-observations", "_key", "observations", "observation")
	conditions := makeStep("three-conditions", "_key", "conditions", "condition")

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
