package load

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
)

func TestFb22ScopedIngressDeniesControlByteAuthResourcePath(t *testing.T) {
	var requests []GenerationLoadRequest
	loader := func(_ context.Context, req GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
		requests = append(requests, req)
		return ingest.LoadSummary{}, nil
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
	loader := func(_ context.Context, req GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
		requests = append(requests, req)
		return ingest.LoadSummary{}, nil
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
	loader := func(_ context.Context, req GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
		requests = append(requests, req)
		return ingest.LoadSummary{}, nil
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
	handler := fb22AllowAllHandler(t, func(_ context.Context, req GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
		captured = append(captured, req)
		return ingest.LoadSummary{}, nil
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
	handler := fb22AllowAllHandler(t, func(_ context.Context, req GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
		captured = append(captured, req)
		return ingest.LoadSummary{}, nil
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
