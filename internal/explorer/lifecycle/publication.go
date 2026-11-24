package lifecycle

import (
	"time"

	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/publication"
)

func DelayArtifactRows(next ports.ArtifactPublishedReader, delay time.Duration) ports.ArtifactPublishedReader {
	return publication.DelayArtifactRows(next, delay)
}

var _ published.Materialization
