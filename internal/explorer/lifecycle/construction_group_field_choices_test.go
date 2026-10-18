package lifecycle

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestGroupConsumedFieldBoundariesFollowExactCompiledOccurrencePrefix(t *testing.T) {
	expansion := &explorer.ReceiptConstructionGroupFieldSourceExpansion{
		OwnerOccurrenceID: "observation-owner",
		OwnerResourceType: "Observation",
		Selector:          "component[].extension[]",
		Ancestors: []explorer.ReceiptConstructionGroupFieldSourceAncestor{
			{StepIndex: 0, BoundarySelector: "component[]", ResourceType: "ObservationComponent"},
		},
	}
	ancestor := explorer.ReceiptConstructionGroupFieldOwner{
		Role: "ANCESTOR", OccurrenceID: "observation-owner", OwnerResourceType: "Observation",
		ResourceType: "ObservationComponent", BoundarySelector: "component[]", StepIndex: 0,
	}
	got, ok := groupConsumedFieldBoundaries(expansion, ancestor)
	if !ok || !reflect.DeepEqual(got, []string{"component[]"}) {
		t.Fatalf("consumed ancestor prefix = %#v, %t, want exact component[] boundary", got, ok)
	}

	staleStep := ancestor
	staleStep.StepIndex = 1
	if got, ok := groupConsumedFieldBoundaries(expansion, staleStep); ok {
		t.Fatalf("accepted stale ancestor step index with same apparent role: %#v", got)
	}

	currentItem := explorer.ReceiptConstructionGroupFieldOwner{
		Role: "CURRENT_ITEM", OccurrenceID: "observation-owner", OwnerResourceType: "Observation",
		ResourceType: "Extension", BoundarySelector: "component[].extension[]",
	}
	got, ok = groupConsumedFieldBoundaries(expansion, currentItem)
	if !ok || !reflect.DeepEqual(got, []string{"component[]", "component[].extension[]"}) {
		t.Fatalf("consumed current-item prefix = %#v, %t, want ordered ancestor plus active item selector", got, ok)
	}

	wrongOwner := currentItem
	wrongOwner.OccurrenceID = "other-observation"
	if got, ok := groupConsumedFieldBoundaries(expansion, wrongOwner); ok {
		t.Fatalf("accepted a different owner occurrence: %#v", got)
	}
}

func TestResolveConstructionGroupFieldSourcesRejectsChoiceFromChangedSourceShape(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "snapshot-a", scope)
	snapshot.Nodes = []capability.Node{{
		ID: "observation-node", ResourceType: "Observation", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true,
	}}
	snapshot.Candidates = []capability.Candidate{{
		ID: "status-candidate", NodeID: "observation-node", ResourceType: "Observation",
		FieldPath: "status", Label: "Status", LogicalType: "string", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true,
	}}
	oldChoice, err := capability.NewGroupFieldConstructionChoice("snapshot-a", nil, capability.GroupFieldChoiceSource{
		SourceStageID: "source_projection", SourceShapeID: "shape-old", SourceIdentityColumn: "_key",
		SourceRole: "ROOT", OwnerOccurrenceID: authoringv2.RootOccurrenceID, OwnerResourceType: "Observation",
		OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation", CandidateID: "status-candidate",
		NodeID: "observation-node", CanonicalPath: "status", FHIRType: "code", Cardinality: "ONE", Shape: "SCALAR", ValueKind: "SCALAR",
	}, snapshot.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	base := constructionBase{
		snapshot:   snapshot,
		authorized: AuthorizedCapability{Snapshot: snapshot, Scope: scope},
		document: authoringv2.Document{
			RootResourceType: "Observation",
			Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"},
		},
		stages: []explorer.ReceiptConstructionStage{{
			ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
			GroupFieldSourceContext: &explorer.ReceiptConstructionGroupFieldSourceContext{
				SourceStageID: recipe.ConstructionSourceProjectionID, SourceShapeID: "shape-new", SourceIdentityColumn: "_key",
				FieldOwners: []explorer.ReceiptConstructionGroupFieldOwner{{
					Role: "ROOT", OccurrenceID: authoringv2.RootOccurrenceID,
					OwnerResourceType: "Observation", ResourceType: "Observation",
				}},
			},
		}},
	}
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "group", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
			ConstructionID: "group", MissingKeyPolicy: authoringv2.ConstructionGroupMissingKeyGroup,
			Keys: []authoringv2.ConstructionGroupKey{{
				FieldSource:    &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: oldChoice.ChoiceID},
				OutputColumnID: "status-group",
			}},
			Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "row-count"}},
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: "status-group", Name: "status_group", Label: "Status", Type: "string"},
			{ID: "row-count", Name: "row_count", Label: "Rows", Type: "integer"},
		},
	}}}
	service := &Service{}
	_, err = service.resolveConstructionGroupFieldSources(context.Background(), base, "observations", candidate)
	if got := lifecycleErrorCode(err); got != "STALE_GROUP_FIELD_CHOICE" {
		t.Fatalf("choice from old source shape error = %v (code %q), want stale-choice rejection", err, got)
	}
}
