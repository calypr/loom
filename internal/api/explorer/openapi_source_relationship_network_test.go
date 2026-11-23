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

	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"

	loomapi "github.com/calypr/loom/api/loomapi"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/rowlineage"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	"github.com/gofiber/fiber/v3"
)

func TestSourceRelationshipAndNetworkHTTPContract(t *testing.T) {
	ctx := context.Background()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope), SchemaDigest: "schema", ShapeDigest: "shape"}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}, nil, nil, nil)
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "c_patient", Expr: recipe.Expression{Select: "root.id"}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	workspace, err := recipeimport.ImportWorkspace(bundle, authoringv2.CatalogFromCapability(snapshot, "custom"), map[string][]recipe.StageColumn{"patients": {{ID: "patient-id", Name: "c_patient", Label: "Patient ID", Type: "string"}}})
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
		}, Catalog: authoringv2.CatalogFromCapability,
	},
		EditImportedRelationship: func(_ context.Context, project string, authorized ports.AuthorizedCapability, document authoringv2.Document, change authoringv2.ImportedRelationshipChange) (authoringv2.Document, error) {
			edits++
			if project != "project-a" || authorized.Snapshot.Token != snapshot.Token || change.Action != "ADD" || change.ParentOccurrenceID != "base" || change.EdgeID != "authorized-edge" || change.MatchMode != authoringv2.RouteMatchOptional {
				t.Fatalf("transport retargeted intent: %#v", change)
			}
			plan := recipeimport.SourcePlan(output)
			document.ImportedSource = &plan
			return document, nil
		},
		EditImportedSource: func(_ context.Context, project string, authorized ports.AuthorizedCapability, document authoringv2.Document, change *authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
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
		EditImportedSources: func(_ context.Context, project string, authorized ports.AuthorizedCapability, document authoringv2.Document, changes []authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
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
		AuthoringNetwork: func(_ context.Context, _ string, _ ports.AuthorizedCapability, document authoringv2.Document) (authoringv2.AuthoringNetwork, error) {
			networks++
			network := authoringv2.NewAuthoringNetwork(document, "IMPORTED_SOURCE")
			network.Occurrences = append(network.Occurrences, authoringv2.NetworkOccurrence{OccurrenceID: "base", ResourceType: "Patient", Label: "Patient", Enabled: true, PreservesRowIdentity: true})
			if !containsSourceStepRemoval(document.ImportedSource, "fields") {
				network.Bindings = append(network.Bindings, authoringv2.NetworkColumnBinding{ColumnID: "patient-id", OccurrenceID: "base", SourceStepID: "fields", ValueMode: "ALL", Status: "EXACT"})
			}
			return network, nil
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
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
			clone.CompilationKey, err = explorer.CompilationKey(clone)
			if err != nil {
				return nil, err
			}
			clone.ID, err = explorer.ReceiptID(clone)
			if err != nil {
				return nil, err
			}
			return service.StoreCompilationReceipt(ctx, clone)
		},
		PreviewReceipt: func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
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
	var proposal lifecycle.SourceRelationshipProposal
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
		return fmt.Sprintf(`{"commandId":%q,"semanticsVersion":%d,%s,"commands":[{"type":"APPLY_IMPORTED_RELATIONSHIP_PROPOSAL","outputId":"patients","proposalId":%q}]}`, id, authoringv2.CurrentSemanticsVersion, strings.Replace(identity, `,"outputId":"patients"`, "", 1), proposal.ProposalID)
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
	savedWorkspace, err := authoringv2.DecodeWorkspace(saved.DraftConfig)
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
	var stepProposal lifecycle.SourceRelationshipProposal
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
	stepApplyBody := fmt.Sprintf(`{"commandId":"source-step-remove","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_IMPORTED_RELATIONSHIP_PROPOSAL","outputId":"patients","proposalId":%q}]}`, authoringv2.CurrentSemanticsVersion, snapshot.Token, saved.DraftVersion, saved.DraftDigest, stepProposal.ProposalID)
	stepApplied := requestJSON(t, app, http.MethodPost, basePath+"/commands", stepApplyBody)
	if stepApplied.StatusCode != http.StatusOK {
		t.Fatalf("source-step removal Apply HTTP %d: %s", stepApplied.StatusCode, stepApplied.Body)
	}
	restored, err := service.Get(ctx, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	restoredWorkspace, err := authoringv2.DecodeWorkspace(restored.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if restored.DraftVersion != 3 || httpSourceStepPresent(restoredWorkspace.Documents[0].ImportedSource, "fields") || !containsSourceStepRemoval(restoredWorkspace.Documents[0].ImportedSource, "fields") || *restoredWorkspace.Documents[0].ImportedRecipe != *workspace.Documents[0].ImportedRecipe {
		t.Fatal("source-step HTTP Apply did not persist removal and preserve immutable pin")
	}

}

func httpSourceStepPresent(plan *authoringv2.ImportedSourcePlan, id string) bool {
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

func containsSourceStepRemoval(plan *authoringv2.ImportedSourcePlan, id string) bool {
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

func removeTestSourceStep(steps []authoringv2.ImportedSourceStep, id string) []authoringv2.ImportedSourceStep {
	retained := steps[:0]
	for _, step := range steps {
		if step.ID != id {
			retained = append(retained, step)
		}
	}
	return retained
}
