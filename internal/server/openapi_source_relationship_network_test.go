package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	"github.com/gofiber/fiber/v3"
)

// This case proves the generated HTTP boundary and lifecycle ownership using
// explicit fake compilation/execution adapters. Native compiler semantics and
// real source edits have a separate resolver test; this is not browser proof.
func TestSourceRelationshipAndNetworkHTTPContract(t *testing.T) {
	ctx := context.Background()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: explorerScopeDigest(scope), SchemaDigest: "schema", ShapeDigest: "shape"}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}, nil, nil, nil)
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "c_patient", Expr: recipe.Expression{Select: "root.id"}}}}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	workspace, err := recipeimport.ImportWorkspace(bundle, authoringV2Catalog(snapshot, "custom"), map[string][]recipe.StageColumn{"patients": {{ID: "patient-id", Name: "c_patient", Label: "Patient ID", Type: "string"}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := workspace.Digest()
	store := newTestExplorerStore()
	if _, err := store.create(explorer.Explorer{Project: "project-a", ExplorerID: "custom", Title: "Patients", DraftConfig: raw, DraftVersion: 1, DraftDigest: digest}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	edits, stepEdits, networks, previews := 0, 0, 0, 0
	config := lifecycle.Config{Capability: lifecycle.CapabilityResolver{
		Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
		ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
			return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		},
		ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
			return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}, Catalog: authoringV2Catalog,
	},
		EditImportedRelationship: func(_ context.Context, project string, authorized lifecycle.AuthorizedCapability, document authoringv2.Document, change authoringv2.ImportedRelationshipChange) (authoringv2.Document, error) {
			edits++
			if project != "project-a" || authorized.Snapshot.Token != snapshot.Token || change.Action != "ADD" || change.ParentOccurrenceID != "base" || change.EdgeID != "authorized-edge" || change.MatchMode != authoringv2.RouteMatchOptional {
				t.Fatalf("transport retargeted intent: %#v", change)
			}
			plan := recipeimport.SourcePlan(output)
			setHTTPSourceStepEnabled(t, &plan, "fields", false)
			document.ImportedSource = &plan
			return document, nil
		},
		EditImportedSource: func(_ context.Context, project string, authorized lifecycle.AuthorizedCapability, document authoringv2.Document, change *authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
			stepEdits++
			if project != "project-a" || authorized.Snapshot.Token != snapshot.Token || change == nil || change.StepID != "fields" || !change.Enabled {
				t.Fatalf("source-step transport lost settings: %#v", change)
			}
			plan := recipeimport.SourcePlan(output)
			setHTTPSourceStepEnabled(t, &plan, "fields", change.Enabled)
			document.ImportedSource = &plan
			return document, nil
		},
		EditImportedSources: func(_ context.Context, project string, authorized lifecycle.AuthorizedCapability, document authoringv2.Document, changes []authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
			if project != "project-a" || authorized.Snapshot.Token != snapshot.Token || len(changes) != 2 {
				t.Fatal("HTTP batch lost one owner/intent")
			}
			plan := recipeimport.SourcePlan(output)
			for _, change := range changes {
				setHTTPSourceStepEnabled(t, &plan, change.StepID, change.Enabled)
			}
			document.ImportedSource = &plan
			return document, nil
		},
		AuthoringNetwork: func(_ context.Context, _ string, _ lifecycle.AuthorizedCapability, document authoringv2.Document) (authoringv2.AuthoringNetwork, error) {
			networks++
			network := authoringv2.NewAuthoringNetwork(document, "IMPORTED_SOURCE")
			network.Occurrences = append(network.Occurrences, authoringv2.NetworkOccurrence{OccurrenceID: "base", ResourceType: "Patient", Label: "Patient", Enabled: true, PreservesRowIdentity: true})
			network.Bindings = append(network.Bindings, authoringv2.NetworkColumnBinding{ColumnID: "patient-id", OccurrenceID: "base", SourceStepID: "fields", ValueMode: "ALL", Status: "EXACT"})
			return network, nil
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			receipt, err := persistTestNativeReceipt(ctx, t, service, request, snapshot)
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
			if receipt.SourceRelationshipProposal == nil || !bindings.IncludeRowIdentity || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "patients" {
				t.Fatal("preview did not use the bound source receipt")
			}
			if err := visit(map[string]any{"__loom_row_id": "patient-1", "c_patient": "p1"}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{RowCount: 1, Complete: true}, nil
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
	if proposal.CandidateDocument.Output.ID != "patients" || proposal.CandidateDocument.ImportedSource == nil || httpSourceStepEnabled(t, proposal.CandidateDocument.ImportedSource, "fields") || proposal.CandidateNetwork.OutputID != "patients" || proposal.CandidateNetwork.Owner != "IMPORTED_SOURCE" || len(proposal.CandidateNetwork.Bindings) != 1 || proposal.CandidateNetwork.Bindings[0].OccurrenceID != "base" || proposal.CandidateRowIdentity.OutputID != "patients" || len(proposal.CandidateRowIdentity.Stages) != 1 || proposal.CandidateRowIdentity.Stages[0].Identity.Kind != "RECORD" {
		t.Fatalf("HTTP transport lost authoritative candidate projection: %s", proposed.Body)
	}
	if *proposal.CandidateDocument.ImportedRecipe != *workspace.Documents[0].ImportedRecipe {
		t.Fatal("HTTP candidate projection changed immutable recipe pin")
	}
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
	if saved.DraftVersion != 2 || previews != 2 {
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
	// The same public receipt proposal reviews earlier bounded source settings.
	stepBody := fmt.Sprintf(`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","sourceStep":{"stepId":"fields","enabled":true}}`, snapshot.Token, saved.DraftVersion, saved.DraftDigest)
	stepResponse := requestJSON(t, app, http.MethodPost, basePath+"/source-relationship-proposals", stepBody)
	if stepResponse.StatusCode != http.StatusOK {
		t.Fatalf("source-step preview HTTP %d: %s", stepResponse.StatusCode, stepResponse.Body)
	}
	var stepProposal lifecycle.SourceRelationshipProposal
	if err := json.Unmarshal([]byte(stepResponse.Body), &stepProposal); err != nil {
		t.Fatal(err)
	}
	if stepProposal.ProposalID == "" || stepEdits != 1 {
		t.Fatal("source-step proposal lost its selected settings")
	}
	if stepProposal.CandidateDocument.ImportedSource == nil || !httpSourceStepEnabled(t, stepProposal.CandidateDocument.ImportedSource, "fields") || stepProposal.CandidateNetwork.OutputID != "patients" || stepProposal.CandidateRowIdentity.OutputID != "patients" {
		t.Fatalf("HTTP source-step candidate did not reflect restored settings: %s", stepResponse.Body)
	}
	afterStepPreview, err := service.Get(ctx, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if afterStepPreview.DraftVersion != saved.DraftVersion || afterStepPreview.DraftDigest != saved.DraftDigest || !bytes.Equal(afterStepPreview.DraftConfig, saved.DraftConfig) {
		t.Fatal("source-step HTTP Preview/Cancel changed draft")
	}
	stepApplyBody := fmt.Sprintf(`{"commandId":"source-step-restore","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_IMPORTED_RELATIONSHIP_PROPOSAL","outputId":"patients","proposalId":%q}]}`, authoringv2.CurrentSemanticsVersion, snapshot.Token, saved.DraftVersion, saved.DraftDigest, stepProposal.ProposalID)
	stepApplied := requestJSON(t, app, http.MethodPost, basePath+"/commands", stepApplyBody)
	if stepApplied.StatusCode != http.StatusOK {
		t.Fatalf("source-step Apply HTTP %d: %s", stepApplied.StatusCode, stepApplied.Body)
	}
	restored, err := service.Get(ctx, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	restoredWorkspace, err := authoringv2.DecodeWorkspace(restored.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if restored.DraftVersion != 3 || !httpSourceStepEnabled(t, restoredWorkspace.Documents[0].ImportedSource, "fields") || *restoredWorkspace.Documents[0].ImportedRecipe != *workspace.Documents[0].ImportedRecipe {
		t.Fatal("source-step HTTP Apply lost restore settings or immutable pin")
	}

	batchIdentity := fmt.Sprintf(`"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients"`, snapshot.Token, restored.DraftVersion, restored.DraftDigest)
	beforeBatch := append([]byte(nil), restored.DraftConfig...)
	batch := requestJSON(t, app, http.MethodPost, basePath+"/source-relationship-proposals", "{"+batchIdentity+`,"sourceSteps":[{"stepId":"output","enabled":true},{"stepId":"fields","enabled":false}]}`)
	if batch.StatusCode != http.StatusOK {
		t.Fatalf("batch HTTP %d: %s", batch.StatusCode, batch.Body)
	}
	var batchProposal lifecycle.SourceRelationshipProposal
	if err := json.Unmarshal([]byte(batch.Body), &batchProposal); err != nil {
		t.Fatal(err)
	}
	if batchProposal.DraftVersion != 3 || httpSourceStepEnabled(t, batchProposal.CandidateDocument.ImportedSource, "fields") || batchProposal.CandidateNetwork.OutputID != "patients" || batchProposal.CandidateRowIdentity.OutputID != "patients" {
		t.Fatal("HTTP batch candidate lost exact owner or setting")
	}
	afterBatch, err := service.Get(ctx, "project-a", "custom")
	if err != nil || !bytes.Equal(beforeBatch, afterBatch.DraftConfig) || afterBatch.DraftVersion != restored.DraftVersion {
		t.Fatal("batch Preview changed saved recipe")
	}

}

func setHTTPSourceStepEnabled(t *testing.T, plan *authoringv2.ImportedSourcePlan, id string, enabled bool) {
	t.Helper()
	for i := range plan.Steps {
		if plan.Steps[i].ID == id {
			plan.Steps[i].Enabled = enabled
			return
		}
	}
	t.Fatalf("missing source step %s", id)
}
func httpSourceStepEnabled(t *testing.T, plan *authoringv2.ImportedSourcePlan, id string) bool {
	t.Helper()
	if plan != nil {
		for _, step := range plan.Steps {
			if step.ID == id {
				return step.Enabled
			}
		}
	}
	t.Fatalf("missing source step %s", id)
	return false
}
