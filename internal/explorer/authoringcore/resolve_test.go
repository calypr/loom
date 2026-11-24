package authoringcore

import (
	"context"
	"strings"
	"testing"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/selection"
)

type fixedSelectionReader struct {
	value *selection.SelectionRevision
	err   error
}

func (r fixedSelectionReader) GetSelection(context.Context, string, string) (*selection.SelectionRevision, error) {
	return r.value, r.err
}

func populationSnapshot() capability.Snapshot {
	return capability.NewSnapshot(capability.SnapshotIdentity{
		Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope-digest",
		SchemaDigest: "schema", ShapeDigest: "shape",
	}, capability.Policy{Route: capability.RoutePolicy{Version: "route-v1", MaxHops: 4}}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}, nil, nil, nil)
}

func TestResolveWorkspacePopulationsRequiresCompleteAuthorizedSelection(t *testing.T) {
	snapshot := populationSnapshot()
	workspace := ws.Workspace{Documents: []ws.Document{{
		Output: ws.Output{ID: "patients"}, RootResourceType: "Patient",
		Population: &ws.Population{SelectionRevisionID: "selection-a"},
	}}}
	completedAt := time.Now().UTC()
	selected := &selection.SelectionRevision{
		ID: "selection-a", Project: "project-a", Generation: "generation-a", ResourceType: "Patient",
		Rule: selection.SelectionRule{Kind: selection.SelectionRuleExplicit}, Source: selection.SelectionSource{Kind: selection.SelectionSourceExplicit},
		ScopeDigest: "scope-digest", RuleDigest: "rule", MembershipDigest: "members", MemberCount: 1, Complete: true, CompletedAt: &completedAt,
	}
	inputs, err := ResolveWorkspacePopulations(context.Background(), "project-a", workspace, snapshot, "scope-digest", fixedSelectionReader{value: selected})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs.Populations) != 1 || inputs.Populations[0].SelectionRevisionID != selected.ID || inputs.Populations[0].MembershipDigest != selected.MembershipDigest {
		t.Fatalf("resolved populations = %#v", inputs.Populations)
	}

	selected.ScopeDigest = "other-scope"
	_, err = ResolveWorkspacePopulations(context.Background(), "project-a", workspace, snapshot, "scope-digest", fixedSelectionReader{value: selected})
	if err == nil || !strings.Contains(err.Error(), "authorization scope is stale") {
		t.Fatalf("stale selection error = %v", err)
	}
}
