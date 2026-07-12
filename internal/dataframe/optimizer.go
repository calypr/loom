package dataframe

// Stable optimizer-rule identifiers appear in compiler explain output and
// conformance evidence. They are deliberately independent of AQL text.
const (
	OptimizerRuleFilterPushdown   = "push_down_typed_filters"
	OptimizerRuleTraversalSharing = "share_identical_traversals"
	// OptimizerRuleRelationshipSemiJoin records that REQUIRED relationship
	// matches became root-scoped bounded existence predicates rather than
	// post-projection filters.
	OptimizerRuleRelationshipSemiJoin = "root_relationship_semi_join"
)

func containsOptimizerRule(rules []string, want string) bool {
	for _, rule := range rules {
		if rule == want {
			return true
		}
	}
	return false
}
