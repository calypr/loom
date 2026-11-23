package explorerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/rowlineage"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	proposalpkg "github.com/calypr/loom/internal/explorer/proposal"
	receiptapi "github.com/calypr/loom/internal/explorer/receipt"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
	"github.com/gofiber/fiber/v3"
)

func TestSourceRelationshipAndNetworkHTTPContract(t *testing.T) {
	ctx := context.Background()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope), SchemaDigest: "schema", ShapeDigest: "shape"}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}, nil, nil, nil)
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "c_patient", Expr: recipe.Expression{Select: "root.id"}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	workspace, err := recipeimport.ImportWorkspace(bundle, ws.CatalogFromCapability(snapshot, "custom"), map[string][]recipe.StageColumn{"patients": {{ID: "patient-id", Name: "c_patient", Label: "Patient ID", Type: "string"}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := workspace.Digest()
	store := newTestExplorerStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{Project: "project-a", ExplorerID: "custom", Title: "Patients", DraftConfig: raw, DraftVersion: 1, DraftDigest: digest}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	edits, stepEdits, networks, previews := 0, 0, 0, 0
	var previewFailure error
	var lastPreviewLimit int
	var previewCell any = "p1"
	previewSummary := dataframeexecution.PreviewSummary{RowCount: 1, Complete: true}
	config := lifecycle.Config{Capability: ports.CapabilityResolver{
		Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
		ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		},
		ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}, Catalog: ws.CatalogFromCapability,
	},
		EditImportedRelationship: func(_ context.Context, project string, authorized ports.AuthorizedCapability, document ws.Document, change authoringv2.ImportedRelationshipChange) (ws.Document, error) {
			edits++
			if project != "project-a" || authorized.Snapshot.Token != snapshot.Token || change.Action != "ADD" || change.ParentOccurrenceID != "base" || change.EdgeID != "authorized-edge" || change.MatchMode != ws.RouteMatchOptional {
				t.Fatalf("transport retargeted intent: %#v", change)
			}
			plan := recipeimport.SourcePlan(output)
			document.ImportedSource = &plan
			return document, nil
		},
		EditImportedSource: func(_ context.Context, project string, authorized ports.AuthorizedCapability, document ws.Document, change *authoringv2.ImportedSourceStepChange) (ws.Document, error) {
			stepEdits++
			if project != "project-a" || authorized.Snapshot.Token != snapshot.Token || change == nil || change.StepID != "fields" || !change.Remove {
				t.Fatalf("source-step transport lost settings: %#v", change)
			}
			plan := recipeimport.SourcePlan(output)
			plan.RemovedStepIDs = append(plan.RemovedStepIDs, change.StepID)
			plan.Steps = removeTestSourceStep(plan.Steps, change.StepID)
			document.ImportedSource = &plan
			return document, nil
		},
		EditImportedSources: func(_ context.Context, project string, authorized ports.AuthorizedCapability, document ws.Document, changes []authoringv2.ImportedSourceStepChange) (ws.Document, error) {
			if project != "project-a" || authorized.Snapshot.Token != snapshot.Token || len(changes) != 2 {
				t.Fatal("HTTP batch lost one owner/intent")
			}
			plan := recipeimport.SourcePlan(output)
			for _, change := range changes {
				if !change.Remove {
					t.Fatalf("expected source step removal: %#v", change)
				}
				plan.RemovedStepIDs = append(plan.RemovedStepIDs, change.StepID)
				plan.Steps = removeTestSourceStep(plan.Steps, change.StepID)
			}
			document.ImportedSource = &plan
			return document, nil
		},
		AuthoringNetwork: func(_ context.Context, _ string, _ ports.AuthorizedCapability, document ws.Document) (authoringnetwork.AuthoringNetwork, error) {
			networks++
			network := authoringnetwork.NewAuthoringNetwork(document, "IMPORTED_SOURCE")
			network.Occurrences = append(network.Occurrences, authoringnetwork.NetworkOccurrence{OccurrenceID: "base", ResourceType: "Patient", Label: "Patient", Enabled: true, PreservesRowIdentity: true})
			if !containsSourceStepRemoval(document.ImportedSource, "fields") {
				network.Bindings = append(network.Bindings, authoringnetwork.NetworkColumnBinding{ColumnID: "patient-id", OccurrenceID: "base", SourceStepID: "fields", ValueMode: "ALL", Status: "EXACT"})
			}
			return network, nil
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			receipt, err := explorerfixtures.PersistTestNativeReceipt(ctx, t, service, request, snapshot)
			if err != nil {
				return nil, err
			}
			if request.SourceRelationshipProposal == nil {
				return receipt, nil
			}
			clone := *receipt
			binding := *request.SourceRelationshipProposal
			clone.SourceRelationshipProposal = &binding
			clone.CompilationKey, err = receiptmodel.CompilationKey(clone)
			if err != nil {
				return nil, err
			}
			clone.ID, err = receiptmodel.ReceiptID(clone)
			if err != nil {
				return nil, err
			}
			return service.StoreCompilationReceipt(ctx, clone)
		},
		PreviewReceipt: func(_ context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			previews++
			lastPreviewLimit = bindings.PreviewLimit
			if receipt.SourceRelationshipProposal == nil || !bindings.IncludeRowIdentity || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "patients" {
				t.Fatal("preview did not use the bound source receipt")
			}
			if err := visit(map[string]any{"__loom_row_id": "patient-1", "c_patient": previewCell, "__loom_preview_source": map[string]any{"kind": "SINGLE", "resourceType": "Patient", "id": "patient-1"}}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			if previewFailure != nil {
				return dataframeexecution.PreviewSummary{}, previewFailure
			}
			return previewSummary, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"
	identity := fmt.Sprintf(`"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients"`, snapshot.Token, digest)
	networkResponse := requestJSON(t, app, http.MethodPost, basePath+"/authoring-network", "{"+identity+"}")
	if networkResponse.StatusCode != http.StatusOK {
		t.Fatalf("network HTTP %d: %s", networkResponse.StatusCode, networkResponse.Body)
	}
	var network lifecycle.AuthoringNetworkResponse
	if err := json.Unmarshal([]byte(networkResponse.Body), &network); err != nil {
		t.Fatal(err)
	}
	if network.DraftVersion != 1 || network.DraftDigest != digest || network.SnapshotToken != snapshot.Token || network.Network.OutputID != "patients" || network.Network.Owner != "IMPORTED_SOURCE" || len(network.Network.Bindings) != 1 || network.Network.Bindings[0].OccurrenceID != "base" || len(network.RowIdentity.Stages) != 1 || network.RowIdentity.Stages[0].Identity.Kind != "RECORD" {
		t.Fatalf("public network fields lost: %s", networkResponse.Body)
	}
	for _, body := range []string{"{" + strings.Replace(identity, `"expectedDraftVersion":1`, `"expectedDraftVersion":2`, 1) + "}", "{" + strings.Replace(identity, snapshot.Token, "stale-token", 1) + "}"} {
		response := requestJSON(t, app, http.MethodPost, basePath+"/authoring-network", body)
		if response.StatusCode != http.StatusConflict {
			t.Fatalf("stale network HTTP %d: %s", response.StatusCode, response.Body)
		}
	}
	for _, extra := range []string{`,"project":"other-project"`, `,"network":{"owner":"DOCUMENT"}`} {
		response := requestJSON(t, app, http.MethodPost, basePath+"/authoring-network", "{"+identity+extra+"}")
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("injected network read intent HTTP %d: %s", response.StatusCode, response.Body)
		}
	}
	if networks != 1 {
		t.Fatal("stale network reached hydration")
	}
	for _, change := range []string{`{"action":"ADD","parentOccurrenceId":"base","edgeId":"authorized-edge"}`, `{"action":"MATCH","occurrenceId":"r","matchMode":"FIRST"}`, `{"action":"ADD","parentOccurrenceId":"base","edgeId":"authorized-edge","matchMode":"OPTIONAL","expr":{"select":"root.secret"}}`} {
		response := requestJSON(t, app, http.MethodPost, basePath+"/source-relationship-proposals", "{"+identity+`,"change":`+change+"}")
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("incomplete/injected change reached application: HTTP %d %s", response.StatusCode, response.Body)
		}
	}
	for _, extra := range []string{`,"project":"other-project"`, `,"candidateWorkspace":{"documents":[]}`, `,"candidateDocument":{"output":{"id":"patients"}}`, `,"candidateNetwork":{"outputId":"patients"}`, `,"candidateRowIdentity":{"outputId":"patients"}`} {
		response := requestJSON(t, app, http.MethodPost, basePath+"/source-relationship-proposals", "{"+identity+`,"change":{"action":"ADD","parentOccurrenceId":"base","edgeId":"authorized-edge","matchMode":"OPTIONAL"}`+extra+"}")
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("injected source proposal envelope HTTP %d: %s", response.StatusCode, response.Body)
		}
	}
	if edits != 0 {
		t.Fatal("invalid typed intent reached native source resolver")
	}
	before, err := service.Get(ctx, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	proposed := requestJSON(t, app, http.MethodPost, basePath+"/source-relationship-proposals", "{"+identity+`,"change":{"action":"ADD","parentOccurrenceId":"base","edgeId":"authorized-edge","matchMode":"OPTIONAL"}}`)
	if proposed.StatusCode != http.StatusOK {
		t.Fatalf("proposal HTTP %d: %s", proposed.StatusCode, proposed.Body)
	}
	var proposal proposalpkg.SourceRelationshipProposal
	if err := json.Unmarshal([]byte(proposed.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalID == "" || proposal.CandidateReceiptID != proposal.ProposalID || proposal.OutputID != "patients" || proposal.SnapshotToken != snapshot.Token || proposal.DraftVersion != 1 || proposal.DraftDigest != digest || proposal.BaseDocumentDigest == "" || proposal.CandidateWorkspaceDigest == "" || proposal.PreviewStatus != "READY" || proposal.SampledRows != 1 || proposal.Sampled || proposal.AffectedColumns == nil || len(proposal.Notices) == 0 || edits != 1 || previews != 1 {
		t.Fatalf("proposal public contract lost fields: %s", proposed.Body)
	}
	if proposal.CandidateDocument.Output.ID != "patients" || proposal.CandidateDocument.ImportedSource == nil || !httpSourceStepPresent(proposal.CandidateDocument.ImportedSource, "fields") || proposal.CandidateNetwork.OutputID != "patients" || proposal.CandidateNetwork.Owner != "IMPORTED_SOURCE" || len(proposal.CandidateNetwork.Bindings) != 1 || proposal.CandidateNetwork.Bindings[0].OccurrenceID != "base" || proposal.CandidateRowIdentity.OutputID != "patients" || len(proposal.CandidateRowIdentity.Stages) != 1 || proposal.CandidateRowIdentity.Stages[0].Identity.Kind != "RECORD" {
		t.Fatalf("HTTP transport lost authoritative candidate projection: %s", proposed.Body)
	}
	if *proposal.CandidateDocument.ImportedRecipe != *workspace.Documents[0].ImportedRecipe {
		t.Fatal("HTTP candidate projection changed immutable recipe pin")
	}
	var oldEnvelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(proposed.Body), &oldEnvelope); err != nil {
		t.Fatal(err)
	}
	if _, exists := oldEnvelope["preview"]; exists {
		t.Fatal("legacy request unexpectedly received preview")
	}
	if _, exists := oldEnvelope["nativePreview"]; exists {
		t.Fatal("runtime preview result leaked into wire contract")
	}
	proposalBody := func(extra string) string {
		return "{" + identity + `,"change":{"action":"ADD","parentOccurrenceId":"base","edgeId":"authorized-edge","matchMode":"OPTIONAL"}` + extra + "}"
	}
	for _, extra := range []string{`,"includePreview":null`, `,"includePreview":"true"`, `,"previewSinkFactory":{}`, `,"nativePreview":{}`} {
		response := requestJSON(t, app, http.MethodPost, basePath+"/source-relationship-proposals", proposalBody(extra))
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid preview intent HTTP%d", response.StatusCode)
		}
	}
	t.Run("explicit-false-keeps-original-response", func(t *testing.T) {
		beforeCalls := previews
		response := requestJSON(t, app, http.MethodPost, basePath+"/source-relationship-proposals", proposalBody(`,"includePreview":false`))
		if response.StatusCode != http.StatusOK || response.Body != proposed.Body || previews-beforeCalls != 1 {
			t.Fatal("false opt-in changed original wire response or execution count")
		}
	})
	t.Run("opted-in-single-execution", func(t *testing.T) {
		previewSummary = dataframeexecution.PreviewSummary{RowCount: 1, Truncated: true, PartialValidation: true, RowLineageCapability: rowlineage.RowLineageCapability{Available: true}}
		beforeCalls := previews
		response := requestJSON(t, app, http.MethodPost, basePath+"/source-relationship-proposals", proposalBody(`,"includePreview":true,"limit":2`))
		if response.StatusCode != http.StatusOK {
			t.Fatalf("embedded preview HTTP%d: %s", response.StatusCode, response.Body)
		}
		var got loomapi.SourceRelationshipProposal
		if err := json.Unmarshal([]byte(response.Body), &got); err != nil {
			t.Fatal(err)
		}
		if got.Preview == nil || got.Preview.ReceiptId != got.CandidateReceiptId || got.Preview.OutputId != "patients" || len(got.Preview.Rows) != 1 || got.Preview.RowCount != 1 || !got.Preview.Sampled || got.Preview.PartialValidation == nil || !*got.Preview.PartialValidation || string(got.Preview.RowLineageCapability.Status) != "AVAILABLE" || previews-beforeCalls != 1 || lastPreviewLimit != 2 {
			t.Fatal("embedded preview lost exact receipt, rows, summary or single execution")
		}
		if len(got.Preview.RowSources) != 1 || string(got.Preview.RowSources[0].Kind) != "SINGLE" || got.Preview.RowSources[0].Id == nil || *got.Preview.RowSources[0].Id != "patient-1" {
			t.Fatal("embedded preview lost aligned FHIR source identity")
		}
		if _, exists := got.Preview.Rows[0]["__loom_preview_source"]; exists {
			t.Fatal("private row source leaked")
		}
		if !got.Sampled || got.SampledRows != 1 {
			t.Fatal("proposal summary disagrees with native preview")
		}
	})
	for _, failure := range []string{"execution", "encoding", "overflow", "canceled", "deadline"} {
		t.Run("opted-in-"+failure, func(t *testing.T) {
			previewFailure = nil
			previewCell = "p1"
			want := http.StatusInternalServerError
			switch failure {
			case "execution":
				previewFailure = errors.New("native query failed")
			case "canceled":
				previewFailure = context.Canceled
				want = 499
			case "deadline":
				previewFailure = context.DeadlineExceeded
				want = http.StatusGatewayTimeout
			case "encoding":
				previewCell = func() {}
			case "overflow":
				previewCell = strings.Repeat("x", maxExplorerPreviewResponseBytes)
				want = http.StatusRequestEntityTooLarge
			}
			beforeCalls := previews
			response := requestJSON(t, app, http.MethodPost, basePath+"/source-relationship-proposals", proposalBody(`,"includePreview":true`))
			if response.StatusCode != want || previews-beforeCalls != 1 || strings.Contains(response.Body, `"proposalId"`) {
				t.Fatalf("failed preview was not atomic: HTTP%d calls%d", response.StatusCode, previews-beforeCalls)
			}
		})
	}
	previewFailure = nil
	previewCell = "p1"
	previewSummary = dataframeexecution.PreviewSummary{RowCount: 1, Complete: true}
	previewsBeforeApply := previews
	after, err := service.Get(ctx, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if before.DraftVersion != after.DraftVersion || before.DraftDigest != after.DraftDigest || !bytes.Equal(before.DraftConfig, after.DraftConfig) {
		t.Fatal("HTTP Preview/Cancel changed saved draft")
	}
	applyBody := func(id string) string {
		return fmt.Sprintf(`{"commandId":%q,"semanticsVersion":%d,%s,"commands":[{"type":"APPLY_IMPORTED_RELATIONSHIP_PROPOSAL","outputId":"patients","proposalId":%q}]}`, id, ws.CurrentSemanticsVersion, strings.Replace(identity, `,"outputId":"patients"`, "", 1), proposal.ProposalID)
	}
	applied := requestJSON(t, app, http.MethodPost, basePath+"/commands", applyBody("source-apply"))
	if applied.StatusCode != http.StatusOK {
		t.Fatalf("source Apply HTTP %d: %s", applied.StatusCode, applied.Body)
	}
	saved, err := service.Get(ctx, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if saved.DraftVersion != 2 || previews != previewsBeforeApply+1 {
		t.Fatal("HTTP Apply did not validate receipt and save once")
	}
	savedWorkspace, err := ws.DecodeWorkspace(saved.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	candidateRaw, err := json.Marshal(proposal.CandidateDocument)
	if err != nil {
		t.Fatal(err)
	}
	savedRaw, err := json.Marshal(savedWorkspace.Documents[0])
	if err != nil || !bytes.Equal(candidateRaw, savedRaw) {
		t.Fatal("HTTP candidate document does not describe the receipt-applied document")
	}
	stale := requestJSON(t, app, http.MethodPost, basePath+"/commands", applyBody("second-editor-stale"))
	if stale.StatusCode != http.StatusConflict || !strings.Contains(stale.Body, `"code":"DRAFT_CONFLICT"`) {
		t.Fatalf("stale Apply HTTP %d: %s", stale.StatusCode, stale.Body)
	}
	final, err := service.Get(ctx, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if final.DraftVersion != saved.DraftVersion || final.DraftDigest != saved.DraftDigest || !bytes.Equal(final.DraftConfig, saved.DraftConfig) {
		t.Fatal("stale HTTP Apply changed saved state")
	}

	stepBody := fmt.Sprintf(`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","sourceStep":{"stepId":"fields","remove":true}}`, snapshot.Token, saved.DraftVersion, saved.DraftDigest)
	stepResponse := requestJSON(t, app, http.MethodPost, basePath+"/source-relationship-proposals", stepBody)
	if stepResponse.StatusCode != http.StatusOK {
		t.Fatalf("source-step preview HTTP %d: %s", stepResponse.StatusCode, stepResponse.Body)
	}
	var stepProposal proposalpkg.SourceRelationshipProposal
	if err := json.Unmarshal([]byte(stepResponse.Body), &stepProposal); err != nil {
		t.Fatal(err)
	}
	if stepProposal.ProposalID == "" || stepEdits != 1 {
		t.Fatal("source-step proposal lost its removal intent")
	}
	if stepProposal.CandidateDocument.ImportedSource == nil || httpSourceStepPresent(stepProposal.CandidateDocument.ImportedSource, "fields") || !containsSourceStepRemoval(stepProposal.CandidateDocument.ImportedSource, "fields") || stepProposal.CandidateNetwork.OutputID != "patients" || len(stepProposal.CandidateNetwork.Bindings) != 0 || stepProposal.CandidateRowIdentity.OutputID != "patients" {
		t.Fatalf("HTTP source-step candidate retained removed source: %s", stepResponse.Body)
	}
	afterStepPreview, err := service.Get(ctx, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if afterStepPreview.DraftVersion != saved.DraftVersion || afterStepPreview.DraftDigest != saved.DraftDigest || !bytes.Equal(afterStepPreview.DraftConfig, saved.DraftConfig) {
		t.Fatal("source-step removal Preview changed draft")
	}
	stepApplyBody := fmt.Sprintf(`{"commandId":"source-step-remove","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_IMPORTED_RELATIONSHIP_PROPOSAL","outputId":"patients","proposalId":%q}]}`, ws.CurrentSemanticsVersion, snapshot.Token, saved.DraftVersion, saved.DraftDigest, stepProposal.ProposalID)
	stepApplied := requestJSON(t, app, http.MethodPost, basePath+"/commands", stepApplyBody)
	if stepApplied.StatusCode != http.StatusOK {
		t.Fatalf("source-step removal Apply HTTP %d: %s", stepApplied.StatusCode, stepApplied.Body)
	}
	restored, err := service.Get(ctx, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	restoredWorkspace, err := ws.DecodeWorkspace(restored.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if restored.DraftVersion != 3 || httpSourceStepPresent(restoredWorkspace.Documents[0].ImportedSource, "fields") || !containsSourceStepRemoval(restoredWorkspace.Documents[0].ImportedSource, "fields") || *restoredWorkspace.Documents[0].ImportedRecipe != *workspace.Documents[0].ImportedRecipe {
		t.Fatal("source-step HTTP Apply did not persist removal and preserve immutable pin")
	}

}

func httpSourceStepPresent(plan *ws.ImportedSourcePlan, id string) bool {
	if plan == nil {
		return false
	}
	for _, step := range plan.Steps {
		if step.ID == id {
			return true
		}
	}
	return false
}

func containsSourceStepRemoval(plan *ws.ImportedSourcePlan, id string) bool {
	if plan == nil {
		return false
	}
	for _, removed := range plan.RemovedStepIDs {
		if removed == id {
			return true
		}
	}
	return false
}

func removeTestSourceStep(steps []ws.ImportedSourceStep, id string) []ws.ImportedSourceStep {
	retained := steps[:0]
	for _, step := range steps {
		if step.ID != id {
			retained = append(retained, step)
		}
	}
	return retained
}

func TestApplyConstructionProposalReturnsExactPreviewOnlyAfterSave(t *testing.T) {
	app, service, snapshot, draftDigest, previewCalls := applyConstructionPreviewRouteFixture(t, false, false, false)
	basePath := "/api/v1/projects/project-a/explorers/apply-preview/authoring/v2"
	proposalID := proposeConstructionRemovalForApplyPreview(t, app, basePath, snapshot, draftDigest)
	applyBody := fmt.Sprintf(
		`{"commandId":"apply-preview-exact","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":%q}]}`,
		ws.CurrentSemanticsVersion, snapshot.Token, draftDigest, proposalID,
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
		ws.CurrentSemanticsVersion, snapshot.Token, draftDigest, proposalID,
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
		ws.CurrentSemanticsVersion, snapshot.Token, draftDigest, proposalID,
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
		ws.CurrentSemanticsVersion, snapshot.Token, draftDigest, proposalID,
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
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]ws.StageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	document.Construction.Steps = []ws.ConstructionStep{{
		ID: "only_step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{
			Kind:   ws.ConstructionOperationFilter,
			Filter: &ws.ConstructionFilter{ColumnID: columns[0].ID, Operator: ws.ConstructionFilterExists},
		},
		Outputs: columns,
	}}
	workspace.Documents[0] = document
	workspace, err = ws.MigrateLegacyContributors(workspace, ws.CatalogFromCapability(snapshot, "apply-preview"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, ws.CatalogFromCapability(snapshot, "apply-preview")).NormalizePresentationOrders()
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
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
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
		PreviewReceipt: func(_ context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
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
				RowLineageCapability: rowlineage.RowLineageCapability{Available: true},
			}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	return app, service, snapshot, draftDigest, &previewCallCount
}

func TestPreviewResponseEncoderProducesAtomicContract(t *testing.T) {
	receipt := &receiptmodel.CompilationReceipt{ID: "same-id"}
	aggregateUnit := &unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}
	derivedUnit := &unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg/m2"}
	columns := []viewer.EmittedColumn{
		{EmissionID: "mean-weight", OutputID: "same-id", AuthoredColumns: []string{"weight"}, PublicColumn: "mean_weight", Label: "Mean weight", LogicalType: "decimal", Shape: "scalar", Nullable: true, ResultUnit: aggregateUnit},
		{EmissionID: "bmi", OutputID: "same-id", AuthoredColumns: []string{"weight", "height"}, PublicColumn: "bmi", Label: "BMI", LogicalType: "decimal", ResultUnit: derivedUnit},
		{EmissionID: "patient-id", OutputID: "same-id", AuthoredColumns: []string{"patient_id"}, PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string", SourceResourceType: "Patient", SourcePath: "id"},
	}
	encoder, err := newPreviewResponseEncoder(receipt, "same-id", columns, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.Visit(map[string]any{
		"public": "value", "__loom_source_resource_id": "secret-id",
		"__loom_preview_source": map[string]any{"kind": "SINGLE", "resourceType": "Observation", "id": "fhir-id"},
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := encoder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		ReceiptID  string                 `json:"receiptId"`
		OutputID   string                 `json:"outputId"`
		Rows       []map[string]any       `json:"rows"`
		RowSources []json.RawMessage      `json:"rowSources"`
		RowCount   int                    `json:"rowCount"`
		Columns    []viewer.EmittedColumn `json:"columns"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response %s: %v", raw, err)
	}
	if decoded.ReceiptID != receipt.ID || decoded.OutputID != "same-id" || decoded.RowCount != 1 || len(decoded.Rows) != 1 || len(decoded.RowSources) != len(decoded.Rows) || len(decoded.Columns) != 3 {
		t.Fatalf("response = %#v", decoded)
	}
	if _, exists := decoded.Rows[0]["__loom_source_resource_id"]; exists {
		t.Fatalf("private FHIR source ID leaked into row JSON: %#v", decoded.Rows[0])
	}
	if _, exists := decoded.Rows[0]["__loom_preview_source"]; exists {
		t.Fatalf("row sidecar metadata leaked into row JSON: %#v", decoded.Rows[0])
	}
	var source map[string]string
	if err := json.Unmarshal(decoded.RowSources[0], &source); err != nil {
		t.Fatalf("decode row source %s: %v", decoded.RowSources[0], err)
	}
	if len(source) != 3 || source["kind"] != "SINGLE" || source["resourceType"] != "Observation" || source["id"] != "fhir-id" {
		t.Fatalf("row source = %#v, want one Observation/fhir-id source", source)
	}
	if len(decoded.Columns[0].AuthoredColumns) != 1 || decoded.Columns[0].AuthoredColumns[0] != "weight" {
		t.Fatalf("authored columns = %#v", decoded.Columns[0].AuthoredColumns)
	}
	if decoded.Columns[0].Shape != "scalar" || !decoded.Columns[0].Nullable {
		t.Fatalf("preview shape/nullability = %q/%t, want scalar/true", decoded.Columns[0].Shape, decoded.Columns[0].Nullable)
	}
	if decoded.Columns[2].SourceResourceType != "Patient" || decoded.Columns[2].SourcePath != "id" {
		t.Fatalf("base ID source = %s.%s, want Patient.id", decoded.Columns[2].SourceResourceType, decoded.Columns[2].SourcePath)
	}
	if got := decoded.Columns[0].ResultUnit; got == nil || got.System != aggregateUnit.System || got.Code != aggregateUnit.Code {
		t.Fatalf("aggregate result unit = %#v, want %#v", got, aggregateUnit)
	}
	if got := decoded.Columns[1].ResultUnit; got == nil || got.System != derivedUnit.System || got.Code != derivedUnit.Code {
		t.Fatalf("derived result unit = %#v, want %#v", got, derivedUnit)
	}
	var wire struct {
		Columns []map[string]json.RawMessage `json:"columns"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decode preview column JSON: %v", err)
	}
	assertResultUnit := func(raw json.RawMessage, code string) {
		t.Helper()
		var got map[string]string
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode resultUnit %s: %v", raw, err)
		}
		if len(got) != 2 || got["system"] != "http://unitsofmeasure.org" || got["code"] != code {
			t.Fatalf("resultUnit JSON = %#v, want {system: UCUM, code: %q}", got, code)
		}
	}
	assertResultUnit(wire.Columns[0]["resultUnit"], "kg")
	assertResultUnit(wire.Columns[1]["resultUnit"], "kg/m2")
	if _, exists := wire.Columns[2]["resultUnit"]; exists {
		t.Fatalf("unitless preview column contains resultUnit: %s", wire.Columns[2]["resultUnit"])
	}
}

func TestPreviewResponseEncoderEmitsAlignedEmptyRowSources(t *testing.T) {
	encoder, err := newPreviewResponseEncoder(&receiptmodel.CompilationReceipt{ID: "receipt"}, "output", nil, 4096)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encoder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Rows       []map[string]any  `json:"rows"`
		RowSources []json.RawMessage `json:"rowSources"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode empty response %s: %v", raw, err)
	}
	if decoded.Rows == nil || decoded.RowSources == nil || len(decoded.Rows) != 0 || len(decoded.RowSources) != len(decoded.Rows) {
		t.Fatalf("empty rows and sidecars = %#v / %#v, want empty arrays", decoded.Rows, decoded.RowSources)
	}
}

func TestPreviewResponseEncoderRejectsOverflowWithoutResult(t *testing.T) {
	encoder, err := newPreviewResponseEncoder(&receiptmodel.CompilationReceipt{ID: "receipt"}, "output", nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := encoder.Visit(map[string]any{"value": strings.Repeat("x", 2048)}); !errors.Is(err, ErrPreviewResponseTooLarge) {
		t.Fatalf("Visit error = %v, want %v", err, ErrPreviewResponseTooLarge)
	}
}

func TestPreviewResponseEncoderBoundsSourceSidecarDuringVisit(t *testing.T) {
	encoder, err := newPreviewResponseEncoder(&receiptmodel.CompilationReceipt{ID: "receipt"}, "output", nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	err = encoder.Visit(map[string]any{
		"value": "small row",
		"__loom_preview_source": map[string]any{
			"kind": "SINGLE", "resourceType": "Observation", "id": strings.Repeat("x", 2048),
		},
	})
	if !errors.Is(err, ErrPreviewResponseTooLarge) {
		t.Fatalf("Visit error = %v, want %v for oversized source sidecar", err, ErrPreviewResponseTooLarge)
	}
}

func TestPreviewErrorPreservesStableClassifications(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"timeout", context.DeadlineExceeded, http.StatusGatewayTimeout, "PREVIEW_TIMEOUT"},
		{"canceled", context.Canceled, 499, "CLIENT_CANCELED"},
		{"oversized", &previewResponseTooLargeError{Limit: 32}, http.StatusRequestEntityTooLarge, "RESPONSE_TOO_LARGE"},
		{"plan", dataframeerrors.NewError(dataframeerrors.CodePlanTooExpensive, "private"), http.StatusTooManyRequests, "PLAN_TOO_EXPENSIVE"},
		{"relationship-cardinality", dataframeerrors.NewError(dataframeerrors.CodeRelationshipCardinalityViolation, "private"), http.StatusUnprocessableEntity, "RELATIONSHIP_CARDINALITY_VIOLATION"},
		{"construction-expansion-empty", dataframeerrors.NewError(dataframeerrors.CodeConstructionExpansionEmpty, ""), http.StatusUnprocessableEntity, "CONSTRUCTION_EXPANSION_EMPTY"},
		{"explicit-group-unassigned", dataframeerrors.NewError(dataframeerrors.CodeExplicitGroupUnassignedMember, ""), http.StatusUnprocessableEntity, "EXPLICIT_GROUP_UNASSIGNED_MEMBER"},
		{"pivot-cell-cardinality", dataframeerrors.NewError(dataframeerrors.CodeTablePivotCellCardinality, ""), http.StatusUnprocessableEntity, "TABLE_PIVOT_CELL_CARDINALITY"},
		{"pivot-unlisted-category", dataframeerrors.NewError(dataframeerrors.ErrorCode("TABLE_PIVOT_UNLISTED_CATEGORY"), ""), http.StatusUnprocessableEntity, "TABLE_PIVOT_UNLISTED_CATEGORY"},
		{"temporal-anchor", dataframeerrors.NewError(dataframeerrors.CodeTemporalAnchorInvalid, "private"), http.StatusUnprocessableEntity, "TEMPORAL_ANCHOR_INVALID"},
		{"temporal-precision", dataframeerrors.NewError(dataframeerrors.CodeTemporalPrecisionUnsupported, "private"), http.StatusUnprocessableEntity, "TEMPORAL_PRECISION_UNSUPPORTED"},
		{"temporal-tie", dataframeerrors.NewError(dataframeerrors.CodeTemporalTieAmbiguous, "private"), http.StatusUnprocessableEntity, "TEMPORAL_TIE_AMBIGUOUS"},
		{"backend", dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "private", dataframeerrors.WithRetryable(true)), http.StatusServiceUnavailable, "BACKEND_UNAVAILABLE"},
		{"preview-timeout", dataframeerrors.NewError(dataframeerrors.CodePreviewTimeout, ""), http.StatusGatewayTimeout, "PREVIEW_TIMEOUT"},
		{"memory-limit", dataframeerrors.NewError(dataframeerrors.CodeQueryMemoryLimitExceeded, "private"), http.StatusServiceUnavailable, "QUERY_MEMORY_LIMIT_EXCEEDED"},
		{"resource-limit", dataframeerrors.NewError(dataframeerrors.CodeQueryResourceLimitExceeded, "private"), http.StatusServiceUnavailable, "QUERY_RESOURCE_LIMIT_EXCEEDED"},
		{"out-of-memory", dataframeerrors.NewError(dataframeerrors.CodeQueryBackendOutOfMemory, "private"), http.StatusServiceUnavailable, "QUERY_BACKEND_OUT_OF_MEMORY"},
		{"receipt", &receiptapi.PreviewResolutionError{ReceiptID: "receipt-1", Err: receiptapi.NewContractMismatch("output_execution", "patients", "private-expected", "private-actual")}, http.StatusConflict, "RECEIPT_RECOMPILE_REQUIRED"},
		{"unknown", errors.New("private"), http.StatusInternalServerError, "PREVIEW_FAILED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got *explorer.AuthoringError
			if !errors.As(previewRouteError(test.err), &got) || got.Status != test.status || got.Diagnostic.Code != test.code {
				t.Fatalf("error=%#v, want status=%d code=%s", got, test.status, test.code)
			}
		})
	}
}

func TestNestedPreviewTimeoutKeepsGatewayTimeoutClassification(t *testing.T) {
	lifecyclePreviewFailure := &ports.Error{
		Class: ports.ClassUnprocessable, Stage: "preview", Code: string(dataframeerrors.CodePreviewTimeout),
		Message: dataframeerrors.PublicMessage(dataframeerrors.NewError(dataframeerrors.CodePreviewTimeout, "")),
		Cause:   dataframeerrors.Wrap(context.DeadlineExceeded, dataframeerrors.CodePreviewTimeout, "", dataframeerrors.WithRetryable(true)),
	}
	status, response := authoringErrorForOpenAPI(context.Background(), "proposeExplorerConstruction", previewRouteError(lifecyclePreviewFailure))
	if status != http.StatusGatewayTimeout || response.Error.Code != "PREVIEW_TIMEOUT" {
		t.Fatalf("nested preview timeout response status/code = %d/%q, want %d/PREVIEW_TIMEOUT", status, response.Error.Code, http.StatusGatewayTimeout)
	}
}

func TestAuthoringErrorAdapterPreservesOuterLifecycleClassification(t *testing.T) {
	inner := &explorer.AuthoringError{Status: http.StatusUnauthorized, Diagnostic: explorer.AuthoringDiagnostic{
		Code: "INNER_FAILURE", Stage: "source", Message: "inner failure",
	}}
	outer := &ports.Error{Class: ports.ClassForbidden, Code: "ACCESS_DENIED", Stage: "preview", Message: "access denied", Cause: inner}
	status, response := authoringErrorForOpenAPI(context.Background(), "preview", fmt.Errorf("wrapped: %w", outer))
	if status != http.StatusForbidden || response.Error.Code != "ACCESS_DENIED" {
		t.Fatalf("outer lifecycle failure lost its classification: %d/%q", status, response.Error.Code)
	}
}

func TestConstructionExpansionEmptyPreviewErrorIsActionable(t *testing.T) {
	var got *explorer.AuthoringError
	if !errors.As(previewRouteError(dataframeerrors.NewError(dataframeerrors.CodeConstructionExpansionEmpty, "")), &got) {
		t.Fatal("previewRouteError() did not return an authoring error")
	}
	want := "At least one row has an empty list or no matching related records. Choose 'Drop the original row' or 'Keep the row with a missing item' for lists, or 'Leave that current row out' or 'Keep that current row once, with no related record ID' for related records."
	if got.Status != http.StatusUnprocessableEntity || got.Diagnostic.Code != string(dataframeerrors.CodeConstructionExpansionEmpty) || got.Diagnostic.Message != want {
		t.Fatalf("diagnostic = %#v, want 422 %s with actionable message", got.Diagnostic, dataframeerrors.CodeConstructionExpansionEmpty)
	}
}

func TestUnlistedPivotCategoryPreviewErrorIsActionable(t *testing.T) {
	var got *explorer.AuthoringError
	err := dataframeerrors.NewError(dataframeerrors.ErrorCode("TABLE_PIVOT_UNLISTED_CATEGORY"), "private")
	if !errors.As(previewRouteError(err), &got) {
		t.Fatal("previewRouteError() did not return an authoring error")
	}
	want := "Pivot found an unlisted category; select all discovered categories or filter rows before Pivot."
	if got.Status != http.StatusUnprocessableEntity || got.Diagnostic.Code != "TABLE_PIVOT_UNLISTED_CATEGORY" || got.Diagnostic.Message != want {
		t.Fatalf("diagnostic = %#v, want 422 TABLE_PIVOT_UNLISTED_CATEGORY with actionable message %q", got.Diagnostic, want)
	}
}

func TestClassifyReceiptPreviewResolutionErrorOnlyMarksContractFailuresForRecompile(t *testing.T) {
	mismatch := receiptapi.ClassifyPreviewResolutionError("receipt-1", receiptapi.NewContractMismatch("output_execution", "patients", "expected", "actual"))
	var resolution *receiptapi.PreviewResolutionError
	if !errors.As(mismatch, &resolution) || resolution.ReceiptID != "receipt-1" {
		t.Fatalf("mismatch classification = %#v", mismatch)
	}
	var authoring *explorer.AuthoringError
	if !errors.As(previewRouteError(mismatch), &authoring) || authoring.Status != http.StatusConflict {
		t.Fatalf("mismatch route error = %#v", authoring)
	}
	if got := authoring.Diagnostic.Details["receiptId"]; got != "receipt-1" {
		t.Fatalf("receiptId detail = %#v", got)
	}
	if got := authoring.Diagnostic.Details["outputId"]; got != "patients" {
		t.Fatalf("outputId detail = %#v", got)
	}

	compileFailure := receiptapi.ClassifyPreviewResolutionError("receipt-1", errors.New("compiler failed"))
	if errors.As(compileFailure, &resolution) {
		t.Fatalf("ordinary compiler failure was classified as a receipt mismatch: %v", compileFailure)
	}
	if !errors.As(previewRouteError(compileFailure), &authoring) || authoring.Status != http.StatusInternalServerError || authoring.Diagnostic.Code != "PREVIEW_FAILED" {
		t.Fatalf("compiler route error = %#v", authoring)
	}
}

func TestConfiguredQueryErrorMappingPreservesTypedSemantics(t *testing.T) {
	canceled := fmt.Errorf("query canceled: %w", context.Canceled)
	var canceledAuthoring *explorer.AuthoringError
	if got := PreserveConfiguredQueryError(canceled); !errors.As(got, &canceledAuthoring) || canceledAuthoring.Status != 499 || canceledAuthoring.Diagnostic.Code != "CLIENT_CANCELED" {
		t.Fatalf("canceled query error = %#v, want redacted 499 CLIENT_CANCELED", got)
	}

	deadline := fmt.Errorf("query deadline: %w", context.DeadlineExceeded)
	if got := PreserveConfiguredQueryError(deadline); !errors.Is(got, context.DeadlineExceeded) || got != deadline {
		t.Fatalf("deadline query error = %#v, want original lifecycle-classifiable deadline", got)
	}

	userErr := dataframeerrors.NewError(dataframeerrors.CodeConstructionExpansionEmpty, "")
	var userAuthoring *explorer.AuthoringError
	if got := PreserveConfiguredQueryError(userErr); !errors.As(got, &userAuthoring) || userAuthoring.Status != http.StatusUnprocessableEntity || userAuthoring.Diagnostic.Code != string(dataframeerrors.CodeConstructionExpansionEmpty) {
		t.Fatalf("typed query error = %#v, want mapped 422 user error", got)
	}

	unknown := errors.New("private backend details")
	if got := PreserveConfiguredQueryError(unknown); got != unknown {
		t.Fatalf("untyped query error = %#v, want original lifecycle-classifiable error", got)
	}
}

func TestConfiguredQueryErrorMappingKeepsAuthStatusAndSafeMessage(t *testing.T) {
	tests := []struct {
		code   dataframeerrors.ErrorCode
		status int
	}{
		{code: dataframeerrors.CodeUnauthenticated, status: http.StatusUnauthorized},
		{code: dataframeerrors.CodeForbidden, status: http.StatusForbidden},
		{code: dataframeerrors.CodeUnauthorizedProject, status: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			cause := dataframeerrors.NewError(test.code, "private authorization detail")
			mapped := PreserveConfiguredQueryError(cause)
			var authoringErr *explorer.AuthoringError
			if !errors.As(mapped, &authoringErr) || authoringErr.Status != test.status || authoringErr.Diagnostic.Code != string(test.code) {
				t.Fatalf("mapped auth error = %#v, want status %d and code %s", mapped, test.status, test.code)
			}
			if authoringErr.Diagnostic.Message != dataframeerrors.PublicMessage(cause) || errors.Unwrap(authoringErr) != cause {
				t.Fatalf("mapped auth message/cause = %q/%v", authoringErr.Diagnostic.Message, errors.Unwrap(authoringErr))
			}
			if strings.Contains(authoringErr.Error(), "private authorization detail") {
				t.Fatalf("mapped auth error exposes private message: %v", authoringErr)
			}
		})
	}
}

func TestClassifyDataframeQueryErrorPreservesEmptyConstructionExpansion(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: CONSTRUCTION_EXPANSION_EMPTY: related expansion construction related_1 has no related records for row row-17 (while executing)",
	}
	err := executionarango.ClassifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeConstructionExpansionEmpty) || userErr.Retryable() {
		t.Fatalf("classified error=%#v, want non-retryable %s", userErr, dataframeerrors.CodeConstructionExpansionEmpty)
	}
	if !errors.Is(err, driverErr) {
		t.Fatal("classified error did not preserve the Arango cause")
	}
	var routeErr *explorer.AuthoringError
	if !errors.As(previewRouteError(err), &routeErr) || routeErr.Status != http.StatusUnprocessableEntity ||
		routeErr.Diagnostic.Code != string(dataframeerrors.CodeConstructionExpansionEmpty) {
		t.Fatalf("preview route error = %#v, want non-retryable construction expansion diagnostic with HTTP 422", routeErr)
	}
}

func TestClassifyGroupedColumnMultiplicity(t *testing.T) {
	cause := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES (while executing)"}
	err := executionarango.ClassifyDataframeQueryError(cause)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeConstructionRowValueMultipleValues) || userErr.Retryable() {
		t.Fatalf("expected actionable grouped-value error, got %v", err)
	}
	var routeErr *explorer.AuthoringError
	if !errors.As(previewRouteError(err), &routeErr) || routeErr.Status != 422 {
		t.Fatalf("expected 422 grouped-value error, got %v", routeErr)
	}
	if !errors.Is(err, cause) {
		t.Fatal("driver cause lost")
	}
}
