package dataframe

// These aliases keep package-local compiler tests readable while their
// implementations live in the canonical compiler/runtime packages.
type storageRoute = StorageRoute

func resolveStorageRoute(fromType, label, toType string) (storageRoute, error) {
	return ResolveStorageRoute(fromType, label, toType)
}

const datasetGenerationBindKey = "dataset_generation"

func isDatasetGenerationScopePredicate(predicate PhysicalPredicate, variable string) bool {
	return predicate.Operator == "EQUALS" &&
		predicate.Left.Variable == variable &&
		len(predicate.Left.Path) == 1 && predicate.Left.Path[0] == "dataset_generation" &&
		predicate.Right != nil && predicate.Right.BindKey == datasetGenerationBindKey &&
		predicate.Right.Variable == "" && len(predicate.Right.Path) == 0
}
