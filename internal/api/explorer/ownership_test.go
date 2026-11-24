package explorerapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	receiptapi "github.com/calypr/loom/internal/explorer/receipt"

	"github.com/calypr/loom/internal/testkit/explorerfixtures"

	"github.com/calypr/loom/internal/testkit/constructionfixtures"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/gofiber/fiber/v3"
)

type countingExplorerStore struct {
	*testExplorerStore
	getCalls   int
	faultGetOn int
}

func (s *countingExplorerStore) Get(ctx context.Context, project, id string) (*explorer.Explorer, error) {
	s.getCalls++
	if s.faultGetOn > 0 && s.getCalls == s.faultGetOn {
		return &explorer.Explorer{Project: "project-b", ExplorerID: "other"}, nil
	}
	return s.testExplorerStore.Get(ctx, project, id)
}

func (s *countingExplorerStore) resetGetCalls() {
	s.getCalls = 0
}

type groupFieldSourceProposalFixture struct {
	app      *fiber.App
	basePath string
	snapshot capability.Snapshot
	digest   string
	proposal string
	store    *countingExplorerStore
}

func newGroupFieldSourceProposalFixture(t *testing.T) *groupFieldSourceProposalFixture {
	t.Helper()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Nodes = []capability.Node{{
		ID: "n_observation", ResourceType: "Observation", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 12,
	}}
	snapshot.Edges = nil
	snapshot.Candidates = []capability.Candidate{{
		ID: "c_observation_coding", NodeID: "n_observation", ResourceType: "Observation",
		FieldPath: "component[].code.coding[]", Label: "Observed coding", LogicalType: "Coding", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{
			{Path: "component[]", MaxItems: 8},
			{Path: "component[].code.coding[]", MaxItems: 8},
		},
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray}, SupportedOperations: []capability.Operation{capability.OperationSelect},
		Observed: true, Populated: true,
	}, {
		ID: "c_observation_component_value", NodeID: "n_observation", ResourceType: "Observation",
		FieldPath: "component[].valueQuantity.value", Label: "Component quantity", LogicalType: "decimal", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "component[]", MaxItems: 8}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
		Observed:           true, Populated: true,
	}, {
		ID: "c_observation_status", NodeID: "n_observation", ResourceType: "Observation",
		FieldPath: "status", Label: "Observation status", LogicalType: "string", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
		Observed:        true, Populated: true,
	}}

	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	rowChoices, err := lifecycle.NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.RootResourceType = "Observation"
	document.Route = ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Observation"}
	document.Rows = ws.RowDefinition{Kind: ws.RowDefinitionExpanded, Expanded: &ws.ExpandedRows{
		OccurrenceID: ws.RootOccurrenceID, ScopePath: "component[].code.coding[]",
		EmptyCollectionPolicy: ws.EmptyCollectionPreserveParent,
	}}
	document.Columns = []ws.Column{}
	document.Output = ws.Output{ID: "observations", Title: "Observations", RowLabel: "Observations"}
	document, err = ws.StartDocumentConstruction(document)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0] = document
	workspace.Tabs[0].OutputID = "observations"
	workspace.Tabs[0].Title = "Observations"
	catalogSnapshot := ws.CatalogFromCapability(snapshot, "observation-explorer")
	workspace, err = ws.MigrateLegacyContributors(workspace, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, catalogSnapshot).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate group field source workspace: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &countingExplorerStore{testExplorerStore: explorerfixtures.NewStore()}
	if _, err := store.Create(context.Background(), explorer.Explorer{
		Project: "project-a", ExplorerID: "observation-explorer", Title: "Observations",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{}, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	var previewContractErr error
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
		RowChoicePlanner: rowChoices,
		ConstructionSourceStage: func(ctx context.Context, request ports.ConstructionSourceStageRequest) (receiptmodel.ReceiptConstructionStage, error) {
			return receiptapi.CompileConstructionSourceStage(ctx, request, recipeEngine)
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return receiptapi.Compile(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		PreviewReceipt: func(_ context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if receipt == nil || receipt.ConstructionProposal == nil || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "observations" {
				previewContractErr = fmt.Errorf("Group field source proposal did not reach compiled preview: receipt=%#v bindings=%#v", receipt, bindings)
				return dataframeexecution.PreviewSummary{}, previewContractErr
			}
			if err := visit(map[string]any{"status_group": "final", "coding_identity": "urn:loinc|2.74|1234-5", "row_count": 1}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{
				Output: "observations", Columns: []string{"status_group", "coding_identity", "row_count"},
				RowCount: 1, Complete: true, PartialValidation: true,
			}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/observation-explorer/authoring/v2"

	capabilitiesResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"observations","stageId":"source_projection","includeGroupChoices":true}`,
		snapshot.Token, digest,
	))
	if capabilitiesResponse.StatusCode != http.StatusOK {
		t.Fatalf("group field source capabilities status=%d body=%s", capabilitiesResponse.StatusCode, capabilitiesResponse.Body)
	}
	var capabilities struct {
		GroupFieldChoices []struct {
			Kind        string `json:"kind"`
			ValueKind   string `json:"valueKind"`
			Path        string `json:"canonicalPath"`
			FieldSource struct {
				ChoiceID string `json:"choiceId"`
			} `json:"fieldSource"`
		} `json:"groupFieldChoices"`
	}
	if err := json.Unmarshal([]byte(capabilitiesResponse.Body), &capabilities); err != nil {
		t.Fatal(err)
	}
	var scalarChoiceID, codingChoiceID string
	for _, choice := range capabilities.GroupFieldChoices {
		if choice.Kind == "FIELD_SOURCE" && choice.ValueKind == "SCALAR" && choice.Path == "status" {
			scalarChoiceID = choice.FieldSource.ChoiceID
		}
		if choice.Kind == "FIELD_SOURCE" && choice.ValueKind == "CODING_IDENTITY" {
			codingChoiceID = choice.FieldSource.ChoiceID
		}
	}
	if scalarChoiceID == "" || codingChoiceID == "" {
		t.Fatalf("group field source choices missing: scalar=%q coding=%q all=%#v", scalarChoiceID, codingChoiceID, capabilities.GroupFieldChoices)
	}

	stepID := "group_current_coding"
	candidate := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: stepID, Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
			ConstructionID: stepID, MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
			Keys: []ws.ConstructionGroupKey{{
				OutputColumnID: "status_group",
				FieldSource:    &ws.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: scalarChoiceID},
			}, {
				OutputColumnID: "coding_identity",
				FieldSource:    &ws.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: codingChoiceID},
			}},
			Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
		}},
		Outputs: []ws.StageColumn{
			{ID: "status_group", Name: "status_group", Label: "Status", Type: "string"},
			{ID: "coding_identity", Name: "coding_identity", Label: "Coding", Type: "string"},
			{ID: "row_count", Name: "row_count", Label: "Count", Type: "integer"},
		},
	}}}
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	proposalResponse := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"observations","changedStepId":%q,"candidateConstruction":%s,"limit":100}`,
		snapshot.Token, digest, stepID, candidateJSON,
	))
	if proposalResponse.StatusCode != http.StatusOK {
		t.Fatalf("group field source proposal status=%d body=%s", proposalResponse.StatusCode, proposalResponse.Body)
	}
	var proposal struct {
		ProposalID            *string         `json:"proposalId"`
		PreviewStatus         string          `json:"previewStatus"`
		CandidateConstruction json.RawMessage `json:"candidateConstruction"`
	}
	if err := json.Unmarshal([]byte(proposalResponse.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalID == nil || proposal.PreviewStatus != "READY" || previewContractErr != nil {
		t.Fatalf("group field source proposal did not complete preview: response=%#v err=%v", proposal, previewContractErr)
	}
	var resolved ws.Construction
	if err := json.Unmarshal(proposal.CandidateConstruction, &resolved); err != nil {
		t.Fatal(err)
	}
	if len(resolved.Steps) != 1 || resolved.Steps[0].Operation.Group == nil || len(resolved.Steps[0].Operation.Group.Keys) != 2 {
		t.Fatalf("proposal did not retain a 2-key Group: %#v", resolved)
	}
	for _, key := range resolved.Steps[0].Operation.Group.Keys {
		if key.FieldSource == nil || key.FieldSource.Kind != "BINDING" {
			t.Fatalf("proposal key lacks a compiled group field source binding: %#v", key)
		}
	}
	return &groupFieldSourceProposalFixture{app: app, basePath: basePath, snapshot: snapshot, digest: digest, proposal: *proposal.ProposalID, store: store}
}

func (f *groupFieldSourceProposalFixture) apply(t *testing.T, commandID string) testHTTPResponse {
	t.Helper()
	return requestJSON(t, f.app, http.MethodPost, f.basePath+"/commands", fmt.Sprintf(
		`{"commandId":%q,"semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"observations","proposalId":%q}]}`,
		commandID, ws.CurrentSemanticsVersion, f.snapshot.Token, f.digest, f.proposal,
	))
}

func TestApplyConstructionProposalOwnerVanishReturnsTypedNotFound(t *testing.T) {
	controlApplyGetCalls := 0

	t.Run("control", func(t *testing.T) {
		fixture := newGroupFieldSourceProposalFixture(t)
		fixture.store.resetGetCalls()
		response := fixture.apply(t, "apply-coding-group-control")
		controlApplyGetCalls = fixture.store.getCalls
		t.Logf("control apply performed %d store.Get calls: #1 applyWorkspaceCommands owner read, #%d loadConstructionBase owner read", controlApplyGetCalls, controlApplyGetCalls)
		if controlApplyGetCalls < 2 {
			t.Fatalf("apply performed %d store.Get calls, want >= 2: the last must be the loadConstructionBase owner read, otherwise this reproduction never reaches construction_proposal.go:1452", controlApplyGetCalls)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("control apply status=%d body=%s, want 200 (proves the fixture reaches the group-field-source prepare path and applies)", response.StatusCode, response.Body)
		}
	})

	t.Run("fault", func(t *testing.T) {
		if controlApplyGetCalls < 2 {
			t.Fatalf("control subtest did not establish the loadConstructionBase Get position (got %d store.Get calls)", controlApplyGetCalls)
		}
		fixture := newGroupFieldSourceProposalFixture(t)
		fixture.store.faultGetOn = controlApplyGetCalls
		fixture.store.resetGetCalls()
		response := fixture.apply(t, "apply-coding-group-owner-vanish")
		if fixture.store.getCalls != controlApplyGetCalls {
			t.Fatalf("apply store.Get count drifted between copies: control=%d fault=%d", controlApplyGetCalls, fixture.store.getCalls)
		}
		if response.StatusCode != http.StatusNotFound || !strings.Contains(response.Body, "EXPLORER_NOT_FOUND") || !strings.Contains(response.Body, `"construction-capabilities"`) {
			t.Fatalf("apply after owner vanishes before loadConstructionBase must be typed 404 EXPLORER_NOT_FOUND: status=%d body=%s", response.StatusCode, response.Body)
		}
	})
}
