package ir

type PhysicalRelatedCategoryHop struct {
	Traversal PhysicalTraversal
	Route     PhysicalStageRelatedRouteStep
}

type PhysicalRelatedCategoryScan struct {
	Root      PhysicalRootScan
	Category  PhysicalStageRelatedField
	Hops      [][]PhysicalRelatedCategoryHop
	IndexName string
	Streaming bool
}
