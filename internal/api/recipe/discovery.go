// Package recipeapi owns transport-neutral adapters used by recipe
// resolution entrypoints.
package recipeapi

import (
	"context"
	"errors"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

// FieldDiscovery adapts the scoped field catalog to recipe schema resolution.
// Both one-shot and stored-recipe callers use this policy so metadata and
// pivot declarations cannot drift between transports.
type FieldDiscovery struct {
	Read func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
}

// NewFieldDiscovery returns the shared catalog adapter used by every
// recipe-resolution entrypoint.
func NewFieldDiscovery(read func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)) schema.Discovery {
	return FieldDiscovery{Read: read}
}

func (d FieldDiscovery) Fields(ctx context.Context, scope schema.Scope, resourceType string) ([]schema.FieldCandidate, error) {
	var unrestricted *bool
	switch authscope.ReadScopeMode(scope.AuthScopeMode) {
	case authscope.ReadScopeUnrestricted:
		value := true
		unrestricted = &value
	case authscope.ReadScopeRestricted:
		value := false
		unrestricted = &value
	}
	if d.Read == nil {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	fields, err := d.Read(ctx, catalog.PopulatedFieldOptions{
		Project: scope.Project, DatasetGeneration: scope.DatasetGeneration,
		AuthResourcePaths: append([]string(nil), scope.AuthResourcePaths...), AuthResourcePathsUnrestricted: unrestricted,
		ResourceType: resourceType,
	})
	if err != nil {
		return nil, discoveryError(err)
	}
	result := make([]schema.FieldCandidate, 0, len(fields))
	for _, field := range fields {
		if isLoomMetadataField(field.Path) {
			continue
		}
		result = append(result, schema.FieldCandidate{
			ResourceType: field.ResourceType, Path: field.Path, Kind: field.Kind,
			DistinctValues: append([]string(nil), field.DistinctValues...), DistinctTruncated: field.DistinctTruncated, PivotCandidate: field.PivotCandidate,
			PivotFamily: field.PivotFamily, PivotColumns: append([]string(nil), field.PivotColumns...),
			PivotColumnSelect: field.PivotColumnSelect, PivotValueSelect: field.PivotValueSelect,
			PivotItemSource: field.PivotItemSource, PivotItemResourceType: field.PivotItemResourceType,
			PivotValueSelectors: append([]string(nil), field.PivotValueSelectors...),
			ExtensionValues:     extensionObservations(field.ExtensionValues),
		})
	}
	return result, nil
}

func discoveryError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return err
	}
	switch {
	case errors.Is(err, authscope.ErrUnauthenticated):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeUnauthenticated, "")
	case errors.Is(err, authscope.ErrForbidden):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeForbidden, "")
	case errors.Is(err, context.Canceled):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeClientCanceled, "")
	case errors.Is(err, context.DeadlineExceeded):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	default:
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
}

func extensionObservations(fields []catalog.ExtensionValueObservation) []schema.ExtensionValueObservation {
	values := make([]schema.ExtensionValueObservation, len(fields))
	for i, value := range fields {
		values[i] = schema.ExtensionValueObservation{URL: value.URL, SourcePath: value.SourcePath, ValuePath: value.ValuePath, ValueType: value.ValueType, URLPath: append([]string(nil), value.URLPath...)}
	}
	return values
}

func isLoomMetadataField(path string) bool {
	switch path {
	case "project_id", "auth_resource_path", "dataset_generation":
		return true
	default:
		return false
	}
}
