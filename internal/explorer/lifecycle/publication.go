package lifecycle

import (
	"context"
	"io"
	"time"

	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/publication"
)

func (s *Service) Publish(ctx context.Context, request publication.PublishRequest) (publication.PublishResult, error) {
	return s.publicationService.Publish(ctx, request)
}

func (s *Service) PublishRepository(ctx context.Context, request publication.RepositoryPublishRequest) (publication.RepositoryPublishResult, error) {
	return s.publicationService.PublishRepository(ctx, request)
}

func (s *Service) PrepareArtifact(ctx context.Context, request publication.ArtifactRequest) (publication.ArtifactResult, error) {
	return s.publicationService.PrepareArtifact(ctx, request)
}

func (s *Service) OpenArtifact(ctx context.Context, request publication.ArtifactOpenRequest) (io.ReadCloser, *explorer.ArtifactRecord, error) {
	return s.publicationService.OpenArtifact(ctx, request)
}

func DelayArtifactRows(next ports.ArtifactPublishedReader, delay time.Duration) ports.ArtifactPublishedReader {
	return publication.DelayArtifactRows(next, delay)
}

var _ published.Materialization
