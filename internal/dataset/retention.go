package dataset

import (
	"context"
	"fmt"
	"time"
)

type RetentionGeneration struct {
	Dataset     Ref
	State       State
	UpdatedAt   time.Time
	Active      bool
	LastGood    bool
	InFlight    bool
	Recoverable bool
}

type RetentionRepository interface {
	ListRetentionGenerations(context.Context) ([]RetentionGeneration, error)
	DeleteGeneration(context.Context, Ref) error
}

type GenerationBlobCleaner interface {
	DeleteGeneration(context.Context, Ref) error
}

type RetentionService struct {
	Repository RetentionRepository
	Blobs      GenerationBlobCleaner
	Retention  time.Duration
	Now        func() time.Time
}

func (s RetentionService) RunOnce(ctx context.Context) ([]Ref, error) {
	if s.Repository == nil || s.Retention <= 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	candidates, err := s.Repository.ListRetentionGenerations(ctx)
	if err != nil {
		return nil, err
	}
	deleted := make([]Ref, 0)
	for _, candidate := range candidates {
		// Active, last-good, staged/recoverable, and in-flight data are always
		// protected regardless of age. Only terminal failed generations expire.
		if candidate.Active || candidate.LastGood || candidate.InFlight || candidate.Recoverable || candidate.State == StateLoading || candidate.State == StateStaged || candidate.State == StateReady || candidate.UpdatedAt.After(now.Add(-s.Retention)) {
			continue
		}
		if err := s.Repository.DeleteGeneration(ctx, candidate.Dataset); err != nil {
			return deleted, fmt.Errorf("delete generation metadata %s/%s: %w", candidate.Dataset.Project, candidate.Dataset.Generation, err)
		}
		if s.Blobs != nil {
			if err := s.Blobs.DeleteGeneration(ctx, candidate.Dataset); err != nil {
				return deleted, fmt.Errorf("delete generation blobs %s/%s: %w", candidate.Dataset.Project, candidate.Dataset.Generation, err)
			}
		}
		deleted = append(deleted, candidate.Dataset)
	}
	return deleted, nil
}
