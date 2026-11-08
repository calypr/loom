package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	materialization "github.com/calypr/loom/internal/dataframe/publication"
)

// Adversarial probes on top of TestGraphQLRecipeExecutionConcealsMalformedPersistedScope.
//
// Probe A (denial-class indistinguishability). The repair routes the R4
// malformed-persisted-scope case into the reader's existing concealed
// RECIPE_EXECUTION_NOT_FOUND arm. The attack: can a caller use the reader as
// an oracle to tell "this execution's persisted declaration is malformed" from
// "this execution does not exist", "I am not a member", "I lack one declared
// path", or "this execution declares no non-blank path"? Every one of those
// denials must carry one and the same envelope (payload nil, identical code,
// retryability and message), while the backend-fault arms must stay outside
// that envelope — otherwise the concealed class is either leaky or a
// catch-all that hides faults.
//
// Probe B (shared-resolver-cache oracle). The production ScopeResolver caches
// the project+generation path listing. A malformed-scope execution and a clean
// execution can share that key, so the attack is to interleave reads and try
// to poison coverage in either direction: a prior clean read must not make the
// malformed execution readable, and a prior malformed denial must not make the
// clean execution deniable. Verdicts and envelopes must be stable across every
// interleaving on one shared resolver.

type msoEnvelope struct {
	code      string
	retryable bool
	message   string
}

func msoEnvelopeOf(t *testing.T, got *RecipeExecution, err error) msoEnvelope {
	t.Helper()
	if got != nil {
		t.Fatalf("denial carried a payload: %#v", got)
	}
	if err == nil {
		t.Fatal("expected a denial, got success")
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("denial was not a semantic user error: %v", err)
	}
	return msoEnvelope{code: userErr.Code(), retryable: userErr.Retryable(), message: userErr.Error()}
}

func TestGraphQLRecipeExecutionMalformedScopeDenialIsIndistinguishableFromEveryDenialClass(t *testing.T) {
	store := mscStore{executions: map[string]materialization.BundleExecution{
		mscExecID:          mscBundle(mscExecID, []string{mscGrantedPath, mscMalformedNUL}),
		mscMultiPathExecID: mscBundle(mscMultiPathExecID, []string{mscGrantedPath, "p2"}),
		mscBlankExecID:     mscBundle(mscBlankExecID, []string{"", "  "}),
	}}
	probe := &mscProbe{existing: []string{mscGrantedPath, "p2"}}
	reader := mscReader(&store, probe)
	member := mscMember(mscGrantedPath)

	arms := map[string]func() (*RecipeExecution, error){
		"malformed-mixed-declaration": func() (*RecipeExecution, error) {
			return reader(mscContext(member), mscExecID)
		},
		"missing-id": func() (*RecipeExecution, error) {
			return reader(mscContext(member), mscMissingID)
		},
		"non-member": func() (*RecipeExecution, error) {
			// Same execution, same grants, but not a member of project P.
			outsider := &authscope.Principal{Subject: "operator-outsider", Projects: []string{"P-ELSEWHERE"}, AuthResourcePaths: []string{mscGrantedPath}}
			return reader(mscContext(outsider), mscExecID)
		},
		"restricted-member-missing-declared-path": func() (*RecipeExecution, error) {
			// Declares [p1, p2]; this member holds only p1.
			return reader(mscContext(member), mscMultiPathExecID)
		},
		"blank-declaration": func() (*RecipeExecution, error) {
			return reader(mscContext(member), mscBlankExecID)
		},
	}

	wantGot, wantErr := arms["missing-id"]()
	want := msoEnvelopeOf(t, wantGot, wantErr)
	if want.code != string(dataframeerrors.CodeRecipeExecutionNotFound) || want.retryable {
		t.Fatalf("baseline missing-id envelope drifted: %+v", want)
	}
	for name, arm := range arms {
		got, err := arm()
		envelope := msoEnvelopeOf(t, got, err)
		if envelope != want {
			t.Fatalf("denial class %q is distinguishable from the missing-id denial: got %+v want %+v", name, envelope, want)
		}
		if leaked := strings.ToLower(envelope.message); strings.Contains(leaked, "foreign") ||
			strings.Contains(leaked, mscRecipeName) || strings.Contains(leaked, mscExecID) {
			t.Fatalf("denial class %q leaked identifying text: %q", name, envelope.message)
		}
	}

	// The fault arms must NOT collapse into the concealed envelope.
	faulty := mscStoreWith(mscExecID, []string{mscGrantedPath, mscMalformedNUL})
	faulty.fault = errors.New("arango connection reset")
	faultReader := mscReader(faulty, &mscProbe{existing: []string{mscGrantedPath}})
	faultGot, faultErr := faultReader(mscContext(member), mscExecID)
	faultEnvelope := msoEnvelopeOf(t, faultGot, faultErr)
	if faultEnvelope.code != string(dataframeerrors.CodeBackendUnavailable) || !faultEnvelope.retryable {
		t.Fatalf("store fault on a malformed-scope execution must stay retryable BACKEND_UNAVAILABLE, got %+v", faultEnvelope)
	}
	if faultEnvelope == want {
		t.Fatal("the concealed denial became a catch-all: a backend fault shares its envelope")
	}
}

const (
	mscMultiPathExecID = "msc-exec-multipath"
	mscBlankExecID     = "msc-exec-blank"
)

func TestGraphQLRecipeExecutionMalformedScopeSharedResolverCacheIsNotAnOracle(t *testing.T) {
	store := mscStore{executions: map[string]materialization.BundleExecution{
		mscExecID:   mscBundle(mscExecID, []string{mscGrantedPath, mscMalformedNUL}),
		"msc-clean": mscBundle("msc-clean", []string{mscGrantedPath}),
	}}
	// One resolver, one cache, both executions share project P + generation
	// g-msc, so the listing entry is written by whichever read lands first.
	probe := &mscProbe{existing: []string{mscGrantedPath}}
	reader := mscReader(&store, probe)
	member := mscMember(mscGrantedPath)

	malformedDenied := func(why string) {
		t.Helper()
		got, err := reader(mscContext(member), mscExecID)
		env := msoEnvelopeOf(t, got, err)
		if env.code != string(dataframeerrors.CodeRecipeExecutionNotFound) || env.retryable {
			t.Fatalf("%s: malformed-scope read must stay the concealed not-found, got %+v", why, env)
		}
	}
	cleanReadable := func(why string) {
		t.Helper()
		got, err := reader(mscContext(member), "msc-clean")
		if err != nil || got == nil || got.ID != "msc-clean" {
			t.Fatalf("%s: clean sibling execution must stay readable, got payload=%#v err=%v", why, got, err)
		}
	}

	// Every interleaving order converges to the same verdicts; the shared
	// cache never poisons coverage in either direction.
	cleanReadable("cold clean-first")
	malformedDenied("after clean read")
	cleanReadable("after malformed denial")
	malformedDenied("second malformed read")

	for i := 0; i < 3; i++ {
		cleanReadable("steady-state clean")
		malformedDenied("steady-state malformed")
	}

	// Reversing the priming order on a fresh resolver must be identical.
	freshProbe := &mscProbe{existing: []string{mscGrantedPath}}
	freshReader := mscReader(&store, freshProbe)
	malformedDeniedWith := func(reader RecipeExecutionReader, why string) {
		t.Helper()
		got, err := reader(mscContext(member), mscExecID)
		env := msoEnvelopeOf(t, got, err)
		if env.code != string(dataframeerrors.CodeRecipeExecutionNotFound) || env.retryable {
			t.Fatalf("%s: malformed-scope read must stay the concealed not-found, got %+v", why, env)
		}
	}
	malformedDeniedWith(freshReader, "cold malformed-first")
	if got, err := freshReader(mscContext(member), "msc-clean"); err != nil || got == nil {
		t.Fatalf("a prior malformed denial must not deny the clean sibling, got payload=%#v err=%v", got, err)
	}

	// A member who holds every path the CLEAN sibling declares still cannot
	// read the malformed execution through the warmed cache.
	wide := mscMember(mscGrantedPath, "p2")
	if _, err := freshReader(mscContext(wide), "msc-clean"); err != nil {
		t.Fatalf("wide member must read the clean sibling: %v", err)
	}
	malformedDeniedWith(freshReader, "after wide-member warm-up")
}
