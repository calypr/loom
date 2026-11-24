package arango

import (
	"fmt"
	"github.com/calypr/loom/internal/semantic"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/catalog/fieldprofile"
)

const (
	semanticCoverageEnvelopeRecordKind = "source_envelope_v1"
)

type semanticCoverageEnvelopePath struct {
	CanonicalPath string                      `json:"canonical_path"`
	Groupable     bool                        `json:"groupable"`
	Path          *semanticCoverageStoredPath `json:"path"`
}

func semanticCoverageSourceEnvelopeKey(identity fieldprofile.SemanticCoverageSourceIdentity, buildID string) string {
	return semantic.CoverageDocumentKey(
		"semantic-coverage-source-envelope",
		fmt.Sprint(fieldprofile.SemanticCoverageStorageFormatVersion),
		identity.Project,
		catalog.NormalizeDatasetGeneration(identity.DatasetGeneration),
		buildID,
		identity.SchemaIdentity,
		identity.SourceKind,
		identity.ResourceType,
		identity.SourceID,
	)
}
