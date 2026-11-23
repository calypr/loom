package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/selection"
)

type Service struct {
	store                 *explorer.Service
	config                Config
	now                   func() time.Time
	constructionBaseCache *constructionBaseCache
	columnPopulationCache *columnPopulationCache
	interpretationService *interpretation.Service
	selectionService      *selection.Service
}

func New(store *explorer.Service, config Config) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("Explorer lifecycle store is required")
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	config.Now = now
	interpretationService, err := interpretation.NewService(interpretation.Config{
		Repository: config.InterpretationRepository,
		Errors: interpretation.ErrorFactory{
			Malformed:          support.Malformed,
			Unavailable:        support.Unavailable,
			Conflict:           support.Conflict,
			NotFound:           support.NotFound,
			Unprocessable:      support.Unprocessable,
			Internal:           support.Internal,
			PersistenceFailure: support.PersistenceFailure,
		},
		IsNotFound: func(err error) bool { return errors.Is(err, explorer.ErrNotFound) },
	})
	if err != nil {
		return nil, err
	}
	service := &Service{
		store:                 store,
		config:                config,
		now:                   now,
		constructionBaseCache: newConstructionBaseCache(config.ConstructionBaseCacheTTL, config.ConstructionBaseCacheEntries, now),
		columnPopulationCache: newColumnPopulationCache(columnPopulationCacheTTL, columnPopulationCacheMaxEntries, now),
		interpretationService: interpretationService,
	}
	selectionService, err := selection.NewService(selection.Config{
		Store: store,
		RevisionReader: selection.RevisionReaderFunc(func(ctx context.Context, id string) (*selection.PublishedRevision, error) {
			revision, err := store.GetRevision(ctx, id)
			if err != nil {
				return nil, err
			}
			outputs := make([]string, len(revision.Dataset.Outputs))
			for i, output := range revision.Dataset.Outputs {
				outputs[i] = output.Name
			}
			return &selection.PublishedRevision{
				Project: revision.Project, ExplorerID: revision.ExplorerID,
				CompilationReceiptID: revision.CompilationReceiptID,
				ExecutionID:          revision.Publication.ExecutionID, Outputs: outputs,
			}, nil
		}),
		CapabilityProvider: func() selection.CapabilityResolver {
			return selectionCapabilityResolver(service.config.Capability)
		},
		SelectionSourceResolver: config.SelectionSourceResolver,
		SelectionReferenceValidatorProvider: func() selection.SelectionReferenceValidator {
			return service.config.SelectionReferenceValidator
		},
		Errors: selection.ErrorFactory{
			Malformed: support.Malformed, Unavailable: support.Unavailable, Conflict: support.Conflict,
			Forbidden: support.Forbidden, NotFound: support.NotFound, Unprocessable: support.Unprocessable,
			PersistenceFailure: support.PersistenceFailure,
		},
		IsNotFound: func(err error) bool { return errors.Is(err, explorer.ErrNotFound) },
	})
	if err != nil {
		return nil, err
	}
	service.selectionService = selectionService
	return service, nil
}

func selectionCapabilityResolver(capabilities ports.CapabilityResolver) selection.CapabilityResolver {
	return selection.CapabilityResolver{
		Current: capabilities.Current,
		ForCompilation: func(ctx context.Context, project, token string) (selection.AuthorizedCapability, error) {
			value, err := capabilities.ForCompilation(ctx, project, token)
			return selection.AuthorizedCapability{Snapshot: value.Snapshot, Scope: value.Scope}, err
		},
		ForExecution: func(ctx context.Context, project, token string) (selection.AuthorizedCapability, error) {
			value, err := capabilities.ForExecution(ctx, project, token)
			return selection.AuthorizedCapability{Snapshot: value.Snapshot, Scope: value.Scope}, err
		},
	}
}

func (s *Service) InterpretationService() *interpretation.Service {
	if s == nil {
		return nil
	}
	return s.interpretationService
}

func (s *Service) SelectionService() *selection.Service {
	if s == nil {
		return nil
	}
	return s.selectionService
}
