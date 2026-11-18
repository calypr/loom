package semantic

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func buildPlanForClass(t *testing.T, raw string) error {
	t.Helper()
	var bundle recipe.Bundle
	if err := json.Unmarshal([]byte(raw), &bundle); err != nil {
		t.Fatalf("bundle unmarshal: %v", err)
	}
	if _, err := bundle.CanonicalBundle(); err != nil {
		t.Fatalf("register-gate CanonicalBundle rejected a semantic-only payload (leg premise broken): %v", err)
	}
	if _, err := bundle.Digest(); err != nil {
		t.Fatalf("register-gate Digest rejected a semantic-only payload: %v", err)
	}
	_, err := BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "HTAN_INT/class-probe"})
	return err
}

func assertStructuralUserFault(t *testing.T, err error, wantMessage string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rejection %q, got success", wantMessage)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("structural user-fault escaped untyped (resolver would serve INTERNAL_ERROR 500): %v", err)
	}
	if userErr.Code() != string(dataframeerrors.CodeInvalidRequest) {
		t.Fatalf("code = %s, want %s (%v)", userErr.Code(), dataframeerrors.CodeInvalidRequest, err)
	}
	if userErr.Retryable() {
		t.Fatalf("user typo must be non-retryable: %v", err)
	}
	if userErr.Error() != wantMessage {
		t.Fatalf("message drifted: got %q, want %q", userErr.Error(), wantMessage)
	}
}

func TestBuildRecipePlanRowGrainMembershipIsUserFault(t *testing.T) {
	err := buildPlanForClass(t, `{"recipeSchemaVersion":1,"name":"p","translationVersion":"p","outputs":[{"name":"rows","rootResourceType":"Patient","rowGrain":"Patient"}]}`)
	assertStructuralUserFault(t, err, `unsupported row grain "Patient"`)
}

func TestBuildRecipePlanGrainRootMismatchIsUserFault(t *testing.T) {
	err := buildPlanForClass(t, `{"recipeSchemaVersion":1,"name":"p","translationVersion":"p","outputs":[{"name":"rows","rootResourceType":"Observation","rowGrain":"patient","traversals":[{"name":"obs","toResourceType":"Observation","alias":"o"}]}]}`)
	assertStructuralUserFault(t, err, `row grain "patient" requires root resource type "Patient", got "Observation"`)
}

func TestBuildRecipePlanRootResourceNotInSchemaIsUserFault(t *testing.T) {
	err := buildPlanForClass(t, `{"recipeSchemaVersion":1,"name":"p","translationVersion":"p","outputs":[{"name":"rows","rootResourceType":"NotAThing","rowGrain":"resource","traversals":[{"name":"obs","toResourceType":"Observation","alias":"o"}]}]}`)
	assertStructuralUserFault(t, err, `root resource type "NotAThing" is not represented by the active generated FHIR schema`)
}

func TestBuildRecipePlanTraversalTargetNotInSchemaIsUserFault(t *testing.T) {
	err := buildPlanForClass(t, `{"recipeSchemaVersion":1,"name":"p","translationVersion":"p","outputs":[{"name":"rows","rootResourceType":"Patient","rowGrain":"patient","traversals":[{"name":"obs","toResourceType":"NotAThing","alias":"o"}]}]}`)
	assertStructuralUserFault(t, err, `traversals[0]: target resource type "NotAThing" is not represented by the active generated FHIR schema`)
}

func TestBuildRecipePlanAliasShadowingRootIsUserFault(t *testing.T) {
	err := buildPlanForClass(t, `{"recipeSchemaVersion":1,"name":"p","translationVersion":"p","outputs":[{"name":"rows","rootResourceType":"Patient","rowGrain":"patient","traversals":[{"name":"obs","toResourceType":"Observation","alias":"root"}]}]}`)
	assertStructuralUserFault(t, err, `traversals[0]: alias "root" shadows an existing lexical binding`)
}

func TestBuildRecipePlanGroupsGrainWithoutGroupRowsIsUserFault(t *testing.T) {
	err := buildPlanForClass(t, `{"recipeSchemaVersion":1,"name":"p","translationVersion":"p","outputs":[{"name":"rows","rootResourceType":"Group","rowGrain":"groups"}]}`)
	assertStructuralUserFault(t, err, `groups row grain requires a pinned group revision`)
}

func TestBuildRecipePlanDuplicateAliasStillCaughtAtRegisterGate(t *testing.T) {
	var bundle recipe.Bundle
	if err := json.Unmarshal([]byte(`{"recipeSchemaVersion":1,"name":"p","translationVersion":"p","outputs":[{"name":"rows","rootResourceType":"Patient","rowGrain":"patient","traversals":[{"name":"a","toResourceType":"Observation","alias":"e"},{"name":"b","toResourceType":"Observation","alias":"e"}]}]}`), &bundle); err != nil {
		t.Fatal(err)
	}
	_, err := bundle.CanonicalBundle()
	var validation *recipe.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("duplicate alias must stay a register-gate *recipe.ValidationError, got %T: %v", err, err)
	}
}

func TestBuildRecipePlanValidStructuralPayloadsStillBuild(t *testing.T) {
	for _, raw := range []string{
		`{"recipeSchemaVersion":1,"name":"p","translationVersion":"p","outputs":[{"name":"rows","rootResourceType":"Patient","rowGrain":"patient","traversals":[{"name":"obs","toResourceType":"Observation","alias":"o"}]}]}`,
		`{"recipeSchemaVersion":1,"name":"p2","translationVersion":"p","outputs":[{"name":"rows","rootResourceType":"Specimen","rowGrain":"specimen","traversals":[{"name":"obs","toResourceType":"Observation","alias":"o"}]}]}`,
	} {
		var bundle recipe.Bundle
		if err := json.Unmarshal([]byte(raw), &bundle); err != nil {
			t.Fatal(err)
		}
		canonical, err := bundle.CanonicalBundle()
		if err != nil {
			t.Fatalf("valid bundle rejected at register gate: %v", err)
		}
		if _, err := BuildRecipePlan(canonical, recipe.RuntimeBindings{Project: "HTAN_INT/class-probe"}); err != nil {
			t.Fatalf("valid bundle failed plan build: %v", err)
		}
	}
}

func TestStructuralUserFaultMessagesAreComposedOnce(t *testing.T) {
	err := buildPlanForClass(t, `{"recipeSchemaVersion":1,"name":"p","translationVersion":"p","outputs":[{"name":"rows","rootResourceType":"Patient","rowGrain":"Patient"}]}`)
	if strings.Count(err.Error(), "outputs[0]") != 1 {
		t.Fatalf("wrapper context duplicated or missing: %v", err)
	}
}

func TestSemanticGraphInvariantRejectionsStayUntyped(t *testing.T) {
	if err := ValidateSemanticGraph(SemanticNode{ResourceType: "NotAThing", Alias: "root"}); err == nil {
		t.Fatal("expected invariant rejection for a bogus graph root")
	} else if _, ok := dataframeerrors.AsUserError(err); ok {
		t.Fatalf("internal-invariant rejection was typed as user fault: %#v", err)
	}
}

func TestBuildRecipePlanGroupRowsWithoutGroupsGrainIsUserFault(t *testing.T) {
	err := buildPlanForClass(t, `{"recipeSchemaVersion":1,"name":"p","translationVersion":"p","outputs":[{"name":"rows","rootResourceType":"Patient","rowGrain":"patient","groupRows":{"revisionId":"gen-group-1","unassignedMemberPolicy":"EXCLUDE"}}]}`)
	assertStructuralUserFault(t, err, `group rows require the groups row grain`)
}

func TestBuildRecipePlanExpandedTraversalTargetNotInSchemaIsUserFault(t *testing.T) {
	err := buildPlanForClass(t, `{"recipeSchemaVersion":1,"name":"p","translationVersion":"p","outputs":[{"name":"rows","rootResourceType":"Patient","rowGrain":"patient","traversals":[{"name":"obs","toResourceType":"NotAThing","alias":"o"}],"expand":{"as":"x","from":{"select":"root.identifier[]"}}}]}`)
	assertStructuralUserFault(t, err, `traversals[0]: target resource type "NotAThing" is not represented by the active generated FHIR schema`)
}

func TestBuildRecipePlanExpandedTraversalAliasShadowingRootIsUserFault(t *testing.T) {
	err := buildPlanForClass(t, `{"recipeSchemaVersion":1,"name":"p","translationVersion":"p","outputs":[{"name":"rows","rootResourceType":"Patient","rowGrain":"patient","traversals":[{"name":"obs","toResourceType":"Observation","alias":"root"}],"expand":{"as":"x","from":{"select":"root.identifier[]"}}}]}`)
	assertStructuralUserFault(t, err, `traversals[0]: alias "root" shadows an existing lexical binding`)
}

func TestBuildRecipePlanExpandedBindingShadowingRootIsUserFault(t *testing.T) {
	err := buildPlanForClass(t, `{"recipeSchemaVersion":1,"name":"p","translationVersion":"p","outputs":[{"name":"rows","rootResourceType":"Patient","rowGrain":"patient","expand":{"as":"root","from":{"select":"root.identifier[]"}}}]}`)
	assertStructuralUserFault(t, err, `alias "root" shadows an existing lexical binding`)
}
