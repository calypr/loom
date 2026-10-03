package published

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	bundlepublication "github.com/calypr/loom/internal/dataframe/publication"
	publication "github.com/calypr/loom/internal/dataset"
)

// CurrentProjectDataset returns the one pointer-visible, successful output for
// a project and exact selector. Project identity is explicit at this boundary;
// this reader never unions tables from different projects.
func (r *Reader) CurrentProjectDataset(ctx context.Context, project string, selector DataframeSelector) (Materialization, error) {
	project = strings.TrimSpace(project)
	if r == nil || r.Catalog == nil {
		return Materialization{}, fmt.Errorf("bundle catalog dependency is required")
	}
	if project == "" || !selector.Valid() {
		return Materialization{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	if r.ActiveReleaseResolver != nil {
		return r.activeReleaseDataset(ctx, project, selector)
	}
	activeGeneration, err := r.activeGeneration(ctx, project)
	if err != nil {
		return Materialization{}, err
	}
	if exact, ok := r.Catalog.(bundlepublication.ExactExecutionCatalog); ok && activeGeneration != "" {
		execution, output, err := exact.FindExecutionBySelector(ctx, project, activeGeneration, selector)
		if err != nil {
			if errors.Is(err, bundlepublication.ErrBundleNotFound) {
				return Materialization{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
			}
			return Materialization{}, err
		}
		if value, ok, err := r.currentMaterialization(ctx, execution, output, selector); err != nil {
			return Materialization{}, err
		} else if ok {
			return value, nil
		}
		return Materialization{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	executions, err := r.Catalog.ListExecutions(ctx, bundlepublication.BundlePublished, time.Now().UTC().Add(time.Second))
	if err != nil {
		return Materialization{}, err
	}
	for _, execution := range executions {
		if execution.Project != project || !execution.State.Successful() {
			continue
		}
		if activeGeneration != "" && execution.DatasetGeneration != activeGeneration {
			continue
		}
		for _, output := range execution.Outputs {
			if output.Name != selector.Output {
				continue
			}
			if value, ok, err := r.currentMaterialization(ctx, execution, output, selector); err != nil {
				return Materialization{}, err
			} else if ok {
				return value, nil
			}
		}
	}
	return Materialization{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
}

func (r *Reader) activeReleaseDataset(ctx context.Context, project string, selector DataframeSelector) (Materialization, error) {
	active, err := r.ActiveReleaseResolver.ReadActiveRelease(ctx, project)
	if err != nil {
		if errors.Is(err, publication.ErrNoActiveRelease) {
			return Materialization{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
		}
		return Materialization{}, fmt.Errorf("resolve active dataframe release: %w", err)
	}
	if active.Release.Project != project {
		return Materialization{}, fmt.Errorf("active dataframe release project %q does not match %q", active.Release.Project, project)
	}
	for _, binding := range active.Release.Publications {
		if binding.Selector != selector || binding.Stale || binding.Generation != active.Release.Generation {
			continue
		}
		return r.releaseMaterialization(ctx, active.Release, binding)
	}
	return Materialization{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
}

func (r *Reader) releaseMaterialization(ctx context.Context, release publication.ProjectRelease, binding publication.ReleasePublication) (Materialization, error) {
	execution, err := r.Catalog.GetExecution(ctx, binding.ExecutionID)
	if err != nil {
		return Materialization{}, fmt.Errorf("resolve active dataframe execution: %w", err)
	}
	if execution.ID != binding.ExecutionID || execution.Project != release.Project || execution.DatasetGeneration != binding.Generation || !execution.State.Successful() {
		return Materialization{}, fmt.Errorf("active dataframe execution %q does not match release %q", binding.ExecutionID, release.ID)
	}
	for _, output := range execution.Outputs {
		if output.Name != binding.Selector.Output || !output.Queryable() || outputSelector(execution, output.Name, output.Selector) != binding.Selector {
			continue
		}
		result := publishedMaterialization(execution, output, binding.Selector.Output)
		result.Selector = binding.Selector
		return result, nil
	}
	return Materialization{}, fmt.Errorf("active dataframe execution %q has no queryable output %q", binding.ExecutionID, binding.Selector.Output)
}

func (r *Reader) currentMaterialization(ctx context.Context, execution bundlepublication.BundleExecution, output bundlepublication.BundleOutputRecord, selector DataframeSelector) (Materialization, bool, error) {
	if !execution.State.Successful() || !output.Queryable() || outputSelector(execution, output.Name, output.Selector) != selector {
		return Materialization{}, false, nil
	}
	pointer, err := r.Catalog.GetPointer(ctx, execution.PointerName())
	if err != nil {
		return Materialization{}, false, fmt.Errorf("resolve dataframe pointer: %w", err)
	}
	if pointer.ExecutionID != execution.ID {
		return Materialization{}, false, nil
	}
	result := publishedMaterialization(execution, output, selector.Output)
	result.Selector = selector
	return result, true, nil
}

// CurrentProjectDatasets returns every current output for one project. Each
// returned materialization is backed by one active ClickHouse table.
func (r *Reader) CurrentProjectDatasets(ctx context.Context, project string) ([]Materialization, error) {
	project = strings.TrimSpace(project)
	if r == nil || r.Catalog == nil {
		return nil, fmt.Errorf("bundle catalog dependency is required")
	}
	if project == "" {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	if r.ActiveReleaseResolver != nil {
		return r.activeReleaseDatasets(ctx, project)
	}
	activeGeneration, err := r.activeGeneration(ctx, project)
	if err != nil {
		return nil, err
	}
	executions, err := r.Catalog.ListExecutions(ctx, bundlepublication.BundlePublished, time.Now().UTC().Add(time.Second))
	if err != nil {
		return nil, err
	}
	result := make([]Materialization, 0)
	for _, execution := range executions {
		if execution.Project != project || !execution.State.Successful() {
			continue
		}
		if activeGeneration != "" && execution.DatasetGeneration != activeGeneration {
			continue
		}
		pointer, err := r.Catalog.GetPointer(ctx, execution.PointerName())
		if err != nil {
			return nil, fmt.Errorf("resolve dataframe pointer: %w", err)
		}
		if pointer.ExecutionID != execution.ID {
			continue
		}
		for _, output := range execution.Outputs {
			if !output.Queryable() {
				continue
			}
			selector := outputSelector(execution, output.Name, output.Selector)
			if !selector.Valid() {
				continue
			}
			value := publishedMaterialization(execution, output, output.Name)
			value.Selector = selector
			result = append(result, value)
		}
	}
	if len(result) == 0 {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (r *Reader) activeReleaseDatasets(ctx context.Context, project string) ([]Materialization, error) {
	active, err := r.ActiveReleaseResolver.ReadActiveRelease(ctx, project)
	if err != nil {
		if errors.Is(err, publication.ErrNoActiveRelease) {
			return nil, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
		}
		return nil, fmt.Errorf("resolve active dataframe release: %w", err)
	}
	if active.Release.Project != project {
		return nil, fmt.Errorf("active dataframe release project %q does not match %q", active.Release.Project, project)
	}
	result := make([]Materialization, 0, len(active.Release.Publications))
	for _, binding := range active.Release.Publications {
		if binding.Stale || binding.Generation != active.Release.Generation {
			continue
		}
		value, err := r.releaseMaterialization(ctx, active.Release, binding)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (r *Reader) activeGeneration(ctx context.Context, project string) (string, error) {
	if r.ActiveManifestResolver == nil {
		return "", nil
	}
	manifest, err := publication.ResolveActive(ctx, r.ActiveManifestResolver, project)
	if err != nil {
		if errors.Is(err, publication.ErrNoActiveGeneration) {
			return "", nil
		}
		return "", err
	}
	return manifest.Dataset.Generation, nil
}

func outputSelector(execution bundlepublication.BundleExecution, output string, value DataframeSelector) DataframeSelector {
	if value.Valid() {
		return value
	}
	return execution.Selector(output)
}
