package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/projectid"
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
	var document authoringv2.Document
	if err := json.Unmarshal(rawDocument, &document); err != nil {
		t.Fatal(err)
	}
	if document.Output.ID != "out_c65a00f563a3e700edb027a8" || document.RootResourceType != "Patient" ||
		document.Construction == nil || len(document.Construction.Steps) != 1 ||
		document.Construction.Steps[0].Operation.Group == nil || len(document.Construction.Steps[0].Operation.Group.Keys) != 0 {
		t.Fatalf("retained fixture no longer contains the captured empty-key Patient Group: %#v", document)
	}

	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	workspace.Explorer.Title = document.Output.Title
	workspace.Documents = []authoringv2.Document{document}
	workspace.Tabs = []authoringv2.Tab{{
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
			Project: projectid.Canonical(project), Generation: "devloop-v1", AuthorizationScopeDigest: explorerScopeDigest(readScope),
			SchemaDigest: "patient-one-schema", ResourceInventoryDigest: "patient-one-inventory",
			RelationshipDigest: "patient-one-relationships", FieldDigest: "patient-one-fields", ShapeDigest: "patient-one-shape",
			ProtocolVersion: explorerCapabilityProtocolVersion, CompilerVersion: explorerCapabilityCompilerVersion,
			TraversalPolicyVersion: explorerTraversalPolicyVersion, ProjectionPolicyVersion: explorerProjectionPolicyVersion,
		},
		capability.Policy{
			Route:      capability.RoutePolicy{Version: explorerTraversalPolicyVersion, AllowsRepeatedEdges: true, AllowsSelfLoops: true},
			Projection: capability.ProjectionPolicy{Version: explorerProjectionPolicyVersion},
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

	// Use the same production catalog projection that emits signed choice IDs.
	catalog := authoringV2Catalog(snapshot, explorerID)
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
	if _, err := baseStore.create(explorer.Explorer{
		Project: project, ExplorerID: explorerID, Title: document.Output.Title,
		DraftConfig: draft, DraftVersion: 3, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	rawFixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "devloop-fixture", "Patient.ndjson"))
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
		Registry: compilerTestRegistry{}, ScopeDigest: recipeScopeDigest,
		// QueryRows stands in for Arango at the database boundary; the renderer test
		// checks the generated assertion, while this route test supplies the two
		// literal IDs from the retained raw fixture as the query result oracle.
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
		Capability: lifecycle.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			receipt, err := compileExplorerReceipt(ctx, request, nil, recipeEngine, service, nil, nil)
			if err != nil {
				return nil, err
			}
			for _, emitted := range receipt.EmittedColumns {
				if emitted.CandidateID == "c_patient_id" && emitted.SourcePath == "id" {
					candidatePublicColumn = emitted.PublicColumn
					break
				}
			}
			return receipt, nil
		},
		PreviewReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
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
	if len(baseStore.receipts) != 2 {
		t.Fatalf("candidate receipt count=%d, want one production receipt for each ONE and ALL preview", len(baseStore.receipts))
	}
}

type jsonRoundTripPatientOneReceiptStore struct {
	*testExplorerStore
}

func (s *jsonRoundTripPatientOneReceiptStore) InsertCompilationReceipt(ctx context.Context, receipt explorer.CompilationReceipt) (*explorer.CompilationReceipt, error) {
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
	var stored explorer.CompilationReceipt
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, err
	}
	return s.testExplorerStore.InsertCompilationReceipt(ctx, stored)
}
