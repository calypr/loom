package explorer

import (
	"errors"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/interpretation"
)

func interpretationFixture() interpretation.InterpretationRevision {
	return interpretation.InterpretationRevision{
		Project: "program/project", LibraryID: "mapping",
		Applicability: interpretation.InterpretationApplicability{ResourceTypes: []string{"Observation", "Patient"}, LogicalTypes: []string{"string"}},
		Rules: []interpretation.InterpretationRule{{
			ID: "status", Match: interpretation.InterpretationStructuralMatch{ResourceType: "Observation", LogicalType: "string", System: "urn:status", Code: "active"},
			Definition: interpretation.InterpretationFeatureDefinition{Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "status", ProjectionMode: "VALUE"}}},
		}},
		Author: "researcher", Explanation: "Use the observed status field",
	}
}

func TestInterpretationCanonicalPermutationAndContentChange(t *testing.T) {
	left := interpretationFixture()
	right := interpretationFixture()
	right.Applicability.ResourceTypes = []string{"Patient", "Observation", "Observation"}
	right.Rules = []interpretation.InterpretationRule{left.Rules[0]}
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
	revision, err := interpretation.PrepareInterpretationRevision(interpretationFixture())
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
		if _, err := interpretation.NewInterpretationLibraryID(raw); err == nil {
			t.Errorf("library ID %q accepted", raw)
		}
	}
	for _, raw := range []string{"", "not-a-digest", "sha256:ABC"} {
		if _, err := interpretation.NewInterpretationContentDigest(raw); err == nil {
			t.Errorf("digest %q accepted", raw)
		}
	}
	invalid := interpretationFixture()
	invalid.Project = "bad project"
	if _, err := interpretation.PrepareInterpretationRevision(invalid); !errors.Is(err, interpretation.ErrInvalidInterpretationProject) {
		t.Fatalf("project error=%v", err)
	}
	invalid = interpretationFixture()
	invalid.LibraryID = "bad/id"
	if _, err := interpretation.PrepareInterpretationRevision(invalid); !errors.Is(err, interpretation.ErrInvalidInterpretationID) {
		t.Fatalf("library error=%v", err)
	}
}

func TestInterpretationRejectsAmbiguousOverlapAndSelectsPriority(t *testing.T) {
	base := interpretationFixture()
	base.Rules = append(base.Rules, interpretation.InterpretationRule{
		ID: "status-specific", Match: interpretation.InterpretationStructuralMatch{ResourceType: "Observation", LogicalType: "string", System: "urn:status", Code: "active"},
		Definition: base.Rules[0].Definition,
	})
	if _, err := interpretation.PrepareInterpretationRevision(base); !errors.Is(err, interpretation.ErrAmbiguousMapping) {
		t.Fatalf("ambiguous overlap error=%v", err)
	}
	priorityOne, priorityTwo := interpretation.InterpretationPriority(1), interpretation.InterpretationPriority(2)
	base.Rules[0].Priority = &priorityOne
	base.Rules[1].Priority = &priorityTwo
	revision, err := interpretation.PrepareInterpretationRevision(base)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := revision.SelectRule(interpretation.InterpretationStructuralCandidate{ResourceType: "Observation", LogicalType: "string", System: "urn:status", Code: "active"})
	if err != nil {
		t.Fatal(err)
	}
	if rule.ID != "status-specific" {
		t.Fatalf("selected rule=%q, want status-specific", rule.ID)
	}
	base.Rules[1].Priority = &priorityOne
	if _, err := interpretation.PrepareInterpretationRevision(base); !errors.Is(err, interpretation.ErrAmbiguousMapping) {
		t.Fatalf("priority tie error=%v", err)
	}
}
