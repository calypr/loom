package resolver

import (
	"context"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"strings"
	"sync"
	"testing"

	fhir "github.com/calypr/loom/generated/fhir"
	"github.com/calypr/loom/generated/graphql/graph/model"
	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
)

const fhirReferenceTestGeneration = "generation-1"

func TestParseFHIRReference(t *testing.T) {
	for _, test := range []struct {
		name, input, target, id, version string
	}{
		{name: "relative", input: "Patient/123", target: "Patient", id: "123"},
		{name: "absolute", input: "https://example.test/fhir/Patient/123", target: "Patient", id: "123"},
		{name: "versioned", input: "Patient/123/_history/4", target: "Patient", id: "123", version: "4"},
		{name: "absolute-versioned", input: "https://example.test/fhir/Patient/123/_history/4", target: "Patient", id: "123", version: "4"},
		{name: "absolute-versioned-multi-segment", input: "https://example.test/fhir/Patient/123/_history/v-1.2", target: "Patient", id: "123", version: "v-1.2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			parsed, ok := parseFHIRReference(test.input)
			if !ok || parsed.contained || parsed.target != test.target || parsed.id != test.id || parsed.version != test.version {
				t.Fatalf("parseFHIRReference(%q) = %#v, %v", test.input, parsed, ok)
			}
		})
	}
	for _, invalid := range []string{
		"not-a-reference",
		"Patient/123/extra",
		"Patient/123/_history",
		"Patient/123/_history/",
		"Patient/123/_history/bad~version",
		"Patient/123/_history/" + strings.Repeat("v", 65),
		"urn:uuid:123",
		"https://example.test/fhir/Unsupported/123",
	} {
		if _, ok := parseFHIRReference(invalid); ok {
			t.Fatalf("malformed reference %q accepted", invalid)
		}
	}
}

func TestResolveFHIRContainedReference(t *testing.T) {
	reference := "#patient"
	root := &fhir.DocumentReference{
		Subject: &fhir.Reference{Reference: &reference},
	}
	resolver := &Resolver{}
	ctx := withFHIRReferenceLoader(context.Background(), resolver)
	loader := fhirReferenceLoaderFromContext(ctx, resolver)
	loader.register(root, fhirReferenceOwner{
		read: queryapi.FHIRReadContext{
			Project: "P1", DatasetGeneration: "generation-1",
			ScopeDigest: "scope",
			Scope:       authscope.ReadScope{Mode: authscope.ReadScopeRestricted},
		},
		contained: indexContainedResources(map[string]any{
			"contained": []any{map[string]any{
				"id": "patient", "resourceType": "Patient", "gender": "female",
			}},
		}),
	})

	requested := model.FHIRResourceTypePatient
	value, err := resolver.resolveFHIRReference(ctx, root.Subject, &requested, false)
	if err != nil {
		t.Fatalf("resolveFHIRReference() error = %v", err)
	}
	patient, ok := value.(*fhir.Patient)
	if !ok || patient.GetID() != "patient" || patient.Gender == nil || *patient.Gender != "female" {
		t.Fatalf("resolveFHIRReference() = %#v, want contained Patient", value)
	}
}

func TestResolveFHIRReferenceDepthLimitIsNotOptional(t *testing.T) {
	reference := "Patient/123"
	ref := &fhir.Reference{Reference: &reference}
	resolver := &Resolver{}
	ctx := withFHIRReferenceLoader(context.Background(), resolver)
	fhirReferenceLoaderFromContext(ctx, resolver).register(ref, fhirReferenceOwner{
		read:  queryapi.FHIRReadContext{Project: "P1", DatasetGeneration: "generation-1", ScopeDigest: "scope"},
		depth: fhirReferenceMaxDepth,
	})

	_, err := resolver.resolveFHIRReference(ctx, ref, nil, true)
	assertFHIRReferenceErrorCode(t, err, dataframeerrors.CodeQueryDepthExceeded)
}

func TestFHIRReferenceBatchSize(t *testing.T) {
	if fhirReferenceBatchSize != 256 {
		t.Fatalf("batch size = %d, want 256", fhirReferenceBatchSize)
	}
}

func TestResolveFHIRReferenceVersionedResolvesMatchingMetaVersionID(t *testing.T) {
	harness := newFHIRReferenceTestHarness(t, []fhirReferenceTestRow{
		{id: "123", version: "4", gender: "female"},
		{id: "999", version: "9", gender: "male"},
	})

	patient := harness.patient(t, "Patient/123/_history/4")
	if patient.Gender == nil || *patient.Gender != "female" {
		t.Fatalf("versioned reference resolved %#v, want the versionId=4 row", patient)
	}
	if got := fhirReferenceTestVersion(t, patient); got != "4" {
		t.Fatalf("versioned reference resolved meta.versionId = %q, want 4", got)
	}
	if calls := harness.probe.callCount(); calls != 1 {
		t.Fatalf("query calls = %d, want one batched lookup", calls)
	}
}

func TestResolveFHIRReferenceUnversionedKeepsSelectedGenerationRow(t *testing.T) {
	harness := newFHIRReferenceTestHarness(t, []fhirReferenceTestRow{
		{id: "123", version: "7", gender: "male"},
	})

	patient := harness.patient(t, "Patient/123")
	if patient.Gender == nil || *patient.Gender != "male" {
		t.Fatalf("unversioned reference resolved %#v, want the selected-generation row", patient)
	}
	if got := fhirReferenceTestVersion(t, patient); got != "7" {
		t.Fatalf("unversioned reference resolved meta.versionId = %q, want 7", got)
	}
	if ids := harness.probe.requestedIDs(); len(ids) != 1 || ids[0] != "123" {
		t.Fatalf("id filter = %v, want [123]", ids)
	}
}

func TestResolveFHIRReferenceVersionedMismatchDoesNotReturnCurrentRow(t *testing.T) {
	harness := newFHIRReferenceTestHarness(t, []fhirReferenceTestRow{
		{id: "123", version: "7", gender: "male"},
	})

	_, err := harness.resolve("Patient/123/_history/9", false)
	assertFHIRReferenceErrorCode(t, err, dataframeerrors.CodeReferenceNotResolved)

	optional, err := harness.resolve("Patient/123/_history/9", true)
	if err != nil || optional != nil {
		t.Fatalf("optional mismatched version = %#v, %v; want nil, nil", optional, err)
	}
}

func TestResolveFHIRReferenceRowWithoutMetaVersionIDDoesNotMatchVersion(t *testing.T) {
	harness := newFHIRReferenceTestHarness(t, []fhirReferenceTestRow{
		{id: "123", gender: "male"},
	})

	if _, err := harness.resolve("Patient/123/_history/1", false); err == nil {
		t.Fatal("versioned reference resolved a row without meta.versionId")
	}
	patient := harness.patient(t, "Patient/123")
	if patient.GetID() != "123" {
		t.Fatalf("unversioned reference resolved %#v, want the same-id row", patient)
	}
}

func TestResolveFHIRReferenceVersionedStaysInPinnedGeneration(t *testing.T) {
	harness := newFHIRReferenceTestHarness(t, []fhirReferenceTestRow{
		{id: "123", version: "4", gender: "female"},
		{id: "123", version: "4", generation: "generation-2", gender: "male"},
	})

	patient := harness.patient(t, "Patient/123/_history/4")
	if patient.Gender == nil || *patient.Gender != "female" {
		t.Fatalf("resolved %#v, want the pinned-generation row", patient)
	}
	for _, generation := range harness.probe.calledGenerations() {
		if generation != fhirReferenceTestGeneration {
			t.Fatalf("query generation = %q, want only %q", generation, fhirReferenceTestGeneration)
		}
	}
}

func TestResolveFHIRReferenceUnauthorizedScopeIsNotResolved(t *testing.T) {
	harness := newFHIRReferenceTestHarnessWithScope(t,
		[]fhirReferenceTestRow{{id: "123", version: "4", gender: "female", authPath: "/programs/example/projects/other"}},
		&authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/example/projects/allowed"}},
	)

	if _, err := harness.resolve("Patient/123/_history/4", false); err == nil {
		t.Fatal("versioned reference resolved outside the authorized scope")
	}
	optional, err := harness.resolve("Patient/123/_history/4", true)
	if err != nil || optional != nil {
		t.Fatalf("optional out-of-scope reference = %#v, %v; want nil, nil", optional, err)
	}
}

func TestResolveFHIRReferenceWrongTargetTypeIsRejectedBeforeLookup(t *testing.T) {
	harness := newFHIRReferenceTestHarness(t, []fhirReferenceTestRow{
		{id: "123", version: "4", gender: "female"},
	})
	ctx, subject := harness.subject("Patient/123/_history/4")

	requested := model.FHIRResourceTypeObservation
	if _, err := harness.resolver.resolveFHIRReference(ctx, subject, &requested, false); err == nil {
		t.Fatal("reference resolved despite a mismatched requested type")
	}
	if calls := harness.probe.callCount(); calls != 0 {
		t.Fatalf("query calls = %d, want type validation before any lookup", calls)
	}
}

func TestResolveFHIRReferenceMissingTargetIsNotResolved(t *testing.T) {
	harness := newFHIRReferenceTestHarness(t, []fhirReferenceTestRow{
		{id: "123", version: "4", gender: "female"},
	})

	if _, err := harness.resolve("Patient/404/_history/4", false); err == nil {
		t.Fatal("reference resolved for an absent logical id")
	}
	optional, err := harness.resolve("Patient/404/_history/4", true)
	if err != nil || optional != nil {
		t.Fatalf("optional absent reference = %#v, %v; want nil, nil", optional, err)
	}
}

// TestFHIRReferenceBatchKeepsVersionedAndUnversionedLookupsApart drives the
// batch path with one versioned and one unversioned lookup for the same
// logical id: both must be answered from a single query and neither may
// receive the other resource version.
func TestFHIRReferenceBatchKeepsVersionedAndUnversionedLookupsApart(t *testing.T) {
	harness := newFHIRReferenceTestHarness(t, []fhirReferenceTestRow{
		{id: "123", version: "4", gender: "female"},
		{id: "123", version: "7", gender: "male"},
	})
	loader := fhirReferenceLoaderFromContext(harness.context(), harness.resolver)
	scope := fhirReferenceTestScopeKey(harness.ownerRead)
	loader.contexts[scope] = harness.ownerRead

	versioned := fhirReferenceLookup{scope: scope, target: "Patient", id: "123", version: "4"}
	unversioned := fhirReferenceLookup{scope: scope, target: "Patient", id: "123"}
	pending := map[fhirReferenceLookup][]*fhirReferenceWaiter{
		versioned:   {{ch: make(chan fhirReferenceResult, 1)}},
		unversioned: {{ch: make(chan fhirReferenceResult, 1)}},
	}
	loader.runBatch(context.Background(), []fhirReferenceLookup{versioned, unversioned}, pending)

	versionedResult := <-pending[versioned][0].ch
	unversionedResult := <-pending[unversioned][0].ch
	if versionedResult.err != nil || fhirResourceVersionID(versionedResult.resource) != "4" {
		t.Fatalf("versioned lookup = %#v, %v; want the versionId=4 row", versionedResult.resource, versionedResult.err)
	}
	if unversionedResult.err != nil || fhirResourceVersionID(unversionedResult.resource) != "7" {
		t.Fatalf("unversioned lookup = %#v, %v; want the selected-generation row", unversionedResult.resource, unversionedResult.err)
	}
	if calls := harness.probe.callCount(); calls != 1 {
		t.Fatalf("query calls = %d, want one batch", calls)
	}
	if ids := harness.probe.requestedIDs(); len(ids) != 1 || ids[0] != "123" {
		t.Fatalf("batch id filter = %v, want the deduplicated id [123]", ids)
	}
	if _, ok := loader.cache[versioned]; !ok {
		t.Fatal("versioned lookup was not cached under its own key")
	}
	if _, ok := loader.cache[unversioned]; !ok {
		t.Fatal("unversioned lookup was not cached under its own key")
	}
}

// TestResolveFHIRReferenceSiblingVersionsResolveIndependently mirrors sibling
// field resolvers in one operation asking for two versions of one logical
// resource plus an absent version; each must get its own outcome.
func TestResolveFHIRReferenceSiblingVersionsResolveIndependently(t *testing.T) {
	harness := newFHIRReferenceTestHarness(t, []fhirReferenceTestRow{
		{id: "123", version: "4", gender: "female"},
		{id: "123", version: "7", gender: "male"},
	})
	versioned := "Patient/123/_history/4"
	unversioned := "Patient/123"
	absent := "Patient/123/_history/9"
	root := &fhir.DocumentReference{
		Subject:   &fhir.Reference{Reference: &versioned},
		Custodian: &fhir.Reference{Reference: &unversioned},
		Context:   []*fhir.Reference{{Reference: &absent}},
	}
	ctx := harness.context()
	fhirReferenceLoaderFromContext(ctx, harness.resolver).register(root, fhirReferenceOwner{read: harness.ownerRead})

	type outcome struct {
		patient *fhir.Patient
		err     error
	}
	results := make([]outcome, 3)
	var wg sync.WaitGroup
	for index, ref := range []*fhir.Reference{root.Subject, root.Custodian, root.Context[0]} {
		wg.Add(1)
		go func(index int, ref *fhir.Reference) {
			defer wg.Done()
			value, err := harness.resolver.resolveFHIRReference(ctx, ref, nil, false)
			patient, _ := value.(*fhir.Patient)
			results[index] = outcome{patient: patient, err: err}
		}(index, ref)
	}
	wg.Wait()

	if results[2].err == nil {
		t.Fatal("absent version resolved without an error")
	}
	for index, want := range []struct{ gender, version string }{{"female", "4"}, {"male", "7"}} {
		if results[index].err != nil {
			t.Fatalf("reference %d error = %v", index, results[index].err)
		}
		patient := results[index].patient
		if patient == nil || patient.Gender == nil || *patient.Gender != want.gender {
			t.Fatalf("reference %d resolved %#v, want gender %q", index, patient, want.gender)
		}
		if got := fhirReferenceTestVersion(t, patient); got != want.version {
			t.Fatalf("reference %d resolved meta.versionId = %q, want %q", index, got, want.version)
		}
	}
}

type fhirReferenceTestRow struct {
	id, version, generation, gender, authPath string
}

type fhirReferenceTestCall struct {
	generation string
	ids        []string
}

type fhirReferenceTestProbe struct {
	mu    sync.Mutex
	calls []fhirReferenceTestCall
	rows  []fhirReferenceTestRow
}

// queryRows emulates the compiled id-IN reference query against one pinned
// dataset generation, including the authorization filter and row limit.
func (p *fhirReferenceTestProbe) queryRows(_ context.Context, command command.CompiledCommand, _ int, visit dataframeexecution.RowVisitor) error {
	_, bindVars, deriveErr := command.AQLParameters()
	if deriveErr != nil {
		return deriveErr
	}

	generation, _ := bindVars["dataset_generation"].(string)
	ids := fhirReferenceTestBindStrings(bindVars["root_filter_1_value"])
	unrestricted, _ := bindVars["auth_resource_paths_unrestricted"].(bool)
	allowedPaths := fhirReferenceTestBindStrings(bindVars["auth_resource_paths"])
	limit := len(ids)
	if raw, ok := bindVars["limit"].(int); ok && raw > limit {
		limit = raw
	}

	p.mu.Lock()
	p.calls = append(p.calls, fhirReferenceTestCall{generation: generation, ids: append([]string(nil), ids...)})
	rows := append([]fhirReferenceTestRow(nil), p.rows...)
	p.mu.Unlock()

	matched := 0
	for _, row := range rows {
		if matched >= limit {
			break
		}
		rowGeneration := row.generation
		if rowGeneration == "" {
			rowGeneration = fhirReferenceTestGeneration
		}
		if !containsFHIRReferenceString(ids, row.id) || rowGeneration != generation {
			continue
		}
		if !unrestricted && !containsFHIRReferenceString(allowedPaths, row.authPath) {
			continue
		}
		matched++
		resource := map[string]any{"resourceType": "Patient", "id": row.id, "gender": row.gender}
		if row.version != "" {
			resource["meta"] = map[string]any{"versionId": row.version}
		}
		if err := visit(map[string]any{"resource": resource}); err != nil {
			return err
		}
	}
	return nil
}

func (p *fhirReferenceTestProbe) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func (p *fhirReferenceTestProbe) calledGenerations() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	generations := make([]string, 0, len(p.calls))
	for _, call := range p.calls {
		generations = append(generations, call.generation)
	}
	return generations
}

func (p *fhirReferenceTestProbe) requestedIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.calls) == 0 {
		return nil
	}
	return p.calls[0].ids
}

type fhirReferenceTestResourceAccess struct{}

func (fhirReferenceTestResourceAccess) GetAllowedResources(context.Context, string, string, string) ([]string, error) {
	return []string{"/programs/example/projects/allowed"}, nil
}

type fhirReferenceTestHarness struct {
	resolver  *Resolver
	probe     *fhirReferenceTestProbe
	ownerRead queryapi.FHIRReadContext
}

func newFHIRReferenceTestHarness(t *testing.T, rows []fhirReferenceTestRow) *fhirReferenceTestHarness {
	t.Helper()
	return newFHIRReferenceTestHarnessWithScope(t, rows, nil)
}

// newFHIRReferenceTestHarnessWithScope serves rows from one fake storage
// generation and registers roots under the given authorized read scope, so
// reference lookups exercise the real generation and scope pinning.
func newFHIRReferenceTestHarnessWithScope(t *testing.T, rows []fhirReferenceTestRow, scope *authscope.ReadScope) *fhirReferenceTestHarness {
	t.Helper()
	probe := &fhirReferenceTestProbe{rows: rows}
	service := queryapi.NewService(queryapi.Config{CompileTarget: compiler.ArangoTarget(),
		ScopeResolver: authscope.NewScopeResolver(authscope.ScopeResolverConfig{
			ResourceAccess: fhirReferenceTestResourceAccess{},
			ListExistingAuthResourcePaths: func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
				return []string{"example-allowed"}, nil
			},
		}),
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			return []catalog.PopulatedField{{
				Project: options.Project, ResourceType: options.ResourceType, Path: "gender", Kind: "scalar", DocCount: 2,
			}}, nil
		},
		Dataframes: dataframeexecution.NewService(dataframeexecution.ServiceConfig{
			QueryRows: probe.queryRows,
		}),
	})
	return &fhirReferenceTestHarness{
		resolver:  &Resolver{query: service},
		probe:     probe,
		ownerRead: fhirReferenceTestReadContext(scope),
	}
}

func (h *fhirReferenceTestHarness) context() context.Context {
	return withFHIRReferenceLoader(context.Background(), h.resolver)
}

// subject registers a root document carrying one subject reference under a
// fresh operation-local loader, the way the typed root resolver does.
func (h *fhirReferenceTestHarness) subject(ref string) (context.Context, *fhir.Reference) {
	reference := ref
	root := &fhir.DocumentReference{Subject: &fhir.Reference{Reference: &reference}}
	ctx := h.context()
	fhirReferenceLoaderFromContext(ctx, h.resolver).register(root, fhirReferenceOwner{read: h.ownerRead})
	return ctx, root.Subject
}

func (h *fhirReferenceTestHarness) resolve(ref string, optional bool) (model.FHIRResource, error) {
	ctx, subject := h.subject(ref)
	return h.resolver.resolveFHIRReference(ctx, subject, nil, optional)
}

func (h *fhirReferenceTestHarness) patient(t *testing.T, ref string) *fhir.Patient {
	t.Helper()
	value, err := h.resolve(ref, false)
	if err != nil {
		t.Fatalf("resolveFHIRReference(%q) error = %v", ref, err)
	}
	patient, ok := value.(*fhir.Patient)
	if !ok {
		t.Fatalf("resolveFHIRReference(%q) = %#v, want *fhir.Patient", ref, value)
	}
	return patient
}

func fhirReferenceTestReadContext(scope *authscope.ReadScope) queryapi.FHIRReadContext {
	read := queryapi.FHIRReadContext{
		Project:           "P1",
		DatasetGeneration: fhirReferenceTestGeneration,
		Scope:             authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted},
	}
	if scope != nil {
		read.Scope = *scope
	}
	read.ScopeDigest = string(read.Scope.Mode) + ":" + strings.Join(read.Scope.AuthResourcePaths, ",")
	return read
}

func fhirReferenceTestScopeKey(read queryapi.FHIRReadContext) fhirReferenceScopeKey {
	return fhirReferenceScopeKey{project: read.Project, generation: read.DatasetGeneration, digest: read.ScopeDigest}
}

func fhirReferenceTestVersion(t *testing.T, patient *fhir.Patient) string {
	t.Helper()
	if patient.Meta == nil || patient.Meta.VersionID == nil {
		return ""
	}
	return *patient.Meta.VersionID
}

func assertFHIRReferenceErrorCode(t *testing.T, err error, code dataframeerrors.ErrorCode) {
	t.Helper()
	userErr, ok := err.(dataframeerrors.UserError)
	if !ok || userErr.Code() != string(code) {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func fhirReferenceTestBindStrings(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				values = append(values, text)
			}
		}
		return values
	default:
		return nil
	}
}

func containsFHIRReferenceString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
