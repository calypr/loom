package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestApplyConstructionProposalReturnsExactPreviewOnlyAfterSave(t *testing.T) {
	app, service, snapshot, draftDigest, previewCalls := applyConstructionPreviewRouteFixture(t, false, false, false)
	basePath := "/api/v1/projects/project-a/explorers/apply-preview/authoring/v2"
	proposalID := proposeConstructionRemovalForApplyPreview(t, app, basePath, snapshot, draftDigest)
	applyBody := fmt.Sprintf(
		`{"commandId":"apply-preview-exact","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, draftDigest, proposalID,
	)

	applyHTTP := requestJSON(t, app, http.MethodPost, basePath+"/commands", applyBody)
	if applyHTTP.StatusCode != http.StatusOK {
		t.Fatalf("apply construction proposal status=%d body=%s", applyHTTP.StatusCode, applyHTTP.Body)
	}
	var applied loomapi.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(applyHTTP.Body), &applied); err != nil {
		t.Fatal(err)
	}
	accepted := applied.AcceptedConstruction
	if accepted == nil {
		t.Fatalf("successful construction Apply omitted acceptedConstruction: %s", applyHTTP.Body)
	}
	if accepted.Receipt.ReceiptId != proposalID || accepted.Receipt.IntentDigest != applied.DraftDigest || accepted.Receipt.SnapshotToken != snapshot.Token {
		t.Fatalf("accepted receipt identity = %#v; want proposal %q, saved digest %q, snapshot %q", accepted.Receipt, proposalID, applied.DraftDigest, snapshot.Token)
	}
	if len(accepted.Receipt.Outputs) != 1 || accepted.Receipt.Outputs[0].OutputId != "patients" || len(accepted.Receipt.Outputs[0].Columns) != 1 {
		t.Fatalf("accepted receipt output contract = %#v", accepted.Receipt.Outputs)
	}
	if accepted.Receipt.Outputs[0].Columns[0].Column != "c_patient" || accepted.Receipt.Outputs[0].Columns[0].Label == "" {
		t.Fatalf("accepted receipt lost emitted output column metadata: %#v", accepted.Receipt.Outputs[0].Columns)
	}
	if accepted.Preview.ReceiptId != proposalID || accepted.Preview.OutputId != "patients" || accepted.PreviewLimit != 7 {
		t.Fatalf("accepted preview identity = %#v, preview limit=%d", accepted.Preview, accepted.PreviewLimit)
	}
	if len(accepted.Preview.Columns) != 1 || accepted.Preview.Columns[0].Column != "c_patient" || accepted.Preview.Columns[0].Label == "" {
		t.Fatalf("accepted preview lost normal emitted column metadata: %#v", accepted.Preview.Columns)
	}
	if len(accepted.Preview.Rows) != 1 || accepted.Preview.Rows[0]["c_patient"] != "patient-1" || accepted.Preview.Sampled {
		t.Fatalf("accepted preview rows/sampling = %#v / %v", accepted.Preview.Rows, accepted.Preview.Sampled)
	}
	if accepted.Preview.PartialValidation == nil || !*accepted.Preview.PartialValidation {
		t.Fatalf("accepted preview lost partial-validation metadata: %#v", accepted.Preview)
	}
	if len(accepted.Preview.RowSources) != 1 || accepted.Preview.RowSources[0].Kind != loomapi.PreviewRowSourceKindSINGLE ||
		accepted.Preview.RowSources[0].ResourceType == nil || *accepted.Preview.RowSources[0].ResourceType != "Patient" ||
		accepted.Preview.RowSources[0].Id == nil || *accepted.Preview.RowSources[0].Id != "patient-1" {
		t.Fatalf("accepted preview source metadata = %#v", accepted.Preview.RowSources)
	}
	if accepted.Preview.RowLineageCapability.Status != loomapi.RowLineageCapabilityStatusAVAILABLE {
		t.Fatalf("accepted preview lost row-lineage capability metadata: %#v", accepted.Preview.RowLineageCapability)
	}
	var acceptedJSON struct {
		Receipt map[string]json.RawMessage `json:"receipt"`
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(applyHTTP.Body), &envelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(envelope["acceptedConstruction"], &acceptedJSON); err != nil {
		t.Fatal(err)
	}
	if _, hasBuilder := acceptedJSON.Receipt["builder"]; hasBuilder {
		t.Fatalf("accepted receipt duplicated the saved workspace: %s", acceptedJSON.Receipt["builder"])
	}
	if strings.Contains(applyHTTP.Body, "constructionProposal") || strings.Contains(applyHTTP.Body, "previewLimitBinding") {
		t.Fatalf("private proposal binding leaked into Apply response: %s", applyHTTP.Body)
	}
	if *previewCalls != 2 {
		t.Fatalf("preview calls after proposal and Apply = %d, want exactly 2", *previewCalls)
	}
	stored, err := service.Get(context.Background(), "project-a", "apply-preview")
	if err != nil {
		t.Fatal(err)
	}
	if stored.DraftVersion != applied.DraftVersion || stored.DraftDigest != applied.DraftDigest || applied.DraftVersion != 2 {
		t.Fatalf("accepted preview did not accompany the saved CAS: stored=%#v response=(%d,%q)", stored, applied.DraftVersion, applied.DraftDigest)
	}

	replayHTTP := requestJSON(t, app, http.MethodPost, basePath+"/commands", applyBody)
	if replayHTTP.StatusCode != http.StatusOK {
		t.Fatalf("replay construction proposal status=%d body=%s", replayHTTP.StatusCode, replayHTTP.Body)
	}
	var replay loomapi.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(replayHTTP.Body), &replay); err != nil {
		t.Fatal(err)
	}
	if replay.AcceptedConstruction != nil {
		t.Fatalf("idempotent command replay retained an old accepted preview: %#v", replay.AcceptedConstruction)
	}
	if *previewCalls != 2 {
		t.Fatalf("command replay re-executed preview: calls=%d", *previewCalls)
	}
}

func TestApplyConstructionProposalCaptureFailureKeepsSaveAndOmitsAcceptedPreview(t *testing.T) {
	app, service, snapshot, draftDigest, previewCalls := applyConstructionPreviewRouteFixture(t, true, false, false)
	basePath := "/api/v1/projects/project-a/explorers/apply-preview/authoring/v2"
	proposalID := proposeConstructionRemovalForApplyPreview(t, app, basePath, snapshot, draftDigest)
	applyHTTP := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"apply-preview-capture-failure","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, draftDigest, proposalID,
	))
	if applyHTTP.StatusCode != http.StatusOK {
		t.Fatalf("Apply with unavailable response capture status=%d body=%s", applyHTTP.StatusCode, applyHTTP.Body)
	}
	var applied loomapi.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(applyHTTP.Body), &applied); err != nil {
		t.Fatal(err)
	}
	if applied.AcceptedConstruction != nil {
		t.Fatalf("capture failure returned a partial accepted preview: %#v", applied.AcceptedConstruction)
	}
	if *previewCalls != 2 {
		t.Fatalf("capture failure skipped Apply-time validation preview: calls=%d", *previewCalls)
	}
	stored, err := service.Get(context.Background(), "project-a", "apply-preview")
	if err != nil {
		t.Fatal(err)
	}
	if stored.DraftVersion != 2 || stored.DraftDigest != applied.DraftDigest {
		t.Fatalf("response capture failure changed Apply save semantics: stored=%#v response=%#v", stored, applied)
	}
}

func TestApplyConstructionProposalCapturesEmptyPreviewAsEmptyRowsArray(t *testing.T) {
	app, service, snapshot, draftDigest, previewCalls := applyConstructionPreviewRouteFixture(t, false, true, false)
	basePath := "/api/v1/projects/project-a/explorers/apply-preview/authoring/v2"
	proposalID := proposeConstructionRemovalForApplyPreview(t, app, basePath, snapshot, draftDigest)
	applyHTTP := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"apply-preview-empty","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, draftDigest, proposalID,
	))
	if applyHTTP.StatusCode != http.StatusOK {
		t.Fatalf("Apply with empty validation preview status=%d body=%s", applyHTTP.StatusCode, applyHTTP.Body)
	}
	var applied loomapi.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(applyHTTP.Body), &applied); err != nil {
		t.Fatal(err)
	}
	if applied.AcceptedConstruction == nil || applied.AcceptedConstruction.Preview.Rows == nil || len(applied.AcceptedConstruction.Preview.Rows) != 0 {
		t.Fatalf("empty Apply preview must be captured as rows: []: %#v", applied.AcceptedConstruction)
	}
	if *previewCalls != 2 {
		t.Fatalf("empty accepted preview call count = %d, want proposal plus Apply validation", *previewCalls)
	}
	stored, err := service.Get(context.Background(), "project-a", "apply-preview")
	if err != nil {
		t.Fatal(err)
	}
	if stored.DraftVersion != applied.DraftVersion || stored.DraftDigest != applied.DraftDigest {
		t.Fatalf("empty accepted preview did not accompany saved CAS: stored=%#v response=%#v", stored, applied)
	}
}

func TestApplyConstructionProposalOverCapCaptureKeepsValidationAndSave(t *testing.T) {
	app, service, snapshot, draftDigest, previewCalls := applyConstructionPreviewRouteFixture(t, false, false, true)
	basePath := "/api/v1/projects/project-a/explorers/apply-preview/authoring/v2"
	proposalID := proposeConstructionRemovalForApplyPreview(t, app, basePath, snapshot, draftDigest)
	applyHTTP := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"apply-preview-over-cap","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, draftDigest, proposalID,
	))
	if applyHTTP.StatusCode != http.StatusOK {
		t.Fatalf("Apply with over-cap response capture status=%d body=%s", applyHTTP.StatusCode, applyHTTP.Body)
	}
	var applied loomapi.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(applyHTTP.Body), &applied); err != nil {
		t.Fatal(err)
	}
	if applied.AcceptedConstruction != nil {
		t.Fatalf("over-cap capture returned a partial accepted preview: %#v", applied.AcceptedConstruction)
	}
	if *previewCalls != 2 {
		t.Fatalf("over-cap capture skipped Apply-time validation: calls=%d", *previewCalls)
	}
	stored, err := service.Get(context.Background(), "project-a", "apply-preview")
	if err != nil {
		t.Fatal(err)
	}
	if stored.DraftVersion != 2 || stored.DraftDigest != applied.DraftDigest {
		t.Fatalf("over-cap response capture changed Apply save semantics: stored=%#v response=%#v", stored, applied)
	}
}

func proposeConstructionRemovalForApplyPreview(t *testing.T, app *fiber.App, basePath string, snapshot capability.Snapshot, draftDigest string) string {
	t.Helper()
	proposalHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","removeStepIds":["only_step"],"candidateConstruction":{"version":1,"steps":[]},"limit":7}`,
		snapshot.Token, draftDigest,
	))
	if proposalHTTP.StatusCode != http.StatusOK {
		t.Fatalf("construction removal proposal status=%d body=%s", proposalHTTP.StatusCode, proposalHTTP.Body)
	}
	var proposal loomapi.ConstructionProposalResponse
	if err := json.Unmarshal([]byte(proposalHTTP.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalId == nil || proposal.Preview == nil || proposal.Preview.ReceiptId != *proposal.ProposalId || proposal.Preview.OutputId != "patients" {
		t.Fatalf("construction proposal has no exact preview receipt: %#v", proposal)
	}
	return *proposal.ProposalId
}

func applyConstructionPreviewRouteFixture(t *testing.T, failApplyCapture, emptyApplyPreview, overCapApplyCapture bool) (*fiber.App, *explorer.Service, capability.Snapshot, string, *int) {
	t.Helper()
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]authoringv2.StageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	document.Construction.Steps = []authoringv2.ConstructionStep{{
		ID: "only_step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{
			Kind:   authoringv2.ConstructionOperationFilter,
			Filter: &authoringv2.ConstructionFilter{ColumnID: columns[0].ID, Operator: authoringv2.ConstructionFilterExists},
		},
		Outputs: columns,
	}}
	workspace.Documents[0] = document
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringV2Catalog(snapshot, "apply-preview"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringV2Catalog(snapshot, "apply-preview")).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate initial construction workspace: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	draftDigest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := newTestExplorerStore()
	if _, err := store.create(explorer.Explorer{
		Project: "project-a", ExplorerID: "apply-preview", Title: "Patients",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: draftDigest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
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
	previewCallCount := 0
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
			return compileExplorerReceipt(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		PreviewReceipt: func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			previewCallCount++
			if receipt == nil || receipt.ConstructionProposal == nil || bindings.PreviewLimit != 7 || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "patients" {
				t.Fatalf("unexpected preview receipt/bindings: receipt=%#v bindings=%#v", receipt, bindings)
			}
			row := map[string]any{
				"c_patient":                 "patient-1",
				"__loom_preview_source":     map[string]any{"kind": "SINGLE", "resourceType": "Patient", "id": "patient-1"},
				"__loom_source_resource_id": "Patient/patient-1",
			}
			if failApplyCapture && previewCallCount > 1 {
				row["unencodable"] = make(chan int)
			}
			if overCapApplyCapture && previewCallCount > 1 {
				row["large"] = strings.Repeat("x", maxExplorerPreviewResponseBytes)
			}
			rowCount := 1
			if emptyApplyPreview && previewCallCount > 1 {
				rowCount = 0
			} else if err := visit(row); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{
				Output: "patients", Columns: []string{"c_patient"}, RowCount: rowCount, Complete: true, PartialValidation: true,
				RowLineageCapability: compiler.RowLineageCapability{Available: true},
			}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	return app, service, snapshot, draftDigest, &previewCallCount
}
