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

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframepublication "github.com/calypr/loom/internal/dataframe/publication"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
)

// F-B27 durable control: scoped dataset-mutation gates must be project-bound.
// Threat model matches F-B20/F-B21: calypr/scoped-principal deployments
// (basic/noAuth installs AllowAll and is the single-operator posture). The
// gate under test is the production authscope.ScopeResolver decision that
// CreateDatasetGeneration (operations.go:60) and ActivateDatasetGeneration
// (operations.go:89) call as their ONLY authorization; ingest-side activation
// (ingest/generation_orchestration.go Activate) has no principal at all.

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
		ListExistingAuthResourcePaths: func(_ context.Context, options catalog.AuthResourcePathOptions) ([]string, error) {
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
		Outputs: []dataframepublication.BundleOutputRecord{{Name: "list", Selector: dataframepublication.DataframeSelector{Recipe: "patients", Output: "list"}, State: dataframepublication.BundlePublished}},
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
	loader := func(_ context.Context, req GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
		requests = append(requests, req)
		return ingest.LoadSummary{}, nil
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
	}, func(context.Context, GenerationLoadRequest, ingest.EventSink) (ingest.LoadSummary, error) {
		t.Fatal("activate leg must not load generations")
		return ingest.LoadSummary{}, nil
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

// Contrast controls (green in both pre-fix and repaired postures): the gate
// exists and legitimate owners keep working.

func TestDatasetPublishControlsSameProjectOwnerAndEmptyGrant(t *testing.T) {
	ownerLoaderCalls := 0
	loader := func(_ context.Context, _ GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
		ownerLoaderCalls++
		return ingest.LoadSummary{}, nil
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

// Bootstrap guard for REPAIR-8: a project with no recorded catalog paths yet
// (first-ever generation) must stay publishable — the repair may only require
// membership among EXISTING project paths, never forbid empty catalogs.

func TestDatasetPublishNewProjectBootstrapRemainsPossible(t *testing.T) {
	loaderCalls := 0
	loader := func(_ context.Context, _ GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
		loaderCalls++
		return ingest.LoadSummary{}, nil
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
