package ir

// PhysicalRelatedCategoryHop binds one compiler-proven physical traversal to
// its authored route witness. The category-scan renderer consumes only these
// typed values and the source plan's bind map.
type PhysicalRelatedCategoryHop struct {
	Traversal PhysicalTraversal
	Route     PhysicalStageRelatedRouteStep
}

// PhysicalRelatedCategoryScan describes one proven related-field category
// scan. It is a renderer input, not a persisted recipe or physical-plan node.
type PhysicalRelatedCategoryScan struct {
	Root      PhysicalRootScan
	Category  PhysicalStageRelatedField
	Hops      [][]PhysicalRelatedCategoryHop
	IndexName string
	Streaming bool
}
