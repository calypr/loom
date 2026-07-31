package resolver

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	fhir "github.com/calypr/loom/generated/fhir"
	"github.com/calypr/loom/generated/graphqlapi/model"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	queryapi "github.com/calypr/loom/internal/graphqlapi/query"
)

const fhirReferenceBatchSize = 256
const fhirReferenceMaxDepth = 4

type fhirReferenceLoaderKey struct{}

type fhirReferenceScopeKey struct {
	project, generation, digest string
}
type fhirReferenceOwner struct {
	read      queryapi.FHIRReadContext
	contained map[string]map[string]any
	depth     int
}
type fhirReferenceLookup struct {
	scope      fhirReferenceScopeKey
	target, id string
}
type fhirReferenceResult struct {
	resource map[string]any
	err      error
}
type fhirReferenceWaiter struct{ ch chan fhirReferenceResult }

// fhirReferenceLoader batches only for the lifetime of one GraphQL operation.
// Keeping it request-local prevents cross-user cache and authorization leaks.
type fhirReferenceLoader struct {
	resolver *Resolver

	mu       sync.Mutex
	owners   map[*fhir.Reference]fhirReferenceOwner
	cache    map[fhirReferenceLookup]fhirReferenceResult
	pending  map[fhirReferenceLookup][]*fhirReferenceWaiter
	contexts map[fhirReferenceScopeKey]queryapi.FHIRReadContext
	timerSet bool
}

func (l *fhirReferenceLoader) owner(ref *fhir.Reference) (fhirReferenceOwner, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	owner, ok := l.owners[ref]
	return owner, ok
}

func withFHIRReferenceLoader(ctx context.Context, resolver *Resolver) context.Context {
	return context.WithValue(ctx, fhirReferenceLoaderKey{}, &fhirReferenceLoader{
		resolver: resolver,
		owners:   make(map[*fhir.Reference]fhirReferenceOwner),
		cache:    make(map[fhirReferenceLookup]fhirReferenceResult),
		pending:  make(map[fhirReferenceLookup][]*fhirReferenceWaiter),
		contexts: make(map[fhirReferenceScopeKey]queryapi.FHIRReadContext),
	})
}

// WithOperationContext installs request-scoped FHIR reference batching.
func (r *Resolver) WithOperationContext(ctx context.Context) context.Context {
	return withFHIRReferenceLoader(ctx, r)
}

func fhirReferenceLoaderFromContext(ctx context.Context, resolver *Resolver) *fhirReferenceLoader {
	if loader, ok := ctx.Value(fhirReferenceLoaderKey{}).(*fhirReferenceLoader); ok {
		return loader
	}
	// Direct resolver tests do not pass through HTTP middleware.
	return &fhirReferenceLoader{
		resolver: resolver,
		owners:   make(map[*fhir.Reference]fhirReferenceOwner),
		cache:    make(map[fhirReferenceLookup]fhirReferenceResult),
		pending:  make(map[fhirReferenceLookup][]*fhirReferenceWaiter),
		contexts: make(map[fhirReferenceScopeKey]queryapi.FHIRReadContext),
	}
}

func (l *fhirReferenceLoader) register(value any, owner fhirReferenceOwner) {
	l.mu.Lock()
	defer l.mu.Unlock()
	seen := map[uintptr]bool{}
	var visit func(reflect.Value)
	visit = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		if v.Kind() == reflect.Interface {
			if !v.IsNil() {
				visit(v.Elem())
			}
			return
		}
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return
			}
			if ref, ok := v.Interface().(*fhir.Reference); ok {
				l.owners[ref] = owner
			}
			p := v.Pointer()
			if seen[p] {
				return
			}
			seen[p] = true
			visit(v.Elem())
			return
		}
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				visit(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		}
	}
	visit(reflect.ValueOf(value))
}

func (l *fhirReferenceLoader) registerRoots(value any, resources []map[string]any, read queryapi.FHIRReadContext) {
	v := reflect.ValueOf(value)
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if !v.IsValid() || (v.Kind() != reflect.Slice && v.Kind() != reflect.Array) {
		return
	}
	for i := 0; i < v.Len() && i < len(resources); i++ {
		l.register(v.Index(i).Interface(), fhirReferenceOwner{
			read:      read,
			contained: indexContainedResources(resources[i]),
		})
	}
}

func (l *fhirReferenceLoader) load(ctx context.Context, lookup fhirReferenceLookup, read queryapi.FHIRReadContext) (map[string]any, error) {
	l.mu.Lock()
	if result, ok := l.cache[lookup]; ok {
		l.mu.Unlock()
		return result.resource, result.err
	}
	l.contexts[lookup.scope] = read
	waiter := &fhirReferenceWaiter{ch: make(chan fhirReferenceResult, 1)}
	l.pending[lookup] = append(l.pending[lookup], waiter)
	if !l.timerSet {
		l.timerSet = true
		// ponytail: fixed 1ms coalescing window; tune only if profiling shows missed sibling batches.
		time.AfterFunc(time.Millisecond, func() { l.dispatch(ctx) })
	}
	l.mu.Unlock()
	select {
	case result := <-waiter.ch:
		return result.resource, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *fhirReferenceLoader) dispatch(ctx context.Context) {
	l.mu.Lock()
	pending := l.pending
	l.pending = make(map[fhirReferenceLookup][]*fhirReferenceWaiter)
	l.timerSet = false
	l.mu.Unlock()

	type groupKey struct {
		scope  fhirReferenceScopeKey
		target string
	}
	groups := make(map[groupKey][]fhirReferenceLookup)
	for lookup := range pending {
		group := groupKey{scope: lookup.scope, target: lookup.target}
		groups[group] = append(groups[group], lookup)
	}
	for _, lookups := range groups {
		for start := 0; start < len(lookups); start += fhirReferenceBatchSize {
			end := start + fhirReferenceBatchSize
			if end > len(lookups) {
				end = len(lookups)
			}
			l.runBatch(ctx, lookups[start:end], pending)
		}
	}
}

func (l *fhirReferenceLoader) runBatch(ctx context.Context, lookups []fhirReferenceLookup, pending map[fhirReferenceLookup][]*fhirReferenceWaiter) {
	if len(lookups) == 0 {
		return
	}
	values := make([]*model.FhirFilterValueInput, 0, len(lookups))
	for _, lookup := range lookups {
		id := lookup.id
		values = append(values, &model.FhirFilterValueInput{Kind: model.FhirFilterValueKindString, String: &id})
	}
	l.mu.Lock()
	read, ok := l.contexts[lookups[0].scope]
	l.mu.Unlock()
	if !ok {
		l.resolveBatchError(lookups, pending, dataframeerrors.NewError(dataframeerrors.CodeReferenceNotResolved, "reference could not be resolved"))
		return
	}
	result, err := l.resolver.query.ListFHIRInContext(ctx, read, queryapi.FHIRListRequest{
		Project: read.Project, ResourceType: lookups[0].target, Limit: len(lookups),
		Filters: []*model.FhirFilterInput{{Select: "id", Operator: model.FhirFilterOperatorIn, Values: values}},
	})
	byID := make(map[string]map[string]any)
	if err == nil {
		for _, resource := range result.Resources {
			if id, ok := resource["id"].(string); ok {
				byID[id] = resource
			}
		}
	}
	for _, lookup := range lookups {
		var resolved fhirReferenceResult
		if err != nil {
			resolved.err = err
		} else if resource, ok := byID[lookup.id]; ok {
			resolved.resource = resource
		} else {
			resolved.err = dataframeerrors.NewError(dataframeerrors.CodeReferenceNotResolved, "reference could not be resolved")
		}
		l.mu.Lock()
		l.cache[lookup] = resolved
		waiters := pending[lookup]
		l.mu.Unlock()
		for _, waiter := range waiters {
			waiter.ch <- resolved
		}
	}
}

func (l *fhirReferenceLoader) resolveBatchError(lookups []fhirReferenceLookup, pending map[fhirReferenceLookup][]*fhirReferenceWaiter, err error) {
	for _, lookup := range lookups {
		resolved := fhirReferenceResult{err: err}
		l.mu.Lock()
		l.cache[lookup] = resolved
		waiters := pending[lookup]
		l.mu.Unlock()
		for _, waiter := range waiters {
			waiter.ch <- resolved
		}
	}
}

func decodeFHIRResource(resourceType string, resource map[string]any) (model.FHIRResource, error) {
	resourceValue, ok := fhir.NewConcreteResource(resourceType)
	if !ok {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidResourceType, "invalid resource type")
	}
	value, ok := resourceValue.(model.FHIRResource)
	if !ok {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeResourceDecodeFailed, "resource decode failed")
	}
	data, err := json.Marshal(resource)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return nil, err
	}
	return value, nil
}

type parsedFHIRReference struct {
	target, id string
	contained  bool
}

func parseFHIRReference(ref string) (parsedFHIRReference, bool) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "#") {
		id := strings.TrimPrefix(ref, "#")
		return parsedFHIRReference{id: id, contained: true}, id != "" && !strings.ContainsAny(id, "/?#")
	}
	parsed, err := url.Parse(ref)
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return parsedFHIRReference{}, false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for _, part := range parts {
		if part == "" {
			return parsedFHIRReference{}, false
		}
	}
	absolute := parsed.IsAbs() && parsed.Host != ""
	if (parsed.IsAbs() && !absolute) || (parsed.Host != "" && !absolute) {
		return parsedFHIRReference{}, false
	}
	if len(parts) >= 4 && parts[len(parts)-2] == "_history" {
		if parts[len(parts)-1] == "" {
			return parsedFHIRReference{}, false
		}
		parts = parts[:len(parts)-2]
	}
	if (!absolute && len(parts) != 2) || (absolute && len(parts) < 2) {
		return parsedFHIRReference{}, false
	}
	target, id := parts[len(parts)-2], parts[len(parts)-1]
	if target == "" || id == "" || !fhirschema.HasResource(target) {
		return parsedFHIRReference{}, false
	}
	return parsedFHIRReference{target: target, id: id}, true
}

func normalizeFHIRReference(ref string) (string, string, bool) {
	parsed, ok := parseFHIRReference(ref)
	return parsed.target, parsed.id, ok && !parsed.contained
}

func indexContainedResources(resource map[string]any) map[string]map[string]any {
	raw, ok := resource["contained"]
	if !ok {
		return nil
	}
	var values []map[string]any
	switch raw := raw.(type) {
	case []any:
		values = make([]map[string]any, 0, len(raw))
		for _, value := range raw {
			if item, ok := value.(map[string]any); ok {
				values = append(values, item)
			}
		}
	case []map[string]any:
		values = raw
	default:
		return nil
	}
	contained := make(map[string]map[string]any, len(values))
	for _, item := range values {
		if id, ok := item["id"].(string); ok && id != "" {
			contained[id] = item
		}
	}
	return contained
}

func referenceError(optional bool) (model.FHIRResource, error) {
	if optional {
		return nil, nil
	}
	return nil, dataframeerrors.NewError(dataframeerrors.CodeReferenceNotResolved, "reference could not be resolved")
}
