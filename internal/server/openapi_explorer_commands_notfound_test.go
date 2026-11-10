package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/gofiber/fiber/v3"
)

// countingExplorerStore wraps testExplorerStore and instruments the single
// Get method the Explorer Service reaches through for owner reads. faultGetOn
// (1-based within the current apply request) returns an identity-mismatched
// owner that loadConstructionBase classifies as typed notFound
// EXPLORER_NOT_FOUND (internal/explorer/lifecycle/construction_proposal.go:1451-1453).
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

// newGroupFieldSourceProposalFixture builds, through the real routes, a saved
// GROUP construction proposal whose candidate receipt carries compiled Group
// field sources (BINDING keys). That is the precondition for
// prepareConstructionProposal's group-field-source branch
// (internal/explorer/lifecycle/construction_proposal.go:1637-1638), which is
// the only apply-path branch that reaches loadConstructionBase.
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
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.RootResourceType = "Observation"
	document.Route = authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"}
	document.Rows = authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionExpanded, Expanded: &authoringv2.ExpandedRows{
		OccurrenceID: authoringv2.RootOccurrenceID, ScopePath: "component[].code.coding[]",
		EmptyCollectionPolicy: authoringv2.EmptyCollectionPreserveParent,
	}}
	document.Columns = []authoringv2.Column{}
	document.Output = authoringv2.Output{ID: "observations", Title: "Observations", RowLabel: "Observations"}
	document, err = authoringv2.UpgradeDocumentToConstruction(document)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0] = document
	workspace.Tabs[0].OutputID = "observations"
	workspace.Tabs[0].Title = "Observations"
	catalogSnapshot := authoringV2Catalog(snapshot, "observation-explorer")
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, catalogSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, catalogSnapshot).NormalizePresentationOrders()
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
	store := &countingExplorerStore{testExplorerStore: newTestExplorerStore()}
	if _, err := store.create(explorer.Explorer{
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
		CompileTarget: compiler.ArangoTarget(),
		Registry:      compilerTestRegistry{}, SourceRows: func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
			command := request.Command
			_, _, deriveErr := command.AQLParameters()
			if deriveErr != nil {
				return deriveErr
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var previewContractErr error
	config := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		RowChoicePlanner: rowChoices,
		ConstructionSourceStage: func(ctx context.Context, request lifecycle.ConstructionSourceStageRequest) (explorer.ReceiptConstructionStage, error) {
			return compileConstructionSourceStage(ctx, request, recipeEngine)
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		PreviewReceipt: func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
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
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"observations","stageId":"source_projection"}`,
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
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: stepID, Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
			ConstructionID: stepID, MissingKeyPolicy: authoringv2.ConstructionGroupMissingKeyGroup,
			Keys: []authoringv2.ConstructionGroupKey{{
				OutputColumnID: "status_group",
				FieldSource:    &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: scalarChoiceID},
			}, {
				OutputColumnID: "coding_identity",
				FieldSource:    &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: codingChoiceID},
			}},
			Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
		}},
		Outputs: []authoringv2.StageColumn{
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
	var resolved authoringv2.Construction
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
		commandID, authoringv2.CurrentSemanticsVersion, f.snapshot.Token, f.digest, f.proposal,
	))
}

// TestApplyConstructionProposalOwnerVanishReturnsTypedNotFound is a RED
// reproduction: applying an APPLY_CONSTRUCTION_PROPOSAL command whose saved
// candidate carries a compiled Group field source, while the owner read in
// loadConstructionBase (internal/explorer/lifecycle/construction_proposal.go:1447-1453)
// returns an identity-mismatched Explorer, must surface the typed lifecycle
// notFound EXPLORER_NOT_FOUND as HTTP 404 via the route's 404 arm
// (internal/server/openapi_explorer_routes.go). Before the AuthoringNotFound
// response was declared on this operation, the same fault produced
// HTTP 500 "applyExplorerBuilderCommands returned undocumented HTTP status 404".
// The control subtest proves the fixture reaches the group-field-source prepare
// path (200 with >=2 store.Get calls during apply: the applyWorkspaceCommands
// owner read plus the loadConstructionBase read); removing the 404 arm turns
// this test back red with that 500 signature.
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
