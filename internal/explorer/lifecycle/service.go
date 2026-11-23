package lifecycle

import (
	"errors"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
)

type Service struct {
	store                 *explorer.Service
	config                Config
	now                   func() time.Time
	constructionBaseCache *constructionBaseCache
	columnPopulationCache *columnPopulationCache
	interpretationService *interpretation.Service
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
	return &Service{
		store:                 store,
		config:                config,
		now:                   now,
		constructionBaseCache: newConstructionBaseCache(config.ConstructionBaseCacheTTL, config.ConstructionBaseCacheEntries, now),
		columnPopulationCache: newColumnPopulationCache(columnPopulationCacheTTL, columnPopulationCacheMaxEntries, now),
		interpretationService: interpretationService,
	}, nil
}

func (s *Service) InterpretationService() *interpretation.Service {
	if s == nil {
		return nil
	}
	return s.interpretationService
}
