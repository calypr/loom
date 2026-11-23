package arango

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/explorer/arango/workspacedb"
)

func TestWorkspaceAuditCountsTableShapeDraftsAndRevisions(t *testing.T) {
	workspace := json.RawMessage("{\"apiVersion\":\"loom.calypr.org/explorer-authoring/v2\",\"kind\":\"ExplorerBuilderWorkspace\",\"semanticsVersion\":9,\"explorer\":{\"title\":\"Legacy\"},\"documents\":[{\"kind\":\"ExplorerBuilderDocument\",\"tableShape\":{\"kind\":\"PIVOT\"}},{\"kind\":\"ExplorerBuilderDocument\",\"tableShape\":null}]}")
	report := workspacedb.WorkspaceAuditReport{}
	for _, collection := range []string{ExplorersCollection, DraftRevisionsCollection, RevisionsCollection} {
		field := "draftConfig"
		if collection == RevisionsCollection {
			field = "config"
		}
		document, _ := json.Marshal(map[string]any{"_key": collection, field: workspace})
		if err := report.ObserveWorkspaceAuditDocument(collection, document); err != nil {
			t.Fatal(err)
		}
	}
	if report.AuthoritativeWorkspaces != 3 || report.LegacyTableShapeDocuments != 3 {
		t.Fatalf("authoritative TableShape counts = workspaces:%d documents:%d, want 3/3", report.AuthoritativeWorkspaces, report.LegacyTableShapeDocuments)
	}
	for _, entry := range report.Entries {
		if entry.TableShapeDocuments != 1 || !containsAuditString(entry.Features, "legacy-table-shape") {
			t.Fatalf("TableShape inventory entry = %+v", entry)
		}
	}
}

func TestWorkspaceAuditSeparatesDiscoveredTableShapeCopies(t *testing.T) {
	workspace := json.RawMessage("{\"apiVersion\":\"loom.calypr.org/explorer-authoring/v2\",\"kind\":\"ExplorerBuilderWorkspace\",\"semanticsVersion\":9,\"explorer\":{\"title\":\"Copy\"},\"documents\":[{\"kind\":\"ExplorerBuilderDocument\",\"tableShape\":{\"kind\":\"PIVOT\"}}]}")
	document, _ := json.Marshal(map[string]any{"_key": "other", "payload": map[string]any{"workspace": workspace}})
	report := workspacedb.WorkspaceAuditReport{}
	if err := report.ObserveWorkspaceAuditDocument("unknown_collection", document); err != nil {
		t.Fatal(err)
	}
	if report.LegacyTableShapeDocuments != 0 || report.DiscoveredTableShapeDocuments != 1 || report.DiscoveryCandidates != 1 {
		t.Fatalf("discovered TableShape counts = %+v", report)
	}
}
