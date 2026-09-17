package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestAssessAndApplyRowChangeThroughPublicAPI(t *testing.T) {
	base := testAuthoringV2CapabilitySnapshot()
	snapshot := capability.NewSnapshot(base.Identity, base.Policy, capability.StatusReady, true, false,
		[]capability.Node{
			{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true},
			{ID: "n_encounter", ResourceType: "Encounter", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true},
		},
		[]capability.Edge{
			{ID: "patient-encounter", FromNodeID: "n_patient", ToNodeID: "n_encounter", Label: "encounters", SourceResourceType: "Patient", TargetResourceType: "Encounter", ObservedEdgeCount: 1},
			{ID: "encounter-patient", FromNodeID: "n_encounter", ToNodeID: "n_patient", Label: "patient", SourceResourceType: "Encounter", TargetResourceType: "Patient", ObservedEdgeCount: 1},
		}, nil, nil)
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{{
			Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
			Route:   authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient", Children: []authoringv2.RouteNode{{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters"}}},
			Columns: []authoringv2.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
		}},
		Tabs: []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := newTestExplorerStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: explorer.ManagementInteractive, DraftConfig: draft, DraftVersion: 1, DraftDigest: digest}); err != nil {
		t.Fatal(err)
	}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	config := lifecycle.Config{Capability: lifecycle.CapabilityResolver{
		Token:   func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
		Catalog: authoringV2Catalog,
	}}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, domain, config)

	assessmentBody := fmt.Sprintf(`{"snapshotToken":%q,"draftVersion":1,"draftDigest":%q,"outputId":"patients","rootNodeId":"n_encounter"}`, snapshot.Token, digest)
	assessed := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/patients/authoring/v2/row-change", assessmentBody)
	if assessed.StatusCode != http.StatusOK {
		t.Fatalf("assessment status=%d body=%s", assessed.StatusCode, assessed.Body)
	}
	var assessment loomapi.RowChangeAssessmentResponse
	if err := json.Unmarshal([]byte(assessed.Body), &assessment); err != nil {
		t.Fatal(err)
	}
	if assessment.Status != loomapi.RowChangeAssessmentResponseStatusREADY || assessment.Proposal == nil || len(assessment.PreservedFeatureKeys) != 1 || assessment.PreservedFeatureKeys[0] != "patient_id" {
		t.Fatalf("assessment=%#v", assessment)
	}
	proposal, err := json.Marshal(assessment.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	commandBody := fmt.Sprintf(`{"commandId":"apply-row-change","semanticsVersion":3,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_TABLE_ROOT_REBASE","rowChange":%s}]}`, snapshot.Token, digest, proposal)
	applied := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/patients/authoring/v2/commands", commandBody)
	if applied.StatusCode != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", applied.StatusCode, applied.Body)
	}
	var response authoringv2.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(applied.Body), &response); err != nil {
		t.Fatal(err)
	}
	document := response.Workspace.Documents[0]
	if response.DraftVersion != 2 || document.RootResourceType != "Encounter" || len(document.Columns) != 1 || document.Columns[0].Column != "patient_id" || document.Columns[0].OccurrenceID != "encounter" {
		t.Fatalf("applied response=%#v", response)
	}

	stale := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/patients/authoring/v2/row-change", assessmentBody)
	if stale.StatusCode != http.StatusConflict {
		t.Fatalf("stale assessment status=%d body=%s", stale.StatusCode, stale.Body)
	}
}
