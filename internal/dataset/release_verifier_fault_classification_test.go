package dataset

import (
	"context"
	"errors"
	"testing"
	"time"
)

type faultClassVerifier struct {
	err      error
	missing  bool
	mismatch bool
}

func (v faultClassVerifier) VerifyPublication(_ context.Context, _, generation string, selector DataframeSelector) (PublicationVerification, error) {
	if v.missing {
		return PublicationVerification{}, ErrPublicationRecordMissing
	}
	if v.err != nil {
		return PublicationVerification{}, v.err
	}
	answerGeneration := generation
	if v.mismatch {
		answerGeneration = "stale-generation"
	}
	return PublicationVerification{Selector: selector, ExecutionID: "exec-fault-class", Generation: answerGeneration, State: "PUBLISHED", Queryable: true, VerifiedAt: time.Unix(1, 0).UTC()}, nil
}

func newFaultClassReleaseService(t *testing.T, verifier PublicationVerifier) ReleaseService {
	t.Helper()
	store := NewMemoryLifecycleStore()
	stageManifest(t, store, "project-fault-class", "commit-a")
	return ReleaseService{
		Manifests: store,
		Releases:  store,
		Verifier:  verifier,
		Required:  []DataframeSelector{{Recipe: "evidence_unit", TranslationVersion: "sha256:t", Output: "units.parquet"}},
		Now:       func() time.Time { return time.Unix(1000, 0).UTC() },
	}
}

func releaseFaultClassRequest() ActivationRequest {
	return ActivationRequest{Project: "project-fault-class", Generation: "commit-a", GitCommit: "commit-a"}
}

func TestReleaseCreateKeepsVerifierFaultOutOfRequirementsError(t *testing.T) {
	storeFault := errors.New("arangodb: connection reset by peer")
	svc := newFaultClassReleaseService(t, faultClassVerifier{err: storeFault})
	_, err := svc.Create(context.Background(), releaseFaultClassRequest())
	if err == nil {
		t.Fatalf("verifier fault accepted release")
	}
	var typed *ReleaseRequirementsError
	if errors.As(err, &typed) {
		t.Fatalf("verifier fault misclassified as contract miss: %#v", typed)
	}
	if !errors.Is(err, storeFault) {
		t.Fatalf("store fault must stay errors.Is-visible for transport classification: %v", err)
	}
	if errors.Is(err, ErrReleaseRequirementsUnmet) {
		t.Fatalf("store fault must not satisfy ErrReleaseRequirementsUnmet: %v", err)
	}
}

func TestReleaseCreateRecordMissStaysRequirementsError(t *testing.T) {
	svc := newFaultClassReleaseService(t, faultClassVerifier{missing: true})
	_, err := svc.Create(context.Background(), releaseFaultClassRequest())
	var typed *ReleaseRequirementsError
	if !errors.As(err, &typed) {
		t.Fatalf("deterministic record miss must be a typed requirements error: %v", err)
	}
	if !errors.Is(err, ErrReleaseRequirementsUnmet) {
		t.Fatalf("record miss must satisfy ErrReleaseRequirementsUnmet: %v", err)
	}
	if len(typed.Verifications) != 1 || typed.Verifications[0].ErrorCode != "RELEASE_REQUIREMENTS_UNMET" {
		t.Fatalf("record miss verdict lost: %#v", typed.Verifications)
	}
}

func TestReleaseCreateShapeMismatchStaysRequirementsError(t *testing.T) {
	svc := newFaultClassReleaseService(t, faultClassVerifier{mismatch: true})
	_, err := svc.Create(context.Background(), releaseFaultClassRequest())
	var typed *ReleaseRequirementsError
	if !errors.As(err, &typed) {
		t.Fatalf("shape mismatch must remain a typed requirements error: %v", err)
	}
	if !errors.Is(err, ErrReleaseRequirementsUnmet) {
		t.Fatalf("shape mismatch must satisfy ErrReleaseRequirementsUnmet: %v", err)
	}
}
