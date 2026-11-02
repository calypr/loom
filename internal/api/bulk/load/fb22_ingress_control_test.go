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

// F-7R3 durable control (REPAIR-18 U2 / REPAIR-18b): CreateDatasetGeneration
// ingress (operations.go) must refuse an auth_resource_path that
// authscope.NormalizeAuthResourcePath would silently blank (control bytes,
// e.g. "a\x00b") in EVERY authorizer posture — no silent fallback.
//
// The guard landed at the ingress (REPAIR-18b): after the trim and BEFORE
// AuthorizeWrite,
//
//	if authResourcePath != "" && authscope.NormalizeAuthResourcePath(authResourcePath) == "" {
//		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
//	}
//
// A clean non-empty path normalizes to itself (guard is a no-op); a genuine
// empty stays "" and remains accepted as the legitimate single-operator
// posture; only a non-empty value that collapses to "" (control bytes and
// other all-junk encodings) is refused with CodeInvalidRequest, reaching the
// AllowAll posture (shipped noAuth/single-operator), which never calls the
// ScopeResolver, as well as the scoped posture, where the guard now fires
// before authorizer consultation.
//
// PRE-FIX behavior, pinned here as history: under AllowAll, AuthorizeWrite
// returned nil, operations.go normalized the control-byte value to "" and
// ingested it with no error (CONFIRMED-PENDING, VERDICT=CONFIRMED-PENDING in
// the pre-guard runs of this file). Under scoped authorization the control
// byte was refused as forbidden by the resolver instead, i.e. the two
// postures diverged on the same input. The guards below pin the unified
// rejection; the genuine-empty controls pin that the guard did NOT swallow
// the single-operator posture while "fixing" it.

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

	// Empty-string direct case: the ingress guard must leave the genuine
	// empty encoding untouched (pre-authorization it is the legitimate
	// single-operator posture), so the scoped refusal here still comes from
	// the resolver's own membership requirement, as forbidden — NOT from the
	// control-byte guard. If this flips to INVALID_REQUEST the guard started
	// swallowing genuine-empty instead of only blankable non-empty values.
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

	// REPAIR-18b contract: the shipped single-operator/noAuth posture must
	// refuse the control-byte path with CodeInvalidRequest at the ingress,
	// not blank it to "" and ingest (pre-guard behavior pinned in the file
	// header as CONFIRMED-PENDING).
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInvalidRequest) {
		t.Fatalf("VERDICT=REGRESSION: AllowAll control-byte auth path must be refused with INVALID_REQUEST before authorizer/ingest, got %v", err)
	}
	if len(captured) != 0 {
		t.Fatalf("VERDICT=REGRESSION: refused control-byte publish reached ingest (silent blank shipped): %+v", captured)
	}

	// Non-vacuity, same mechanism as TestFb22ScopedIngressAllowsLegitAuthResourcePath:
	// the same handler and recorded loader MUST still ingest a control-free
	// publish exactly once, so the zero-ingest assertion above is a real
	// refusal of this input, not a loader that never runs.
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

	// The genuine-empty encoding is the legitimate single-operator posture:
	// with no authorizer the empty path is accepted and stored. This must
	// survive the ingress guard — the trim keeps whitespace-only form values
	// in this accepted shape, and the guard's authResourcePath != "" clause
	// exists precisely so genuine-empty is never rejected as a blanked
	// control byte.
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

// RESOLVED(REPAIR-18 U2 / F-7R3 -> REPAIR-18b): The contract "no silent
// fallback at boundaries" required CreateDatasetGeneration to REJECT an
// auth_resource_path carrying a control byte in EVERY authorizer posture,
// not to let authscope.NormalizeAuthResourcePath blank it at the post-
// authorization store step. Under the AllowAll posture (shipped
// noAuth/single-operator) the ScopeResolver is never consulted, so the
// reject landed at the ingress, before the value reaches the authorizer or
// is normalized and staged — NOT inside internal/authscope/scope_resolver.go
// (REPAIR-15's file), which only gates the scoped posture. The landed guard
// (operations.go, after the trim and before AuthorizeWrite):
//
//	if authResourcePath != "" && authscope.NormalizeAuthResourcePath(authResourcePath) == "" {
//		return nil, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
//	}
//
// A clean non-empty path normalizes to itself (guard is a no-op); a genuine
// empty stays "", matching the accepted single-operator posture (pinned by
// the two AllowAll and scoped genuine-empty controls above, including the
// whitespace post-trim shape); only a non-empty value collapsing to "" is
// refused with CodeInvalidRequest — reaching every posture, AllowAll
// included. The pre-guard divergence (scoped=forbidden vs
// AllowAll=blank-and-ingest, VERDICT=CONFIRMED-PENDING) is preserved in the
// file header and in /tmp/opencode/fb18b-red.log as the RED evidence this
// guard was validated against.
