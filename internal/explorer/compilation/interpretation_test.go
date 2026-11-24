package compilation

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/interpretation"

	fhirbinding "github.com/calypr/loom/internal/fhir/binding"

	"github.com/calypr/loom/internal/explorer/capability"
)

func TestResolveInterpretationsSelectsUniquePriorityRule(t *testing.T) {
	snapshot := interpretationSnapshot()
	low := interpretation.InterpretationPriority(1)
	high := interpretation.InterpretationPriority(2)
	revision := prepareInterpretation(t, interpretation.InterpretationRevision{
		ID: "revision-priority", Project: "project-a", LibraryID: "library-a", Author: "tester", Explanation: "priority",
		Applicability: interpretation.InterpretationApplicability{ResourceTypes: []string{"Patient"}, LogicalTypes: []string{"string"}},
		Rules: []interpretation.InterpretationRule{
			{ID: "rule-low", Priority: &low, Match: interpretation.InterpretationStructuralMatch{ResourceType: "Patient", LogicalType: "string"}, Definition: fieldDefinition("id")},
			{ID: "rule-high", Priority: &high, Match: interpretation.InterpretationStructuralMatch{ResourceType: "Patient", LogicalType: "string"}, Definition: fieldDefinition("name.family")},
		},
	})
	workspace := interpretationWorkspace(string(revision.ID))
	inputs, err := ResolveWorkspaceInterpretations("project-a", workspace, snapshot, map[interpretation.InterpretationRevisionID]interpretation.InterpretationRevision{revision.ID: revision})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs.Interpretations) != 1 || inputs.Interpretations[0].SelectedRuleID != "rule-high" {
		t.Fatalf("resolved interpretations = %#v", inputs.Interpretations)
	}
}

func TestResolveInterpretationsRejectsMissingInapplicableAndAmbiguous(t *testing.T) {
	snapshot := interpretationSnapshot()
	workspace := interpretationWorkspace("revision-missing")
	if _, err := ResolveWorkspaceInterpretations("project-a", workspace, snapshot, nil); err == nil {
		t.Fatal("missing revision was accepted")
	}
	inapplicable := prepareInterpretation(t, interpretation.InterpretationRevision{
		ID: "revision-inapplicable", Project: "project-a", LibraryID: "library-a", Author: "tester", Explanation: "wrong resource",
		Applicability: interpretation.InterpretationApplicability{ResourceTypes: []string{"Observation"}},
		Rules:         []interpretation.InterpretationRule{{ID: "rule", Match: interpretation.InterpretationStructuralMatch{ResourceType: "Observation"}, Definition: fieldDefinition("id")}},
	})
	workspace = interpretationWorkspace(string(inapplicable.ID))
	if _, err := ResolveWorkspaceInterpretations("project-a", workspace, snapshot, map[interpretation.InterpretationRevisionID]interpretation.InterpretationRevision{inapplicable.ID: inapplicable}); err == nil {
		t.Fatal("inapplicable revision was accepted")
	}
	first := interpretation.InterpretationPriority(1)
	second := interpretation.InterpretationPriority(1)
	ambiguous := interpretation.InterpretationRevision{Project: "project-a", LibraryID: "library-a", Author: "tester", Explanation: "tie", Rules: []interpretation.InterpretationRule{
		{ID: "rule-a", Priority: &first, Match: interpretation.InterpretationStructuralMatch{ResourceType: "Patient"}, Definition: fieldDefinition("id")},
		{ID: "rule-b", Priority: &second, Match: interpretation.InterpretationStructuralMatch{ResourceType: "Patient"}, Definition: fieldDefinition("name.family")},
	}}
	if _, err := ambiguous.SelectRule(interpretation.InterpretationStructuralCandidate{ResourceType: "Patient"}); !errors.Is(err, interpretation.ErrAmbiguousMapping) {
		t.Fatalf("ambiguous selection error = %v", err)
	}
}

func TestResolveInterpretationCandidateRejectsSourceWithoutFHIRIdentity(t *testing.T) {
	snapshot := interpretationSnapshot()
	snapshot.Candidates = snapshot.Candidates[:1]
	document := interpretationWorkspace("revision").Documents[0]
	column := document.Columns[0]
	column.Source = ws.ColumnSource{Kind: ws.SourceProjectID}

	resolution, err := ResolveInterpretationCandidate(document, column, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.State() != InterpretationCandidateUnsupported || resolution.Reason() == "" {
		t.Fatalf("project ID source resolution = %s (%q)", resolution.State(), resolution.Reason())
	}
	if match, ok := resolution.Match(); ok || !reflect.DeepEqual(match, InterpretationCandidateMatch{}) {
		t.Fatalf("unsupported resolution exposed semantic payload: %#v", match)
	}
}

func TestResolveInterpretationCandidateReturnsOrdinaryFieldIdentity(t *testing.T) {
	document := interpretationWorkspace("revision").Documents[0]
	resolution, err := ResolveInterpretationCandidate(document, document.Columns[0], interpretationSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	match := readyInterpretationMatch(t, resolution)
	if !reflect.DeepEqual(match.CapabilityCandidateIDs, []string{"candidate-id"}) {
		t.Fatalf("capability candidate IDs = %#v", match.CapabilityCandidateIDs)
	}
	want := interpretation.InterpretationStructuralCandidate{ResourceType: "Patient", LogicalType: "string", Cardinality: "optional_one", SchemaDigest: "schema"}
	if !reflect.DeepEqual(match.StructuralCandidate, want) {
		t.Fatalf("structural candidate = %#v, want %#v", match.StructuralCandidate, want)
	}
}

func TestResolveInterpretationCandidateMatchesCodedValueSystemAndCode(t *testing.T) {
	snapshot := interpretationSnapshot()
	candidate := snapshot.Candidates[0]
	candidate.FieldPath = "code.coding[].display"
	candidate.ConceptCandidates = []capability.ConceptCandidate{
		{SourceResourceType: "Patient", SourcePath: candidate.FieldPath, System: "urn:codes", Code: "active", LogicalType: "string"},
		{SourceResourceType: "Patient", SourcePath: candidate.FieldPath, System: "urn:other", Code: "active", LogicalType: "string"},
	}
	snapshot.Candidates = []capability.Candidate{candidate}
	document := interpretationWorkspace("revision").Documents[0]
	document.Columns[0].Source = ws.ColumnSource{Kind: ws.SourceCodedValue, Lookup: &ws.LookupSource{
		Binding: &fhirbinding.CorrelatedBinding{ValuePath: candidate.FieldPath},
		Key:     &fhirbinding.CorrelatedKey{System: "urn:codes", Code: "active"},
	}}

	resolution, err := ResolveInterpretationCandidate(document, document.Columns[0], snapshot)
	if err != nil {
		t.Fatal(err)
	}
	match := readyInterpretationMatch(t, resolution)
	if got := match.StructuralCandidate; got.System != "urn:codes" || got.Code != "active" {
		t.Fatalf("coded structural identity = %#v", got)
	}
}

func TestResolveInterpretationCandidateMatchesExtensionAncestry(t *testing.T) {
	snapshot := interpretationSnapshot()
	candidate := snapshot.Candidates[0]
	candidate.FieldPath = "extension[].extension[].valueString"
	candidate.ConceptCandidates = []capability.ConceptCandidate{{
		SourceResourceType: "Patient", SourcePath: candidate.FieldPath,
		ExtensionURLPath: []string{"urn:parent", "urn:leaf"}, LogicalType: "string",
	}}
	snapshot.Candidates = []capability.Candidate{candidate}
	document := interpretationWorkspace("revision").Documents[0]
	document.Columns[0].Source = ws.ColumnSource{Kind: ws.SourceExtensionByURL, Lookup: &ws.LookupSource{
		Extension: &fhirbinding.ExtensionBinding{
			OwnerPath: "extension[].extension[]", URLPath: []string{"urn:parent", "urn:leaf"}, ValuePath: "valueString",
		},
	}}

	resolution, err := ResolveInterpretationCandidate(document, document.Columns[0], snapshot)
	if err != nil {
		t.Fatal(err)
	}
	match := readyInterpretationMatch(t, resolution)
	if !reflect.DeepEqual(match.StructuralCandidate.ExtensionURLPath, []string{"urn:parent", "urn:leaf"}) {
		t.Fatalf("extension ancestry = %#v", match.StructuralCandidate.ExtensionURLPath)
	}
}

func TestResolveInterpretationCandidateReportsAmbiguousCapabilitiesAndConcepts(t *testing.T) {
	t.Run("missing capability", func(t *testing.T) {
		snapshot := interpretationSnapshot()
		snapshot.Candidates = snapshot.Candidates[1:]
		document := interpretationWorkspace("revision").Documents[0]
		assertUnavailableInterpretation(t, document, document.Columns[0], snapshot, InterpretationCandidateMissing)
	})
	t.Run("capabilities", func(t *testing.T) {
		snapshot := interpretationSnapshot()
		duplicate := snapshot.Candidates[0]
		duplicate.ID = "candidate-id-duplicate"
		snapshot.Candidates = append(snapshot.Candidates, duplicate)
		document := interpretationWorkspace("revision").Documents[0]
		assertUnavailableInterpretation(t, document, document.Columns[0], snapshot, InterpretationCandidateAmbiguous)
	})
	t.Run("concepts", func(t *testing.T) {
		snapshot := interpretationSnapshot()
		concept := capability.ConceptCandidate{SourceResourceType: "Patient", SourcePath: "id", System: "urn:codes", Code: "x", LogicalType: "string"}
		snapshot.Candidates[0].ConceptCandidates = []capability.ConceptCandidate{concept, concept}
		document := interpretationWorkspace("revision").Documents[0]
		assertUnavailableInterpretation(t, document, document.Columns[0], snapshot, InterpretationCandidateAmbiguous)
	})
}

func TestResolveInterpretationCandidateNormalizesLegacyIdentifierSource(t *testing.T) {
	snapshot := interpretationSnapshot()
	candidate := snapshot.Candidates[0]
	candidate.FieldPath = "identifier[].value"
	candidate.ConceptCandidates = []capability.ConceptCandidate{{
		SourceResourceType: "Patient", SourcePath: candidate.FieldPath, System: "urn:old-system", LogicalType: "string",
	}}
	snapshot.Candidates = []capability.Candidate{candidate}
	document := interpretationWorkspace("revision").Documents[0]
	legacy := document.Columns[0]
	legacy.Source = ws.ColumnSource{Kind: ws.SourceIdentifierBySystem, Lookup: &ws.LookupSource{Match: "urn:old-system", Path: candidate.FieldPath}}
	modern := document.Columns[0]
	modern.Source = ws.ColumnSource{Kind: ws.SourceIdentifierBySystem, Lookup: &ws.LookupSource{Identifier: &fhirbinding.IdentifierBinding{
		OwnerPath: "identifier[]", ValuePath: "value", SystemURI: "urn:old-system", LogicalType: "string",
	}}}

	legacyResolution, err := ResolveInterpretationCandidate(document, legacy, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	modernResolution, err := ResolveInterpretationCandidate(document, modern, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	legacyMatch := readyInterpretationMatch(t, legacyResolution)
	modernMatch := readyInterpretationMatch(t, modernResolution)
	if !reflect.DeepEqual(legacyMatch, modernMatch) {
		t.Fatalf("legacy and normalized source resolutions differ: legacy=%#v modern=%#v", legacyMatch, modernMatch)
	}
}

func readyInterpretationMatch(t *testing.T, resolution interpretationCandidateResolution) InterpretationCandidateMatch {
	t.Helper()
	match, ok := resolution.Match()
	if !ok || resolution.State() != InterpretationCandidateReady {
		t.Fatalf("resolution = %s (%q), want READY", resolution.State(), resolution.Reason())
	}
	return match
}

func assertUnavailableInterpretation(t *testing.T, document ws.Document, column ws.Column, snapshot capability.Snapshot, want InterpretationCandidateResolutionState) {
	t.Helper()
	resolution, err := ResolveInterpretationCandidate(document, column, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.State() != want || resolution.Reason() == "" {
		t.Fatalf("resolution = %s (%q), want %s with a reason", resolution.State(), resolution.Reason(), want)
	}
	if match, ok := resolution.Match(); ok || !reflect.DeepEqual(match, InterpretationCandidateMatch{}) {
		t.Fatalf("%s resolution exposed semantic payload: %#v", want, match)
	}
}

func TestCompileWorkspaceUsesResolvedDefinitionAndResolvedDigest(t *testing.T) {
	snapshot := interpretationSnapshot()
	revision := prepareInterpretation(t, interpretation.InterpretationRevision{
		ID: "revision-source", Project: "project-a", LibraryID: "library-a", Author: "tester", Explanation: "replace suggestion",
		Rules: []interpretation.InterpretationRule{{ID: "rule", Match: interpretation.InterpretationStructuralMatch{ResourceType: "Patient", LogicalType: "string"}, Definition: fieldDefinition("name.family")}},
	})
	workspace := interpretationWorkspace(string(revision.ID))
	inputs, err := ResolveWorkspaceInterpretations("project-a", workspace, snapshot, map[interpretation.InterpretationRevisionID]interpretation.InterpretationRevision{revision.ID: revision})
	if err != nil {
		t.Fatal(err)
	}
	first, err := CompileWorkspace(context.Background(), "project-a", "patients", workspace, snapshot, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Bundle.Outputs) != 1 || len(first.Bundle.Outputs[0].Fields) != 1 || !strings.Contains(first.Bundle.Outputs[0].Fields[0].Expr.Select, "name.family") {
		t.Fatalf("compiled source = %#v", first.Bundle.Outputs[0].Fields)
	}
	if strings.Contains(first.Bundle.Outputs[0].Fields[0].Expr.Select, "root.id") {
		t.Fatalf("original source suggestion remained executable: %#v", first.Bundle.Outputs[0].Fields[0].Expr)
	}
	_, err = CompileWorkspace(context.Background(), "project-a", "patients", workspace, snapshot, ResolvedInputs{})
	if err == nil {
		t.Fatal("pinned workspace compiled without resolved interpretation")
	}
	changed := revision
	changed.Rules = append([]interpretation.InterpretationRule(nil), revision.Rules...)
	changed.Rules[0].Definition = fieldDefinition("id")
	changed, err = interpretation.PrepareInterpretationRevision(changed)
	if err != nil {
		t.Fatal(err)
	}
	changedInputs, err := ResolveWorkspaceInterpretations("project-a", workspaceWithRevision(string(changed.ID)), snapshot, map[interpretation.InterpretationRevisionID]interpretation.InterpretationRevision{changed.ID: changed})
	if err != nil {
		t.Fatal(err)
	}
	second, err := CompileWorkspace(context.Background(), "project-a", "patients", workspaceWithRevision(string(changed.ID)), snapshot, changedInputs)
	if err != nil {
		t.Fatal(err)
	}
	if first.ResolvedInputsDigest == second.ResolvedInputsDigest || first.RecipeDigest == second.RecipeDigest {
		t.Fatalf("resolved definition change did not change identity: %q/%q vs %q/%q", first.ResolvedInputsDigest, first.RecipeDigest, second.ResolvedInputsDigest, second.RecipeDigest)
	}
	stable, err := CompileWorkspace(context.Background(), "project-a", "patients", workspace, snapshot, inputs)
	if err != nil || stable.ResolvedInputsDigest != first.ResolvedInputsDigest || stable.RecipeDigest != first.RecipeDigest {
		t.Fatalf("same resolved content was unstable: stable=%#v err=%v first=%#v", stable, err, first)
	}
}

func interpretationSnapshot() capability.Snapshot {
	return capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope", SchemaDigest: "schema", ShapeDigest: "shape"},
		capability.Policy{Route: capability.RoutePolicy{}, Projection: capability.ProjectionPolicy{Modes: []capability.ProjectionMode{capability.ProjectionScalar}}}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient"}}, nil,
		[]capability.Candidate{
			{ID: "candidate-id", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}},
			{ID: "candidate-family", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "name.family", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}},
		}, nil)
}

func interpretationWorkspace(revisionID string) ws.Workspace {
	return workspaceWithRevision(revisionID)
}

func workspaceWithRevision(revisionID string) ws.Workspace {
	return ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Interpretations"},
		Documents: []ws.Document{{Rows: ws.RecordsRowDefinition(), Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Columns: []ws.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}, Interpretation: &ws.FeatureInterpretation{Kind: ws.FeatureInterpretationPinned, Pinned: &ws.PinnedInterpretation{RevisionID: revisionID}}}}}},
		Tabs:      []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
}

func fieldDefinition(path string) interpretation.InterpretationFeatureDefinition {
	return interpretation.InterpretationFeatureDefinition{Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: path, ProjectionMode: "VALUE"}}}
}

func prepareInterpretation(t *testing.T, revision interpretation.InterpretationRevision) interpretation.InterpretationRevision {
	t.Helper()
	if revision.CreatedAt.IsZero() {
		revision.CreatedAt = time.Unix(1, 0).UTC()
	}
	prepared, err := interpretation.PrepareInterpretationRevision(revision)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}
