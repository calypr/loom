package authoringv2

import "fmt"

// Independent many-valued branches cannot be combined into a product by
// repeatedly expanding the same retained root anchor. Onward expansion from
// a compiler-proven related-record anchor remains a separate supported lane.
func validateIndependentRelatedExpansions(steps []ConstructionStep) error {
	for index, step := range steps {
		current := step.Operation.RelatedExpand
		if step.Operation.Kind != ConstructionOperationRelatedExpand || current == nil {
			continue
		}
		for prior := index - 1; prior >= 0; prior-- {
			ancestor := steps[prior]
			switch ancestor.Operation.Kind {
			case ConstructionOperationGroup, ConstructionOperationCodedGroup, ConstructionOperationPivot, ConstructionOperationCodedPivot, ConstructionOperationCombine:
				prior = -1
				continue
			}
			previous := ancestor.Operation.RelatedExpand
			if ancestor.Operation.Kind != ConstructionOperationRelatedExpand || previous == nil || previous.AnchorColumnID != current.AnchorColumnID {
				continue
			}
			if sameRelatedExpansionRoute(previous, current) {
				continue
			}
			return fmt.Errorf("INDEPENDENT_RELATED_EXPANSIONS_UNSUPPORTED: step %q expands the same anchor %q along an independent branch after %q; retain related values as lists or expand onward from the selected related-record anchor", step.ID, current.AnchorColumnID, ancestor.ID)
		}
	}
	return nil
}

func sameRelatedExpansionRoute(a, b *ConstructionRelatedExpand) bool {
	if len(a.Route) != len(b.Route) {
		return false
	}
	for index, hop := range a.Route {
		other := b.Route[index]
		if hop.EdgeID != other.EdgeID || hop.FromNodeID != other.FromNodeID || hop.ToNodeID != other.ToNodeID || hop.StorageDirection != other.StorageDirection {
			return false
		}
	}
	return true
}
