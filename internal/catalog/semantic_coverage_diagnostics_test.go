package catalog

import (
	"testing"

	fhirsemantic "github.com/calypr/loom/internal/fhir/semantic"
)

func TestSemanticCoverageBoundarySignaturesDefaultEmpty(t *testing.T) {
	if (SemanticCoveragePathAggregate{}).ResourceBoundarySignature != "" {
		t.Fatal("ordinary path aggregate boundary signature must default to empty")
	}
	if (SemanticCoverageFacetAggregate{}).ResourceBoundarySignature != "" {
		t.Fatal("ordinary facet aggregate boundary signature must default to empty")
	}
}

func TestSemanticCoverageDiagnosticSamplingDoesNotBlockCompleteProof(t *testing.T) {
	census := SemanticCoverageCensus{
		ResourceType: "Patient",
		SourceID:     "diagnostic-sample-source",
		Identity: SemanticCoverageSourceIdentity{
			SourceID: "diagnostic-sample-source", ResourceType: "Patient", SchemaIdentity: "schema-test",
		},
		Status: SemanticCoverageCensusComplete,
	}
	for index := 0; index < semanticCoverageMaxDiagnostics+1; index++ {
		census.Diagnostics = append(census.Diagnostics, fhirsemantic.WalkDiagnostic{
			Code: fhirsemantic.WalkDiagnosticCode("MISSING_REQUIRED_ELEMENT"),
		})
	}
	contribution, err := ReconcileSemanticCoverage(census, nil, nil)
	if err != nil {
		t.Fatalf("reconcile diagnostic sample: %v", err)
	}
	if !contribution.Complete || !contribution.DiagnosticLocatorsTruncated || len(contribution.Diagnostics) != semanticCoverageMaxDiagnostics {
		t.Fatalf("bounded diagnostic sample incorrectly blocked complete proof: %#v", contribution)
	}
}
