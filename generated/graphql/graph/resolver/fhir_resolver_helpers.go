package resolver

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	fhir "github.com/calypr/loom/generated/fhir"
	"github.com/calypr/loom/generated/graphql/graph/model"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
)

// resolveFHIRField is used by gqlgen's generated autobind adapters for the
// generated FHIR structs. It honors both JSON and gqlgen tags (including
// primitive-extension names such as _birthDate) and keeps missing historical
// fields nullable instead of panicking.
func resolveFHIRField[T any](obj any, name string) (T, error) {
	var zero T
	v := reflect.ValueOf(obj)
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return zero, nil
		}
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return zero, nil
	}
	target := reflect.TypeOf((*T)(nil)).Elem()
	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		gqlName := field.Tag.Get("gqlgen")
		if jsonName != name && gqlName != name {
			continue
		}
		value := v.Field(i)
		if !value.IsValid() || !value.CanInterface() {
			return zero, nil
		}
		if value.Type().AssignableTo(target) {
			return value.Interface().(T), nil
		}
		if value.Type().ConvertibleTo(target) {
			return value.Convert(target).Interface().(T), nil
		}
		return zero, nil
	}
	return zero, nil
}

// listFHIR is shared by the generated exact-capitalization root adapters. It
// keeps generated methods as thin type-conversion wrappers while all query
// planning and execution remains in queryapi.Service.
func (r *queryResolver) listFHIR(ctx context.Context, project string, filters []*model.FhirFilterInput, requestedLimit *int, out any, resourceType string) error {
	limit := 25
	if requestedLimit != nil {
		limit = *requestedLimit
	}
	result, err := r.query.ListFHIR(ctx, queryapi.FHIRListRequest{Project: project, ResourceType: resourceType, Filters: filters, Limit: limit})
	if err != nil {
		return err
	}
	data, err := json.Marshal(result.Resources)
	if err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeResourceDecodeFailed, "")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeResourceDecodeFailed, "")
	}
	fhirReferenceLoaderFromContext(ctx, r.Resolver).registerRoots(out, result.Resources, result.ReadContext)
	return nil
}

func (r *Resolver) resolveFHIRReference(ctx context.Context, ref *fhir.Reference, requested *model.FHIRResourceType, optional bool) (model.FHIRResource, error) {
	if ref == nil || ref.Reference == nil || strings.TrimSpace(*ref.Reference) == "" {
		if optional {
			return nil, nil
		}
		return nil, dataframeerrors.NewError(dataframeerrors.CodeReferenceNotResolved, "reference could not be resolved")
	}
	loader := fhirReferenceLoaderFromContext(ctx, r)
	owner, ok := loader.owner(ref)
	if !ok {
		if optional {
			return nil, nil
		}
		return nil, dataframeerrors.NewError(dataframeerrors.CodeReferenceNotResolved, "reference could not be resolved")
	}
	if owner.depth >= fhirReferenceMaxDepth {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeQueryDepthExceeded, "reference query depth limit exceeded")
	}
	parsed, valid := parseFHIRReference(*ref.Reference)
	if !valid {
		return referenceError(optional)
	}
	if parsed.contained {
		resource, ok := owner.contained[parsed.id]
		if !ok {
			return referenceError(optional)
		}
		target, ok := resource["resourceType"].(string)
		if !ok || !sameFHIRResourceType(target, requested) {
			return referenceError(optional)
		}
		value, err := decodeFHIRResource(target, resource)
		if err != nil {
			return referenceError(optional)
		}
		loader.register(value, fhirReferenceOwner{
			read:      owner.read,
			contained: owner.contained,
			depth:     owner.depth + 1,
		})
		return value, nil
	}
	if requested != nil {
		if !sameFHIRResourceType(parsed.target, requested) {
			return referenceError(optional)
		}
	}
	scope := fhirReferenceScopeKey{
		project:    owner.read.Project,
		generation: owner.read.DatasetGeneration,
		digest:     owner.read.ScopeDigest,
	}
	resource, err := loader.load(ctx, fhirReferenceLookup{scope: scope, target: parsed.target, id: parsed.id}, owner.read)
	if err != nil {
		return referenceError(optional)
	}
	value, err := decodeFHIRResource(parsed.target, resource)
	if err != nil {
		return referenceError(optional)
	}
	loader.register(value, fhirReferenceOwner{
		read:      owner.read,
		contained: indexContainedResources(resource),
		depth:     owner.depth + 1,
	})
	return value, nil
}

func sameFHIRResourceType(resourceType string, requested *model.FHIRResourceType) bool {
	if requested == nil {
		return true
	}
	return strings.ReplaceAll(strings.ToLower(resourceType), "_", "") ==
		strings.ReplaceAll(strings.ToLower(requested.String()), "_", "")
}
