package catalog

// Semantic coverage lookup is the consumer-facing read contract for asking
// whether candidate canonical paths are covered by a semantic coverage
// build. It is a neutral domain contract: Explorer lifecycle consumes it,
// and source adapters implement the read against their own storage. The
// build/write-side machinery (source-set accumulators, expectations,
// materialization limits) stays with the implementing adapter.

type SemanticCoverageBuildState string

const (
	SemanticCoverageBuildBuilding    SemanticCoverageBuildState = "BUILDING"
	SemanticCoverageBuildFailed      SemanticCoverageBuildState = "FAILED"
	SemanticCoverageBuildUnavailable SemanticCoverageBuildState = "UNAVAILABLE"
	SemanticCoverageBuildComplete    SemanticCoverageBuildState = "COMPLETE"
	SemanticCoverageBuildPartial     SemanticCoverageBuildState = "PARTIAL"
	SemanticCoverageBuildMissing     SemanticCoverageBuildState = "MISSING"
)

type SemanticCoverageCandidateStatus string

const (
	SemanticCoverageCandidateGroupable   SemanticCoverageCandidateStatus = "GROUPABLE"
	SemanticCoverageCandidateKeepRaw     SemanticCoverageCandidateStatus = "KEEP_RAW"
	SemanticCoverageCandidateUnavailable SemanticCoverageCandidateStatus = "UNAVAILABLE"
)

type SemanticCoverageCandidatePath struct {
	ResourceType  string
	CanonicalPath string
}

type SemanticCoverageLookupOptions struct {
	Project                       string
	DatasetGeneration             string
	BuildID                       string
	SchemaIdentity                string
	SourceKind                    string
	AuthResourcePaths             []string
	AuthResourcePathsUnrestricted bool
	Candidates                    []SemanticCoverageCandidatePath
}

type SemanticCoverageCandidateResult struct {
	ResourceType   string
	CanonicalPath  string
	CoverageID     string
	Status         SemanticCoverageCandidateStatus
	ResourceState  SemanticCoverageBuildState
	ResourceReason string
	Reason         SemanticCoverageReason
	References     []SemanticCoverageReference
}

type SemanticCoverageLookupResult struct {
	State      SemanticCoverageBuildState
	Reason     string
	Candidates []SemanticCoverageCandidateResult
}
