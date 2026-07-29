package graphqlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/fhirstructs"
	"github.com/calypr/loom/graphqlapi/model"
	queryapi "github.com/calypr/loom/graphqlapi/query"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
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
		return fmt.Errorf("resource decode failed: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("resource decode failed: %w", err)
	}
	fhirReferenceLoaderFromContext(ctx, r.Resolver).register(out, project)
	return nil
}

func (r *Resolver) resolveFHIRReference(ctx context.Context, ref *fhirstructs.Reference, requested *model.FHIRResourceType, optional bool) (model.FHIRResource, error) {
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
	target, id, valid := normalizeFHIRReference(*ref.Reference)
	if !valid {
		return referenceError(optional)
	}
	if requested != nil {
		expected := strings.TrimSuffix(requested.String(), "")
		expected = strings.ReplaceAll(strings.ToLower(expected), "_", "")
		if strings.ReplaceAll(strings.ToLower(target), "_", "") != expected {
			if optional {
				return nil, nil
			}
			return nil, dataframeerrors.NewError(dataframeerrors.CodeReferenceNotResolved, "reference could not be resolved")
		}
	}
	value, err := loader.load(ctx, fhirReferenceLookup{project: owner.project, target: target, id: id})
	if err != nil {
		return referenceError(optional)
	}
	return value, nil
}
