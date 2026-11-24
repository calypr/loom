package semantic

type SemanticCoverageBuildState string

const (
	SemanticCoverageBuildBuilding    SemanticCoverageBuildState = "BUILDING"
	SemanticCoverageBuildFailed      SemanticCoverageBuildState = "FAILED"
	SemanticCoverageBuildUnavailable SemanticCoverageBuildState = "UNAVAILABLE"
	SemanticCoverageBuildComplete    SemanticCoverageBuildState = "COMPLETE"
	SemanticCoverageBuildPartial     SemanticCoverageBuildState = "PARTIAL"
	SemanticCoverageBuildMissing     SemanticCoverageBuildState = "MISSING"
)
