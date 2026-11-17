package resolver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	materialization "github.com/calypr/loom/internal/dataframe/publication"
)

const (
	mscHomeProject    = "P"
	mscGeneration     = "g-msc"
	mscGrantedPath    = "p1"
	mscMalformedNUL   = "foreign\x00secret"
	mscExecID         = "msc-exec-1"
	mscMissingID      = "msc-exec-missing"
	mscRecipeName     = "persisted-scope-recipe"
	mscRecipeDigest   = "sha256-recipe-msc"
	mscSchemaDigest   = "sha256-schema-msc"
	mscOutputName     = "rows"
	mscOutputRowCount = int64(424242)
)

type mscStore struct {
	executions map[string]materialization.BundleExecution
	fault      error
	ids        []string
}

func (s *mscStore) GetExecution(_ context.Context, id string) (materialization.BundleExecution, error) {
	s.ids = append(s.ids, strings.Clone(id))
	if s.fault != nil {
		return materialization.BundleExecution{}, s.fault
	}
	if execution, ok := s.executions[id]; ok {
		return execution, nil
	}
	return materialization.BundleExecution{}, materialization.ErrBundleNotFound
}

func mscBundle(id string, declared []string) materialization.BundleExecution {
	return materialization.BundleExecution{
		ID: id,
		BundleIdentity: materialization.BundleIdentity{
			Name:              mscRecipeName,
			Project:           mscHomeProject,
			DatasetGeneration: mscGeneration,
			RecipeDigest:      mscRecipeDigest,
			SchemaDigest:      mscSchemaDigest,
			AuthResourcePaths: declared,
		},
		State: materialization.BundlePublished,
		Outputs: []materialization.BundleOutputRecord{{
			Name:     mscOutputName,
			State:    materialization.BundleReady,
			RowCount: mscOutputRowCount,
		}},
	}
}

func mscStoreWith(id string, declared []string) *mscStore {
	return &mscStore{executions: map[string]materialization.BundleExecution{id: mscBundle(id, declared)}}
}

type mscProbe struct {
	existing []string
	calls    []catalog.AuthResourcePathOptions
	fault    error
}

func (p *mscProbe) listExisting(_ context.Context, _ catalog.AuthResourcePathOptions) ([]string, error) {
	p.calls = append(p.calls, catalog.AuthResourcePathOptions{})
	if p.fault != nil {
		return nil, p.fault
	}
	return append([]string(nil), p.existing...), nil
}

func mscReader(store RecipeExecutionStore, probe *mscProbe) RecipeExecutionReader {
	resolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ListExistingAuthResourcePaths: probe.listExisting,
	})
	return NewAuthorizedRecipeExecutionReader(store, resolver)
}

func mscMember(grants ...string) *authscope.Principal {
	return &authscope.Principal{Subject: "operator-msc", Projects: []string{mscHomeProject}, AuthResourcePaths: grants}
}

func mscContext(principal *authscope.Principal) context.Context {
	return authscope.ContextWithPrincipal(context.Background(), principal)
}

func mscAssertConcealed(t *testing.T, got *RecipeExecution, err error) dataframeerrors.UserError {
	t.Helper()
	if got != nil {
		t.Fatalf("concealed refusal carried a payload alongside an error: %#v", got)
	}
	if err == nil {
		t.Fatal("expected the concealed RECIPE_EXECUTION_NOT_FOUND denial, got success")
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("concealed refusal was not a semantic user error: %v", err)
	}
	if userErr.Code() != fb20ConcealedNotFound {
		t.Fatalf("refusal code = %q, want the concealed code %q", userErr.Code(), fb20ConcealedNotFound)
	}
	return userErr
}

func mscAssertReadable(t *testing.T, got *RecipeExecution, err error, why string) {
	t.Helper()
	if err != nil || got == nil {
		t.Fatalf("%s: permitted execution must stay readable, got payload=%#v err=%v", why, got, err)
	}
	if got.ID != mscExecID || got.Name != mscRecipeName || got.RecipeDigest != mscRecipeDigest ||
		got.ResolvedSchemaDigest != mscSchemaDigest || got.SourceGeneration != mscGeneration || got.State != "PUBLISHED" {
		t.Fatalf("%s: disclosed metadata drifted: %#v", why, got)
	}
	if len(got.Outputs) != 1 || got.Outputs[0].Name != mscOutputName || got.Outputs[0].State != "READY" ||
		got.Outputs[0].RowCount == nil || *got.Outputs[0].RowCount != int(mscOutputRowCount) {
		t.Fatalf("%s: disclosed outputs drifted: %#v", why, got.Outputs)
	}
}

func mscVerdict(t *testing.T, id, verdict, observed string) {
	t.Helper()
	t.Logf("R4-GRAPHQL %s | VERDICT=%s | OBSERVED=%s", id, verdict, observed)
}

func TestGraphQLRecipeExecutionConcealsMalformedPersistedScope(t *testing.T) {
	t.Run("mixed-valid-and-nul-declaration-is-concealed-for-a-member-granted-only-p1", func(t *testing.T) {
		store := mscStoreWith(mscExecID, []string{mscGrantedPath, mscMalformedNUL})
		probe := &mscProbe{existing: []string{mscGrantedPath}}
		reader := mscReader(store, probe)
		principal := mscMember(mscGrantedPath)

		if err := authscope.AuthorizeProject(principal, mscHomeProject, false); err != nil {
			t.Fatalf("precondition: shipped AuthorizeProject must admit this member, got %v", err)
		}

		got, err := reader(mscContext(principal), mscExecID)
		userErr := mscAssertConcealed(t, got, err)
		if len(probe.calls) == 0 {
			t.Fatal("the read must travel the production resolver path (catalog consulted), got zero calls")
		}
		if leaked := mscLeakedMetadata(err, got); leaked != "" {
			t.Fatalf("concealed refusal disclosed metadata (%s): code=%q message=%q", leaked, userErr.Code(), userErr.Error())
		}
		missingGot, missingErr := reader(mscContext(principal), mscMissingID)
		missingUserErr := mscAssertConcealed(t, missingGot, missingErr)
		if missingUserErr.Code() != userErr.Code() || missingUserErr.Retryable() != userErr.Retryable() {
			t.Fatalf("malformed-scope refusal (%q retryable=%t) must be indistinguishable from a missing execution (%q retryable=%t)",
				userErr.Code(), userErr.Retryable(), missingUserErr.Code(), missingUserErr.Retryable())
		}
		mscVerdict(t, "malformed-mixed", "CONCEALED",
			"member granted only p1 reading a persisted declaration of [p1, foreign\\u0000secret] got code="+userErr.Code()+
				" retryable="+mscBool(userErr.Retryable())+", identical to the missing-id envelope")
	})

	t.Run("control-character-families-are-all-concealed", func(t *testing.T) {
		malformed := map[string][]string{
			"nul-interior":       {mscGrantedPath, "foreign\x00secret"},
			"nul-only-entry":     {mscGrantedPath, "\x00"},
			"nul-prefixed-valid": {mscGrantedPath, "\x00" + mscGrantedPath},
			"nul-suffixed-valid": {mscGrantedPath, mscGrantedPath + "\x00"},
			"del-interior":       {mscGrantedPath, "foreign\x7fsecret"},
			"del-only-entry":     {mscGrantedPath, "\x7f"},
			"tab-interior":       {mscGrantedPath, mscGrantedPath + "\tx"},
			"newline-interior":   {mscGrantedPath, "p1\np2"},
			"esc-interior":       {mscGrantedPath, "p1\x1bp2"},
			"unit-separator":     {mscGrantedPath, "p1\x1fp2"},
			"slash-only-entry":   {mscGrantedPath, "/"},
		}
		for name, declared := range malformed {
			t.Run(name, func(t *testing.T) {
				store := mscStoreWith(mscExecID, declared)
				probe := &mscProbe{existing: []string{mscGrantedPath}}
				reader := mscReader(store, probe)
				got, err := reader(mscContext(mscMember(mscGrantedPath)), mscExecID)
				userErr := mscAssertConcealed(t, got, err)
				if leaked := mscLeakedMetadata(err, got); leaked != "" {
					t.Fatalf("concealed refusal for declaration %q disclosed metadata (%s) under code %q",
						strings.Join(declared, "|"), leaked, userErr.Code())
				}
			})
		}
	})

	t.Run("permitted-normal-execution-remains-readable", func(t *testing.T) {
		permitted := map[string][]string{
			"single-granted-path":      {mscGrantedPath},
			"duplicated-granted-path":  {mscGrantedPath, mscGrantedPath},
			"slash-padded-path":        {"/" + mscGrantedPath + "/"},
			"granted-path-plus-blank":  {mscGrantedPath, "", "  ", "\t", "\n"},
			"granted-path-plus-tabbed": {mscGrantedPath, mscGrantedPath + "\t"},
		}
		for name, declared := range permitted {
			t.Run(name, func(t *testing.T) {
				store := mscStoreWith(mscExecID, declared)
				probe := &mscProbe{existing: []string{mscGrantedPath}}
				reader := mscReader(store, probe)
				got, err := reader(mscContext(mscMember(mscGrantedPath)), mscExecID)
				mscAssertReadable(t, got, err, name)
			})
		}
		t.Run("multi-path-both-granted", func(t *testing.T) {
			declared := []string{mscGrantedPath, "p2"}
			store := mscStoreWith(mscExecID, declared)
			probe := &mscProbe{existing: []string{mscGrantedPath, "p2"}}
			reader := mscReader(store, probe)
			got, err := reader(mscContext(mscMember(mscGrantedPath, "p2")), mscExecID)
			mscAssertReadable(t, got, err, "multi-path-both-granted")
			mscVerdict(t, "permitted", "READABLE", "clean persisted declarations still serve full execution metadata to an authorized member")
		})
	})

	t.Run("legitimate-blank-policy-unchanged", func(t *testing.T) {
		for _, declared := range [][]string{nil, {}, {""}, {"  "}, {"", " ", "\t"}} {
			store := mscStoreWith(mscExecID, declared)
			probe := &mscProbe{existing: []string{mscGrantedPath}}
			reader := mscReader(store, probe)
			got, err := reader(mscContext(mscMember(mscGrantedPath)), mscExecID)
			userErr := mscAssertConcealed(t, got, err)
			if strings.Contains(userErr.Error(), "foreign") {
				t.Fatalf("blank-declaration refusal leaked declaration text: %q", userErr.Error())
			}
		}
		mscVerdict(t, "blank-policy", "UNCHANGED", "restricted executions declaring no non-blank path stay concealed for restricted callers, as before")
	})

	t.Run("foreign-path-declaration-still-concealed", func(t *testing.T) {
		store := mscStoreWith(mscExecID, []string{mscGrantedPath, "p-foreign"})
		probe := &mscProbe{existing: []string{mscGrantedPath, "p-foreign"}}
		reader := mscReader(store, probe)
		got, err := reader(mscContext(mscMember(mscGrantedPath)), mscExecID)
		mscAssertConcealed(t, got, err)
	})

	t.Run("backend-faults-retain-their-classification", func(t *testing.T) {
		t.Run("store-fault-is-retryable-backend-unavailable", func(t *testing.T) {
			store := mscStoreWith(mscExecID, []string{mscGrantedPath, mscMalformedNUL})
			store.fault = errors.New("arango connection reset")
			probe := &mscProbe{existing: []string{mscGrantedPath}}
			reader := mscReader(store, probe)
			got, err := reader(mscContext(mscMember(mscGrantedPath)), mscExecID)
			mscAssertFaultClassification(t, got, err, dataframeerrors.CodeBackendUnavailable, true, "raw store fault")
		})
		t.Run("catalog-listing-fault-is-retryable-backend-unavailable", func(t *testing.T) {
			store := mscStoreWith(mscExecID, []string{mscGrantedPath, mscMalformedNUL})
			probe := &mscProbe{fault: errors.New("catalog auth path listing exploded")}
			reader := mscReader(store, probe)
			got, err := reader(mscContext(mscMember(mscGrantedPath)), mscExecID)
			mscAssertFaultClassification(t, got, err, dataframeerrors.CodeBackendUnavailable, true, "catalog listing fault")
		})
		t.Run("sentinel-authorization-outage-stays-retryable", func(t *testing.T) {
			store := mscStoreWith(mscExecID, []string{mscGrantedPath, mscMalformedNUL})
			probe := &mscProbe{fault: authscope.ErrAuthorizationBackendUnavailable}
			reader := mscReader(store, probe)
			got, err := reader(mscContext(mscMember(mscGrantedPath)), mscExecID)
			mscAssertFaultClassification(t, got, err, dataframeerrors.CodeBackendUnavailable, true, "authorization outage sentinel")
		})
		t.Run("durable-not-found-sentinel-stays-not-found", func(t *testing.T) {
			store := mscStoreWith(mscExecID, []string{mscGrantedPath, mscMalformedNUL})
			store.fault = materialization.ErrBundleNotFound
			probe := &mscProbe{existing: []string{mscGrantedPath}}
			reader := mscReader(store, probe)
			got, err := reader(mscContext(mscMember(mscGrantedPath)), mscExecID)
			mscAssertFaultClassification(t, got, err, dataframeerrors.CodeRecipeExecutionNotFound, false, "durable not-found sentinel")
		})
	})

	t.Run("malformed-scope-refusal-is-not-a-membership-or-store-artifact", func(t *testing.T) {
		clean := mscReader(mscStoreWith(mscExecID, []string{mscGrantedPath}), &mscProbe{existing: []string{mscGrantedPath}})
		got, err := clean(mscContext(mscMember(mscGrantedPath)), mscExecID)
		mscAssertReadable(t, got, err, "baseline-clean-declaration")

		dirty := mscReader(mscStoreWith(mscExecID, []string{mscGrantedPath, mscMalformedNUL}), &mscProbe{existing: []string{mscGrantedPath}})
		dirtyGot, dirtyErr := dirty(mscContext(mscMember(mscGrantedPath)), mscExecID)
		mscAssertConcealed(t, dirtyGot, dirtyErr)
		mscVerdict(t, "sole-discriminator", "CONCEALED",
			"identical fixture, principal and resolver; adding the malformed non-blank declaration entry flips 200-readable to concealed")
	})

	t.Run("store-is-still-consulted-before-authorization", func(t *testing.T) {
		store := mscStoreWith(mscExecID, []string{mscGrantedPath, mscMalformedNUL})
		probe := &mscProbe{existing: []string{mscGrantedPath}}
		reader := mscReader(store, probe)
		if _, err := reader(mscContext(mscMember(mscGrantedPath)), mscExecID); err == nil {
			t.Fatal("expected the concealed denial")
		}
		if len(store.ids) != 1 || store.ids[0] != mscExecID {
			t.Fatalf("expected exactly one fetch of the opaque id, got %v", store.ids)
		}
	})
}

func mscAssertFaultClassification(t *testing.T, got *RecipeExecution, err error, wantCode dataframeerrors.ErrorCode, wantRetryable bool, cause string) {
	t.Helper()
	if got != nil {
		t.Fatalf("%s: fault carried a payload: %#v", cause, got)
	}
	if err == nil {
		t.Fatalf("%s: expected a fault, got success", cause)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("%s: fault was not a semantic user error: %v", cause, err)
	}
	if dataframeerrors.ErrorCode(userErr.Code()) != wantCode || userErr.Retryable() != wantRetryable {
		t.Fatalf("%s: fault classified as %q retryable=%t, want %q retryable=%t",
			cause, userErr.Code(), userErr.Retryable(), wantCode, wantRetryable)
	}
}

func mscLeakedMetadata(err error, got *RecipeExecution) string {
	if got != nil {
		return "payload present"
	}
	if err == nil {
		return ""
	}
	text := err.Error()
	for _, secret := range []string{
		mscMalformedNUL, "foreign", mscRecipeName, mscRecipeDigest, mscSchemaDigest, mscGeneration,
		mscHomeProject, mscExecID, "424242", mscOutputName,
	} {
		if strings.Contains(text, secret) {
			return "error text contains " + secret
		}
	}
	return ""
}

func mscBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
