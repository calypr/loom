package lifecycle

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestSchemaFieldCandidateChoiceRecompilesWithoutObservedCatalogEntry(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := schemaFieldsTestSnapshot(scope, nil)
	snapshot.Nodes[0].RowRootEligible = true
	authorized := AuthorizedCapability{Snapshot: snapshot, Scope: scope}
	candidate, ok := uniqueCapabilityCandidate(snapshot, schemaFieldCandidateID(snapshot.Token, "patient-node", "gender"))
	if !ok {
		t.Fatal("generated field identity was not resolved")
	}
	proved, err := proveConstructionCandidate(context.Background(), authorized, "Patient", candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	choice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, nil, proved)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := capability.DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatal(err)
	}
	field := choice.Source.(capability.FieldChoiceSource)
	resolved, _, err := resolveFieldConstructionChoice(context.Background(), authorized, snapshot,
		authoringv2.CatalogSnapshot{SnapshotToken: snapshot.Token}, "Patient",
		authoringv2.ConstructionChoiceSelection{ChoiceID: choice.ChoiceID, Form: "VALUE"}, identity, field)
	if err != nil || resolved.CandidateID != candidate.ID || resolved.LogicalType != "string" {
		t.Fatalf("resolved generated choice = %#v, error=%v", resolved, err)
	}
}

func TestSchemaFieldCandidateMissingGenderHasNoObservedOrOperationClaims(t *testing.T) {
	snapshot := schemaFieldsTestSnapshot(authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, nil)
	id := schemaFieldCandidateID(snapshot.Token, "patient-node", "gender")
	candidate, ok := resolveSchemaFieldCandidate(snapshot, id)
	if !ok || candidate.FieldPath != "gender" || candidate.LogicalType != "string" || candidate.Cardinality != "optional_one" {
		t.Fatalf("generated candidate = %#v, found=%v", candidate, ok)
	}
	if candidate.Observed || candidate.Populated || len(candidate.ProjectionModes) != 0 || len(candidate.SupportedOperations) != 0 {
		t.Fatalf("schema metadata must not claim population evidence or compiler proof: %#v", candidate)
	}
}

func TestSchemaFieldCandidateRejectsWrongSnapshotNodeAndPath(t *testing.T) {
	snapshot := schemaFieldsTestSnapshot(authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, nil)
	for _, id := range []string{
		schemaFieldCandidateID("another-snapshot", "patient-node", "gender"),
		schemaFieldCandidateID(snapshot.Token, "outside-node", "gender"),
		schemaFieldCandidateID(snapshot.Token, "patient-node", "notAField"),
		"schema_field.invalid",
	} {
		if candidate, ok := resolveSchemaFieldCandidate(snapshot, id); ok {
			t.Fatalf("accepted unavailable identity %q: %#v", id, candidate)
		}
	}
	snapshot.Nodes[0].Populated = false
	if _, ok := resolveSchemaFieldCandidate(snapshot, schemaFieldCandidateID(snapshot.Token, "patient-node", "gender")); ok {
		t.Fatal("accepted an unpopulated node")
	}
}
