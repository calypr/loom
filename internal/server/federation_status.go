package server

import (
	"context"
	"errors"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataset"
)

// releaseProjectStatusResolver joins the active release with durable attempts.
// Its caller supplies an authorization-filtered project list, and the GraphQL
// service filters the result against that list again before returning it.
type releaseProjectStatusResolver struct {
	releases   dataset.ReleaseRepository
	executions publication.BundleCatalog
}

func (r releaseProjectStatusResolver) DataframeProjectStatuses(ctx context.Context, projects []string, selector dataset.DataframeSelector) ([]published.ProjectStatus, error) {
	_, statuses, _, err := r.ResolveFederationSnapshot(ctx, projects, selector)
	return statuses, err
}

func (r releaseProjectStatusResolver) ResolveFederationSnapshot(ctx context.Context, projects []string, selector dataset.DataframeSelector) (map[string]string, []published.ProjectStatus, bool, error) {
	latest, err := r.latestAttempts(ctx, selector)
	if err != nil {
		return nil, nil, false, err
	}
	executionIDs := make(map[string]string, len(projects))
	result := make([]published.ProjectStatus, 0, len(projects))
	complete := true
	for _, project := range projects {
		status := published.ProjectStatus{ProjectID: project, State: published.ProjectMissing}
		active, activeErr := r.releases.ReadActiveRelease(ctx, project)
		if activeErr != nil && !errors.Is(activeErr, dataset.ErrNoActiveRelease) {
			return nil, nil, false, activeErr
		}
		if errors.Is(activeErr, dataset.ErrNoActiveRelease) {
			complete = false
		}
		if activeErr == nil {
			for _, binding := range active.Release.Publications {
				if binding.Selector != selector {
					continue
				}
				status.State = published.ProjectCurrent
				if binding.Stale {
					status.State = published.ProjectStale
				}
				status.Generation, status.ExecutionID = binding.Generation, binding.ExecutionID
				executionIDs[project] = binding.ExecutionID
				status.UpdatedAt = binding.VerifiedAt
				break
			}
		}
		if execution, ok := latest[project]; ok {
			isReplacement := execution.ID != status.ExecutionID && (status.UpdatedAt.IsZero() || execution.UpdatedAt.After(status.UpdatedAt))
			if status.State == published.ProjectMissing || isReplacement {
				canonical := execution.State.Canonical()
				if canonical != publication.BundleFailed && canonical != publication.BundleQueued && canonical != publication.BundleRunning && canonical != publication.BundleValidating {
					result = append(result, status)
					continue
				}
				status.Generation, status.ExecutionID = execution.DatasetGeneration, execution.ID
				status.CreatedAt, status.UpdatedAt = execution.CreatedAt, execution.UpdatedAt
				status.ErrorCode, status.Retryable = execution.FailureCode, execution.FailureRetryable
				if canonical == publication.BundleFailed {
					status.State = published.ProjectFailed
				} else {
					status.State = published.ProjectBuilding
				}
			}
		}
		result = append(result, status)
	}
	return executionIDs, result, complete, nil
}

func (r releaseProjectStatusResolver) ActiveReleaseExecutionIDs(ctx context.Context, projects []string, selector dataset.DataframeSelector) (map[string]string, error) {
	result := make(map[string]string)
	for _, project := range projects {
		active, err := r.releases.ReadActiveRelease(ctx, project)
		if errors.Is(err, dataset.ErrNoActiveRelease) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result[project] = ""
		for _, binding := range active.Release.Publications {
			if binding.Selector == selector {
				result[project] = binding.ExecutionID
				break
			}
		}
	}
	return result, nil
}

func (r releaseProjectStatusResolver) ActiveReleaseSelectors(ctx context.Context, projects []string) ([]published.DataframeSelector, map[string]bool, error) {
	selectors := make(map[string]published.DataframeSelector)
	controlled := make(map[string]bool)
	for _, project := range projects {
		active, err := r.releases.ReadActiveRelease(ctx, project)
		if errors.Is(err, dataset.ErrNoActiveRelease) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		controlled[project] = true
		for _, binding := range active.Release.Publications {
			selectors[binding.Selector.Key()] = binding.Selector
		}
	}
	result := make([]published.DataframeSelector, 0, len(selectors))
	for _, selector := range selectors {
		result = append(result, selector)
	}
	return result, controlled, nil
}

func (r releaseProjectStatusResolver) latestAttempts(ctx context.Context, selector dataset.DataframeSelector) (map[string]publication.BundleExecution, error) {
	latest := make(map[string]publication.BundleExecution)
	if r.executions == nil {
		return latest, nil
	}
	if catalog, ok := r.executions.(publication.SelectorExecutionCatalog); ok {
		executions, err := catalog.ListSelectorExecutions(ctx, selector, time.Now().UTC().Add(time.Second))
		if err != nil {
			return nil, err
		}
		for _, execution := range executions {
			if execution.Project == "" || !executionContainsSelector(execution, selector) {
				continue
			}
			if prior, exists := latest[execution.Project]; !exists || execution.UpdatedAt.After(prior.UpdatedAt) {
				latest[execution.Project] = execution
			}
		}
		return latest, nil
	}
	for _, state := range []publication.BundleState{publication.BundleQueued, publication.BundleRunning, publication.BundleValidating, publication.BundlePublished, publication.BundleFailed} {
		executions, err := r.executions.ListExecutions(ctx, state, time.Now().UTC().Add(time.Second))
		if err != nil {
			return nil, err
		}
		for _, execution := range executions {
			if execution.Project == "" || !executionContainsSelector(execution, selector) {
				continue
			}
			if prior, ok := latest[execution.Project]; !ok || execution.UpdatedAt.After(prior.UpdatedAt) {
				latest[execution.Project] = execution
			}
		}
	}
	return latest, nil
}

func executionContainsSelector(execution publication.BundleExecution, selector dataset.DataframeSelector) bool {
	for _, output := range execution.Outputs {
		candidate := output.Selector
		if !candidate.Valid() {
			candidate = execution.Selector(output.Name)
		}
		if candidate == selector {
			return true
		}
	}
	// A queued command intentionally has no outputs until validation. Its exact
	// recipe/version identity still makes the requested output BUILDING.
	return len(execution.Outputs) == 0 && execution.Name == selector.Recipe && execution.TranslationVersion == selector.TranslationVersion
}

var _ published.ProjectStatusResolver = releaseProjectStatusResolver{}
var _ published.ReleaseExecutionResolver = releaseProjectStatusResolver{}
var _ published.FederationSnapshotResolver = releaseProjectStatusResolver{}
