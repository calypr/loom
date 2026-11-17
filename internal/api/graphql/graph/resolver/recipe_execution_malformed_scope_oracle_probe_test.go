package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	materialization "github.com/calypr/loom/internal/dataframe/publication"
)

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
			outsider := &authscope.Principal{Subject: "operator-outsider", Projects: []string{"P-ELSEWHERE"}, AuthResourcePaths: []string{mscGrantedPath}}
			return reader(mscContext(outsider), mscExecID)
		},
		"restricted-member-missing-declared-path": func() (*RecipeExecution, error) {
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

	cleanReadable("cold clean-first")
	malformedDenied("after clean read")
	cleanReadable("after malformed denial")
	malformedDenied("second malformed read")

	for i := 0; i < 3; i++ {
		cleanReadable("steady-state clean")
		malformedDenied("steady-state malformed")
	}

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

	wide := mscMember(mscGrantedPath, "p2")
	if _, err := freshReader(mscContext(wide), "msc-clean"); err != nil {
		t.Fatalf("wide member must read the clean sibling: %v", err)
	}
	malformedDeniedWith(freshReader, "after wide-member warm-up")
}
