package catalog

// AvailabilityOptions binds a graph search to one retained dataset. Authorization
// and the exact root cohort remain explicit even though the graph is shared.
type AvailabilityOptions struct {
	Project           string
	DatasetGeneration string
	Query             AvailabilityQuery
}

type AvailabilityResult struct {
	State     SemanticInventoryState
	BuildID   string
	Witnesses []AvailabilityWitness
}
