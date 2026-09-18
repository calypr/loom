package explorer

import (
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func interpretationFixture() InterpretationRevision {
	return InterpretationRevision{
		Project: "program/project", LibraryID: "mapping",
		Applicability: InterpretationApplicability{ResourceTypes: []string{"Observation", "Patient"}, LogicalTypes: []string{"string"}},
		Rules: []InterpretationRule{{
			ID: "status", Match: InterpretationStructuralMatch{ResourceType: "Observation", LogicalType: "string", System: "urn:status", Code: "active"},
			Definition: InterpretationFeatureDefinition{Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "status", ProjectionMode: "VALUE"}}},
		}},
		Author: "researcher", Explanation: "Use the observed status field",
	}
}

func TestInterpretationCanonicalPermutationAndContentChange(t *testing.T) {
	left := interpretationFixture()
	right := interpretationFixture()
	right.Applicability.ResourceTypes = []string{"Patient", "Observation", "Observation"}
	right.Rules = []InterpretationRule{left.Rules[0]}
	leftDigest, err := left.ComputeContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.ComputeContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("permutation changed digest: %s != %s", leftDigest, rightDigest)
	}
	right.Rules[0].Definition.Source.Field.Path = "code"
	changed, err := right.ComputeContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	if changed == leftDigest {
		t.Fatal("executable source change reused content digest")
	}
	right = interpretationFixture()
	right.Rules[0].ID = "different-rule"
	changed, err = right.ComputeContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	if changed == leftDigest {
		t.Fatal("rule identity change reused content digest")
	}
}

func TestInterpretationPrepareCanonicalizesAndDerivesIDs(t *testing.T) {
	revision, err := PrepareInterpretationRevision(interpretationFixture())
	if err != nil {
		t.Fatal(err)
	}
	if revision.ContentDigest == "" || !strings.HasPrefix(string(revision.ID), "interpretation_") {
		t.Fatalf("missing derived identities: %#v", revision)
	}
	if err := revision.Validate(); err != nil {
		t.Fatal(err)
	}
	if revision.Project != "program/project" {
		t.Fatalf("project=%q", revision.Project)
	}
}

func TestInterpretationRejectsInvalidProjectAndIDs(t *testing.T) {
	for _, raw := range []string{"", " leading", "with space", "bad/id"} {
		if _, err := NewInterpretationLibraryID(raw); err == nil {
			t.Errorf("library ID %q accepted", raw)
		}
	}
	for _, raw := range []string{"", "not-a-digest", "sha256:ABC"} {
		if _, err := NewInterpretationContentDigest(raw); err == nil {
			t.Errorf("digest %q accepted", raw)
		}
	}
	invalid := interpretationFixture()
	invalid.Project = "bad project"
	if _, err := PrepareInterpretationRevision(invalid); !errors.Is(err, ErrInvalidInterpretationProject) {
		t.Fatalf("project error=%v", err)
	}
	invalid = interpretationFixture()
	invalid.LibraryID = "bad/id"
	if _, err := PrepareInterpretationRevision(invalid); !errors.Is(err, ErrInvalidInterpretationID) {
		t.Fatalf("library error=%v", err)
	}
}

func TestInterpretationRejectsAmbiguousOverlapAndSelectsPriority(t *testing.T) {
	base := interpretationFixture()
	base.Rules = append(base.Rules, InterpretationRule{
		ID: "status-specific", Match: InterpretationStructuralMatch{ResourceType: "Observation", LogicalType: "string", System: "urn:status", Code: "active"},
		Definition: base.Rules[0].Definition,
	})
	if _, err := PrepareInterpretationRevision(base); !errors.Is(err, ErrAmbiguousMapping) {
		t.Fatalf("ambiguous overlap error=%v", err)
	}
	priorityOne, priorityTwo := InterpretationPriority(1), InterpretationPriority(2)
	base.Rules[0].Priority = &priorityOne
	base.Rules[1].Priority = &priorityTwo
	revision, err := PrepareInterpretationRevision(base)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := revision.SelectRule(InterpretationStructuralCandidate{ResourceType: "Observation", LogicalType: "string", System: "urn:status", Code: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if rule.ID != "status-specific" {
		t.Fatalf("selected rule=%q, want status-specific", rule.ID)
	}
	base.Rules[1].Priority = &priorityOne
	if _, err := PrepareInterpretationRevision(base); !errors.Is(err, ErrAmbiguousMapping) {
		t.Fatalf("priority tie error=%v", err)
	}
}
