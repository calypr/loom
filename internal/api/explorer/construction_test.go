package explorerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	constructionpkg "github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	receiptapi "github.com/calypr/loom/internal/explorer/receipt"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
	"github.com/gofiber/fiber/v3"
)

func TestConstructionCodedGroupOpenAPIDTOPreservesPayloadAndChoices(t *testing.T) {
	requestJSON := []byte(`{"candidateConstruction":{"version":1,"steps":[{"id":"group_codes","inputs":[{"kind":"SOURCE_PROJECTION"}],"operation":{"kind":"CODED_GROUP","codedGroup":{"constructionId":"group_codes","choiceId":"fresh-choice","source":{"occurrenceId":"base","resourceType":"BodyStructure","codingPath":"includedStructure[].structure.coding[]","fhirType":"Coding","cardinality":"MANY","shape":"ARRAY","route":[]},"missingKeyPolicy":"GROUP","systemOutputColumnId":"system","versionOutputColumnId":"version","codeOutputColumnId":"code","distinctSourceCountOutputColumnId":"count"}},"outputs":[]}]}}`)
	var request loomapi.ConstructionProposalRequest
	if err := json.Unmarshal(requestJSON, &request); err != nil {
		t.Fatal(err)
	}
	construction, err := directAuthoringJSON[ws.Construction](request.CandidateConstruction)
	if err != nil {
		t.Fatal(err)
	}
	if len(construction.Steps) != 1 || construction.Steps[0].Operation.CodedGroup == nil ||
		construction.Steps[0].Operation.CodedGroup.Source.CodingPath != "includedStructure[].structure.coding[]" ||
		construction.Steps[0].Operation.CodedGroup.ChoiceID != "fresh-choice" {
		t.Fatalf("OpenAPI request conversion dropped CODED_GROUP payload: %#v", construction)
	}

	stage := receiptmodel.ReceiptConstructionStage{
		ID: recipe.ConstructionSourceProjectionID,
		CodedGroupChoices: []receiptmodel.ReceiptConstructionCodedGroupChoice{{
			ChoiceID: "fresh-choice", OccurrenceID: "base", ResourceType: "BodyStructure",
			CodingPath: "includedStructure[].structure.coding[]", Label: "BodyStructure · includedStructure[] › structure › coding[]",
		}},
	}
	response, err := directAuthoringJSON[loomapi.ConstructionCapabilitiesResponse](constructionpkg.ConstructionCapabilitiesResponse{
		Stages: []receiptmodel.ReceiptConstructionStage{stage}, SelectedStage: stage,
		SourceProjectionAvailability: &constructionpkg.ConstructionSourceProjectionAvailability{
			Supported: true, StageID: recipe.ConstructionSourceProjectionID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.SelectedStage.CodedGroupChoices == nil || len(*response.SelectedStage.CodedGroupChoices) != 1 ||
		(*response.SelectedStage.CodedGroupChoices)[0].CodingPath != "includedStructure[].structure.coding[]" {
		t.Fatalf("OpenAPI response conversion dropped codedGroupChoices: %#v", response.SelectedStage)
	}
	if response.SourceProjectionAvailability == nil || !response.SourceProjectionAvailability.Supported ||
		response.SourceProjectionAvailability.StageId != recipe.ConstructionSourceProjectionID {
		t.Fatalf("OpenAPI response conversion dropped stage-bound source projection availability: %#v", response.SourceProjectionAvailability)
	}
	unsupported, err := directAuthoringJSON[loomapi.ConstructionCapabilitiesResponse](constructionpkg.ConstructionCapabilitiesResponse{
		SourceProjectionAvailability: &constructionpkg.ConstructionSourceProjectionAvailability{
			StageID: "group_stage", ReasonCode: "ROOT_SOURCE_PROJECTION_UNAVAILABLE", Reason: "root fields are not carried to this stage",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if unsupported.SourceProjectionAvailability == nil || unsupported.SourceProjectionAvailability.Supported ||
		unsupported.SourceProjectionAvailability.StageId != "group_stage" ||
		unsupported.SourceProjectionAvailability.ReasonCode == nil || *unsupported.SourceProjectionAvailability.ReasonCode != "ROOT_SOURCE_PROJECTION_UNAVAILABLE" ||
		unsupported.SourceProjectionAvailability.Reason == nil || *unsupported.SourceProjectionAvailability.Reason != "root fields are not carried to this stage" {
		t.Fatalf("OpenAPI response conversion dropped root-projection unavailability: %#v", unsupported.SourceProjectionAvailability)
	}
}

func TestConstructionColumnPopulationHTTPRouteMapsMalformedBody(t *testing.T) {
	store, err := explorerfixtures.NewService(explorerfixtures.NewStore())
	if err != nil {
		t.Fatal(err)
	}
	application, err := lifecycle.New(store, lifecycle.Config{})
	if err != nil {
		t.Fatal(err)
	}
	handlers := &Handlers{
		application:   application,
		authorizeRead: func(context.Context, *authscope.Principal, string) error { return nil },
	}
	response, err := handlers.GetExplorerConstructionColumnPopulation(context.Background(), loomapi.GetExplorerConstructionColumnPopulationRequestObject{
		Project: loomapi.Project("project-a"), ExplorerId: loomapi.ExplorerId("explorer-a"),
		Body: &loomapi.ConstructionColumnPopulationRequest{SnapshotToken: "snapshot", ExpectedDraftDigest: "digest", OutputId: "patients", StageId: "source_projection", ColumnIds: []string{"column"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := response.(loomapi.GetExplorerConstructionColumnPopulation400JSONResponse); !ok {
		t.Fatalf("malformed request response = %T, want HTTP 400", response)
	}
}

func TestBuilderHTTPSerializesEmptyConstructionStepsAsArray(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.Columns = []ws.Column{}
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion}
	workspace.Documents[0] = document
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(draft, []byte(`"steps":[]`)) {
		t.Fatalf("test draft did not contain canonical empty steps: %s", draft)
	}

	draft = bytes.Replace(draft, []byte(`"steps":[]`), []byte(`"steps":null`), 1)
	legacyDigest, err := workspace.LegacyNilConstructionStepsDigest()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if legacyDigest == "" || legacyDigest == digest {
		t.Fatalf("legacy and current construction digests should differ: legacy=%q current=%q", legacyDigest, digest)
	}
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: "Patients",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: legacyDigest,
	}); err != nil {
		t.Fatal(err)
	}

	migration, err := ws.MigrateStoredWorkspace(draft, ws.CatalogFromCapability(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	if migration.MigratedCanonicalDigest != digest {
		t.Fatal("offline migration changed expected canonical digest")
	}
	owner, err := store.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	owner.DraftConfig = migration.CanonicalJSON
	owner.DraftDigest = migration.MigratedCanonicalDigest
	if _, err := store.SaveDraft(context.Background(), *owner, 1, legacyDigest); err != nil {
		t.Fatal(err)
	}
	service, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	config := lifecycle.Config{Capability: ports.CapabilityResolver{
		Current: func(context.Context, string, string, string) (capability.Snapshot, error) {
			return snapshot, nil
		},
		ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		},
		Catalog: ws.CatalogFromCapability,
	}, ConstructionSourceStage: func(context.Context, ports.ConstructionSourceStageRequest) (receiptmodel.ReceiptConstructionStage, error) {
		return receiptmodel.ReceiptConstructionStage{
			ID: "source_projection", RowIdentityColumn: "__loom_row_id",
			Columns: []receiptmodel.ReceiptConstructionStageColumn{}, Capabilities: []receiptmodel.ReceiptConstructionOperationChoice{},
		}, nil
	}}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"
	response := requestJSON(t, app, http.MethodGet, basePath+"/builder", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("builder status=%d body=%s", response.StatusCode, response.Body)
	}
	var state loomapi.BuilderState
	if err := json.Unmarshal([]byte(response.Body), &state); err != nil {
		t.Fatalf("decode BuilderState: %v; body=%s", err, response.Body)
	}
	if state.Workspace == nil || len(state.Workspace.Documents) != 1 || state.Workspace.Documents[0].Construction == nil {
		t.Fatalf("builder omitted blank table construction: %#v", state.Workspace)
	}
	steps := state.Workspace.Documents[0].Construction.Steps
	if steps == nil || len(steps) != 0 {
		t.Fatalf("builder returned construction steps %#v, want non-nil empty slice; body=%s", steps, response.Body)
	}
	if state.DraftVersion != 2 || state.DraftDigest == legacyDigest || state.DraftDigest != digest {
		t.Fatalf("builder did not return the migrated durable draft version/digest: version=%d digest=%q legacy=%q current=%q", state.DraftVersion, state.DraftDigest, legacyDigest, digest)
	}
	stored, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if stored.DraftVersion != state.DraftVersion || stored.DraftDigest != state.DraftDigest || !bytes.Contains(stored.DraftConfig, []byte(`"steps":[]`)) || bytes.Contains(stored.DraftConfig, []byte(`"steps":null`)) {
		t.Fatalf("stored draft was not durably normalized: version=%d digest=%q draft=%s", stored.DraftVersion, stored.DraftDigest, stored.DraftConfig)
	}
	capabilitiesHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection","includeGroupChoices":true}`,
		snapshot.Token, state.DraftVersion, state.DraftDigest,
	))
	if capabilitiesHTTP.StatusCode != http.StatusOK {
		t.Fatalf("construction capabilities after Builder load status=%d body=%s", capabilitiesHTTP.StatusCode, capabilitiesHTTP.Body)
	}
	var capabilities loomapi.ConstructionCapabilitiesResponse
	if err := json.Unmarshal([]byte(capabilitiesHTTP.Body), &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.SelectedStage.Id != "source_projection" || int64(capabilities.DraftVersion) != state.DraftVersion || capabilities.DraftDigest != state.DraftDigest {
		t.Fatalf("capabilities did not accept the exact migrated Builder state: %#v", capabilities)
	}
}

func TestEmptyConstructionBootstrapAddFirstColumnAndProposeOperation(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Nodes = []capability.Node{{ID: "n_observation", ResourceType: "Observation", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 1}}
	snapshot.Candidates = []capability.Candidate{{
		ID: "c_observation_status", NodeID: "n_observation", ResourceType: "Observation",
		FieldPath: "status", Label: "Status", LogicalType: "string", Cardinality: "OPTIONAL_ONE",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}}
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.RootResourceType = "Observation"
	document.Route.ResourceType = "Observation"
	document.Columns = nil
	document.Construction = nil
	workspace.Documents[0] = document
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: "Observations",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
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
			return receiptapi.Compile(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		ConstructionSourceStage: func(ctx context.Context, request ports.ConstructionSourceStageRequest) (receiptmodel.ReceiptConstructionStage, error) {
			return receiptapi.CompileConstructionSourceStage(ctx, request, recipeEngine)
		},
		PreviewReceipt: func(_ context.Context, receipt *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if receipt == nil || len(receipt.EmittedColumns) != 1 {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("expected one exact candidate output column")
			}
			column := receipt.EmittedColumns[0]
			if err := visit(map[string]any{column.PublicColumn: "final"}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{column.PublicColumn}, RowCount: 1, Complete: true}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"

	capabilitiesHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection","includeGroupChoices":true}`,
		snapshot.Token, digest,
	))
	if capabilitiesHTTP.StatusCode != http.StatusOK {
		t.Fatalf("empty source capabilities status=%d body=%s", capabilitiesHTTP.StatusCode, capabilitiesHTTP.Body)
	}
	var capabilities loomapi.ConstructionCapabilitiesResponse
	if err := json.Unmarshal([]byte(capabilitiesHTTP.Body), &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.SelectedStage.Id != "source_projection" || capabilities.SelectedStage.RowIdentityColumn == nil || *capabilities.SelectedStage.RowIdentityColumn == "" || len(capabilities.SelectedStage.Columns) != 0 {
		t.Fatalf("empty source descriptor is not compiler-resolved: %#v", capabilities.SelectedStage)
	}
	for _, operation := range capabilities.SelectedStage.Capabilities {
		if string(operation.Kind) == "RELATED_SOURCE" {
			if !operation.Supported {
				t.Fatalf("hidden root identity should support related-source addition: %#v", operation)
			}
			continue
		}
		if string(operation.Kind) == "RELATED_EXPAND" {
			if !operation.Supported {
				t.Fatalf("hidden root key should support related expansion: %#v", operation)
			}
			continue
		}
		if string(operation.Kind) == "RELATED_ELIGIBILITY" {
			if !operation.Supported {
				t.Fatalf("hidden root key should support related eligibility: %#v", operation)
			}
			continue
		}
		if string(operation.Kind) == "CODED_PIVOT" {
			if !operation.Supported {
				t.Fatalf("coded pivot should be available before adding source columns: %#v", operation)
			}
			continue
		}
		if operation.Supported {
			t.Fatalf("zero-column source unexpectedly supports %s: %#v", operation.Kind, operation)
		}
	}

	choice, err := capability.NewFieldConstructionChoice(snapshot.Token, snapshot.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	addHTTP := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"bootstrap-add-first-column","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_CHOICE","outputId":"patients","constructionChoice":{"choiceId":%q,"form":"VALUE"}}]}`,
		ws.CurrentSemanticsVersion, snapshot.Token, digest, choice.ChoiceID,
	))
	if addHTTP.StatusCode != http.StatusOK {
		t.Fatalf("add first source column status=%d body=%s", addHTTP.StatusCode, addHTTP.Body)
	}
	updated, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = ws.DecodeWorkspace(updated.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Documents[0].Columns) != 1 {
		t.Fatalf("first Add persisted %d columns (draft version %d): %s", len(workspace.Documents[0].Columns), updated.DraftVersion, addHTTP.Body)
	}
	if workspace.Documents[0].Columns[0].ColumnID == "" {
		t.Fatal("first Add persisted a source column without stable columnId")
	}
	digest = updated.DraftDigest
	capabilitiesHTTP = requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection","includeGroupChoices":true}`,
		snapshot.Token, updated.DraftVersion, digest,
	))
	if capabilitiesHTTP.StatusCode != http.StatusOK {
		t.Fatalf("post-Add source capabilities status=%d body=%s", capabilitiesHTTP.StatusCode, capabilitiesHTTP.Body)
	}
	if err := json.Unmarshal([]byte(capabilitiesHTTP.Body), &capabilities); err != nil {
		t.Fatal(err)
	}
	if len(capabilities.SelectedStage.Columns) != 1 || capabilities.SelectedStage.Columns[0].Id != workspace.Documents[0].Columns[0].ColumnID {
		t.Fatalf("compiled source schema lost the added column ID: %#v", capabilities.SelectedStage)
	}
	column := workspace.Documents[0].Columns[0]
	candidate := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "keep_observation_status", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: column.ColumnID, Operator: ws.ConstructionFilterExists}},
		Outputs:   []ws.StageColumn{{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType}},
	}}}
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	proposalHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"keep_observation_status","candidateConstruction":%s}`,
		snapshot.Token, updated.DraftVersion, digest, candidateJSON,
	))
	if proposalHTTP.StatusCode != http.StatusOK {
		t.Fatalf("first typed proposal status=%d body=%s", proposalHTTP.StatusCode, proposalHTTP.Body)
	}
	var proposal loomapi.ConstructionProposalResponse
	if err := json.Unmarshal([]byte(proposalHTTP.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalId == nil || proposal.PreviewStatus != "READY" || proposal.Preview == nil || proposal.Preview.RowCount != 1 {
		t.Fatalf("first typed proposal did not complete preview: %#v", proposal)
	}
	applyHTTP := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"apply-first-operation","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		ws.CurrentSemanticsVersion, snapshot.Token, updated.DraftVersion, digest, *proposal.ProposalId,
	))
	if applyHTTP.StatusCode != http.StatusOK {
		t.Fatalf("apply first typed operation status=%d body=%s", applyHTTP.StatusCode, applyHTTP.Body)
	}
	updated, err = service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = ws.DecodeWorkspace(updated.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Documents[0].Construction.Steps) != 1 || workspace.Documents[0].Construction.Steps[0].ID != "keep_observation_status" {
		t.Fatalf("proposal did not atomically install its typed step: %#v", workspace.Documents[0].Construction)
	}
}
