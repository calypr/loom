package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer"
)

func TestNormalizeDocumentMapsZeroHopCandidateToBase(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	document := explorer.ExplorerBuilderDocumentV1{
		Kind:       explorer.ExplorerBuilderV1Kind,
		Output:     explorer.ExplorerOutputIdentityV1{ID: "patient"},
		BaseNodeID: "n_base",
		RowNodeID:  "n_base",
		CandidateIDs: []string{
			"s_base",
		},
	}
	result, err := ResolveAuthoringBundle(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) {
		return snapshot, nil
	}, ExplorerAuthoringV1CompileRequest{
		Bundle:        authoringTestBundleWithoutCandidateOccurrences(document),
		SnapshotToken: snapshot.Token,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Bundle.AuthoringDocuments()[0].CandidateOccurrences
	if len(got) != 1 || got[0].CandidateID != "s_base" || got[0].OccurrenceID != "base" {
		t.Fatalf("candidate occurrences=%#v", got)
	}
}

func TestNormalizeDocumentMapsOneHopCandidateToOnlyMatchingOccurrence(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	document := explorer.ExplorerBuilderDocumentV1{
		Kind:         explorer.ExplorerBuilderV1Kind,
		Output:       explorer.ExplorerOutputIdentityV1{ID: "observations"},
		BaseNodeID:   "n_base",
		RowNodeID:    "n_child",
		RouteEdgeIDs: []string{"e_forward"},
		CandidateIDs: []string{"s_child"},
	}
	result, err := ResolveAuthoringBundle(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) {
		return snapshot, nil
	}, ExplorerAuthoringV1CompileRequest{
		Bundle:        authoringTestBundleWithoutCandidateOccurrences(document),
		SnapshotToken: snapshot.Token,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Bundle.AuthoringDocuments()[0]
	if len(got.CandidateOccurrences) != 1 || got.CandidateOccurrences[0].CandidateID != "s_child" || got.CandidateOccurrences[0].OccurrenceID == "base" {
		t.Fatalf("normalized one-hop document=%#v", got)
	}
	if got.CandidateOccurrences[0].OccurrenceID != got.RouteOccurrences[1].ID {
		t.Fatalf("candidate occurrence=%#v route occurrences=%#v", got.CandidateOccurrences, got.RouteOccurrences)
	}
}

func TestNormalizeDocumentRequiresOccurrenceForRepeatedResource(t *testing.T) {
	catalog := explorer.Catalog{
		Nodes: []explorer.CatalogNode{
			{ID: "n_base", ResourceType: "Patient"},
			{ID: "n_child", ResourceType: "Observation"},
		},
		Edges: []explorer.CatalogEdge{
			{ID: "e_forward", FromNodeID: "n_base", ToNodeID: "n_child"},
			{ID: "e_back", FromNodeID: "n_child", ToNodeID: "n_base"},
		},
		Selections: map[string]explorer.CatalogSelection{
			"s_base": {ID: "s_base", NodeID: "n_base", Select: "id"},
		},
	}
	document := explorer.ExplorerBuilderDocumentV1{
		Kind:         explorer.ExplorerBuilderV1Kind,
		Output:       explorer.ExplorerOutputIdentityV1{ID: "patients"},
		BaseNodeID:   "n_base",
		RowNodeID:    "n_base",
		RouteEdgeIDs: []string{"e_forward", "e_back"},
		CandidateIDs: []string{"s_base"},
	}
	snapshot, err := explorer.NewCatalogSnapshot("project-a", "generation-a", "scope-a", catalog, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ResolveAuthoringBundle(context.Background(), nil, func(context.Context, string, string, string) (explorer.CatalogSnapshot, error) {
		return snapshot, nil
	}, ExplorerAuthoringV1CompileRequest{
		Bundle:        authoringTestBundleWithoutCandidateOccurrences(document),
		SnapshotToken: snapshot.Token,
	})
	if err == nil || !strings.Contains(err.Error(), "CANDIDATE_OCCURRENCE_REQUIRED") {
		t.Fatalf("ambiguous candidate error=%v", err)
	}
	var authoringErr *explorer.AuthoringError
	if !errors.As(err, &authoringErr) || len(authoringErr.Diagnostic.Details["validOccurrences"].([]string)) != 2 {
		t.Fatalf("ambiguous candidate details=%#v", err)
	}
}

func TestNormalizeDocumentRejectsStaleAndMismatchedCandidates(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	baseBinding := explorer.ExplorerResolvedBindingV1{
		BaseNodeID: "n_base",
		RouteOccurrences: []explorer.ExplorerResolvedOccurrenceV1{
			{OccurrenceID: "base", Index: 0, NodeID: "n_base"},
		},
	}
	unknown := explorer.ExplorerBuilderDocumentV1{CandidateIDs: []string{"unknown"}}
	if _, err := normalizeDocument(unknown, snapshot.Catalog, baseBinding); err == nil || !strings.Contains(err.Error(), "STALE_CANDIDATE_ID") {
		t.Fatalf("unknown candidate error=%v", err)
	}
	mismatch := explorer.ExplorerBuilderDocumentV1{CandidateIDs: []string{"s_child"}, BaseNodeID: "n_base"}
	if _, err := normalizeDocument(mismatch, snapshot.Catalog, baseBinding); err == nil || !strings.Contains(err.Error(), "SELECTION_NODE_MISMATCH") {
		t.Fatalf("mismatched candidate error=%v", err)
	}
}

func TestNormalizeDocumentDropsDownstreamSelectionsAfterRouteTruncation(t *testing.T) {
	catalog := explorer.Catalog{
		Nodes: []explorer.CatalogNode{
			{ID: "n_base", ResourceType: "Patient"},
			{ID: "n_child", ResourceType: "Observation"},
			{ID: "n_grandchild", ResourceType: "Task"},
		},
		Edges: []explorer.CatalogEdge{{ID: "e_forward", FromNodeID: "n_base", ToNodeID: "n_child"}},
		Selections: map[string]explorer.CatalogSelection{
			"s_child":      {ID: "s_child", NodeID: "n_child", Select: "status"},
			"s_grandchild": {ID: "s_grandchild", NodeID: "n_grandchild", Select: "status"},
		},
	}
	document := explorer.ExplorerBuilderDocumentV1{
		Kind:         explorer.ExplorerBuilderV1Kind,
		Output:       explorer.ExplorerOutputIdentityV1{ID: "observations"},
		BaseNodeID:   "n_base",
		RowNodeID:    "n_child",
		RouteEdgeIDs: []string{"e_forward"},
		RouteOccurrences: []explorer.ExplorerRouteOccurrenceV1{
			{ID: "base", Index: 0, NodeID: "n_base"},
			{ID: "child", Index: 1, NodeID: "n_child"},
			{ID: "grandchild", Index: 2, NodeID: "n_grandchild"},
		},
		CandidateIDs: []string{"s_child", "s_grandchild"},
		CandidateOccurrences: []explorer.ExplorerCandidateOccurrenceV1{
			{CandidateID: "s_child", OccurrenceID: "child"},
			{CandidateID: "s_grandchild", OccurrenceID: "grandchild"},
		},
	}
	structural, err := normalizeStructuralAuthoringRoutes([]explorer.ExplorerBuilderDocumentV1{document}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := normalizeAuthoringDocuments(structural, catalog)
	if err != nil {
		t.Fatal(err)
	}
	got := normalized[0]
	if len(got.CandidateIDs) != 1 || got.CandidateIDs[0] != "s_child" || len(got.CandidateOccurrences) != 1 || got.CandidateOccurrences[0].OccurrenceID != "child" {
		t.Fatalf("truncated route retained downstream selection: %#v", got)
	}
}

func TestNormalizeDocumentIsIdempotent(t *testing.T) {
	snapshot := authoringTestSnapshot(t)
	document := explorer.ExplorerBuilderDocumentV1{
		Kind:       explorer.ExplorerBuilderV1Kind,
		Output:     explorer.ExplorerOutputIdentityV1{ID: "patient"},
		BaseNodeID: "n_base",
		RowNodeID:  "n_base",
		CandidateIDs: []string{
			"s_base",
		},
	}
	firstStructural, err := normalizeStructuralAuthoringRoutes([]explorer.ExplorerBuilderDocumentV1{document}, snapshot.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	first, err := normalizeAuthoringDocuments(firstStructural, snapshot.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	secondStructural, err := normalizeStructuralAuthoringRoutes(first, snapshot.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	second, err := normalizeAuthoringDocuments(secondStructural, snapshot.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	firstBundle := authoringTestBundleWithoutCandidateOccurrences(first[0])
	firstBundle.Documents = first
	firstBundle.Document = explorer.ExplorerBuilderDocumentV1{}
	secondBundle := authoringTestBundleWithoutCandidateOccurrences(second[0])
	secondBundle.Documents = second
	secondBundle.Document = explorer.ExplorerBuilderDocumentV1{}
	firstDigest, err := firstBundle.DocumentDigest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := secondBundle.DocumentDigest()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("normalization is not idempotent: first=%s second=%s", firstDigest, secondDigest)
	}
}

func authoringTestBundleWithoutCandidateOccurrences(document explorer.ExplorerBuilderDocumentV1) explorer.ExplorerAuthoringBundleV1 {
	return explorer.ExplorerAuthoringBundleV1{
		APIVersion: explorer.ExplorerAuthoringV1APIVersion,
		Kind:       explorer.ExplorerAuthoringV1Kind,
		Project:    "project-a",
		ExplorerID: "custom",
		Documents:  []explorer.ExplorerBuilderDocumentV1{document},
	}
}
