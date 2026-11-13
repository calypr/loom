package catalog

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
