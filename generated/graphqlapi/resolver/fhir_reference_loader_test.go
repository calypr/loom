package resolver

import (
	"context"
	"testing"

	fhir "github.com/calypr/loom/generated/fhir"
	"github.com/calypr/loom/generated/graphqlapi/model"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	queryapi "github.com/calypr/loom/internal/graphqlapi/query"
)

func TestNormalizeFHIRReference(t *testing.T) {
	for _, test := range []struct {
		name, input, target, id string
	}{
		{name: "relative", input: "Patient/123", target: "Patient", id: "123"},
		{name: "absolute", input: "https://example.test/fhir/Patient/123", target: "Patient", id: "123"},
		{name: "versioned", input: "Patient/123/_history/4", target: "Patient", id: "123"},
		{name: "absolute-versioned", input: "https://example.test/fhir/Patient/123/_history/4", target: "Patient", id: "123"},
	} {
		t.Run(test.name, func(t *testing.T) {
			target, id, ok := normalizeFHIRReference(test.input)
			if !ok || target != test.target || id != test.id {
				t.Fatalf("normalizeFHIRReference(%q) = %q, %q, %v", test.input, target, id, ok)
			}
		})
	}
	for _, invalid := range []string{
		"not-a-reference",
		"Patient/123/extra",
		"Patient/123/_history",
		"urn:uuid:123",
		"https://example.test/fhir/Unsupported/123",
	} {
		if _, _, ok := normalizeFHIRReference(invalid); ok {
			t.Fatalf("malformed reference %q accepted", invalid)
		}
	}
}

func TestResolveFHIRContainedReference(t *testing.T) {
	reference := "#patient"
	root := &fhir.DocumentReference{
		Subject: &fhir.Reference{Reference: &reference},
	}
	resolver := &Resolver{}
	ctx := withFHIRReferenceLoader(context.Background(), resolver)
	loader := fhirReferenceLoaderFromContext(ctx, resolver)
	loader.register(root, fhirReferenceOwner{
		read: queryapi.FHIRReadContext{
			Project: "P1", DatasetGeneration: "generation-1",
			ScopeDigest: "scope",
			Scope:       authscope.ReadScope{Mode: authscope.ReadScopeRestricted},
		},
		contained: indexContainedResources(map[string]any{
			"contained": []any{map[string]any{
				"id": "patient", "resourceType": "Patient", "gender": "female",
			}},
		}),
	})

	requested := model.FHIRResourceTypePatient
	value, err := resolver.resolveFHIRReference(ctx, root.Subject, &requested, false)
	if err != nil {
		t.Fatalf("resolveFHIRReference() error = %v", err)
	}
	patient, ok := value.(*fhir.Patient)
	if !ok || patient.GetID() != "patient" || patient.Gender == nil || *patient.Gender != "female" {
		t.Fatalf("resolveFHIRReference() = %#v, want contained Patient", value)
	}
}

func TestResolveFHIRReferenceDepthLimitIsNotOptional(t *testing.T) {
	reference := "Patient/123"
	ref := &fhir.Reference{Reference: &reference}
	resolver := &Resolver{}
	ctx := withFHIRReferenceLoader(context.Background(), resolver)
	fhirReferenceLoaderFromContext(ctx, resolver).register(ref, fhirReferenceOwner{
		read:  queryapi.FHIRReadContext{Project: "P1", DatasetGeneration: "generation-1", ScopeDigest: "scope"},
		depth: fhirReferenceMaxDepth,
	})

	_, err := resolver.resolveFHIRReference(ctx, ref, nil, true)
	userErr, ok := err.(dataframeerrors.UserError)
	if !ok || userErr.Code() != string(dataframeerrors.CodeQueryDepthExceeded) {
		t.Fatalf("resolveFHIRReference() error = %v, want QUERY_DEPTH_EXCEEDED", err)
	}
}

func TestFHIRReferenceBatchSize(t *testing.T) {
	if fhirReferenceBatchSize != 256 {
		t.Fatalf("batch size = %d, want 256", fhirReferenceBatchSize)
	}
}
