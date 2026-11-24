package queryapi

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/catalog/fieldcatalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	publication "github.com/calypr/loom/internal/dataset"
)

const (
	queryapiBackendDelay = 90 * time.Second

	queryapiCursorDelay = 9 * time.Second
)

type controlledClock struct {
	mu   sync.Mutex
	time time.Time
	step time.Duration
}

func newControlledClock() *controlledClock {
	return &controlledClock{time: time.Date(2026, time.November, 6, 12, 0, 0, 0, time.UTC), step: time.Second}
}

func (c *controlledClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.time = c.time.Add(c.step)
	return c.time
}

func (c *controlledClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.time = c.time.Add(d)
}

type queryapiBackend struct {
	mu          sync.Mutex
	calls       int
	fieldCalls  int
	queries     []string
	bindVars    []map[string]any
	rows        []map[string]any
	err         error
	cursorDelay time.Duration
	entered     chan struct{}
	enteredOnce sync.Once
}

func (b *queryapiBackend) queryRows(_ context.Context, query string, _ int, bindVars map[string]any, visit func(map[string]any) error) error {
	b.mu.Lock()
	b.calls++
	b.queries = append(b.queries, query)
	b.bindVars = append(b.bindVars, bindVars)
	rows := b.rows
	err := b.err
	cursorDelay := b.cursorDelay
	entered := b.entered
	b.mu.Unlock()
	if err != nil {
		return err
	}
	if entered != nil {
		b.enteredOnce.Do(func() { close(entered) })
	}
	if cursorDelay > 0 {
		time.Sleep(cursorDelay)
	}
	for _, row := range rows {
		if visitErr := visit(row); visitErr != nil {
			return visitErr
		}
	}
	return nil
}

func (b *queryapiBackend) begin() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entered = make(chan struct{})
	b.enteredOnce = sync.Once{}
}

func (b *queryapiBackend) waitEntered(t *testing.T, done <-chan runOutcome) {
	t.Helper()
	b.mu.Lock()
	entered := b.entered
	b.mu.Unlock()
	if entered == nil {
		t.Fatal("backend has no entered signal, call begin() inside the bubble first")
	}
	select {
	case <-entered:
	case outcome := <-done:
		t.Fatalf("Run finished without holding the cursor open, err = %v", outcome.err)
	}
}

func (b *queryapiBackend) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func (b *queryapiBackend) catalogCallCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.fieldCalls
}

func queryapiGenderRows() []map[string]any {
	return []map[string]any{{"_key": "p1", "gender": "female"}}
}

func queryapiSelector(valuePath string) *recipe.FhirFieldSelectorInput {
	return &recipe.FhirFieldSelectorInput{ValuePath: valuePath}
}

func queryapiGenderInput() recipe.FhirDataframeInput {
	return recipe.FhirDataframeInput{
		Project:          "P1",
		RootResourceType: "Patient",
		RootFields:       []*recipe.FhirFieldSelectInput{{Name: "gender", Selector: queryapiSelector("gender")}},
	}
}

func queryapiService(t *testing.T, backend *queryapiBackend, clock *controlledClock) *Service {
	t.Helper()
	service := NewService(Config{
		ActiveManifestResolver: &builderActiveManifestResolver{manifest: builderReadyManifest(t, "P1", "generation-1")},
		DiscoverFields: func(_ context.Context, options fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
			backend.mu.Lock()
			backend.fieldCalls++
			backend.mu.Unlock()
			switch options.ResourceType {
			case "", "Patient":
				return []fieldcatalog.PopulatedField{{ResourceType: "Patient", Path: "gender", Kind: "scalar", DocCount: 1}}, nil
			default:
				return []fieldcatalog.PopulatedField{}, nil
			}
		},
		Dataframes: dataframeexecution.NewService(dataframeexecution.ServiceConfig{QueryRows: backend.queryRows}),
	})
	if clock != nil {
		service.now = clock.now
	}
	return service
}

type runOutcome struct {
	result *dataframeexecution.Result
	err    error
}

func runAsync(service *Service, input recipe.FhirDataframeInput) <-chan runOutcome {
	done := make(chan runOutcome, 1)
	go func() {
		result, err := service.Run(context.Background(), input, nil)
		done <- runOutcome{result: result, err: err}
	}()
	return done
}

func TestRunDoesNotChargeQueryExecutionToCompilation(t *testing.T) {
	baselineBackend := &queryapiBackend{rows: queryapiGenderRows()}
	baseline, err := queryapiService(t, baselineBackend, newControlledClock()).Run(context.Background(), queryapiGenderInput(), nil)
	if err != nil {
		t.Fatalf("Run() baseline error = %v", err)
	}

	heldBackend := &queryapiBackend{
		rows:        queryapiGenderRows(),
		cursorDelay: queryapiCursorDelay,
	}
	heldClock := newControlledClock()
	heldService := queryapiService(t, heldBackend, heldClock)

	var heldDiagnostics dataframeexecution.QueryDiagnostics
	synctest.Test(t, func(t *testing.T) {
		heldBackend.begin()
		done := runAsync(heldService, queryapiGenderInput())
		heldBackend.waitEntered(t, done)

		heldClock.advance(queryapiBackendDelay)

		outcome := <-done
		if outcome.err != nil {
			t.Fatalf("Run() held error = %v", outcome.err)
		}
		if baselineBackend.callCount() != 1 || heldBackend.callCount() != 1 {
			t.Fatalf("backend calls = baseline %d held %d, want one each", baselineBackend.callCount(), heldBackend.callCount())
		}

		heldDiagnostics = outcome.result.Diagnostics
		baselineDiagnostics := baseline.Diagnostics

		if baselineDiagnostics.Compilation <= 0 {
			t.Fatalf("baseline compilation = %v, want a non-zero compile interval", baselineDiagnostics.Compilation)
		}
		if heldDiagnostics.Compilation != baselineDiagnostics.Compilation {
			t.Fatalf("compilation changed with the cursor hold: held %v, baseline %v; query execution is being charged to compilation", heldDiagnostics.Compilation, baselineDiagnostics.Compilation)
		}
		if heldDiagnostics.Compilation >= queryapiBackendDelay {
			t.Fatalf("compilation %v still covers the %v backend hold", heldDiagnostics.Compilation, queryapiBackendDelay)
		}

		if heldDiagnostics.ArangoQuery != queryapiCursorDelay {
			t.Fatalf("held arango query = %v, want exactly the %v held cursor quantum", heldDiagnostics.ArangoQuery, queryapiCursorDelay)
		}
		if charged := heldDiagnostics.ArangoQuery + heldDiagnostics.RowMaterialization; charged != queryapiCursorDelay {
			t.Fatalf("cursor quantum %v charged %v across query %v and materialization %v", queryapiCursorDelay, charged, heldDiagnostics.ArangoQuery, heldDiagnostics.RowMaterialization)
		}
		if heldDiagnostics.ArangoQuery >= heldDiagnostics.Total {
			t.Fatalf("held arango query %v is not bounded by total %v", heldDiagnostics.ArangoQuery, heldDiagnostics.Total)
		}

		if want := baselineDiagnostics.Total + queryapiBackendDelay; heldDiagnostics.Total != want {
			t.Fatalf("held total = %v, want %v (baseline %v + cursor delay %v)", heldDiagnostics.Total, want, baselineDiagnostics.Total, queryapiBackendDelay)
		}
	})

	for _, diagnostics := range []dataframeexecution.QueryDiagnostics{baseline.Diagnostics, heldDiagnostics} {
		if diagnostics.InputResolution <= 0 || diagnostics.RequestPreparation <= 0 {
			t.Fatalf("unpopulated run phases: input resolution %v, request preparation %v", diagnostics.InputResolution, diagnostics.RequestPreparation)
		}
		if owned := diagnostics.InputResolution + diagnostics.RequestPreparation + diagnostics.Compilation; diagnostics.Total < owned {
			t.Fatalf("total %v is below the sum %v of the Run-owned phases", diagnostics.Total, owned)
		}
		if diagnostics.ArangoQuery < 0 || diagnostics.RowMaterialization < 0 || diagnostics.ResultAssembly < 0 {
			t.Fatalf("negative backend diagnostics: %+v", diagnostics)
		}
	}
	if heldDiagnostics.InputResolution != baseline.Diagnostics.InputResolution ||
		heldDiagnostics.RequestPreparation != baseline.Diagnostics.RequestPreparation {
		t.Fatalf("run-phase durations drifted: held input=%v preparation=%v, baseline input=%v preparation=%v",
			heldDiagnostics.InputResolution, heldDiagnostics.RequestPreparation,
			baseline.Diagnostics.InputResolution, baseline.Diagnostics.RequestPreparation)
	}
}

func TestRunChargesCursorQuantumOnceAcrossRows(t *testing.T) {
	backend := &queryapiBackend{
		rows: []map[string]any{
			{"_key": "p1", "gender": "female"},
			{"_key": "p2", "gender": "male"},
			{"_key": "p3", "gender": "female"},
		},
		cursorDelay: queryapiCursorDelay,
	}
	service := queryapiService(t, backend, nil)

	synctest.Test(t, func(t *testing.T) {
		backend.begin()
		done := runAsync(service, queryapiGenderInput())
		backend.waitEntered(t, done)
		outcome := <-done
		if outcome.err != nil {
			t.Fatalf("Run() error = %v", outcome.err)
		}
		if backend.callCount() != 1 {
			t.Fatalf("backend calls = %d, want 1", backend.callCount())
		}
		if outcome.result.RowCount != 3 {
			t.Fatalf("row count = %d, want the 3 delivered rows", outcome.result.RowCount)
		}
		diagnostics := outcome.result.Diagnostics
		if diagnostics.ArangoQuery != queryapiCursorDelay {
			t.Fatalf("arango query = %v, want the single %v cursor quantum, not one per row", diagnostics.ArangoQuery, queryapiCursorDelay)
		}
		if diagnostics.RowMaterialization >= queryapiCursorDelay {
			t.Fatalf("row materialization = %v, want the cursor quantum charged only to the query span", diagnostics.RowMaterialization)
		}
	})
}

func TestRunPreservesResultAndSeparateDiagnostics(t *testing.T) {
	backend := &queryapiBackend{rows: queryapiGenderRows()}
	result, err := queryapiService(t, backend, newControlledClock()).Run(context.Background(), queryapiGenderInput(), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if backend.callCount() != 1 {
		t.Fatalf("backend calls = %d, want 1", backend.callCount())
	}
	if result.RowCount != 1 || len(result.Rows) != 1 || result.Rows[0]["gender"] != "female" {
		t.Fatalf("unexpected dataframe payload: %#v", result)
	}
	if len(result.Columns) == 0 {
		t.Fatal("columns are empty, want the compiled projection")
	}
	if backend.queries[0] == "" {
		t.Fatal("backend received an empty query")
	}
	if got := backend.bindVars[0]["dataset_generation"]; got != "generation-1" {
		t.Fatalf("backend generation bind = %#v, want generation-1", got)
	}
	diagnostics := result.Diagnostics
	if diagnostics.ArangoQuery < 0 || diagnostics.RowMaterialization < 0 || diagnostics.ResultAssembly < 0 {
		t.Fatalf("negative backend diagnostics: %+v", diagnostics)
	}
	if diagnostics.Compilation >= diagnostics.Total {
		t.Fatalf("compilation %v is not bounded by total %v", diagnostics.Compilation, diagnostics.Total)
	}
}

func TestRunFailedCompileNeverInvokesBackend(t *testing.T) {
	for _, test := range []struct {
		name     string
		input    recipe.FhirDataframeInput
		wantCode dataframeerrors.ErrorCode
	}{
		{

			name: "compiler rejects reserved projection name",
			input: recipe.FhirDataframeInput{
				Project: "P1", RootResourceType: "Patient",
				RootFields: []*recipe.FhirFieldSelectInput{{Name: "_key", Selector: queryapiSelector("gender")}},
			},
			wantCode: dataframeerrors.CodeBackendUnavailable,
		},
		{

			name: "preparation rejects cross-grain row grain",
			input: func() recipe.FhirDataframeInput {
				grain := "specimen"
				input := queryapiGenderInput()
				input.RowGrain = &grain
				return input
			}(),
			wantCode: dataframeerrors.CodeInvalidRequest,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &queryapiBackend{rows: queryapiGenderRows()}
			result, err := queryapiService(t, backend, nil).Run(context.Background(), test.input, nil)
			if err == nil {
				t.Fatalf("Run() result = %#v, error = nil, want a failure", result)
			}
			if result != nil {
				t.Fatalf("Run() result = %#v, want nil on failure", result)
			}

			if backend.catalogCallCount() == 0 {
				t.Fatal("catalog discovery was never called, want a request that reached preparation")
			}
			if backend.callCount() != 0 {
				t.Fatalf("backend calls = %d, want 0 when the request fails before execution", backend.callCount())
			}
			userErr, ok := dataframeerrors.AsUserError(err)
			if !ok {
				t.Fatalf("Run() error = %v (%T), want a classified UserError", err, err)
			}
			if userErr.Code() != string(test.wantCode) {
				t.Fatalf("Run() error code = %q, want %q", userErr.Code(), test.wantCode)
			}
		})
	}
}

func TestRunBackendErrorKeepsClassification(t *testing.T) {
	t.Run("unknown cause becomes retryable backend unavailable", func(t *testing.T) {
		backend := &queryapiBackend{err: errors.New("cursor exploded")}
		_, err := queryapiService(t, backend, nil).Run(context.Background(), queryapiGenderInput(), nil)
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok {
			t.Fatalf("Run() error = %v (%T), want a classified UserError", err, err)
		}
		if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
			t.Fatalf("Run() error = %q retryable=%t, want retryable BACKEND_UNAVAILABLE", userErr.Code(), userErr.Retryable())
		}
		if backend.callCount() != 1 {
			t.Fatalf("backend calls = %d, want 1", backend.callCount())
		}
	})

	t.Run("semantic backend error keeps its code", func(t *testing.T) {
		backend := &queryapiBackend{err: dataframeerrors.NewError(dataframeerrors.CodeQueryResourceLimitExceeded, "", dataframeerrors.WithRetryable(false))}
		_, err := queryapiService(t, backend, nil).Run(context.Background(), queryapiGenderInput(), nil)
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok {
			t.Fatalf("Run() error = %v (%T), want a classified UserError", err, err)
		}
		if userErr.Code() != string(dataframeerrors.CodeQueryResourceLimitExceeded) {
			t.Fatalf("Run() error code = %q, want QUERY_RESOURCE_LIMIT_EXCEEDED", userErr.Code())
		}
	})
}

func TestQueryDiagnosticsFieldShape(t *testing.T) {
	structure := reflect.TypeOf(dataframeexecution.QueryDiagnostics{})
	got := make([]string, 0, structure.NumField())
	for index := 0; index < structure.NumField(); index++ {
		got = append(got, structure.Field(index).Name)
	}
	want := []string{
		"InputResolution", "RequestPreparation", "Compilation",
		"ArangoQuery", "RowMaterialization", "ResultAssembly", "Total", "Plan",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("QueryDiagnostics fields = %#v, want %#v", got, want)
	}
}

type builderActiveManifestResolver struct {
	manifest publication.Manifest
	projects []string
}

type noActiveManifestResolver struct{}

func (noActiveManifestResolver) ResolveActiveManifest(context.Context, string) (publication.Manifest, error) {
	return publication.Manifest{}, publication.ErrNoActiveGeneration
}

func (r *builderActiveManifestResolver) ResolveActiveManifest(_ context.Context, project string) (publication.Manifest, error) {
	r.projects = append(r.projects, project)
	return r.manifest, nil
}

func builderReadyManifest(t *testing.T, project, generationID string) publication.Manifest {
	t.Helper()
	schema, err := publication.NewSchemaSnapshot(
		"urn:loom:dataframebuilder-active-test",
		"",
		strings.Repeat("b", 64),
		[]string{"Patient", "Specimen"},
	)
	if err != nil {
		t.Fatalf("NewSchemaIdentitySnapshot() error = %v", err)
	}
	ref, err := publication.NewRef(project, generationID)
	if err != nil {
		t.Fatalf("NewDatasetRef() error = %v", err)
	}
	manifest, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	manifest.State = publication.StateReady
	return manifest
}

func TestActiveGenerationPropagatesThroughBuilderCatalogEntrypoints(t *testing.T) {
	const project = "project-a"
	const generation = "generation-a"
	active := &builderActiveManifestResolver{manifest: builderReadyManifest(t, project, generation)}
	var fieldCalls []fieldcatalog.PopulatedFieldOptions
	var referenceCalls []catalog.PopulatedReferenceOptions

	discoverFields := func(_ context.Context, options fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
		fieldCalls = append(fieldCalls, options)
		if options.PivotOnly {
			return []fieldcatalog.PopulatedField{}, nil
		}
		switch options.ResourceType {
		case "", "Patient":
			return []fieldcatalog.PopulatedField{{
				Project: project, ResourceType: "Patient", Path: "gender", Kind: "scalar", DocCount: 2,
				DistinctValues: []string{"female", "male"},
			}}, nil
		case "Specimen":
			return []fieldcatalog.PopulatedField{{Project: project, ResourceType: "Specimen", Path: "id", Kind: "scalar", DocCount: 1}}, nil
		default:
			return []fieldcatalog.PopulatedField{}, nil
		}
	}
	discoverReferences := func(_ context.Context, options catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
		referenceCalls = append(referenceCalls, options)
		return []catalog.PopulatedReference{{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen", EdgeCount: 1}}, nil
	}
	dataframes := dataframeexecution.NewService(dataframeexecution.ServiceConfig{
		QueryRows: func(_ context.Context, _ string, _ int, bindVars map[string]any, _ func(map[string]any) error) error {
			if got := bindVars["dataset_generation"]; got != generation {
				t.Fatalf("dataframe execution generation = %#v, want %q", got, generation)
			}
			return nil
		},
	})
	service := NewService(Config{
		ActiveManifestResolver: active,
		DiscoverFields:         discoverFields,
		DiscoverReferences:     discoverReferences,
		Dataframes:             dataframes,
	})

	if _, err := service.Run(context.Background(), recipe.FhirDataframeInput{
		Project: project, RootResourceType: "Patient",
	}, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, _, _, err := service.prepareRunInput(context.Background(), recipe.FhirDataframeInput{
		Project: project, RootResourceType: "Patient",
	}); err != nil {
		t.Fatalf("PrepareRunInput() error = %v", err)
	}
	if _, err := service.Introspect(context.Background(), IntrospectionRequest{
		Project: project, RootResourceType: "Patient",
	}); err != nil {
		t.Fatalf("Introspect() error = %v", err)
	}
	if len(active.projects) < 3 {
		t.Fatalf("active resolver calls = %#v, want one selection for each live dataframe entrypoint", active.projects)
	}
	if len(fieldCalls) == 0 || len(referenceCalls) == 0 {
		t.Fatalf("catalog calls fields=%d references=%d, want both", len(fieldCalls), len(referenceCalls))
	}
	for index, options := range fieldCalls {
		if options.Project != project || options.DatasetGeneration != generation {
			t.Fatalf("field catalog call %d = %+v, want %s/%s", index, options, project, generation)
		}
	}
	for index, options := range referenceCalls {
		if options.Project != project || options.DatasetGeneration != generation {
			t.Fatalf("reference catalog call %d = %+v, want %s/%s", index, options, project, generation)
		}
	}
}

func TestResolveActiveGenerationFallsBackToUnversionedCatalog(t *testing.T) {
	service := NewService(Config{ActiveManifestResolver: noActiveManifestResolver{}})

	generation, err := service.resolveActiveGeneration(context.Background(), "project-a")
	if err != nil {
		t.Fatalf("resolveActiveGeneration() error = %v", err)
	}
	if generation != "" {
		t.Fatalf("generation = %q, want empty unversioned namespace", generation)
	}
}

type dataframeBuilderRestrictedEmptyResourceAccess struct{}

func (dataframeBuilderRestrictedEmptyResourceAccess) GetAllowedResources(context.Context, string, string, string) ([]string, error) {
	return []string{"/programs/example/projects/allowed"}, nil
}

func dataframeBuilderRestrictedEmptyScopeResolver() *authscope.ScopeResolver {
	return authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: dataframeBuilderRestrictedEmptyResourceAccess{},
		ListExistingAuthResourcePaths: func(context.Context, authscope.AuthResourcePathOptions) ([]string, error) {
			return []string{"example-different"}, nil
		},
	})
}

func dataframeBuilderRestrictedEmptyContext() context.Context {
	return authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		AuthorizationHeader: "Bearer header.payload.signature",
	})
}

func TestRunPreservesRestrictedEmptyScopeIntoDataframeService(t *testing.T) {
	resolver := dataframeBuilderRestrictedEmptyScopeResolver()
	preparedCatalogCalls := 0

	dataframes := dataframeexecution.NewService(dataframeexecution.ServiceConfig{

		QueryRows: func(_ context.Context, _ string, _ int, bindVars map[string]any, _ func(map[string]any) error) error {
			if got, ok := bindVars["auth_resource_paths_unrestricted"].(bool); !ok || got {
				t.Fatalf("dataframe AQL unrestricted bind = %#v, want false", bindVars["auth_resource_paths_unrestricted"])
			}
			return nil
		},
	})
	service := NewService(Config{
		ScopeResolver:          resolver,
		ActiveManifestResolver: &builderActiveManifestResolver{manifest: builderReadyManifest(t, "P1", "generation-1")},
		DiscoverFields: func(_ context.Context, options fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
			preparedCatalogCalls++
			assertRestrictedEmptyFieldScope(t, options)
			return []fieldcatalog.PopulatedField{}, nil
		},
		Dataframes: dataframes,
	})

	result, err := service.Run(dataframeBuilderRestrictedEmptyContext(), recipe.FhirDataframeInput{
		Project:          "P1",
		RootResourceType: "Patient",
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.RowCount != 0 {
		t.Fatalf("result row count = %d, want no fake rows", result.RowCount)
	}
	if preparedCatalogCalls == 0 {
		t.Fatalf("prepared catalog calls = %d, want scoped preparation", preparedCatalogCalls)
	}
}

func TestIntrospectKeepsRestrictedEmptyCatalogMode(t *testing.T) {
	resolver := dataframeBuilderRestrictedEmptyScopeResolver()
	fieldCalls := 0
	referenceCalls := 0
	service := NewService(Config{
		ScopeResolver:          resolver,
		ActiveManifestResolver: &builderActiveManifestResolver{manifest: builderReadyManifest(t, "P1", "generation-1")},
		DiscoverFields: func(_ context.Context, options fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
			fieldCalls++
			assertRestrictedEmptyFieldScope(t, options)
			return []fieldcatalog.PopulatedField{}, nil
		},
		DiscoverReferences: func(_ context.Context, options catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			referenceCalls++
			assertRestrictedEmptyReferenceScope(t, options)
			return []catalog.PopulatedReference{}, nil
		},
	})
	ctx := dataframeBuilderRestrictedEmptyContext()

	if _, err := service.Introspect(ctx, IntrospectionRequest{Project: "P1", RootResourceType: "Patient"}); err != nil {
		t.Fatalf("Introspect() error = %v", err)
	}
	if fieldCalls == 0 || referenceCalls == 0 {
		t.Fatalf("catalog calls = fields %d references %d, want both", fieldCalls, referenceCalls)
	}
}

func assertRestrictedEmptyFieldScope(t *testing.T, options fieldcatalog.PopulatedFieldOptions) {
	t.Helper()
	if options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted {
		t.Fatalf("field catalog scope = %#v, want explicit false", options.AuthResourcePathsUnrestricted)
	}
	if len(options.AuthResourcePaths) != 0 {
		t.Fatalf("field catalog paths = %#v, want empty", options.AuthResourcePaths)
	}
}

func assertRestrictedEmptyReferenceScope(t *testing.T, options catalog.PopulatedReferenceOptions) {
	t.Helper()
	if options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted {
		t.Fatalf("reference catalog scope = %#v, want explicit false", options.AuthResourcePathsUnrestricted)
	}
	if len(options.AuthResourcePaths) != 0 {
		t.Fatalf("reference catalog paths = %#v, want empty", options.AuthResourcePaths)
	}
}

func TestProjectFormsShareOneShotCatalogAndIntrospectionIdentity(t *testing.T) {
	const (
		canonical  = "HTAN_INT/BForePC"
		legacy     = "HTAN_INT-BForePC"
		generation = "generation-1"
		scopePath  = "HTAN_INT-BForePC"
	)

	active := &builderActiveManifestResolver{manifest: builderReadyManifest(t, canonical, generation)}
	var fieldCalls []fieldcatalog.PopulatedFieldOptions
	var referenceCalls []catalog.PopulatedReferenceOptions
	var scopeCatalogCalls []authscope.AuthResourcePathOptions
	scopeResolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ListExistingAuthResourcePaths: func(_ context.Context, options authscope.AuthResourcePathOptions) ([]string, error) {
			scopeCatalogCalls = append(scopeCatalogCalls, options)
			return []string{scopePath}, nil
		},
	})

	checkScope := func(unrestricted *bool, paths []string) {
		t.Helper()
		if unrestricted == nil || *unrestricted {
			t.Fatalf("catalog scope bypass = %#v, want explicit restricted mode", unrestricted)
		}
		if !reflect.DeepEqual(paths, []string{scopePath}) {
			t.Fatalf("catalog auth paths = %#v, want %#v", paths, []string{scopePath})
		}
	}
	service := NewService(Config{
		ScopeResolver:          scopeResolver,
		ActiveManifestResolver: active,
		DiscoverFields: func(_ context.Context, options fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
			fieldCalls = append(fieldCalls, options)
			if options.Project != canonical || options.DatasetGeneration != generation {
				t.Fatalf("field catalog identity = %+v, want %s/%s", options, canonical, generation)
			}
			checkScope(options.AuthResourcePathsUnrestricted, options.AuthResourcePaths)
			return []fieldcatalog.PopulatedField{{Project: canonical, DatasetGeneration: generation, ResourceType: options.ResourceType, Path: "gender", Kind: "scalar"}}, nil
		},
		DiscoverReferences: func(_ context.Context, options catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			referenceCalls = append(referenceCalls, options)
			if options.Project != canonical || options.DatasetGeneration != generation {
				t.Fatalf("reference catalog identity = %+v, want %s/%s", options, canonical, generation)
			}
			checkScope(options.AuthResourcePathsUnrestricted, options.AuthResourcePaths)
			return []catalog.PopulatedReference{}, nil
		},
		Dataframes: dataframeexecution.NewService(dataframeexecution.ServiceConfig{
			QueryRows: func(_ context.Context, _ string, _ int, bindVars map[string]any, _ func(map[string]any) error) error {
				if unrestricted, ok := bindVars["auth_resource_paths_unrestricted"].(bool); !ok || unrestricted {
					t.Fatalf("dataframe scope bypass = %#v, want false", bindVars["auth_resource_paths_unrestricted"])
				}
				if !reflect.DeepEqual(bindVars["auth_resource_paths"], []string{scopePath}) {
					t.Fatalf("dataframe auth paths = %#v, want %#v", bindVars["auth_resource_paths"], []string{scopePath})
				}
				return nil
			},
		}),
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject:           "user",
		AuthResourcePaths: []string{scopePath},
	})

	for _, project := range []string{canonical, legacy} {
		t.Run(project, func(t *testing.T) {
			if _, err := service.Run(ctx, recipe.FhirDataframeInput{Project: project, RootResourceType: "Patient"}, nil); err != nil {
				t.Fatalf("Run(%q) error = %v", project, err)
			}
			response, err := service.Introspect(ctx, IntrospectionRequest{Project: project, RootResourceType: "Patient"})
			if err != nil {
				t.Fatalf("Introspect(%q) error = %v", project, err)
			}
			if response.Project != canonical || !reflect.DeepEqual(response.AuthResourcePaths, []string{scopePath}) {
				t.Fatalf("Introspect(%q) response = %+v, want canonical project and restricted path", project, response)
			}
		})
	}

	if len(scopeCatalogCalls) != 1 || scopeCatalogCalls[0].Project != legacy || scopeCatalogCalls[0].DatasetGeneration != generation {
		t.Fatalf("scope catalog calls = %#v, want one legacy storage lookup", scopeCatalogCalls)
	}
	if len(active.projects) != 4 {
		t.Fatalf("active resolver projects = %#v, want one canonical lookup per Run/Introspect", active.projects)
	}
	for _, project := range active.projects {
		if project != canonical {
			t.Fatalf("active resolver project = %q, want %q", project, canonical)
		}
	}
	if len(fieldCalls) == 0 || len(referenceCalls) != 2 {
		t.Fatalf("catalog call counts = fields:%d references:%d, want fields and one reference call per introspection", len(fieldCalls), len(referenceCalls))
	}
}

func errorcode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var userErr dataframeerrors.UserError
	if errors.As(err, &userErr) {
		return userErr.Code()
	}
	return "untyped:" + err.Error()
}

func TestClientSuppliedControlCharPathRefused(t *testing.T) {
	svc := NewService(Config{})
	scope, err := svc.resolveReadScopeForGeneration(context.Background(), nil, "proj-a", "gen-1", []string{"x\x00y"})
	if scope.Mode == authscope.ReadScopeRestricted && reflect.DeepEqual(scope.AuthResourcePaths, []string{"x\x00y"}) && err == nil {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): client-supplied path with NUL was copied raw into a restricted scope (AuthResourcePaths=%q); the \x00-join digest fold of distinct restricted grants remains constructible from request bytes", scope.AuthResourcePaths)
	}
	if err == nil || errorcode(t, err) != string(dataframeerrors.CodeInvalidRequest) {
		t.Fatalf("VERDICT=UNEXPECTED scope=%+v err=%v code=%q", scope, err, errorcode(t, err))
	}
	if scope.Mode != "" || len(scope.AuthResourcePaths) != 0 {
		t.Fatalf("VERDICT=UNEXPECTED refused request still produced scope %+v", scope)
	}
	t.Logf("VERDICT=REPAIRED all-control-char request refused with CodeInvalidRequest")
}

func TestClientSuppliedMixedPathsNormalized(t *testing.T) {
	svc := NewService(Config{})
	scope, err := svc.resolveReadScopeForGeneration(context.Background(), nil, "proj-a", "gen-1", []string{"/c-d/", "a-b", "junk\x01x", "a-b"})
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED err=%v", err)
	}
	if reflect.DeepEqual(scope.AuthResourcePaths, []string{"/c-d/", "a-b", "junk\x01x", "a-b"}) {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): requested paths were copied raw into the restricted scope (AuthResourcePaths=%q): duplicates, stray slashes, and the control-byte entry all reach the \x00-join digest", scope.AuthResourcePaths)
	}
	if scope.Mode != authscope.ReadScopeRestricted || !reflect.DeepEqual(scope.AuthResourcePaths, []string{"a-b", "c-d"}) {
		t.Fatalf("VERDICT=UNEXPECTED scope=%+v", scope)
	}
	t.Logf("VERDICT=REPAIRED mixed request normalized to %q (control-char entry dropped)", scope.AuthResourcePaths)
}

func TestPrincipalGarbageOnlyPathsFailClosed(t *testing.T) {
	svc := NewService(Config{})
	principal := &authscope.Principal{AuthResourcePaths: []string{"a\x00b"}}
	scope, err := svc.resolveReadScopeForGeneration(context.Background(), principal, "proj-a", "gen-1", nil)
	if scope.Mode == authscope.ReadScopeRestricted && reflect.DeepEqual(scope.AuthResourcePaths, []string{"a\x00b"}) && err == nil {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): IdP-supplied path with NUL survived into the restricted scope (AuthResourcePaths=%q)", scope.AuthResourcePaths)
	}
	if scope.Mode == authscope.ReadScopeUnrestricted {
		t.Fatalf("VERDICT=REGRESSION: principal whose only declared path is garbage was widened to unrestricted scope")
	}
	if err == nil || errorcode(t, err) != string(dataframeerrors.CodeForbidden) {
		t.Fatalf("VERDICT=UNEXPECTED scope=%+v err=%v code=%q", scope, err, errorcode(t, err))
	}
	t.Logf("VERDICT=REPAIRED garbage-only principal paths fail closed with CodeForbidden")
}

func TestMembershipComparedInNormalizedSpace(t *testing.T) {
	svc := NewService(Config{})
	principal := &authscope.Principal{AuthResourcePaths: []string{"a-b"}}
	scope, err := svc.resolveReadScopeForGeneration(context.Background(), principal, "proj-a", "gen-1", []string{"/a-b/"})
	if err != nil && strings.HasPrefix(errorcode(t, err), string(dataframeerrors.CodeForbidden)) {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived side finding): membership used raw string equality, so the authorized caller requesting %q was refused although it is entitled to %q", []string{"/a-b/"}, "a-b")
	}
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED err=%v code=%q", err, errorcode(t, err))
	}
	if scope.Mode != authscope.ReadScopeRestricted || !reflect.DeepEqual(scope.AuthResourcePaths, []string{"a-b"}) {
		t.Fatalf("VERDICT=UNEXPECTED scope=%+v", scope)
	}
	t.Logf("VERDICT=REPAIRED slash-variant request matched the authorized path")
}
