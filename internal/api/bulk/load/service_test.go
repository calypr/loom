package load

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"testing"
	"time"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframepublication "github.com/calypr/loom/internal/dataframe/publication"
	publication "github.com/calypr/loom/internal/dataset"
	ingestevent "github.com/calypr/loom/internal/ingest/event"
	"github.com/calypr/loom/internal/ingest/generation"
)

type activationGenerationRunner struct{}

func (activationGenerationRunner) RunGeneration(context.Context, GenerationLoadRequest, ingestevent.EventSink) (generation.LoadSummary, error) {
	return generation.LoadSummary{}, nil
}

type activationManifestStore struct {
	manifest      publication.Manifest
	readErr       error
	activateErr   error
	readCalls     int
	activateCalls int
	activated     publication.Manifest
}

func (s *activationManifestStore) ReadManifest(context.Context, publication.Ref) (publication.Manifest, error) {
	s.readCalls++
	if s.readErr != nil {
		return publication.Manifest{}, s.readErr
	}
	return s.manifest, nil
}

func (s *activationManifestStore) Activate(_ context.Context, manifest publication.Manifest) error {
	s.activateCalls++
	s.activated = manifest
	return s.activateErr
}

type activationReleaseStore struct {
	execution  dataframepublication.BundleExecution
	pointer    dataframepublication.BundlePointer
	getErr     error
	pointerErr error
}

func (s *activationReleaseStore) GetExecution(context.Context, string) (dataframepublication.BundleExecution, error) {
	if s.getErr != nil {
		return dataframepublication.BundleExecution{}, s.getErr
	}
	return s.execution, nil
}

func (s *activationReleaseStore) GetPointer(context.Context, string) (dataframepublication.BundlePointer, error) {
	if s.pointerErr != nil {
		return dataframepublication.BundlePointer{}, s.pointerErr
	}
	return s.pointer, nil
}

func TestRunGenerationPropagatesDeferActivationAndResult(t *testing.T) {
	runner := &activationCapturingRunner{}
	svc, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration})
	if err != nil {
		t.Fatal(err)
	}
	for _, deferred := range []bool{false, true} {
		result, err := svc.runGeneration(context.Background(), GenerationLoadRequest{Project: "project-a", Generation: "generation-a", StagedDir: t.TempDir(), DeferActivation: deferred})
		if err != nil {
			t.Fatal(err)
		}
		if runner.request.DeferActivation != deferred {
			t.Fatalf("runner request DeferActivation = %v, want %v", runner.request.DeferActivation, deferred)
		}
		if result.Activated == deferred {
			t.Fatalf("result Activated = %v for DeferActivation=%v", result.Activated, deferred)
		}
	}
}

type activationCapturingRunner struct {
	request GenerationLoadRequest
	calls   int
}

func (r *activationCapturingRunner) RunGeneration(_ context.Context, request GenerationLoadRequest, _ ingestevent.EventSink) (generation.LoadSummary, error) {
	r.calls++
	r.request = request
	return generation.LoadSummary{}, nil
}

func TestRunGenerationReusesReadyManifest(t *testing.T) {
	runner := &activationCapturingRunner{}
	manifest := activationManifest(t, "project-a", "generation-a")
	store := &activationManifestStore{manifest: manifest}
	svc, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration, GenerationActivator: store})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.runGeneration(context.Background(), GenerationLoadRequest{
		Project: "project-a", Generation: "generation-a", StagedDir: t.TempDir(), DeferActivation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reused || result.Activated {
		t.Fatalf("result = %#v, want reused and inactive", result)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.calls)
	}
}

func TestGenerationStatusReportsReusableManifest(t *testing.T) {
	manifest := activationManifest(t, "project-a", "generation-a")
	store := &activationManifestStore{manifest: manifest}
	svc, err := NewService(ServiceConfig{
		LoadGeneration:      activationGenerationRunner{}.RunGeneration,
		GenerationActivator: store,
	})
	if err != nil {
		t.Fatal(err)
	}

	status, err := svc.generationStatus(context.Background(), "project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	if status.Project != "project-a" || status.Generation != "generation-a" || status.State != publication.StateReady || !status.Reusable {
		t.Fatalf("status = %#v", status)
	}
	if store.readCalls != 1 {
		t.Fatalf("manifest reads = %d, want 1", store.readCalls)
	}
}

func TestGenerationStatusPreservesManifestNotFound(t *testing.T) {
	store := &activationManifestStore{readErr: fmt.Errorf("lookup: %w", publication.ErrManifestNotFound)}
	svc, err := NewService(ServiceConfig{
		LoadGeneration:      activationGenerationRunner{}.RunGeneration,
		GenerationActivator: store,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.generationStatus(context.Background(), "project-a", "generation-a")
	if !errors.Is(err, publication.ErrManifestNotFound) {
		t.Fatalf("generationStatus error = %v, want ErrManifestNotFound", err)
	}
}

func TestRunGenerationLoadsWhenManifestDoesNotExist(t *testing.T) {
	runner := &activationCapturingRunner{}
	store := &activationManifestStore{readErr: fmt.Errorf("lookup: %w", publication.ErrManifestNotFound)}
	svc, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration, GenerationActivator: store})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.runGeneration(context.Background(), GenerationLoadRequest{
		Project: "project-a", Generation: "generation-a", StagedDir: t.TempDir(), DeferActivation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reused || runner.calls != 1 {
		t.Fatalf("result reused = %v, runner calls = %d; want false, 1", result.Reused, runner.calls)
	}
}

func TestRunGenerationRejectsManifestLookupFailure(t *testing.T) {
	runner := &activationCapturingRunner{}
	store := &activationManifestStore{readErr: errors.New("database unavailable")}
	svc, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration, GenerationActivator: store})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.runGeneration(context.Background(), GenerationLoadRequest{
		Project: "project-a", Generation: "generation-a", StagedDir: t.TempDir(), DeferActivation: true,
	})
	if err == nil || runner.calls != 0 {
		t.Fatalf("error = %v, runner calls = %d; want lookup failure and no load", err, runner.calls)
	}
}

func TestActivateGenerationRejectsInvalidDataframeRelease(t *testing.T) {
	readyManifest := activationManifest(t, "project-a", "generation-a")
	base := dataframepublication.BundleExecution{
		ID: "release-1", BundleIdentity: dataframepublication.BundleIdentity{Project: "project-a", DatasetGeneration: "generation-a", Name: "all"},
		State: dataframepublication.BundlePublished, UpdatedAt: time.Now().UTC(),
		Outputs: []dataframepublication.BundleOutputRecord{
			{Name: "Patient", State: dataframepublication.BundlePublished},
			{Name: "Observation", State: dataframepublication.BundlePublished},
		},
	}
	tests := []struct {
		name   string
		mutate func(*dataframepublication.BundleExecution, *dataframepublication.BundlePointer)
	}{
		{name: "mismatched project", mutate: func(execution *dataframepublication.BundleExecution, _ *dataframepublication.BundlePointer) {
			execution.Project = "project-b"
		}},
		{name: "mismatched generation", mutate: func(execution *dataframepublication.BundleExecution, _ *dataframepublication.BundlePointer) {
			execution.DatasetGeneration = "generation-b"
		}},
		{name: "bundle not ready", mutate: func(execution *dataframepublication.BundleExecution, _ *dataframepublication.BundlePointer) {
			execution.State = "LOADING"
		}},
		{name: "output not ready", mutate: func(execution *dataframepublication.BundleExecution, _ *dataframepublication.BundlePointer) {
			execution.Outputs[1].State = "LOADING"
		}},
		{name: "pointer mismatch", mutate: func(_ *dataframepublication.BundleExecution, pointer *dataframepublication.BundlePointer) {
			pointer.ExecutionID = "another-release"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			execution := base
			execution.Outputs = append([]dataframepublication.BundleOutputRecord(nil), base.Outputs...)
			pointer := dataframepublication.BundlePointer{Name: execution.PointerName(), ExecutionID: execution.ID}
			test.mutate(&execution, &pointer)
			activator := &activationManifestStore{manifest: readyManifest}
			releases := &activationReleaseStore{execution: execution, pointer: pointer}
			runner := activationGenerationRunner{}
			svc, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration, GenerationActivator: activator, DataframeReleases: releases})
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.activateGeneration(context.Background(), "project-a", "generation-a", execution.ID); err == nil {
				t.Fatal("ActivateGeneration() unexpectedly succeeded")
			}
			if activator.readCalls != 0 || activator.activateCalls != 0 {
				t.Fatalf("manifest calls = read %d activate %d, want no activation", activator.readCalls, activator.activateCalls)
			}
		})
	}
}

func TestActivateGenerationActivatesPublishedAllOutputBundle(t *testing.T) {
	manifest := activationManifest(t, "project-a", "generation-a")
	execution := dataframepublication.BundleExecution{
		ID: "release-1", BundleIdentity: dataframepublication.BundleIdentity{Project: "project-a", DatasetGeneration: "generation-a", Name: "all"},
		State: dataframepublication.BundlePublished, UpdatedAt: time.Now().UTC(),
		Outputs: []dataframepublication.BundleOutputRecord{
			{Name: "Patient", State: dataframepublication.BundlePublished},
			{Name: "Observation", State: dataframepublication.BundlePublished},
		},
	}
	activator := &activationManifestStore{manifest: manifest}
	calledSuccess := false
	svc, err := NewService(ServiceConfig{
		LoadGeneration:      activationGenerationRunner{}.RunGeneration,
		GenerationActivator: activator,
		DataframeReleases:   &activationReleaseStore{execution: execution, pointer: dataframepublication.BundlePointer{Name: execution.PointerName(), ExecutionID: execution.ID}},
		OnSuccess:           func(string) { calledSuccess = true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.activateGeneration(context.Background(), "project-a", "generation-a", execution.ID); err != nil {
		t.Fatal(err)
	}
	if activator.readCalls != 1 || activator.activateCalls != 1 || !calledSuccess {
		t.Fatalf("activation calls = read %d activate %d success %v", activator.readCalls, activator.activateCalls, calledSuccess)
	}
	if activator.activated.Dataset != manifest.Dataset || !activator.activated.IsStaged() {
		t.Fatalf("activated manifest = %#v", activator.activated)
	}
}

func activationManifest(t *testing.T, project, generation string) publication.Manifest {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := publication.NewSchemaSnapshot("urn:test", "R5", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	manifest.State = publication.StateReady
	return manifest
}

func TestActivateGenerationLoadingManifestMapsTo409(t *testing.T) {
	ref, err := publication.NewRef("project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := publication.NewSchemaSnapshot("urn:test", "R5", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	loading, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	if loading.IsStaged() {
		t.Fatal("fixture must be non-staged")
	}
	execution := dataframepublication.BundleExecution{
		ID: "release-1", BundleIdentity: dataframepublication.BundleIdentity{Project: "project-a", DatasetGeneration: "generation-a", Name: "all"},
		State: dataframepublication.BundlePublished, UpdatedAt: time.Now().UTC(),
		Outputs: []dataframepublication.BundleOutputRecord{{Name: "Patient", State: dataframepublication.BundlePublished}},
	}
	activator := &activationManifestStore{manifest: loading}
	svc, err := NewService(ServiceConfig{
		LoadGeneration:      activationGenerationRunner{}.RunGeneration,
		GenerationActivator: activator,
		DataframeReleases:   &activationReleaseStore{execution: execution, pointer: dataframepublication.BundlePointer{Name: execution.PointerName(), ExecutionID: execution.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = svc.activateGeneration(context.Background(), "project-a", "generation-a", execution.ID)
	if err == nil {
		t.Fatal("activating a loading generation must fail")
	}
	if !errors.Is(err, publication.ErrGenerationNotReady) {
		t.Fatalf("not-ready activation lost its domain sentinel (route serves opaque 500): %v", err)
	}
	if activator.activateCalls != 0 {
		t.Fatalf("Activate called for non-staged manifest: %d", activator.activateCalls)
	}
	userErr, ok := dataframeerrors.AsUserError(normalizeError(err))
	if !ok || userErr.Code() != "GENERATION_NOT_READY" || userErr.Retryable() {
		t.Fatalf("boundary classification failed: ok=%v err=%v", ok, err)
	}
	mapped := httpapi.MapDataframeError(normalizeError(err), "req")
	if mapped.Status != 409 || mapped.Body.Error.Code != "GENERATION_NOT_READY" || mapped.Body.Error.Retryable {
		t.Fatalf("served response = %d %s retryable=%v, want 409 GENERATION_NOT_READY non-retryable", mapped.Status, mapped.Body.Error.Code, mapped.Body.Error.Retryable)
	}
}

type stubResourceAccess struct{ writePaths []string }

func (s stubResourceAccess) GetAllowedResources(_ context.Context, _, _, _ string) ([]string, error) {
	return s.writePaths, nil
}

type recordingActivator struct {
	existing  map[string]publication.Manifest
	activated []publication.Ref
}

func (a *recordingActivator) ReadManifest(_ context.Context, ref publication.Ref) (publication.Manifest, error) {
	if m, ok := a.existing[ref.Project+"\x00"+ref.Generation]; ok {
		return m, nil
	}
	return publication.Manifest{}, publication.ErrManifestNotFound
}

func (a *recordingActivator) Activate(_ context.Context, candidate publication.Manifest) error {
	a.activated = append(a.activated, candidate.Dataset)
	return nil
}

type stubReleases struct {
	execution dataframepublication.BundleExecution
}

func (s stubReleases) GetExecution(context.Context, string) (dataframepublication.BundleExecution, error) {
	return s.execution, nil
}

func (s stubReleases) GetPointer(_ context.Context, name string) (dataframepublication.BundlePointer, error) {
	if name != s.execution.PointerName() {
		return dataframepublication.BundlePointer{}, errors.New("pointer not found")
	}
	return dataframepublication.BundlePointer{Name: name, ExecutionID: s.execution.ID}, nil
}

func validSchema() (publication.SchemaSnapshot, error) {
	return publication.NewSchemaSnapshot("schema-fb27", "4.1.0", fmt.Sprintf("%064x", 0xABCD), []string{"Patient"})
}

func stagedManifest(t *testing.T, project, generation string) publication.Manifest {
	t.Helper()
	schema, err := validSchema()
	if err != nil {
		t.Fatalf("schema snapshot: %v", err)
	}
	manifest, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	candidate := publication.Manifest{Dataset: manifest, State: publication.StateStaged, SchemaIdentity: schema}
	if err := candidate.Validate(); err != nil {
		t.Fatalf("manifest invalid: %v", err)
	}
	return candidate
}

func newTestHandler(t *testing.T, writePaths []string, catalogPaths map[string][]string, loader GenerationLoader, activator *recordingActivator, releases DataframeReleaseStore) *Handler {
	t.Helper()
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: stubResourceAccess{writePaths: writePaths},
		ListExistingAuthResourcePaths: func(_ context.Context, options authscope.AuthResourcePathOptions) ([]string, error) {
			return append([]string(nil), catalogPaths[options.Project]...), nil
		},
	})
	service, err := NewService(ServiceConfig{
		LoadGeneration:      loader,
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		GenerationActivator: activator,
		DataframeReleases:   releases,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	handler, err := NewHandler(Config{Service: service, Authorizer: authscope.ScopeAuthorizer{Resolver: resolver}})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	return handler
}

func victimBundle(project, generation, executionID string) dataframepublication.BundleExecution {
	digest := fmt.Sprintf("%064x", 0x1234)
	execution := dataframepublication.BundleExecution{
		ID: executionID,
		BundleIdentity: dataframepublication.BundleIdentity{
			Name: "patients", Project: project, DatasetGeneration: generation,
			RecipeDigest: digest, SchemaDigest: digest,
		},
		State:   dataframepublication.BundlePublished,
		Outputs: []dataframepublication.BundleOutputRecord{{Name: "list", Selector: publication.DataframeSelector{Recipe: "patients", Output: "list"}, State: dataframepublication.BundlePublished}},
	}
	return execution
}

func attackerPrincipal() *authscope.Principal {
	return &authscope.Principal{Subject: "user-a", Projects: []string{"projA"}, AuthResourcePaths: []string{"/projA/data"}}
}

func multipartGenerationBody(t *testing.T) *multipart.Reader {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.WriteField("auth_resource_path", "/projA/data"); err != nil {
		t.Fatalf("form field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "graph.ndjson")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write([]byte(`{"resourceType":"Patient","id":"p1"}`)); err != nil {
		t.Fatalf("form payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return multipart.NewReader(&buffer, writer.Boundary())
}

func TestCrossProjectDatasetPublishGateIsProjectBound(t *testing.T) {
	var requests []GenerationLoadRequest
	loader := func(_ context.Context, req GenerationLoadRequest, _ ingestevent.EventSink) (generation.LoadSummary, error) {
		requests = append(requests, req)
		return generation.LoadSummary{}, nil
	}
	activator := &recordingActivator{existing: map[string]publication.Manifest{}}
	handler := newTestHandler(t, []string{"/projA/data"}, map[string][]string{
		"projA": {"/projA/data"},
		"projB": {"/projB/data"},
	}, loader, activator, stubReleases{})

	err := mustError(handler.CreateDatasetGeneration(context.Background(), "projB", "g-evil", multipartGenerationBody(t), attackerPrincipal()))
	if err == nil && len(requests) == 1 && requests[0].Project == "projB" && requests[0].AuthResourcePath == authscope.NormalizeAuthResourcePath("/projA/data") {
		fmt.Println("F-B27 CONFIRMED publish: projA-scoped principal staged+activated generation in projB; gate decision consulted only path membership (scope_resolver AuthorizeWrite; project used only in the error string)")
		t.Fatalf("F-B27 CONFIRMED (expected red until REPAIR-8): cross-project dataset publish accepted: %+v", requests[0])
	}
	if !errors.Is(err, authscope.ErrForbidden) {
		t.Fatalf("cross-project publish must be refused as forbidden, got %v", err)
	}
	if len(requests) != 0 {
		t.Fatalf("refused publish still reached ingest: %+v", requests)
	}
}

func TestCrossProjectDatasetActivateGateIsProjectBound(t *testing.T) {
	activator := &recordingActivator{existing: map[string]publication.Manifest{}}
	activator.existing["projB\x00g1"] = stagedManifest(t, "projB", "g1")
	handler := newTestHandler(t, []string{"/projA/data"}, map[string][]string{
		"projA": {"/projA/data"},
		"projB": {"/projB/data"},
	}, func(context.Context, GenerationLoadRequest, ingestevent.EventSink) (generation.LoadSummary, error) {
		t.Fatal("activate leg must not load generations")
		return generation.LoadSummary{}, nil
	}, activator, stubReleases{execution: victimBundle("projB", "g1", "exec-victim")})

	_, activationErr := handler.ActivateDatasetGeneration(context.Background(), "projB", "g1", "exec-victim", "/projA/data", attackerPrincipal())
	err := activationErr
	if err == nil && len(activator.activated) == 1 && activator.activated[0] == (publication.Ref{Project: "projB", Generation: "g1"}) {
		fmt.Println("F-B27 CONFIRMED activate: projA-scoped principal flipped the ACTIVE pointer of an existing staged projB generation; service-level checks (:191/:199/:206) only cross-validate caller-supplied inputs against each other")
		t.Fatalf("F-B27 CONFIRMED (expected red until REPAIR-8): cross-project activation flipped victim pointer: %+v", activator.activated)
	}
	if !errors.Is(err, authscope.ErrForbidden) {
		t.Fatalf("cross-project activation must be refused as forbidden, got %v", err)
	}
	if len(activator.activated) != 0 {
		t.Fatalf("refused activation still flipped pointer: %+v", activator.activated)
	}
}

func TestDatasetPublishControlsSameProjectOwnerAndEmptyGrant(t *testing.T) {
	ownerLoaderCalls := 0
	loader := func(_ context.Context, _ GenerationLoadRequest, _ ingestevent.EventSink) (generation.LoadSummary, error) {
		ownerLoaderCalls++
		return generation.LoadSummary{}, nil
	}
	handler := newTestHandler(t, []string{"/projA/data"}, map[string][]string{
		"projA": {"/projA/data"},
	}, loader, &recordingActivator{existing: map[string]publication.Manifest{}}, stubReleases{})
	if err := mustAllow(handler.CreateDatasetGeneration(context.Background(), "projA", "g-ok", multipartGenerationBodyOwner(t, "/projA/data"), attackerPrincipal())); err != nil {
		t.Fatalf("same-project owner publish refused: %v", err)
	}
	if ownerLoaderCalls != 1 {
		t.Fatalf("owner publish did not reach ingest: calls=%d", ownerLoaderCalls)
	}

	principalWithoutGrant := &authscope.Principal{Subject: "user-x", Projects: []string{"projX"}, AuthorizationHeader: "Bearer none"}
	err := mustError(handler.CreateDatasetGeneration(context.Background(), "projB", "g-nope", multipartGenerationBodyOwner(t, "/projB/data"), principalWithoutGrant))
	if !errors.Is(err, authscope.ErrForbidden) {
		t.Fatalf("empty-grant publish must be refused as forbidden, got %v", err)
	}
}

func TestDatasetPublishNewProjectBootstrapRemainsPossible(t *testing.T) {
	loaderCalls := 0
	loader := func(_ context.Context, _ GenerationLoadRequest, _ ingestevent.EventSink) (generation.LoadSummary, error) {
		loaderCalls++
		return generation.LoadSummary{}, nil
	}
	handler := newTestHandler(t, []string{"/projA/data"}, map[string][]string{"projA": {"/projA/data"}},
		loader, &recordingActivator{existing: map[string]publication.Manifest{}}, stubReleases{})
	if err := mustAllow(handler.CreateDatasetGeneration(context.Background(), "projNew", "g-1", multipartGenerationBodyOwner(t, "/projA/data"), attackerPrincipal())); err != nil {
		t.Fatalf("first-generation publish into un-cataloged project refused (breaks bootstrap): %v", err)
	}
	if loaderCalls != 1 {
		t.Fatalf("bootstrap publish did not reach ingest: calls=%d", loaderCalls)
	}
}

func multipartGenerationBodyOwner(t *testing.T, path string) *multipart.Reader {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.WriteField("auth_resource_path", path); err != nil {
		t.Fatalf("form field: %v", err)
	}
	part, err := writer.CreateFormFile("file", "graph.ndjson")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write([]byte(`{"resourceType":"Patient","id":"p2"}`)); err != nil {
		t.Fatalf("form payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return multipart.NewReader(&buffer, writer.Boundary())
}

func mustError(result *GenerationLoadResult, err error) error {
	_ = result
	if err == nil {
		return nil
	}
	return err
}

func mustAllow(result *GenerationLoadResult, err error) error {
	_ = result
	return err
}

func TestFb22ScopedIngressDeniesControlByteAuthResourcePath(t *testing.T) {
	var requests []GenerationLoadRequest
	loader := func(_ context.Context, req GenerationLoadRequest, _ ingestevent.EventSink) (generation.LoadSummary, error) {
		requests = append(requests, req)
		return generation.LoadSummary{}, nil
	}
	handler := newTestHandler(t,
		[]string{"p1"},
		map[string][]string{"projA": {"p1"}},
		loader,
		&recordingActivator{existing: map[string]publication.Manifest{}},
		stubReleases{},
	)

	_, err := handler.CreateDatasetGeneration(context.Background(), "projA", "g1", multipartGenerationBodyOwner(t, "a\x00b"), fb22Principal())
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInvalidRequest) {
		t.Fatalf("VERDICT=UNEXPECTED: scoped control-byte auth path must be refused by the ingress guard with INVALID_REQUEST, got %v", err)
	}
	if len(requests) != 0 {
		t.Fatalf("VERDICT=REGRESSION: refused control-byte publish still reached ingest: %+v", requests)
	}
	t.Logf("VERDICT=REPAIRED scoped ingress refused the control-byte auth path with INVALID_REQUEST before ingest (no silent blank)")
}

func TestFb22ScopedIngressDeniesEmptyAuthResourcePath(t *testing.T) {
	var requests []GenerationLoadRequest
	loader := func(_ context.Context, req GenerationLoadRequest, _ ingestevent.EventSink) (generation.LoadSummary, error) {
		requests = append(requests, req)
		return generation.LoadSummary{}, nil
	}
	handler := newTestHandler(t,
		[]string{"p1"},
		map[string][]string{"projA": {"p1"}},
		loader,
		&recordingActivator{existing: map[string]publication.Manifest{}},
		stubReleases{},
	)

	_, err := handler.CreateDatasetGeneration(context.Background(), "projA", "g1", multipartGenerationBodyOwner(t, ""), fb22Principal())
	if !errors.Is(err, authscope.ErrForbidden) {
		t.Fatalf("VERDICT=UNEXPECTED: scoped empty auth path must stay a forbidden refusal from the authorizer, got %v", err)
	}
	if len(requests) != 0 {
		t.Fatalf("VERDICT=REGRESSION: refused empty-path publish still reached ingest: %+v", requests)
	}
	t.Logf("VERDICT=REPAIRED scoped ingress required a non-empty auth path (guard left genuine-empty to the authorizer)")
}

func TestFb22ScopedIngressAllowsLegitAuthResourcePath(t *testing.T) {
	var requests []GenerationLoadRequest
	loader := func(_ context.Context, req GenerationLoadRequest, _ ingestevent.EventSink) (generation.LoadSummary, error) {
		requests = append(requests, req)
		return generation.LoadSummary{}, nil
	}
	handler := newTestHandler(t,
		[]string{"p1"},
		map[string][]string{"projA": {"p1"}},
		loader,
		&recordingActivator{existing: map[string]publication.Manifest{}},
		stubReleases{},
	)

	if _, err := handler.CreateDatasetGeneration(context.Background(), "projA", "g1", multipartGenerationBodyOwner(t, "p1"), fb22Principal()); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED: legitimate granted path must publish, got %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("VERDICT=UNEXPECTED: legit publish reached ingest %d times, want 1", len(requests))
	}
	if requests[0].AuthResourcePath != authscope.NormalizeAuthResourcePath("p1") {
		t.Fatalf("VERDICT=UNEXPECTED: stored auth path = %q, want %q", requests[0].AuthResourcePath, authscope.NormalizeAuthResourcePath("p1"))
	}
	t.Logf("VERDICT=REPAIRED scoped ingress published the granted path (guard and gate are not vacuous)")
}

func TestFb22AllowAllIngressRejectsControlByteAuthResourcePath(t *testing.T) {
	captured := []GenerationLoadRequest{}
	handler := fb22AllowAllHandler(t, func(_ context.Context, req GenerationLoadRequest, _ ingestevent.EventSink) (generation.LoadSummary, error) {
		captured = append(captured, req)
		return generation.LoadSummary{}, nil
	})

	_, err := handler.CreateDatasetGeneration(context.Background(), "P1", "g1", multipartGenerationBodyOwner(t, "a\x00b"), fb22Principal())

	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInvalidRequest) {
		t.Fatalf("VERDICT=REGRESSION: AllowAll control-byte auth path must be refused with INVALID_REQUEST before authorizer/ingest, got %v", err)
	}
	if len(captured) != 0 {
		t.Fatalf("VERDICT=REGRESSION: refused control-byte publish reached ingest (silent blank shipped): %+v", captured)
	}

	if _, err := handler.CreateDatasetGeneration(context.Background(), "P1", "g2", multipartGenerationBodyOwner(t, ""), fb22Principal()); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED: allow-all control publish (genuine empty) refused, gate is vacuous: %v", err)
	}
	if len(captured) != 1 || captured[0].AuthResourcePath != "" {
		t.Fatalf("VERDICT=UNEXPECTED: non-vacuity control did not reach ingest exactly once: %+v", captured)
	}
	t.Logf("VERDICT=REPAIRED AllowAll refused the control-byte auth path with INVALID_REQUEST (ingest untouched) while the genuine-empty control still ingested")
}

func TestFb22AllowAllIngressEmptyAuthResourcePathPosture(t *testing.T) {
	captured := []GenerationLoadRequest{}
	handler := fb22AllowAllHandler(t, func(_ context.Context, req GenerationLoadRequest, _ ingestevent.EventSink) (generation.LoadSummary, error) {
		captured = append(captured, req)
		return generation.LoadSummary{}, nil
	})

	_, err := handler.CreateDatasetGeneration(context.Background(), "P1", "g1", multipartGenerationBodyOwner(t, ""), fb22Principal())
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED: AllowAll genuine empty path must be accepted, got %v", err)
	}
	if len(captured) != 1 || captured[0].AuthResourcePath != "" {
		t.Fatalf("VERDICT=UNEXPECTED: AllowAll genuine empty posture shape changed: requests=%+v", captured)
	}
	wsErr := func() error {
		_, err := handler.CreateDatasetGeneration(context.Background(), "P1", "g2", multipartGenerationBodyOwner(t, "  \t "), fb22Principal())
		return err
	}()
	if wsErr != nil {
		t.Fatalf("VERDICT=UNEXPECTED: whitespace-only auth path must trim to the accepted genuine empty, got %v", wsErr)
	}
	if len(captured) != 2 || captured[1].AuthResourcePath != "" {
		t.Fatalf("VERDICT=UNEXPECTED: whitespace-only post-trim shape changed: requests=%+v", captured)
	}
	t.Logf("VERDICT=PINED AllowAll accepts and stores the genuine empty auth path, including its whitespace post-trim shape (single-operator posture)")
}

func fb22Principal() *authscope.Principal {
	return &authscope.Principal{Subject: "fb22-user", Projects: []string{"projA"}, AuthResourcePaths: []string{"p1"}}
}

func fb22AllowAllHandler(t *testing.T, loader GenerationLoader) *Handler {
	t.Helper()
	service, err := NewService(ServiceConfig{
		LoadGeneration: loader,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	handler, err := NewHandler(Config{Service: service, Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	return handler
}
