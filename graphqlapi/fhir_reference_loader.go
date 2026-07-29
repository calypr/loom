package graphqlapi

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/calypr/loom/fhirstructs"
	"github.com/calypr/loom/graphqlapi/model"
	queryapi "github.com/calypr/loom/graphqlapi/query"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

const fhirReferenceBatchSize = 256

type fhirReferenceLoaderKey struct{}

type fhirReferenceOwner struct{ project string }
type fhirReferenceLookup struct {
	project, target, id string
}
type fhirReferenceResult struct {
	value model.FHIRResource
	err   error
}
type fhirReferenceWaiter struct{ ch chan fhirReferenceResult }

// fhirReferenceLoader batches only for the lifetime of one GraphQL operation.
// Keeping it request-local prevents cross-user cache and authorization leaks.
type fhirReferenceLoader struct {
	resolver *Resolver

	mu       sync.Mutex
	owners   map[*fhirstructs.Reference]fhirReferenceOwner
	cache    map[fhirReferenceLookup]fhirReferenceResult
	pending  map[fhirReferenceLookup][]*fhirReferenceWaiter
	timerSet bool
}

func (l *fhirReferenceLoader) owner(ref *fhirstructs.Reference) (fhirReferenceOwner, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	owner, ok := l.owners[ref]
	return owner, ok
}

func withFHIRReferenceLoader(ctx context.Context, resolver *Resolver) context.Context {
	return context.WithValue(ctx, fhirReferenceLoaderKey{}, &fhirReferenceLoader{
		resolver: resolver,
		owners:   make(map[*fhirstructs.Reference]fhirReferenceOwner),
		cache:    make(map[fhirReferenceLookup]fhirReferenceResult),
		pending:  make(map[fhirReferenceLookup][]*fhirReferenceWaiter),
	})
}

func fhirReferenceLoaderFromContext(ctx context.Context, resolver *Resolver) *fhirReferenceLoader {
	if loader, ok := ctx.Value(fhirReferenceLoaderKey{}).(*fhirReferenceLoader); ok {
		return loader
	}
	// Direct resolver tests do not pass through HTTP middleware.
	return &fhirReferenceLoader{
		resolver: resolver,
		owners:   make(map[*fhirstructs.Reference]fhirReferenceOwner),
		cache:    make(map[fhirReferenceLookup]fhirReferenceResult),
		pending:  make(map[fhirReferenceLookup][]*fhirReferenceWaiter),
	}
}

func (l *fhirReferenceLoader) register(value any, project string) {
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
			if ref, ok := v.Interface().(*fhirstructs.Reference); ok {
				l.owners[ref] = fhirReferenceOwner{project: project}
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

func (l *fhirReferenceLoader) load(ctx context.Context, lookup fhirReferenceLookup) (model.FHIRResource, error) {
	l.mu.Lock()
	if result, ok := l.cache[lookup]; ok {
		l.mu.Unlock()
		return result.value, result.err
	}
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
		return result.value, result.err
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

	groups := make(map[string][]fhirReferenceLookup)
	for lookup := range pending {
		group := lookup.project + "\x00" + lookup.target
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
	result, err := l.resolver.query.ListFHIR(ctx, queryapi.FHIRListRequest{
		Project: lookups[0].project, ResourceType: lookups[0].target, Limit: len(lookups),
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
			resolved.value, resolved.err = decodeFHIRResource(lookup.target, resource)
		} else {
			resolved.err = dataframeerrors.NewError(dataframeerrors.CodeReferenceNotResolved, "reference could not be resolved")
		}
		if resolved.value != nil {
			l.register(resolved.value, lookups[0].project)
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

func decodeFHIRResource(resourceType string, resource map[string]any) (model.FHIRResource, error) {
	var value model.FHIRResource
	switch resourceType {
	case "BodyStructure":
		value = &fhirstructs.BodyStructure{}
	case "Condition":
		value = &fhirstructs.Condition{}
	case "DiagnosticReport":
		value = &fhirstructs.DiagnosticReport{}
	case "Patient":
		value = &fhirstructs.Patient{}
	case "Specimen":
		value = &fhirstructs.Specimen{}
	case "DocumentReference":
		value = &fhirstructs.DocumentReference{}
	case "Group":
		value = &fhirstructs.Group{}
	case "FamilyMemberHistory":
		value = &fhirstructs.FamilyMemberHistory{}
	case "ImagingStudy":
		value = &fhirstructs.ImagingStudy{}
	case "Medication":
		value = &fhirstructs.Medication{}
	case "MedicationAdministration":
		value = &fhirstructs.MedicationAdministration{}
	case "MedicationRequest":
		value = &fhirstructs.MedicationRequest{}
	case "MedicationStatement":
		value = &fhirstructs.MedicationStatement{}
	case "Observation":
		value = &fhirstructs.Observation{}
	case "Organization":
		value = &fhirstructs.Organization{}
	case "Practitioner":
		value = &fhirstructs.Practitioner{}
	case "PractitionerRole":
		value = &fhirstructs.PractitionerRole{}
	case "Procedure":
		value = &fhirstructs.Procedure{}
	case "ResearchStudy":
		value = &fhirstructs.ResearchStudy{}
	case "ResearchSubject":
		value = &fhirstructs.ResearchSubject{}
	case "Substance":
		value = &fhirstructs.Substance{}
	case "SubstanceDefinition":
		value = &fhirstructs.SubstanceDefinition{}
	case "Task":
		value = &fhirstructs.Task{}
	default:
		return nil, fmt.Errorf("unsupported resource type %s", resourceType)
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

func normalizeFHIRReference(ref string) (string, string, bool) {
	ref = strings.TrimSpace(ref)
	if scheme := strings.Index(ref, "://"); scheme >= 0 {
		if slash := strings.Index(ref[scheme+3:], "/"); slash >= 0 {
			ref = ref[scheme+3+slash:]
		} else {
			return "", "", false
		}
	}
	parts := strings.Split(strings.Trim(ref, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func referenceError(optional bool) (model.FHIRResource, error) {
	if optional {
		return nil, nil
	}
	return nil, dataframeerrors.NewError(dataframeerrors.CodeReferenceNotResolved, "reference could not be resolved")
}
