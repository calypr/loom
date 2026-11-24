package arango

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestScopedSourcePlanMigrationAgainstArango(t *testing.T) {
	ctx, client, adapter, _ := draftCASDB(t)
	document := ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"},
		RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
		Rows: ws.RecordsRowDefinition(), ImportedRecipe: &ws.ImportedRecipeRef{Name: "pin", TranslationVersion: "v1", Digest: strings.Repeat("a", 64), Output: "patients"},
		ImportedSource: &ws.ImportedSourcePlan{Steps: []ws.ImportedSourceStep{
			{ID: "output", Kind: "ROOT", Title: "Output", Summary: "Output"},
			{ID: "dynamicColumns[0]", Kind: "DYNAMIC_COLUMNS", Title: "Identifiers", Summary: "Identifiers", Editable: true},
		}},
	}
	workspace := ws.Workspace{APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion,
		Explorer: ws.ExplorerMetadata{Title: "Legacy source"}, Documents: []ws.Document{document},
		Tabs: []ws.Tab{{ID: "patients", OutputID: "patients", Title: "Patients", Visible: true}},
	}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"summary":"Output","editable":false`), []byte(`"summary":"Output","enabled":true,"editable":false`), 1)
	raw = bytes.Replace(raw, []byte(`"summary":"Identifiers","editable":true`), []byte(`"summary":"Identifiers","enabled":false,"editable":true`), 1)
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	owner := workspaceMigrationSeed(t, ctx, adapter, "target", "Target", raw, digest, "snapshot")
	other := workspaceMigrationSeed(t, ctx, adapter, "other", "Other", raw, digest, "snapshot")
	dryRun, err := MigrateStoredSourcePlan(ctx, client, owner.Project, owner.ExplorerID, true)
	if err != nil || !dryRun.Changed || dryRun.DraftVersion != owner.DraftVersion+1 {
		t.Fatalf("dry run: %+v, %v", dryRun, err)
	}
	if draft, err := adapter.Get(ctx, owner.Project, owner.ExplorerID); err != nil || draft.DraftDigest != digest {
		t.Fatalf("dry run changed draft: %v", err)
	}
	report, err := MigrateStoredSourcePlan(ctx, client, owner.Project, owner.ExplorerID, false)
	if err != nil || report.DraftDigest != dryRun.DraftDigest || report.DraftVersion != dryRun.DraftVersion {
		t.Fatalf("apply: %+v, %v", report, err)
	}
	draft, err := adapter.Get(ctx, owner.Project, owner.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.DraftDigest != report.DraftDigest || bytes.Contains(draft.DraftConfig, []byte(`"enabled"`)) || draft.PreviousDraftRevisionID == "" {
		t.Fatal("draft was not atomically migrated with its prior revision")
	}
	history, err := adapter.GetDraftRevision(ctx, owner.Project, owner.ExplorerID, draft.PreviousDraftRevisionID)
	if err != nil || history.DraftDigest != digest {
		t.Fatalf("original draft history unavailable: %v", err)
	}
	if _, changed, err := ws.ConvertStoredSourcePlan(history.DraftConfig, history.DraftDigest); err != nil || !changed {
		t.Fatalf("original draft history failed digest verification: %v", err)
	}
	if untouched, err := adapter.Get(ctx, other.Project, other.ExplorerID); err != nil || untouched.DraftVersion != other.DraftVersion || untouched.DraftDigest != digest {
		t.Fatalf("migration changed an unrelated draft: %v", err)
	}
	if repeated, err := MigrateStoredSourcePlan(ctx, client, owner.Project, owner.ExplorerID, false); err != nil || repeated.Changed || repeated.DraftVersion != report.DraftVersion {
		t.Fatalf("repeated migration: %+v, %v", repeated, err)
	}
}
