package explorerapi

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	explorerconfig "github.com/calypr/loom/internal/explorer/config"
	"github.com/calypr/loom/internal/explorer/explicitgroup"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	receiptapi "github.com/calypr/loom/internal/explorer/receipt"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/testkit"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
	"github.com/gofiber/fiber/v3"
)

//go:embed testdata/patient_one_pre_one_saved_document.json
var patientOnePreOneDocumentFS embed.FS

func TestPatientOneChoiceProposalRouteRefusesOneAndRepairsWithAll(t *testing.T) {
	const (
		project    = "loom_dev_verify_mv0jn3yb-f3ac032"
		explorerID = "verify-yb-f3ac032-patient-one-disagreement"
		commandID  = "c62df42b-c65f-412a-81fa-5b00ea56a4c6"
	)

	rawDocument, err := patientOnePreOneDocumentFS.ReadFile("testdata/patient_one_pre_one_saved_document.json")
	if err != nil {
		t.Fatal(err)
	}
	var document ws.Document
	if err := json.Unmarshal(rawDocument, &document); err != nil {
		t.Fatal(err)
	}
	if document.Output.ID != "out_c65a00f563a3e700edb027a8" || document.RootResourceType != "Patient" ||
		document.Construction == nil || len(document.Construction.Steps) != 1 ||
		document.Construction.Steps[0].Operation.Group == nil || len(document.Construction.Steps[0].Operation.Group.Keys) != 0 {
		t.Fatalf("retained fixture no longer contains the captured empty-key Patient Group: %#v", document)
	}

	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	workspace.Explorer.Title = document.Output.Title
	workspace.Documents = []ws.Document{document}
	workspace.Tabs = []ws.Tab{{
		ID: "patient-one-captured-tab", Title: document.Output.Title, OutputID: document.Output.ID, Order: 0, Visible: true,
	}}
	workspace = workspace.NormalizePresentationOrders()
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonicalize retained Group document: %v", err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatalf("digest retained Group workspace: %v", err)
	}

	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: projectid.Canonical(project), Generation: "devloop-v1", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(readScope),
			SchemaDigest: "patient-one-schema", ResourceInventoryDigest: "patient-one-inventory",
			RelationshipDigest: "patient-one-relationships", FieldDigest: "patient-one-fields", ShapeDigest: "patient-one-shape",
			ProtocolVersion: capabilityresolver.ProtocolVersion, CompilerVersion: capabilityresolver.CompilerVersion,
			TraversalPolicyVersion: capabilityresolver.TraversalPolicyVersion, ProjectionPolicyVersion: capabilityresolver.ProjectionPolicyVersion,
		},
		capability.Policy{
			Route:      capability.RoutePolicy{Version: capabilityresolver.TraversalPolicyVersion, AllowsRepeatedEdges: true, AllowsSelfLoops: true},
			Projection: capability.ProjectionPolicy{Version: capabilityresolver.ProjectionPolicyVersion},
		},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 2, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil,
		[]capability.Candidate{{
			ID: "c_patient_id", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "id", Label: "Patient ID",
			LogicalType: "string", Cardinality: "OPTIONAL_ONE",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst},
			SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true,
		}},
		nil,
	)

	catalog := ws.CatalogFromCapability(snapshot, explorerID)
	var choiceID string
	for _, candidate := range catalog.Candidates {
		if candidate.ID == "c_patient_id" && candidate.FieldPath == "id" && candidate.ConstructionChoice != nil {
			choiceID = candidate.ConstructionChoice.ChoiceID
			break
		}
	}
	if choiceID == "" {
		t.Fatal("production chooser omitted the Patient.id VALUE choice")
	}

	baseStore := newTestExplorerStore()
	store := &jsonRoundTripPatientOneReceiptStore{testExplorerStore: baseStore}
	if _, err := baseStore.Create(context.Background(), explorer.Explorer{
		Project: project, ExplorerID: explorerID, Title: document.Output.Title,
		DraftConfig: draft, DraftVersion: 3, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	rawFixture, err := os.ReadFile(testkit.FixturePath(t, "devloop-fixture", "Patient.ndjson"))
	if err != nil {
		t.Fatalf("read retained Patient fixture: %v", err)
	}
	patientIDs := make([]string, 0, 2)
	for _, line := range strings.Split(strings.TrimSpace(string(rawFixture)), "\n") {
		var patient struct {
			ResourceType string `json:"resourceType"`
			ID           string `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &patient); err != nil {
			t.Fatalf("decode retained Patient fixture row: %v", err)
		}
		if patient.ResourceType != "Patient" || patient.ID == "" {
			t.Fatalf("retained raw fixture row = %#v, want a Patient with an exact ID", patient)
		}
		patientIDs = append(patientIDs, patient.ID)
	}
	if len(patientIDs) != 2 || patientIDs[0] != "dev-patient-001" || patientIDs[1] != "dev-patient-002" {
		t.Fatalf("retained raw fixture Patient IDs = %#v, want dev-patient-001 and dev-patient-002", patientIDs)
	}
	candidatePublicColumn := ""
	previewQueryCalls := 0
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{}, ScopeDigest: reciperuntime.ScopeDigest,

		QueryRows: func(_ context.Context, query string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			previewQueryCalls++
			if !strings.Contains(query, "root.payload.id") {
				return fmt.Errorf("Patient.id chooser query did not read the raw source id: %s", query)
			}
			if strings.Contains(query, "CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES") {
				if len(patientIDs) != 2 || patientIDs[0] == patientIDs[1] {
					t.Fatalf("ONE query was evaluated without the two distinct raw Patient IDs: %#v", patientIDs)
				}
				return dataframeerrors.NewError(dataframeerrors.CodeConstructionRowValueMultipleValues, "Patient.id has multiple distinct values")
			}
			if !strings.Contains(query, "SORTED_UNIQUE(") {
				return fmt.Errorf("preview query did not lower Group row values: %s", query)
			}
			if candidatePublicColumn == "" {
				return fmt.Errorf("candidate Patient.id output column was not captured from the production receipt")
			}
			return visit(map[string]any{
				"__loom_row_id": "patient-one-group-row",
				"row_count":     len(patientIDs),
				candidatePublicColumn: []string{
					patientIDs[0], patientIDs[1],
				},
			})
		},
		RootPageRows: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	var previewResolutionErr error
	config := lifecycle.Config{
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			Catalog: ws.CatalogFromCapability,
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			receipt, err := receiptapi.Compile(ctx, request, nil, recipeEngine, service, nil, nil)
			if err != nil {
				return nil, err
			}
			for _, emitted := range receipt.EmittedColumns {
				if emitted.OutputID == document.Output.ID && emitted.ConstructionID != "" && emitted.Label == "Patient ID" && emitted.PublicColumn != "" {
					candidatePublicColumn = emitted.PublicColumn
					break
				}
			}
			return receipt, nil
		},
		PreviewReceipt: func(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			resolved, err := receiptapi.CompileValidatedResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				previewResolutionErr = err
				return dataframeexecution.PreviewSummary{}, err
			}
			return recipeEngine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{
				Output: bindings.OutputNames[0], Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity,
			}, visit)
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	proposalPath := fmt.Sprintf("/api/v1/projects/%s/explorers/%s/authoring/v2/construction-choice-proposals", project, explorerID)
	postChoice := func(policy, id string) testHTTPResponse {
		t.Helper()
		rowValuePolicy := loomapi.ConstructionChoiceProposalSelectionRowValuePolicy(policy)
		body, err := json.Marshal(loomapi.ConstructionChoiceProposalRequest{
			CommandId: id, SnapshotToken: snapshot.Token, ExpectedDraftVersion: 3,
			ExpectedDraftDigest: digest, OutputId: document.Output.ID,
			ConstructionChoices: []loomapi.ConstructionChoiceProposalSelection{{
				ChoiceId: choiceID, Form: loomapi.ConstructionChoiceProposalSelectionForm("VALUE"), RowValuePolicy: &rowValuePolicy,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return requestJSON(t, app, http.MethodPost, proposalPath, string(body))
	}
	var payload struct {
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
	}
	oneResponse := postChoice("ONE", commandID)
	if oneResponse.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("Patient.id VALUE/ONE status=%d body=%s; receipt validation error=%v", oneResponse.StatusCode, oneResponse.Body, previewResolutionErr)
	}
	if err := json.Unmarshal([]byte(oneResponse.Body), &payload); err != nil {
		t.Fatalf("decode ONE rejection: %v; body=%s", err, oneResponse.Body)
	}
	if len(payload.Diagnostics) == 0 || payload.Diagnostics[0].Code != string(dataframeerrors.CodeConstructionRowValueMultipleValues) {
		t.Fatalf("ONE diagnostic = %#v, want %s", payload.Diagnostics, dataframeerrors.CodeConstructionRowValueMultipleValues)
	}
	if previewResolutionErr != nil || previewQueryCalls != 1 {
		t.Fatalf("ONE receipt validation/query calls: resolution error=%v query calls=%d", previewResolutionErr, previewQueryCalls)
	}
	storedAfterONE, err := service.Get(context.Background(), project, explorerID)
	if err != nil {
		t.Fatal(err)
	}
	if storedAfterONE.DraftVersion != 3 || storedAfterONE.DraftDigest != digest {
		t.Fatalf("ONE refusal changed the saved Group draft: version=%d digest=%s", storedAfterONE.DraftVersion, storedAfterONE.DraftDigest)
	}

	allResponse := postChoice("ALL", "c62df42b-c65f-412a-81fa-5b00ea56a4c6-all")
	if allResponse.StatusCode != http.StatusOK {
		t.Fatalf("Patient.id VALUE/ALL status=%d body=%s; receipt validation error=%v", allResponse.StatusCode, allResponse.Body, previewResolutionErr)
	}
	var allProposal loomapi.ConstructionChoiceProposalResponse
	if err := json.Unmarshal([]byte(allResponse.Body), &allProposal); err != nil {
		t.Fatalf("decode ALL proposal: %v; body=%s", err, allResponse.Body)
	}
	if previewResolutionErr != nil || previewQueryCalls != 2 || allProposal.PreviewStatus != loomapi.ConstructionChoiceProposalResponsePreviewStatusREADY ||
		allProposal.Preview.OutputId != document.Output.ID || allProposal.Preview.RowCount != 1 || len(allProposal.Preview.Rows) != 1 {
		t.Fatalf("ALL proposal/preview = %#v; query calls=%d validation error=%v", allProposal, previewQueryCalls, previewResolutionErr)
	}
	values, ok := allProposal.Preview.Rows[0][candidatePublicColumn].([]any)
	if !ok || len(values) != 2 || values[0] != patientIDs[0] || values[1] != patientIDs[1] {
		t.Fatalf("ALL Patient.id preview values = %#v, want exact IDs %#v", allProposal.Preview.Rows[0][candidatePublicColumn], patientIDs)
	}
	if len(baseStore.Receipts) != 2 {
		t.Fatalf("candidate receipt count=%d, want one production receipt for each ONE and ALL preview", len(baseStore.Receipts))
	}
}

type jsonRoundTripPatientOneReceiptStore struct {
	*testExplorerStore
}

func (s *jsonRoundTripPatientOneReceiptStore) InsertCompilationReceipt(ctx context.Context, receipt receiptmodel.CompilationReceipt) (*receiptmodel.CompilationReceipt, error) {
	raw, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	document["_key"] = receipt.ID
	raw, err = json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var stored receiptmodel.CompilationReceipt
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, err
	}
	return s.testExplorerStore.InsertCompilationReceipt(ctx, stored)
}

func TestAssessAndApplyRowChangeThroughPublicAPI(t *testing.T) {
	base := testAuthoringV2CapabilitySnapshot()
	snapshot := capability.NewSnapshot(base.Identity, base.Policy, capability.StatusReady, true, false,
		[]capability.Node{
			{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true},
			{ID: "n_encounter", ResourceType: "Encounter", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true},
			{ID: "n_report", ResourceType: "DiagnosticReport", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true},
		},
		[]capability.Edge{
			{ID: "patient-encounter", FromNodeID: "n_patient", ToNodeID: "n_encounter", Label: "encounters", SourceResourceType: "Patient", TargetResourceType: "Encounter", ObservedEdgeCount: 1},
			{ID: "encounter-patient", FromNodeID: "n_encounter", ToNodeID: "n_patient", Label: "patient", SourceResourceType: "Encounter", TargetResourceType: "Patient", ObservedEdgeCount: 1},
		}, []capability.Candidate{{ID: "c_report_id", NodeID: "n_report", ResourceType: "DiagnosticReport", FieldPath: "id", Label: "Report ID", LogicalType: "string", Cardinality: "OPTIONAL_ONE", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}}}, nil)
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion, Explorer: ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{{
			Kind: ws.Kind, Rows: ws.RecordsRowDefinition(), Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
			Route:   ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient", Children: []ws.RouteNode{{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters"}}},
			Columns: []ws.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
		}},
		Tabs: []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
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
	if _, err := store.Create(context.Background(), explorer.Explorer{Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: viewer.ManagementInteractive, DraftConfig: draft, DraftVersion: 1, DraftDigest: digest}); err != nil {
		t.Fatal(err)
	}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	config := lifecycle.Config{Capability: ports.CapabilityResolver{
		Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
		ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		},
		ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		},
		Catalog: ws.CatalogFromCapability,
	},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return explorerfixtures.PersistTestNativeReceipt(ctx, t, domain, request, snapshot)
		},
		PreviewReceipt: func(_ context.Context, _ *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if err := visit(map[string]any{"__loom_row_id": "row-1", "c_patient": "encounter-1"}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"c_patient"}, RowCount: 1, Complete: true}, nil
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
	assertRowChangeWireArrays(t, []byte(assessed.Body), true)
	if assessment.Status != loomapi.RowChangeAssessmentResponseStatusREADY || assessment.Mode == nil || string(*assessment.Mode) != string(authoringv2.RowChangeModePreserve) || assessment.Proposal == nil || assessment.CandidateReceiptId == nil || *assessment.CandidateReceiptId == "" || len(assessment.PreservedFeatureKeys) != 1 || assessment.PreservedFeatureKeys[0] != "patient_id" {
		t.Fatalf("assessment=%#v", assessment)
	}
	if assessment.CandidateDocument == nil || assessment.CandidateNetwork == nil || assessment.CandidateRowIdentity == nil || assessment.CandidateDocument.RootResourceType != "Encounter" || assessment.CandidateDocument.Columns[0].OccurrenceId != "encounter" || assessment.CandidateNetwork.OutputId != "patients" || len(assessment.CandidateNetwork.Bindings) != 0 || assessment.CandidateDocument.Columns[0].ColumnId != nil || assessment.CandidateRowIdentity.Stages[0].Identity.ResourceType == nil || *assessment.CandidateRowIdentity.Stages[0].Identity.ResourceType != "Encounter" {
		t.Fatalf("READY root response lost its exact candidate document/network/identity: %s", assessed.Body)
	}
	unknownMode := fmt.Sprintf(`{"mode":"MERGE","snapshotToken":%q,"draftVersion":1,"draftDigest":%q,"outputId":"patients","rootNodeId":"n_encounter"}`, snapshot.Token, digest)
	unknownModeResponse := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/patients/authoring/v2/row-change", unknownMode)
	if unknownModeResponse.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown row-change mode status=%d body=%s", unknownModeResponse.StatusCode, unknownModeResponse.Body)
	}
	blockedReplacementBody := fmt.Sprintf(`{"mode":"REPLACE","snapshotToken":%q,"draftVersion":1,"draftDigest":%q,"outputId":"patients","rootNodeId":"n_encounter"}`, snapshot.Token, digest)
	blockedReplacementResponse := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/patients/authoring/v2/row-change", blockedReplacementBody)
	if blockedReplacementResponse.StatusCode != http.StatusOK {
		t.Fatalf("blocked replacement assessment status=%d body=%s", blockedReplacementResponse.StatusCode, blockedReplacementResponse.Body)
	}
	var blockedReplacement loomapi.RowChangeAssessmentResponse
	if err := json.Unmarshal([]byte(blockedReplacementResponse.Body), &blockedReplacement); err != nil {
		t.Fatal(err)
	}
	if blockedReplacement.Status != loomapi.RowChangeAssessmentResponseStatusBLOCKED || blockedReplacement.Mode == nil || string(*blockedReplacement.Mode) != string(authoringv2.RowChangeModeReplace) {
		t.Fatalf("blocked replacement assessment omitted mode: %#v", blockedReplacement)
	}
	var blockedWire map[string]json.RawMessage
	if err := json.Unmarshal([]byte(blockedReplacementResponse.Body), &blockedWire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"candidateDocument", "candidateNetwork", "candidateRowIdentity"} {
		if _, ok := blockedWire[key]; ok {
			t.Fatalf("BLOCKED root assessment exposed %s", key)
		}
	}
	noChangeBody := fmt.Sprintf(`{"snapshotToken":%q,"draftVersion":1,"draftDigest":%q,"outputId":"patients","rootNodeId":"n_patient"}`, snapshot.Token, digest)
	noChange := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/patients/authoring/v2/row-change", noChangeBody)
	if noChange.StatusCode != http.StatusOK {
		t.Fatalf("no-change HTTP %d: %s", noChange.StatusCode, noChange.Body)
	}
	var noChangeWire map[string]json.RawMessage
	if err := json.Unmarshal([]byte(noChange.Body), &noChangeWire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"candidateDocument", "candidateNetwork", "candidateRowIdentity"} {
		if _, ok := noChangeWire[key]; ok {
			t.Fatalf("NO_CHANGE root assessment exposed %s", key)
		}
	}
	previewBody := fmt.Sprintf(`{"receiptId":%q,"outputId":"patients"}`, *assessment.CandidateReceiptId)
	previewed := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/patients/authoring/v2/preview", previewBody)
	if previewed.StatusCode != http.StatusOK {
		t.Fatalf("candidate preview status=%d body=%s", previewed.StatusCode, previewed.Body)
	}
	var preview loomapi.PreviewResponse
	if err := json.Unmarshal([]byte(previewed.Body), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.ReceiptId != *assessment.CandidateReceiptId || preview.OutputId != "patients" || len(preview.Rows) != 1 {
		t.Fatalf("candidate preview=%#v", preview)
	}
	proposal, err := json.Marshal(assessment.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	commandBody := fmt.Sprintf(`{"commandId":"apply-row-change","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_TABLE_ROOT_REBASE","rowChange":%s}]}`, ws.CurrentSemanticsVersion, snapshot.Token, digest, proposal)
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

	assertRootCandidateDocumentEqualsApplied(t, assessment.CandidateDocument, document)

	replacementDigest, err := response.Workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	replacementBody := fmt.Sprintf(`{"mode":"REPLACE","snapshotToken":%q,"draftVersion":2,"draftDigest":%q,"outputId":"patients","rootNodeId":"n_report"}`, snapshot.Token, replacementDigest)
	replacementAssessed := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/patients/authoring/v2/row-change", replacementBody)
	if replacementAssessed.StatusCode != http.StatusOK {
		t.Fatalf("replacement assessment status=%d body=%s", replacementAssessed.StatusCode, replacementAssessed.Body)
	}
	var replacementAssessment loomapi.RowChangeAssessmentResponse
	if err := json.Unmarshal([]byte(replacementAssessed.Body), &replacementAssessment); err != nil {
		t.Fatal(err)
	}
	assertRowChangeWireArrays(t, []byte(replacementAssessed.Body), false)
	if replacementAssessment.Mode == nil || string(*replacementAssessment.Mode) != string(authoringv2.RowChangeModeReplace) || replacementAssessment.Proposal == nil || replacementAssessment.Proposal.Mode == nil || string(*replacementAssessment.Proposal.Mode) != string(authoringv2.RowChangeModeReplace) ||
		len(replacementAssessment.RemovedFeatureKeys) == 0 || replacementAssessment.Proposal.CatalogDigest == nil || *replacementAssessment.Proposal.CatalogDigest == "" || replacementAssessment.Proposal.CatalogGeneration == nil || *replacementAssessment.Proposal.CatalogGeneration == "" {
		t.Fatalf("replacement assessment=%#v", replacementAssessment)
	}
	if replacementAssessment.CandidateDocument == nil || replacementAssessment.CandidateNetwork == nil || replacementAssessment.CandidateRowIdentity == nil || replacementAssessment.CandidateDocument.RootResourceType != "DiagnosticReport" || replacementAssessment.CandidateDocument.ImportedRecipe != nil || replacementAssessment.CandidateNetwork.Owner != "DOCUMENT" {
		t.Fatalf("replacement response retained saved source semantics: %s", replacementAssessed.Body)
	}
	replacementPreviewBody := fmt.Sprintf(`{"receiptId":%q,"outputId":"patients"}`, *replacementAssessment.CandidateReceiptId)
	replacementPreviewed := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/patients/authoring/v2/preview", replacementPreviewBody)
	if replacementPreviewed.StatusCode != http.StatusOK {
		t.Fatalf("replacement candidate preview status=%d body=%s", replacementPreviewed.StatusCode, replacementPreviewed.Body)
	}
	replacementProposal, err := json.Marshal(replacementAssessment.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	replacementCommandBody := fmt.Sprintf(`{"commandId":"apply-row-replacement","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":2,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_TABLE_ROOT_REBASE","outputId":"patients","rowChange":%s}]}`, ws.CurrentSemanticsVersion, snapshot.Token, replacementDigest, replacementProposal)
	replacementApplied := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/patients/authoring/v2/commands", replacementCommandBody)
	if replacementApplied.StatusCode != http.StatusOK {
		t.Fatalf("replacement apply status=%d body=%s", replacementApplied.StatusCode, replacementApplied.Body)
	}
	var replacementResponse authoringv2.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(replacementApplied.Body), &replacementResponse); err != nil {
		t.Fatal(err)
	}
	if replacementResponse.DraftVersion != 3 || replacementResponse.Workspace.Documents[0].Output.ID != "patients" || replacementResponse.Workspace.Documents[0].RootResourceType != "DiagnosticReport" ||
		len(replacementResponse.Workspace.Documents[0].Columns) != 1 || replacementResponse.Workspace.Documents[0].Columns[0].Column != "id" || replacementResponse.Workspace.Tabs[0].ID != "patients" {
		t.Fatalf("replacement applied response=%#v", replacementResponse)
	}

	assertRootCandidateDocumentEqualsApplied(t, replacementAssessment.CandidateDocument, replacementResponse.Workspace.Documents[0])

	stale := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/patients/authoring/v2/row-change", assessmentBody)
	if stale.StatusCode != http.StatusConflict {
		t.Fatalf("stale assessment status=%d body=%s", stale.StatusCode, stale.Body)
	}
}

func assertRowChangeWireArrays(t *testing.T, body []byte, expectEmptyEffects bool) {
	t.Helper()
	var assessment map[string]json.RawMessage
	if err := json.Unmarshal(body, &assessment); err != nil {
		t.Fatalf("decode assessment wire body: %v", err)
	}
	assertWireArray(t, assessment, "removedFeatureKeys", expectEmptyEffects)
	assertWireArray(t, assessment, "workspaceOutputConsumers", true)
	assertWireArray(t, assessment, "sharedFilterBindings", true)

	var proposal map[string]json.RawMessage
	if err := json.Unmarshal(assessment["proposal"], &proposal); err != nil {
		t.Fatalf("decode proposal wire body: %v", err)
	}
	assertWireArray(t, proposal, "removedFeatureKeys", expectEmptyEffects)
	assertWireArray(t, proposal, "workspaceOutputConsumers", true)
	assertWireArray(t, proposal, "sharedFilterBindings", true)
}

func assertWireArray(t *testing.T, object map[string]json.RawMessage, key string, expectEmpty bool) {
	t.Helper()
	raw, ok := object[key]
	if !ok || len(bytes.TrimSpace(raw)) < 2 || bytes.TrimSpace(raw)[0] != '[' || bytes.TrimSpace(raw)[len(bytes.TrimSpace(raw))-1] != ']' {
		t.Fatalf("wire field %q must be a JSON array, got %s", key, raw)
	}
	if expectEmpty && !bytes.Equal(bytes.TrimSpace(raw), []byte("[]")) {
		t.Fatalf("wire field %q must be an empty JSON array, got %s", key, raw)
	}
	if !expectEmpty && bytes.Equal(bytes.TrimSpace(raw), []byte("[]")) {
		t.Fatalf("wire field %q must be a nonempty JSON array, got %s", key, raw)
	}
}

func assertRootCandidateDocumentEqualsApplied(t *testing.T, candidate *loomapi.Document, applied ws.Document) {
	t.Helper()
	raw, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	var expected ws.Document
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := json.Marshal(applied)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, actual) {
		t.Fatalf("root Apply differs from exact candidate document: candidate=%s applied=%s", want, actual)
	}
}

func TestExplorerStateRouteBuildsDefaultProjectionFromRecipe(t *testing.T) {
	store := newTestExplorerStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "patients", "Patients", "", "test"); err != nil {
		t.Fatal(err)
	}
	selector := dataset.DataframeSelector{Recipe: "patients-query", TranslationVersion: "v1", Output: "patients"}
	revision := explorer.Revision{
		ID:               "revision-a",
		Project:          "project-a",
		ExplorerID:       "patients",
		Recipe:           recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: selector.Recipe, TranslationVersion: selector.TranslationVersion, Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "patient_id", FieldRef: "Patient.id"}}}}},
		SourceGeneration: "generation-a",
		Dataset: viewer.DatasetMetadata{Generation: "generation-a", Outputs: []viewer.DatasetOutput{{
			Name: "patients", State: "ACTIVE", Queryable: true, Selector: &selector,
			Columns: []publication.PhysicalColumn{{Name: "patient_id", LogicalType: "string", ClickHouse: "String"}},
		}}},
		EmittedColumns:       []viewer.EmittedColumn{{OutputID: "patients", EmissionID: "em_patient_id", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string"}},
		PublicOutputContract: json.RawMessage(`{"outputs":[{"outputId":"patients","columns":[{"column":"patient_id","label":"Patient ID","logicalType":"string","filterable":false,"chartable":false}]}]}`),
		Publication:          viewer.PublicationMetadata{State: "ACTIVE", Generation: "generation-a"},
		Status:               explorer.RevisionActive,
	}
	store.Mu.Lock()
	store.Revisions[revision.ID] = revision
	owner := store.Explorers[explorerfixtures.Key("project-a", "patients")]
	owner.ActiveRevisionID = revision.ID
	store.Explorers[explorerfixtures.Key("project-a", "patients")] = owner
	store.Mu.Unlock()

	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, lifecycle.Config{})
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/patients", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	var state viewer.ExplorerStateV1
	if err := json.Unmarshal([]byte(response.Body), &state); err != nil {
		t.Fatal(err)
	}
	if state.APIVersion != viewer.ExplorerStateV1APIVersion || state.Kind != viewer.ExplorerStateV1Kind {
		t.Fatalf("state identity = %q/%q", state.APIVersion, state.Kind)
	}
	if state.Runtime == nil {
		t.Fatalf("runtime projection is nil: %s", response.Body)
	}
	if state.Runtime.Status != "ACTIVE" || len(state.Runtime.Outputs) != 1 {
		t.Fatalf("runtime = %#v", state.Runtime)
	}
	output := state.Runtime.Outputs[0]
	if output.OutputID != "patients" || output.Selector != selector || len(output.Columns) != 1 || output.Columns[0].Column != "patient_id" {
		t.Fatalf("default output = %#v", output)
	}
	if !output.Columns[0].Visible || len(output.Table.Columns) != 1 || len(output.Filters) != 0 || len(output.Charts) != 0 {
		t.Fatalf("default presentation = %#v", output)
	}
}

func TestExplorerStateRoutePreservesCompilerResultUnits(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "patients-query", TranslationVersion: "v1", Output: "patients"}
	weightUnit := &unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}
	bmiUnit := &unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg/m2"}
	revision := explorer.Revision{
		ID: "revision-result-units", Project: "project-a", ExplorerID: "patients",
		Recipe: recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: selector.Recipe, TranslationVersion: selector.TranslationVersion,
			Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}},
		},
		SourceGeneration: "generation-a",
		Dataset: viewer.DatasetMetadata{Generation: "generation-a", Outputs: []viewer.DatasetOutput{{
			Name: "patients", State: "ACTIVE", Queryable: true, Selector: &selector,
			Columns: []publication.PhysicalColumn{
				{Name: "mean_weight", LogicalType: "decimal", ClickHouse: "Decimal"},
				{Name: "bmi", LogicalType: "decimal", ClickHouse: "Decimal"},
				{Name: "patient_id", LogicalType: "string", ClickHouse: "String"},
			},
		}}},
		EmittedColumns: []viewer.EmittedColumn{
			{OutputID: "patients", EmissionID: "em_mean_weight", PublicColumn: "mean_weight", Label: "Mean weight", LogicalType: "decimal", ResultUnit: weightUnit},
			{OutputID: "patients", EmissionID: "em_bmi", PublicColumn: "bmi", Label: "BMI", LogicalType: "decimal", ResultUnit: bmiUnit},
			{OutputID: "patients", EmissionID: "em_patient_id", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string"},
		},
		PublicOutputContract: json.RawMessage(`{"outputs":[{"outputId":"patients","columns":[{"column":"mean_weight","label":"Mean weight","logicalType":"decimal","filterable":false,"chartable":false,"resultUnit":{"system":"http://unitsofmeasure.org","code":"kg"}},{"column":"bmi","label":"BMI","logicalType":"decimal","filterable":false,"chartable":false,"resultUnit":{"system":"http://unitsofmeasure.org","code":"kg/m2"}},{"column":"patient_id","label":"Patient ID","logicalType":"string","filterable":false,"chartable":false}]}]}`),
		Publication:          viewer.PublicationMetadata{State: "ACTIVE", Generation: "generation-a"},
		Status:               explorer.RevisionActive,
	}
	store := newTestExplorerStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "patients", "Patients", "", "test"); err != nil {
		t.Fatal(err)
	}
	store.Mu.Lock()
	store.Revisions[revision.ID] = revision
	owner := store.Explorers[explorerfixtures.Key("project-a", "patients")]
	owner.ActiveRevisionID = revision.ID
	store.Explorers[explorerfixtures.Key("project-a", "patients")] = owner
	store.Mu.Unlock()

	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, lifecycle.Config{})
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/patients", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	var state viewer.ExplorerStateV1
	if err := json.Unmarshal([]byte(response.Body), &state); err != nil {
		t.Fatal(err)
	}
	if state.Runtime == nil || len(state.Runtime.Outputs) != 1 || len(state.Runtime.Outputs[0].Columns) != 3 {
		t.Fatalf("runtime = %#v", state.Runtime)
	}
	columns := make(map[string]viewer.ExplorerRuntimeColumnV1, len(state.Runtime.Outputs[0].Columns))
	for _, column := range state.Runtime.Outputs[0].Columns {
		columns[column.Column] = column
	}
	for name, want := range map[string]*unit.UnitIdentity{"mean_weight": weightUnit, "bmi": bmiUnit} {
		got := columns[name].ResultUnit
		if got == nil || got.System != want.System || got.Code != want.Code {
			t.Fatalf("runtime %s result unit = %#v, want %#v", name, got, want)
		}
	}
	if columns["patient_id"].ResultUnit != nil {
		t.Fatalf("unitless runtime column carries result unit: %#v", columns["patient_id"].ResultUnit)
	}
	var wire struct {
		Runtime struct {
			Outputs []struct {
				Columns []map[string]json.RawMessage `json:"columns"`
			} `json:"outputs"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal([]byte(response.Body), &wire); err != nil {
		t.Fatalf("decode Viewer state JSON: %v", err)
	}
	wireColumns := make(map[string]map[string]json.RawMessage, len(wire.Runtime.Outputs[0].Columns))
	for _, column := range wire.Runtime.Outputs[0].Columns {
		var name string
		if err := json.Unmarshal(column["column"], &name); err != nil {
			t.Fatalf("decode Viewer column name: %v", err)
		}
		wireColumns[name] = column
	}
	for name, code := range map[string]string{"mean_weight": "kg", "bmi": "kg/m2"} {
		var got map[string]string
		if err := json.Unmarshal(wireColumns[name]["resultUnit"], &got); err != nil {
			t.Fatalf("decode Viewer %s resultUnit: %v", name, err)
		}
		if len(got) != 2 || got["system"] != "http://unitsofmeasure.org" || got["code"] != code {
			t.Fatalf("Viewer %s resultUnit JSON = %#v", name, got)
		}
	}
	if _, exists := wireColumns["patient_id"]["resultUnit"]; exists {
		t.Fatalf("unitless Viewer column includes resultUnit: %s", wireColumns["patient_id"]["resultUnit"])
	}
}

func TestExplorerStateRouteReportsMalformedCurrentProjection(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "patients-query", TranslationVersion: "v1", Output: "patients"}
	baseRevision := explorer.Revision{
		ID: "revision-integrity", Project: "project-a", ExplorerID: "patients",
		Recipe: recipe.Bundle{
			RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: selector.Recipe, TranslationVersion: selector.TranslationVersion,
			Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "patient_id", FieldRef: "Patient.id"}}}},
		},
		Dataset: viewer.DatasetMetadata{Generation: "generation-a", Outputs: []viewer.DatasetOutput{{
			Name: "patients", State: "ACTIVE", Queryable: true, Selector: &selector,
			Columns: []publication.PhysicalColumn{{Name: "patient_id", LogicalType: "string", ClickHouse: "String"}},
		}}},
		EmittedColumns: []viewer.EmittedColumn{{OutputID: "patients", EmissionID: "em_patient_id", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string"}},
		Status:         explorer.RevisionActive,
	}
	for _, test := range []struct {
		name     string
		config   json.RawMessage
		contract json.RawMessage
	}{
		{name: "malformed current config", config: json.RawMessage(`{"apiVersion":"` + explorerconfig.ConfigV2APIVersion + `","kind":"ExplorerConfig","unknown":true}`)},
		{name: "malformed current public contract", contract: json.RawMessage(`{"outputs":`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestExplorerStore()
			service, err := explorer.NewService(store)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "patients", "Patients", "", "test"); err != nil {
				t.Fatal(err)
			}
			revision := baseRevision
			revision.Config = test.config
			revision.PublicOutputContract = test.contract
			store.Mu.Lock()
			store.Revisions[revision.ID] = revision
			owner := store.Explorers[explorerfixtures.Key("project-a", "patients")]
			owner.ActiveRevisionID = revision.ID
			store.Explorers[explorerfixtures.Key("project-a", "patients")] = owner
			store.Mu.Unlock()

			app := fiber.New()
			registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, lifecycle.Config{})
			response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/patients", "")
			if response.StatusCode != http.StatusInternalServerError || !strings.Contains(response.Body, `"code":"EXPLORER_INTEGRITY_FAILURE"`) {
				t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
			}
		})
	}
}

func TestExplorerStateRouteReturnsExplicitUnpublishedProjection(t *testing.T) {
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "empty", "Empty", "", "test"); err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, lifecycle.Config{})
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/empty", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal([]byte(response.Body), &wire); err != nil {
		t.Fatal(err)
	}
	if raw, ok := wire["runtime"]; !ok || string(raw) != "null" {
		t.Fatalf("runtime = %s, want explicit null", raw)
	}
	var state viewer.ExplorerStateV1
	if err := json.Unmarshal([]byte(response.Body), &state); err != nil {
		t.Fatal(err)
	}
	if state.Runtime != nil || state.Generated.Publication.State != viewer.ExplorerRuntimeV1NotPublished {
		t.Fatalf("unpublished state = %#v", state)
	}
}

//go:embed testdata/case008_saved_builder_workspace.json
var case008SavedWorkspaceFS embed.FS

const (
	case008Project     = "loom_dev_verify_mv0q3i44-bfd8829"
	case008ExplorerID  = "verify-44-bfd8829-cohort-recode"
	case008OutputID    = "out_a8daf7635002468eb86169ed"
	case008RevisionID  = "grouprev_0003bc86ece6dec199c1d4b61530b36633ebac9ef06b1f7dc035bbae221b011e"
	case008DraftDigest = "sha256:f63cfcd8f42aa3d20f75ee344aa3e26e8a3c6820bd348a9dfdc729c8aefbde9f"
)

func TestCase008ExplicitGroupRowDefinitionPreviewCompilesCandidateReceipt(t *testing.T) {
	rawWorkspace, err := case008SavedWorkspaceFS.ReadFile("testdata/case008_saved_builder_workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	var workspace ws.Workspace
	if err := json.Unmarshal(rawWorkspace, &workspace); err != nil {
		t.Fatal(err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != case008DraftDigest || len(workspace.Documents) != 1 || workspace.Documents[0].Output.ID != case008OutputID ||
		workspace.Documents[0].RootResourceType != "Patient" || workspace.Documents[0].Rows.Kind != ws.RowDefinitionRecords ||
		len(workspace.Documents[0].Columns) != 1 || workspace.Documents[0].Columns[0].Source.Field.Path != "id" ||
		workspace.Documents[0].Columns[0].Source.Field.ProjectionMode != "VALUE" {
		t.Fatalf("retained pre-proposal workspace does not match its digest/output/source: digest=%s document=%#v", digest, workspace.Documents)
	}

	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: projectid.Canonical(case008Project), Generation: "devloop-v1",
			AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope), SchemaDigest: "case008-schema",
			ResourceInventoryDigest: "case008-inventory", RelationshipDigest: "case008-relationships",
			FieldDigest: "case008-fields", ShapeDigest: "case008-shape",
			ProtocolVersion: capabilityresolver.ProtocolVersion, CompilerVersion: capabilityresolver.CompilerVersion,
			TraversalPolicyVersion: capabilityresolver.TraversalPolicyVersion, ProjectionPolicyVersion: capabilityresolver.ProjectionPolicyVersion,
		},
		capability.Policy{
			Route:      capability.RoutePolicy{Version: capabilityresolver.TraversalPolicyVersion, AllowsRepeatedEdges: true, AllowsSelfLoops: true},
			Projection: capability.ProjectionPolicy{Version: capabilityresolver.ProjectionPolicyVersion},
		},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_88fd86589e77093ea1cd19a7", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient", Populated: true, DocumentCount: 2, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil,
		[]capability.Candidate{{ID: "c_13314d7bfa5241c3464b0dbd", NodeID: "n_88fd86589e77093ea1cd19a7", ResourceType: "Patient", FieldPath: "id", Label: "id", LogicalType: "string", Cardinality: "OPTIONAL_ONE", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst}, SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true}},
		nil,
	)

	migration, err := ws.MigrateStoredWorkspace(draft, ws.CatalogFromCapability(snapshot, case008ExplorerID))
	if err != nil {
		t.Fatal(err)
	}
	draft, digest = migration.CanonicalJSON, migration.MigratedCanonicalDigest
	store := newTestExplorerStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), explorer.Explorer{
		Project: case008Project, ExplorerID: case008ExplorerID, Title: workspace.Explorer.Title,
		DraftConfig: draft, DraftVersion: 2, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}

	var queryCalls int
	var basePreviewRows []map[string]any
	var candidatePreviewRows []map[string]any
	var basePreviewColumns []string
	var candidatePreviewColumns []string
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{}, ScopeDigest: reciperuntime.ScopeDigest,
		QueryRows: func(_ context.Context, _ string, _ int, bindVars map[string]any, visit func(map[string]any) error) error {
			queryCalls++
			if _, grouped := bindVars["group_rows_revision_id"]; grouped {
				return visit(map[string]any{
					"group_revision_id": case008RevisionID, "group_id": "group-a", "group_label": "Cohort A", "group_ordinal": 0,
					"members": []any{
						map[string]any{"source_identity": map[string]any{"project": "loom_dev_verify_mv0q3i44/bfd8829", "generation": "devloop-v1", "resource_type": "Patient", "id": "dev-patient-001"}, "payload": map[string]any{"id": "dev-patient-001"}},
						map[string]any{"source_identity": map[string]any{"project": "loom_dev_verify_mv0q3i44/bfd8829", "generation": "devloop-v1", "resource_type": "Patient", "id": "dev-patient-002"}, "payload": map[string]any{"id": "dev-patient-002"}},
					},
					"__loom_row_id":                map[string]any{"group_revision_id": case008RevisionID, "group_id": "group-a"},
					"__loom_root_contributor_keys": []string{"patient-storage-key-001", "patient-storage-key-002"},
				})
			}
			for _, row := range []map[string]any{
				{"id": "dev-patient-001", "col_09f81323c7d363cb25381a05": "dev-patient-001", "__loom_row_id": "patient-row-001"},
				{"id": "dev-patient-002", "col_09f81323c7d363cb25381a05": "dev-patient-002", "__loom_row_id": "patient-row-002"},
			} {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	groupResolver := case008ExplicitGroupResolver{}
	var basePreviewSummary, candidatePreviewSummary dataframeexecution.PreviewSummary
	var candidateReceipt *receiptmodel.CompilationReceipt
	config := lifecycle.Config{
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: ws.CatalogFromCapability,
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			receipt, err := receiptapi.Compile(ctx, request, nil, engine, service, nil, nil)
			if err == nil && request.RequestID == "row-definition-proposal-candidate" {
				candidateReceipt = receipt
			}
			return receipt, err
		},
		ExplicitGroupResolver: groupResolver,
		PreviewReceipt: func(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			resolved, err := receiptapi.CompileValidatedResolution(ctx, engine, receipt, bindings)
			if err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			var capturedRows *[]map[string]any
			if receipt.RowDefinitionProposal != nil {
				capturedRows = &candidatePreviewRows
			} else {
				capturedRows = &basePreviewRows
			}
			summary, err := engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{
				Output: bindings.OutputNames[0], Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity,
			}, func(row map[string]any) error {
				*capturedRows = append(*capturedRows, row)
				return visit(row)
			})
			if receipt.RowDefinitionProposal != nil {
				candidatePreviewSummary = summary
				candidatePreviewColumns = append([]string(nil), summary.Columns...)
			} else {
				basePreviewSummary = summary
				basePreviewColumns = append([]string(nil), summary.Columns...)
			}
			return summary, err
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	body := fmt.Sprintf(`{"snapshotToken":%q,"expectedDraftVersion":2,"expectedDraftDigest":%q,"outputId":%q,"selection":{"kind":"EXPLICIT_GROUP","explicitGroup":{"revisionId":%q,"unassignedMemberPolicy":"ERROR"}}}`,
		snapshot.Token, digest, case008OutputID, case008RevisionID)
	proposal := requestJSON(t, app, http.MethodPost,
		"/api/v1/projects/"+case008Project+"/explorers/"+case008ExplorerID+"/authoring/v2/row-definition-proposals", body)
	if proposal.StatusCode != http.StatusOK {
		t.Fatalf("retained CASE-008 row-definition proposal status=%d body=%s queryCalls=%d", proposal.StatusCode, proposal.Body, queryCalls)
	}
	if candidateReceipt == nil || candidateReceipt.RowDefinitionProposal == nil || candidateReceipt.RowDefinitionProposal.OutputID != case008OutputID {
		t.Fatalf("production candidate receipt was not produced/bound before preview: %#v", candidateReceipt)
	}
	var proposalResponse struct {
		ProposalID string `json:"proposalId"`
		OutputID   string `json:"outputId"`
		Mode       string `json:"mode"`
	}
	if err := json.Unmarshal([]byte(proposal.Body), &proposalResponse); err != nil {
		t.Fatalf("decode successful proposal response: %v; body=%s", err, proposal.Body)
	}
	if proposalResponse.ProposalID != candidateReceipt.ID || proposalResponse.OutputID != case008OutputID || proposalResponse.Mode != "EXPLICIT_GROUP" {
		t.Fatalf("proposal response=%+v, candidate receipt=%s output=%s", proposalResponse, candidateReceipt.ID, case008OutputID)
	}
	if queryCalls != 2 || basePreviewSummary.RowCount != 2 || candidatePreviewSummary.RowCount != 1 {
		t.Fatalf("preview counts base=%d candidate=%d queryCalls=%d; want 2, 1, 2; body=%s", basePreviewSummary.RowCount, candidatePreviewSummary.RowCount, queryCalls, proposal.Body)
	}
	wantCandidateRows := []map[string]any{{
		"group_id":      "group-a",
		"group_label":   "Cohort A",
		"group_ordinal": 0,
		"members": []any{
			map[string]any{"source_identity": map[string]any{"project": "loom_dev_verify_mv0q3i44/bfd8829", "generation": "devloop-v1", "resource_type": "Patient", "id": "dev-patient-001"}, "payload": map[string]any{"id": "dev-patient-001"}},
			map[string]any{"source_identity": map[string]any{"project": "loom_dev_verify_mv0q3i44/bfd8829", "generation": "devloop-v1", "resource_type": "Patient", "id": "dev-patient-002"}, "payload": map[string]any{"id": "dev-patient-002"}},
		},
		"__loom_row_id": map[string]any{"group_revision_id": case008RevisionID, "group_id": "group-a"},
	}}
	if !reflect.DeepEqual(candidatePreviewRows, wantCandidateRows) {
		t.Fatalf("candidate preview rows=%#v, want literal group row with the two retained Patient IDs", candidatePreviewRows)
	}
	if !reflect.DeepEqual(basePreviewColumns, []string{"col_09f81323c7d363cb25381a05"}) || !reflect.DeepEqual(candidatePreviewColumns, []string{"group_id", "group_label", "group_ordinal", "members"}) {
		t.Fatalf("preview columns base=%v candidate=%v", basePreviewColumns, candidatePreviewColumns)
	}
	t.Logf("candidate receipt=%s output=%s basePreview=%#v candidatePreview=%#v", candidateReceipt.ID, case008OutputID, basePreviewRows, candidatePreviewRows)
}

type case008ExplicitGroupResolver struct{}

func (case008ExplicitGroupResolver) ListExplicitGroupRevisions(context.Context, explicitgroup.ExplicitGroupRevisionListRequest) ([]explicitgroup.ExplicitGroupRevisionChoice, error) {
	return nil, nil
}

func (case008ExplicitGroupResolver) ResolveExplicitGroupRevision(_ context.Context, request explicitgroup.ExplicitGroupRevisionResolveRequest) (explicitgroup.ExplicitGroupRevisionProof, error) {
	return explicitgroup.ExplicitGroupRevisionProof{
		RevisionID: request.RevisionID, Project: request.Project, SourceGeneration: request.Snapshot.Identity.Generation,
		AuthorizationScopeDigest: request.Snapshot.Identity.AuthorizationScopeDigest, RootResourceType: request.RootResourceType,
		SelectionRevisionID: "selection_case008", SelectionMembershipDigest: "sha256:case008-selection-membership",
		DefinitionDigest: "sha256:case008-definition", MembershipDigest: "sha256:case008-membership", Complete: true,
	}, nil
}

func (case008ExplicitGroupResolver) ValidateCompilationReceipt(_ context.Context, proof explicitgroup.ExplicitGroupRevisionProof, receipt *receiptmodel.CompilationReceipt) error {
	workspace, err := ws.DecodeWorkspace(receipt.NormalizedBundle)
	if err != nil {
		return err
	}
	for _, document := range workspace.Documents {
		if document.Output.ID == case008OutputID && document.Rows.Kind == ws.RowDefinitionGroups && document.Rows.Groups != nil &&
			document.Rows.Groups.Source.Kind == ws.GroupSourceExplicit && document.Rows.Groups.Source.Explicit != nil &&
			document.Rows.Groups.Source.Explicit.RevisionID == proof.RevisionID &&
			document.Rows.Groups.Source.Explicit.UnassignedMemberPolicy == ws.UnassignedMemberError {
			return nil
		}
	}
	return fmt.Errorf("receipt does not carry exact explicit group revision and ERROR policy")
}

func TestRepositoryDeploymentPersistsExecutableDataframeSelectors(t *testing.T) {
	store := newTestExplorerStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := testAuthoringV2CapabilitySnapshot()
	materialize := func(_ context.Context, bundle recipe.Bundle, _ recipe.RuntimeBindings) (ports.Execution, error) {
		if bundle.Name != "native-test" {
			t.Fatalf("materialized identity = %s/%s", bundle.Name, bundle.TranslationVersion)
		}
		return ports.Execution{ID: "execution-a", SourceGeneration: "generation-a", State: "PUBLISHED", Outputs: []ports.ExecutionOutput{{Name: "patients", State: "PUBLISHED", Columns: []publication.PhysicalColumn{{Name: "c_patient", LogicalType: "string", ClickHouse: "String"}}}}}, nil
	}
	activationCount := 0
	activateRelease := func(_ context.Context, project, generation string, selectors []dataset.DataframeSelector) error {
		activationCount++
		if project != "project-a" || generation != "generation-a" || len(selectors) != 1 || selectors[0].Output != "patients" {
			t.Fatalf("release activation = %s/%s %#v", project, generation, selectors)
		}
		return nil
	}
	app := fiber.New()
	config := lifecycle.Config{
		Capability: ports.CapabilityResolver{
			Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
			Token:   func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			},
			Catalog: ws.CatalogFromCapability,
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return explorerfixtures.PersistTestNativeReceipt(ctx, t, service, request, snapshot)
		},
		MaterializeReceipt: func(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings) (ports.Execution, error) {
			execution, err := materialize(ctx, receipt.Bundle, bindings)
			if err != nil {
				return ports.Execution{}, err
			}
			execution.Project = receipt.Project
			execution.ScopeDigest = "execution-scope"
			execution.QualityReports = []publication.QualityReport{{
				ID: "quality-patients", ReceiptID: receipt.ID, Project: receipt.Project,
				DatasetGeneration: receipt.SourceGeneration, ScopeDigest: execution.ScopeDigest,
				Output: "patients", PolicyVersion: publication.DefaultQualityPolicyVersion,
				Completeness: publication.QualityComplete, Verdict: publication.QualityPassed,
			}}
			return execution, nil
		},
		ActivateRelease: activateRelease,
	}
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)

	request := requestJSONWithHeaders(t, app, http.MethodPost, "/api/v1/projects/project-a/generations/generation-a/explorer-config", string(constructionfixtures.BaselineExplorerWorkspace()), map[string]string{"X-Loom-Source-Commit": "commit-a"})
	if request.StatusCode != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", request.StatusCode, request.Body)
	}
	repeated := requestJSONWithHeaders(t, app, http.MethodPost, "/api/v1/projects/project-a/generations/generation-a/explorer-config", string(constructionfixtures.BaselineExplorerWorkspace()), map[string]string{"X-Loom-Source-Commit": "commit-a"})
	if repeated.StatusCode != http.StatusOK {
		t.Fatalf("repeated deploy status=%d body=%s", repeated.StatusCode, repeated.Body)
	}
	if activationCount != 2 {
		t.Fatalf("release activation count = %d, want 2", activationCount)
	}
	active, err := service.ActiveRevision(context.Background(), "project-a", "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(active.Dataset.Outputs) != 1 || active.Dataset.Outputs[0].Selector == nil {
		t.Fatalf("dataset selector missing: %#v", active.Dataset.Outputs)
	}
	selector := active.Dataset.Outputs[0].Selector
	if selector.Recipe != "native-test" || selector.Output != "patients" {
		t.Fatalf("dataset selector = %#v", selector)
	}
	if len(active.Materializations) != 1 || active.Materializations[0].Selector == nil || *active.Materializations[0].Selector != *selector {
		t.Fatalf("materialization selector = %#v", active.Materializations)
	}
	if len(active.QualityReports) != 1 || active.QualityReports[0].Output != "patients" {
		t.Fatalf("quality evidence missing from active revision: %#v", active.QualityReports)
	}
	if len(active.AuthoringBundle) == 0 || active.CompilationReceiptID == "" || len(active.PublicOutputContract) == 0 {
		t.Fatalf("active repository revision lost V2 artifacts: %#v", active)
	}
	builder := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default/authoring/v2/builder", "")
	if builder.StatusCode != http.StatusOK || !strings.Contains(builder.Body, `"workspace"`) {
		t.Fatalf("builder reload status=%d body=%s", builder.StatusCode, builder.Body)
	}
	viewer := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default", "")
	if viewer.StatusCode != http.StatusOK || !strings.Contains(viewer.Body, `"label":"Patient ID"`) || !strings.Contains(viewer.Body, `"qualityReports"`) {
		t.Fatalf("viewer state status=%d body=%s", viewer.StatusCode, viewer.Body)
	}
	store.Mu.Lock()
	broken := store.Revisions[active.ID]
	broken.PublicOutputContract = nil
	store.Revisions[active.ID] = broken
	store.Mu.Unlock()
	withoutContract := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default/authoring/v2/builder", "")
	if withoutContract.StatusCode != http.StatusOK || !strings.Contains(withoutContract.Body, `"workspace"`) {
		t.Fatalf("builder should hydrate valid intent without runtime contract: status=%d body=%s", withoutContract.StatusCode, withoutContract.Body)
	}
	store.Mu.Lock()
	broken = store.Revisions[active.ID]
	broken.AuthoringBundle = nil
	store.Revisions[active.ID] = broken
	store.Mu.Unlock()
	missing := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default/authoring/v2/builder", "")
	if missing.StatusCode != http.StatusConflict || !strings.Contains(missing.Body, `"code":"AUTHORING_STATE_MISSING"`) {
		t.Fatalf("missing active authoring state status=%d body=%s", missing.StatusCode, missing.Body)
	}
}

func requestJSONWithHeaders(t *testing.T, app *fiber.App, method, path, body string, headers map[string]string) testHTTPResponse {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return testHTTPResponse{StatusCode: response.StatusCode, Body: string(raw)}
}
