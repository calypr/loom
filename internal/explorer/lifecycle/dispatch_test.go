package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/rowdef"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestBuilderFullUIRowDefinitionProposalDispatchUsesRealService(t *testing.T) {
	stateID := "state:builder-full-ui-row-definition"
	modelState := builderFullUITestModelState(t, stateID, "rooted-source", recipe.ConstructionSourceProjectionID, nil)
	seedJSON, err := json.Marshal(map[string]any{"id": "fixture.row-definition.seed", "kind": "rooted_table_row_definition"})
	if err != nil {
		t.Fatal(err)
	}
	row := builderFullUIInventoryRow{CaseID: "row-definition-case", EntryID: "builder.row_definition.expanded_source_rows", ActionIndex: 0, RequestMode: "emitted", StateRefBefore: stateID, Before: modelState}
	prepared, err := prepareBuilderFullUISeed(row, builderFullUIInventoryManifestEntry{ID: row.EntryID, Seed: seedJSON})
	if err != nil {
		t.Fatalf("prepare row-definition seed: %v (%s)", err, prepared.Error)
	}
	harness, err := newBuilderFullUIHarnessFromPrepared(prepared)
	if err != nil {
		t.Fatal(err)
	}
	choices, err := harness.Service.OwnerServices().RowDefinition.ListRowDefinitionChoices(context.Background(), rowdef.RowDefinitionChoicesRequest{
		Project: prepared.Project, ExplorerID: prepared.ExplorerID, SnapshotToken: prepared.SnapshotToken, OutputID: prepared.OutputID,
	})
	if err != nil {
		t.Fatalf("actual ListRowDefinitionChoices: %v", err)
	}
	var expandedID string
	for _, choice := range choices.Choices {
		if choice.Kind == rowdef.RowChoiceExpanded {
			expandedID = choice.ChoiceID
			break
		}
	}
	if expandedID == "" {
		t.Fatalf("actual row-choice resolver returned no expanded choice: %#v", choices)
	}
	body, err := json.Marshal(map[string]any{
		"snapshotToken": prepared.SnapshotToken, "expectedDraftVersion": prepared.AcceptedVersion,
		"expectedDraftDigest": prepared.AcceptedDigest, "outputId": prepared.OutputID,
		"selection": rowdef.RowDefinitionSelection{Kind: rowdef.RowDefinitionSelectionExpanded, Expanded: &rowdef.ExpandedSelection{RowChoiceID: expandedID, EmptyCollectionPolicy: ws.EmptyCollectionPreserveParent}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := builderFullUIRequest{Kind: "rowDefinitionProposal", Method: "POST", Endpoint: "/row-definition-proposals", Body: body, BodyRaw: mustJSONRawString(t, body), HasBody: true}
	actualValue, layers, err := executeBuilderFullUIRequest(row, request, prepared, harness)
	if err != nil {
		t.Fatalf("dispatch exact row-definition request through Service.ProposeRowDefinition: %v", err)
	}
	actual, ok := actualValue.(builderFullUIActual)
	if !ok || actual.RowDefinitionProposal == nil || actual.RowDefinitionProposal.ProposalID == "" || actual.RowDefinitionProposal.Mode != rowdef.RowDefinitionSelectionExpanded {
		t.Fatalf("actual row-definition proposal response disposition=%q error=%#v proposal=%#v", actual.Disposition, actual.ServiceError, actual.RowDefinitionProposal)
	}
	if !reflect.DeepEqual(layers, []string{"lifecycle_analyzer_compiler", "preview_orchestration"}) || harness.Store.saveDraftCalls != 0 {
		t.Fatalf("row-definition request layers=%v saves=%d, want lifecycle+preview and no persistence", layers, harness.Store.saveDraftCalls)
	}
}

func TestBuilderFullUIEmptyRootCreationDispatchUsesRealService(t *testing.T) {
	stateID := "state:builder-full-ui-empty-root"
	modelState := builderFullUITestRootModelState(t, stateID)
	seedJSON, err := json.Marshal(map[string]any{"id": "fixture.first-table.seed", "kind": "first_table_creation_initial_state"})
	if err != nil {
		t.Fatal(err)
	}
	row := builderFullUIInventoryRow{CaseID: "first-table-case", EntryID: "builder.first_table_from_root", ActionIndex: 0, RequestMode: "emitted", StateRefBefore: stateID, Before: modelState}
	prepared, err := prepareBuilderFullUISeed(row, builderFullUIInventoryManifestEntry{ID: row.EntryID, Seed: seedJSON})
	if err != nil {
		t.Fatalf("prepare empty-root seed: %v (%s)", err, prepared.Error)
	}
	if prepared.Status != "PREPARED" || !isJSONNullOrEmpty(prepared.AcceptedWorkspace) || prepared.AcceptedVersion != 0 || prepared.OutputID != "" {
		t.Fatalf("root-uninitialized seed fabricated saved workspace identity: %#v", prepared)
	}
	var builderState ws.BuilderState
	if err := json.Unmarshal(prepared.BuilderState, &builderState); err != nil {
		t.Fatal(err)
	}
	if builderState.LifecycleState != ws.LifecycleNew || builderState.Workspace != nil {
		t.Fatalf("actual Builder root state=%#v, want new lifecycle without workspace", builderState)
	}
	harness, err := newBuilderFullUIHarnessFromPrepared(prepared)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(authoringv2.ApplyCommandsRequest{
		CommandID: "create-first-table", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: prepared.SnapshotToken, ExpectedDraftVersion: 0,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandCreateTable, Title: "Patients", Payload: &authoringv2.TablePayload{RootNodeID: "node-patient"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := builderFullUIRequest{Kind: "commands", Method: "POST", Endpoint: "/commands", Body: body, BodyRaw: mustJSONRawString(t, body), HasBody: true}
	actualValue, layers, err := executeBuilderFullUIRequest(row, request, prepared, harness)
	if err != nil {
		t.Fatalf("dispatch CREATE_TABLE through actual Service.ApplyCommands: %v", err)
	}
	actual, ok := actualValue.(builderFullUIActual)
	if !ok || actual.Applied == nil || len(actual.Applied.Workspace.Documents) != 1 || len(actual.Applied.Workspace.Tabs) != 1 {
		t.Fatalf("actual first-table command response=%#v", actualValue)
	}
	if harness.Store.saveDraftCalls != 1 || harness.Store.created.DraftVersion != 1 || len(harness.Store.created.DraftConfig) == 0 || !reflect.DeepEqual(layers, []string{"lifecycle_analyzer_compiler", "fake_store_persistence"}) {
		t.Fatalf("actual root creation save/layers=%d/%v version=%d workspaceBytes=%d", harness.Store.saveDraftCalls, layers, harness.Store.created.DraftVersion, len(harness.Store.created.DraftConfig))
	}
}

func TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint(t *testing.T) {
	noBodyEndpoint := "/api/v1/projects/p/explorers/e/authoring/v2/row-definition-choices?outputId=patients&limit=0"
	noBody, err := decodeBuilderFullUIRequest(json.RawMessage(fmt.Sprintf(`{"kind":"rowDefinitionChoices","method":"POST","endpoint":%q,"bodyRaw":null,"body":null}`, noBodyEndpoint)))
	if err != nil {
		t.Fatalf("decode explicit no-body request: %v", err)
	}
	if noBody.HasBody || string(noBody.BodyRaw) != "null" || string(noBody.Body) != "null" || noBody.Endpoint != noBodyEndpoint {
		t.Fatalf("explicit null body or concrete query/path was changed: %#v", noBody)
	}

	objectEndpoint := "/api/v1/projects/p/explorers/e/authoring/v2/suggestions?nodeId=node-1&query=gender"
	objectBody, err := decodeBuilderFullUIRequest(json.RawMessage(fmt.Sprintf(`{"kind":"suggestions","method":"GET","endpoint":%q,"bodyRaw":"{}","body":{}}`, objectEndpoint)))
	if err != nil {
		t.Fatalf("decode empty-object body independent of HTTP method: %v", err)
	}
	if !objectBody.HasBody || string(objectBody.BodyRaw) != `"{}"` || string(objectBody.Body) != "{}" || objectBody.Endpoint != objectEndpoint {
		t.Fatalf("empty-object body or concrete query/path was changed: %#v", objectBody)
	}

	emptyRawBody := json.RawMessage(`{"kind":"suggestions","method":"POST","endpoint":"/suggestions","bodyRaw":"","body":null}`)
	if _, err := decodeBuilderFullUIRequest(emptyRawBody); err == nil {
		t.Fatal(`bodyRaw:"" must remain distinct from an absent body represented by bodyRaw:null`)
	}
	missingBodyRaw := json.RawMessage(`{"kind":"suggestions","method":"POST","endpoint":"/suggestions","body":null}`)
	if _, err := decodeBuilderFullUIRequest(missingBodyRaw); err == nil {
		t.Fatal("missing bodyRaw must not be inferred from the HTTP method")
	}
}

func TestBuilderFullUIDispatchKindSeparatesCapturedAndSemanticNames(t *testing.T) {
	for _, test := range []struct{ captured, semantic string }{
		{captured: "constructionProposal", semantic: "construction-proposal"},
		{captured: "rowDefinitionProposal", semantic: "row-definition-proposal"},
		{captured: "rowDefinitionChoices", semantic: "row-definition-choices"},
		{captured: "explicitGroups", semantic: "explicit-groups"},
	} {
		if got := builderFullUIDispatchKind(test.captured); got != test.semantic {
			t.Errorf("captured UI request kind %q dispatch kind=%q, want semantic model kind %q", test.captured, got, test.semantic)
		}
	}
	if got := builderFullUIDispatchKind("unknownAdapterRequest"); got != "" {
		t.Fatalf("unknown UI request kind produced semantic dispatch kind %q", got)
	}
}

func executeBuilderFullUIRequest(row builderFullUIInventoryRow, request builderFullUIRequest, prepared builderFullUISeed, harness *builderFullUIHarness) (any, []string, error) {
	actual := builderFullUIActual{Kind: request.Kind}
	layers := []string{}
	ctx := context.Background()
	identity := prepared
	resultError := func(err error, reached ...string) (any, []string, error) {
		actual.Disposition = "SERVICE_ERROR"
		actual.ServiceError = lifecycleErrorObservation(err)
		if harness.Store != nil && harness.Store.created != nil {
			actual.AcceptedWorkspace = append(json.RawMessage(nil), harness.Store.created.DraftConfig...)
			actual.FakeStore = &builderFullUIFakeStoreObservation{SaveDraftCalls: harness.Store.saveDraftCalls, DraftVersion: harness.Store.created.DraftVersion, DraftDigest: harness.Store.created.DraftDigest}
		}
		return actual, append(layers, reached...), nil
	}
	attachAccepted := func() {
		actual.AcceptedWorkspace = append(json.RawMessage(nil), harness.Store.created.DraftConfig...)
		actual.FakeStore = &builderFullUIFakeStoreObservation{SaveDraftCalls: harness.Store.saveDraftCalls, DraftVersion: harness.Store.created.DraftVersion, DraftDigest: harness.Store.created.DraftDigest}
	}
	switch normalizeBuilderFullUIKind(request.Kind) {
	case "suggestions":
		var body struct {
			SnapshotToken string `json:"snapshotToken"`
			NodeID        string `json:"nodeId"`
			Query         string `json:"query"`
		}
		if err := json.Unmarshal(request.Body, &body); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: decode Suggestions request: %w", err)
		}
		value, err := harness.Service.Suggestions(ctx, SuggestionsRequest{Project: identity.Project, ExplorerID: identity.ExplorerID, SnapshotToken: body.SnapshotToken, NodeID: body.NodeID, Query: body.Query})
		layers = append(layers, "lifecycle_analyzer_compiler")
		if err != nil {
			return resultError(err)
		}
		actual.Disposition = "SUGGESTIONS"
		actual.Suggestions = &builderFullUIActualSuggestions{SnapshotToken: value.SnapshotToken, NodeID: value.NodeID, Candidates: value.Candidates}
		attachAccepted()
		return actual, layers, nil
	case "rowdefinitionchoices":
		parsed, parseErr := url.Parse(request.Endpoint)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: parse row-definition-choices endpoint: %w", parseErr)
		}
		query := parsed.Query()
		value, err := harness.Service.OwnerServices().RowDefinition.ListRowDefinitionChoices(ctx, rowdef.RowDefinitionChoicesRequest{
			Project: identity.Project, ExplorerID: identity.ExplorerID,
			SnapshotToken: query.Get("snapshotToken"), OutputID: query.Get("outputId"),
		})
		layers = append(layers, "lifecycle_analyzer_compiler")
		if err != nil {
			return resultError(err)
		}
		actual.Disposition = "ROW_DEFINITION_CHOICES"
		actual.RowDefinitionChoices = &value
		attachAccepted()
		return actual, layers, nil
	case "constructionproposal":
		var body struct {
			ExpectedDraftDigest   string                                           `json:"expectedDraftDigest"`
			ExpectedDraftVersion  int64                                            `json:"expectedDraftVersion"`
			SnapshotToken         string                                           `json:"snapshotToken"`
			OutputID              string                                           `json:"outputId"`
			ChangedStepID         *string                                          `json:"changedStepId"`
			RemoveStepIDs         *[]string                                        `json:"removeStepIds"`
			CandidateConstruction json.RawMessage                                  `json:"candidateConstruction"`
			GroupSource           *construction.ConstructionGroupSourceSelection   `json:"groupSource"`
			GroupSources          *[]construction.ConstructionGroupSourceSelection `json:"groupSources"`
			Limit                 *int                                             `json:"limit"`
		}
		if err := json.Unmarshal(request.Body, &body); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: construction proposal request body is not decodable: %w", err)
		}
		if len(body.CandidateConstruction) == 0 || isJSONNullOrEmpty(body.CandidateConstruction) {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: construction proposal body omits candidateConstruction")
		}
		var candidate ws.Construction
		if err := json.Unmarshal(body.CandidateConstruction, &candidate); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: decode candidateConstruction: %w", err)
		}
		requestValue := construction.ConstructionProposalRequest{
			Project: identity.Project, ExplorerID: identity.ExplorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputID, CandidateConstruction: candidate,
		}
		if body.ChangedStepID != nil {
			requestValue.ChangedStepID = *body.ChangedStepID
		}
		if body.RemoveStepIDs != nil {
			requestValue.RemoveStepIDs = append([]string(nil), (*body.RemoveStepIDs)...)
		}
		if body.GroupSource != nil {
			groupSource := *body.GroupSource
			requestValue.GroupSource = &groupSource
		}
		if body.GroupSources != nil {
			requestValue.GroupSources = append([]construction.ConstructionGroupSourceSelection(nil), (*body.GroupSources)...)
		}
		if body.Limit != nil {
			requestValue.Limit = *body.Limit
		}
		layers = append(layers, "lifecycle_analyzer_compiler")
		proposal, err := harness.Service.OwnerServices().Construction.ProposeConstruction(ctx, requestValue)
		if err != nil {
			return resultError(err)
		}
		actual.Proposal, actual.CompiledReceipt = &proposal, actualReceipt(harness.Store.receipt)
		actual.Disposition = proposal.PreviewStatus
		if proposal.PreviewStatus == "PREVIEW_PENDING" && proposal.ProposalID != "" {
			preview, previewErr := harness.Service.OwnerServices().Core.Preview(ctx, authoringcore.PreviewRequest{
				Project: identity.Project, ExplorerID: identity.ExplorerID, ReceiptID: proposal.ProposalID, OutputID: proposal.OutputID,
				SinkFactory: func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
					return func(map[string]any) error { return nil }, nil
				},
			})
			layers = append(layers, "preview_orchestration")
			if previewErr != nil {
				return resultError(previewErr, "preview_orchestration")
			}
			harness.LastPreview = &preview
			actual.Preview = &builderFullUIPreviewObservation{ReceiptID: preview.Receipt.ID, OutputID: proposal.OutputID, Columns: emittedPublicColumnsForOutput(preview.Receipt, proposal.OutputID), DataExecutionObserved: false}
			actual.Disposition = "PREVIEWED_INTERNAL_PENDING"
		}
		attachAccepted()
		return actual, layers, nil
	case "rowdefinitionproposal":
		var body struct {
			SnapshotToken        string                        `json:"snapshotToken"`
			ExpectedDraftVersion int64                         `json:"expectedDraftVersion"`
			ExpectedDraftDigest  string                        `json:"expectedDraftDigest"`
			OutputID             string                        `json:"outputId"`
			Selection            rowdef.RowDefinitionSelection `json:"selection"`
			Limit                int                           `json:"limit"`
		}
		if err := json.Unmarshal(request.Body, &body); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: decode rowdef.RowDefinitionProposal request: %w", err)
		}
		layers = append(layers, "lifecycle_analyzer_compiler")
		proposal, err := harness.Service.OwnerServices().RowDefinition.ProposeRowDefinition(ctx, rowdef.RowDefinitionProposalRequest{
			Project: identity.Project, ExplorerID: identity.ExplorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputID, Selection: body.Selection, Limit: body.Limit,
		})
		if err != nil {
			return resultError(err)
		}
		actual.RowDefinitionProposal = &proposal
		actual.Disposition = "ROW_DEFINITION_PROPOSED"
		attachAccepted()
		return actual, append(layers, "preview_orchestration"), nil
	case "commands":
		var command authoringv2.ApplyCommandsRequest
		if err := json.Unmarshal(request.Body, &command); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: decode ApplyCommands request: %w", err)
		}
		layers = append(layers, "lifecycle_analyzer_compiler")
		applied, err := harness.Service.ApplyCommands(ctx, identity.Project, identity.ExplorerID, command, "builder-full-ui-replay")
		if err != nil {
			return resultError(err)
		}
		actual.Applied = applied
		actual.Disposition = "APPLIED"
		actual.AcceptedWorkspace = append(json.RawMessage(nil), harness.Store.created.DraftConfig...)
		actual.FakeStore = &builderFullUIFakeStoreObservation{SaveDraftCalls: harness.Store.saveDraftCalls, DraftVersion: harness.Store.created.DraftVersion, DraftDigest: harness.Store.created.DraftDigest}
		return actual, append(layers, "fake_store_persistence"), nil
	case "reconcile":
		var body struct {
			SnapshotToken string `json:"snapshotToken"`
			DraftVersion  int64  `json:"draftVersion"`
			DraftDigest   string `json:"draftDigest"`
			OutputID      string `json:"outputId"`
		}
		if err := json.Unmarshal(request.Body, &body); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: decode Reconcile request: %w", err)
		}
		layers = append(layers, "lifecycle_analyzer_compiler")
		reconciled, err := harness.Service.ReconcileWithWorkspace(ctx, ReconcileRequest{Project: identity.Project, ExplorerID: identity.ExplorerID, SnapshotToken: body.SnapshotToken, DraftVersion: body.DraftVersion, DraftDigest: body.DraftDigest, OutputID: body.OutputID})
		if err != nil {
			return resultError(err)
		}
		actual.Disposition, actual.CompiledReceipt = "RECONCILED", actualReceipt(reconciled.Receipt)
		attachAccepted()
		return actual, layers, nil
	case "preview":
		var body struct {
			ReceiptID string `json:"receiptId"`
			OutputID  string `json:"outputId"`
			Limit     *int   `json:"limit"`
		}
		if err := json.Unmarshal(request.Body, &body); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: decode preview request: %w", err)
		}
		limit := 0
		if body.Limit != nil {
			limit = *body.Limit
		}
		preview, err := harness.Service.OwnerServices().Core.Preview(ctx, authoringcore.PreviewRequest{Project: identity.Project, ExplorerID: identity.ExplorerID, ReceiptID: body.ReceiptID, OutputID: body.OutputID, Limit: limit, SinkFactory: func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		}})
		layers = append(layers, "preview_orchestration")
		if err != nil {
			return resultError(err)
		}
		actual.Disposition = "PREVIEWED"
		actual.CompiledReceipt = actualReceipt(preview.Receipt)
		actual.Preview = &builderFullUIPreviewObservation{ReceiptID: preview.Receipt.ID, OutputID: body.OutputID, Columns: emittedPublicColumnsForOutput(preview.Receipt, body.OutputID), DataExecutionObserved: false}
		attachAccepted()
		return actual, layers, nil
	case "constructioncapabilities":
		var body struct {
			SnapshotToken        string `json:"snapshotToken"`
			ExpectedDraftVersion int64  `json:"expectedDraftVersion"`
			ExpectedDraftDigest  string `json:"expectedDraftDigest"`
			OutputID             string `json:"outputId"`
			StageID              string `json:"stageId"`
			GroupStepID          string `json:"groupStepId"`
		}
		if err := json.Unmarshal(request.Body, &body); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: decode ConstructionCapabilities request: %w", err)
		}
		value, err := harness.Service.OwnerServices().Construction.GetConstructionCapabilities(ctx, construction.ConstructionCapabilitiesRequest{Project: identity.Project, ExplorerID: identity.ExplorerID, SnapshotToken: body.SnapshotToken, ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest, OutputID: body.OutputID, StageID: body.StageID, GroupStepID: body.GroupStepID})
		layers = append(layers, "lifecycle_analyzer_compiler")
		if err != nil {
			return resultError(err)
		}
		actual.Disposition = "CAPABILITIES"
		actual.Capabilities = &value
		attachAccepted()
		return actual, layers, nil
	default:
		return nil, nil, fmt.Errorf("GAP: no lifecycle adapter for emitted request kind=%q endpoint=%q method=%q", request.Kind, request.Endpoint, request.Method)
	}
}

func builderFullUIDispatchKind(kind string) string {
	switch normalizeBuilderFullUIKind(kind) {
	case "suggestions":
		return "suggestions"
	case "commands":
		return "commands"
	case "reconcile":
		return "reconcile"
	case "preview":
		return "preview"
	case "constructionproposal":
		return "construction-proposal"
	case "explicitgroups":
		return "explicit-groups"
	case "rowdefinitionproposal":
		return "row-definition-proposal"
	case "rowdefinitionchoices":
		return "row-definition-choices"
	default:
		return ""
	}
}

func builderFullUISerializedBody(request builderFullUIRequest) (string, bool, error) {
	if isJSONNullOrEmpty(request.BodyRaw) {
		return "", false, nil
	}
	var bodyRaw string
	if err := json.Unmarshal(request.BodyRaw, &bodyRaw); err != nil {
		return "", false, fmt.Errorf("request.bodyRaw must be null or a serialized body string: %w", err)
	}
	return bodyRaw, true, nil
}
