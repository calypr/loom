package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	explorerarango "github.com/calypr/loom/internal/explorer/arango"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/gofiber/fiber/v3"
)

// Recovers the deleted saved-route journey's command/identity guarantees at the
// real public HTTP/service/compiler boundary. Preview execution is deliberately
// empty: this does not pretend a stubbed preview proves graph-row semantics.
func TestSavedRelatedRouteProposalApplyRestoreAndFreshRead(t *testing.T) {
	savedRelatedRouteRoundtrip(t, nil)
}

func savedRelatedRouteRoundtrip(t *testing.T, rowClient *storearango.Client) {
	t.Helper()
	snapshot := testAuthoringV2CapabilitySnapshot()
	for _, resource := range []string{"Observation", "Group", "Specimen"} {
		snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "n_" + strings.ToLower(resource), ResourceType: resource, Populated: true, DocumentCount: 2})
	}
	edge := func(id, from, to, relationship string) capability.Edge {
		return capability.Edge{ID: id, FromNodeID: "n_" + strings.ToLower(from), ToNodeID: "n_" + strings.ToLower(to), SourceResourceType: from, TargetResourceType: to, Label: relationship, StorageDirection: "INBOUND"}
	}
	snapshot.Edges = []capability.Edge{edge("route_a", "Patient", "Observation", "subject_Patient"), edge("route_b_1", "Patient", "Group", "member_entity_Patient"), edge("route_b_2", "Group", "Specimen", "subject_Group"), edge("route_b_3", "Specimen", "Observation", "specimen_Specimen")}
	route := func(indices ...int) []capability.ConstructionRouteStep {
		var result []capability.ConstructionRouteStep
		for _, i := range indices {
			e := snapshot.Edges[i]
			result = append(result, capability.ConstructionRouteStep{EdgeID: e.ID, FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, FromResourceType: e.SourceResourceType, ToResourceType: e.TargetResourceType, Relationship: e.Label, StorageDirection: e.StorageDirection, MatchMode: "OPTIONAL"})
		}
		return result
	}
	routeA, routeB := route(0), route(1, 2, 3)
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	outputs := []authoringv2.StageColumn{}
	for _, column := range document.Columns {
		outputs = append(outputs, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	outputs = append(outputs, authoringv2.StageColumn{ID: "observation_id", Name: "observation_id", Label: "Observation FHIR resource ID", Type: "string"})
	construction := func(path []capability.ConstructionRouteStep) authoringv2.Construction {
		choice, err := capability.NewConstructionRelatedResourceRouteChoiceFromAnchor(snapshot.Token, "source_projection", "_key", "root", "n_patient", "Patient", "n_observation", "Observation", path)
		if err != nil {
			t.Fatal(err)
		}
		return authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{ID: "expand_observations", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}}, Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedExpand, RelatedExpand: &authoringv2.ConstructionRelatedExpand{AnchorColumnID: "_key", ChoiceID: choice.ChoiceID, TargetNodeID: "n_observation", TargetResourceType: "Observation", Route: path, ContributorRule: authoringv2.ConstructionRelatedContributorRule{Policy: authoringv2.ConstructionRelatedAllMatches}, EmptyPolicy: authoringv2.ConstructionExpandEmptyExclude, RelatedRecordColumnID: "observation_id"}}, Outputs: outputs}}}
	}
	initial := construction(routeA)
	document.Construction = &initial
	workspace.Documents[0] = document
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringV2Catalog(snapshot, "route-roundtrip"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringV2Catalog(snapshot, "route-roundtrip")).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatal(err)
	}
	original, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	var store explorer.Store = newTestExplorerStore()
	if rowClient != nil {
		if err := rowClient.Bootstrap(context.Background(), explorerarango.BootstrapSpec()); err != nil {
			t.Fatal(err)
		}
		store, err = explorerarango.New(rowClient)
		if err != nil {
			t.Fatal(err)
		}
	}
	owner := explorer.Explorer{Project: "project-a", ExplorerID: "route-roundtrip", Title: "Patients", DraftConfig: original, DraftVersion: 3, DraftDigest: digest, DraftSnapshotToken: snapshot.Token, DraftSourceGeneration: snapshot.Identity.Generation, DraftAuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, ActiveRevisionID: "published_original"}
	if _, err := store.Create(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertRevision(context.Background(), explorer.Revision{ID: owner.ActiveRevisionID, Project: owner.Project, ExplorerID: owner.ExplorerID, SourceGeneration: snapshot.Identity.Generation, AuthoringBundle: original}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	queryRows := func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }
	if rowClient != nil {
		queryRows = func(ctx context.Context, query string, batch int, variables map[string]any, visit func(map[string]any) error) error {
			return rowClient.QueryRows(ctx, query, batch, variables, storearango.RowVisitor(visit))
		}
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{Registry: compilerTestRegistry{}, QueryRows: queryRows})
	if err != nil {
		t.Fatal(err)
	}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	compiled := 0
	config := lifecycle.Config{Capability: lifecycle.CapabilityResolver{Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil }, Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil }, ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
		return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}, ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
		return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}, Catalog: authoringV2Catalog}, CompileReceipt: func(ctx context.Context, r lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		result, err := compileExplorerReceipt(ctx, r, nil, engine, service, nil, nil)
		if err == nil {
			compiled++
		}
		return result, err
	}, PreviewReceipt: func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"c_patient", "observation_id"}, RowCount: 0, Complete: true}, nil
	}}
	if rowClient != nil {
		config.PreviewReceipt = func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			resolved, err := compileValidatedReceiptResolution(ctx, engine, receipt, bindings)
			if err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{Output: bindings.OutputNames[0], Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity}, visit)
		}
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	base := "/api/v1/projects/project-a/explorers/route-roundtrip/authoring/v2"
	type saved struct {
		Workspace *authoringv2.Workspace `json:"workspace"`
		Version   int64                  `json:"draftVersion"`
		Digest    string                 `json:"draftDigest"`
		Previous  string                 `json:"previousDraftRevisionId"`
	}
	load := func() saved {
		t.Helper()
		r := requestJSON(t, app, http.MethodGet, base+"/builder", "")
		if r.StatusCode != http.StatusOK {
			t.Fatalf("fresh read status=%d body=%s", r.StatusCode, r.Body)
		}
		var v saved
		if err := json.Unmarshal([]byte(r.Body), &v); err != nil {
			t.Fatal(err)
		}
		if v.Workspace == nil {
			t.Fatal("fresh read omitted workspace")
		}
		return v
	}
	assertRoute := func(v saved, path []capability.ConstructionRouteStep) {
		t.Helper()
		d := v.Workspace.Documents[0]
		if d.Output.ID != "patients" || d.Construction == nil || len(d.Construction.Steps) != 1 || d.Construction.Steps[0].ID != "expand_observations" || d.Construction.Steps[0].Operation.RelatedExpand.RelatedRecordColumnID != "observation_id" || !reflect.DeepEqual(d.Construction.Steps[0].Operation.RelatedExpand.Route, path) {
			t.Fatalf("fresh read lost exact route or stable IDs: %#v", d)
		}
		actualOutputs := d.Construction.Steps[0].Outputs
		if len(actualOutputs) != len(outputs) {
			t.Fatal("route edit changed output column count")
		}
		for i, expected := range outputs {
			if actualOutputs[i].ID != expected.ID || actualOutputs[i].Name != expected.Name {
				t.Fatalf("route edit changed column identity/order: got=%#v want=%#v", actualOutputs, outputs)
			}
		}
	}
	assertRoute(load(), routeA)
	reconcile := func(version int64, draftDigest string) string {
		t.Helper()
		response := requestJSON(t, app, http.MethodPost, base+"/reconcile", fmt.Sprintf(`{"snapshotToken":%q,"draftVersion":%d,"draftDigest":%q,"outputId":"patients"}`, snapshot.Token, version, draftDigest))
		if response.StatusCode != http.StatusOK {
			t.Fatalf("route compiler status=%d body=%s", response.StatusCode, response.Body)
		}
		var value struct {
			ReceiptID   string `json:"receiptId"`
			Diagnostics []struct {
				Severity string `json:"severity"`
			} `json:"diagnostics"`
		}
		if err := json.Unmarshal([]byte(response.Body), &value); err != nil {
			t.Fatal(err)
		}
		if value.ReceiptID == "" {
			t.Fatalf("route compiler returned no receipt: %s", response.Body)
		}
		for _, diagnostic := range value.Diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("route compiler rejected route: %s", response.Body)
			}
		}
		return value.ReceiptID
	}
	assertRows := func(receiptID string, want []string) {
		t.Helper()
		if rowClient == nil {
			return
		}
		response := requestJSON(t, app, http.MethodPost, base+"/preview", fmt.Sprintf(`{"receiptId":%q,"outputId":"patients","limit":20}`, receiptID))
		if response.StatusCode != http.StatusOK {
			t.Fatalf("actual route row query status=%d body=%s", response.StatusCode, response.Body)
		}
		var preview struct {
			Rows      []map[string]any `json:"rows"`
			ReceiptID string           `json:"receiptId"`
			OutputID  string           `json:"outputId"`
			RowCount  int              `json:"rowCount"`
		}
		if err := json.Unmarshal([]byte(response.Body), &preview); err != nil {
			t.Fatal(err)
		}
		var pairs []string
		for _, row := range preview.Rows {
			pairs = append(pairs, fmt.Sprintf("%v/%v", row["c_patient"], row["observation_id"]))
		}
		sort.Strings(pairs)
		if preview.ReceiptID != receiptID || preview.OutputID != "patients" || preview.RowCount != len(want) || !reflect.DeepEqual(pairs, want) {
			t.Fatalf("real compiled route rows = %v (count=%d), want literal %v; body=%s", pairs, preview.RowCount, want, response.Body)
		}
	}
	assertRows(reconcile(3, digest), []string{"p1/o1", "p2/o2"})
	candidate, _ := json.Marshal(construction(routeB))
	proposalHTTP := requestJSON(t, app, http.MethodPost, base+"/construction-proposals", fmt.Sprintf(`{"snapshotToken":%q,"expectedDraftVersion":3,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"expand_observations","candidateConstruction":%s}`, snapshot.Token, digest, candidate))
	if proposalHTTP.StatusCode != http.StatusOK {
		t.Fatalf("route B proposal status=%d body=%s", proposalHTTP.StatusCode, proposalHTTP.Body)
	}
	var proposed loomapi.ConstructionProposalResponse
	if err := json.Unmarshal([]byte(proposalHTTP.Body), &proposed); err != nil {
		t.Fatal(err)
	}
	if proposed.ProposalId == nil || proposed.Preview == nil || proposed.Preview.ReceiptId != *proposed.ProposalId {
		t.Fatalf("proposal missing reviewed preview: %#v", proposed)
	}
	// Cancel is client-local: merely reviewing a proposal must not mutate the draft.
	canceled := load()
	raw, _ := canceled.Workspace.CanonicalJSON()
	if canceled.Version != 3 || canceled.Digest != digest || !bytes.Equal(raw, original) {
		t.Fatal("unapplied proposal changed saved draft")
	}
	command := func(id string, version int64, expected string, commands string) string {
		return fmt.Sprintf(`{"commandId":%q,"semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":%s}`, id, authoringv2.CurrentSemanticsVersion, snapshot.Token, version, expected, commands)
	}
	apply := fmt.Sprintf(`[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":%q}]`, *proposed.ProposalId)
	rejected := requestJSON(t, app, http.MethodPost, base+"/commands", command("stale-route-apply", 2, digest, apply))
	if rejected.StatusCode != http.StatusConflict {
		t.Fatalf("stale Apply status=%d body=%s", rejected.StatusCode, rejected.Body)
	}
	assertRoute(load(), routeA)
	appliedHTTP := requestJSON(t, app, http.MethodPost, base+"/commands", command("apply-route-b", 3, digest, apply))
	if appliedHTTP.StatusCode != http.StatusOK {
		t.Fatalf("Apply status=%d body=%s", appliedHTTP.StatusCode, appliedHTTP.Body)
	}
	applied := load()
	assertRoute(applied, routeB)
	if applied.Version != 4 || applied.Digest == digest || applied.Previous == "" {
		t.Fatalf("Apply lost CAS/Undo identity: %#v", applied)
	}
	if rowClient != nil {
		assertRows(reconcile(applied.Version, applied.Digest), []string{"p1/o2"})
	}
	replay := requestJSON(t, app, http.MethodPost, base+"/commands", command("apply-route-b", 3, digest, apply))
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("duplicate Apply status=%d body=%s", replay.StatusCode, replay.Body)
	}
	if got := load(); got.Version != applied.Version || got.Digest != applied.Digest {
		t.Fatal("duplicate Apply mutated saved draft")
	}
	restoredHTTP := requestJSON(t, app, http.MethodPost, base+"/commands", command("restore-route-a", applied.Version, applied.Digest, fmt.Sprintf(`[{"type":"RESTORE_DRAFT_REVISION","draftRevisionId":%q}]`, applied.Previous)))
	if restoredHTTP.StatusCode != http.StatusOK {
		t.Fatalf("Restore status=%d body=%s", restoredHTTP.StatusCode, restoredHTTP.Body)
	}
	restored := load()
	assertRoute(restored, routeA)
	restoredRaw, _ := restored.Workspace.CanonicalJSON()
	if restored.Version != 5 || restored.Digest != digest || !bytes.Equal(restoredRaw, original) {
		t.Fatalf("restore did not recover exact route-A workspace/version: %#v", restored)
	}
	lateApply := requestJSON(t, app, http.MethodPost, base+"/commands", command("late-route-b", 3, digest, apply))
	if lateApply.StatusCode != http.StatusConflict {
		t.Fatalf("late Apply after restore status=%d body=%s", lateApply.StatusCode, lateApply.Body)
	}
	if got := load(); got.Version != restored.Version || got.Digest != restored.Digest {
		t.Fatal("late Apply changed restored CAS")
	}
	assertRoute(load(), routeA)
	assertRows(reconcile(restored.Version, restored.Digest), []string{"p1/o1", "p2/o2"})
	stored, err := service.Get(context.Background(), owner.Project, owner.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ActiveRevisionID != owner.ActiveRevisionID {
		t.Fatal("draft roundtrip changed published revision")
	}
	if rowClient != nil {
		// A fresh repository instance reads persisted bytes rather than the service's
		// response or the in-memory fixture used by the fast structural case.
		fresh, err := explorerarango.New(rowClient)
		if err != nil {
			t.Fatal(err)
		}
		persisted, err := fresh.Get(context.Background(), owner.Project, owner.ExplorerID)
		if err != nil {
			t.Fatal(err)
		}
		persistedWorkspace, err := authoringv2.DecodeWorkspace(persisted.DraftConfig)
		if err != nil {
			t.Fatal(err)
		}
		persistedRaw, err := persistedWorkspace.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if persisted.DraftVersion != restored.Version || persisted.DraftDigest != digest || !bytes.Equal(persistedRaw, original) {
			t.Fatalf("new Arango repository read lost restored draft: version=%d digest=%s workspace=%s", persisted.DraftVersion, persisted.DraftDigest, persistedRaw)
		}
		prior, err := fresh.GetDraftRevision(context.Background(), owner.Project, owner.ExplorerID, applied.Previous)
		if err != nil {
			t.Fatal(err)
		}
		priorWorkspace, err := authoringv2.DecodeWorkspace(prior.DraftConfig)
		if err != nil {
			t.Fatal(err)
		}
		priorRaw, err := priorWorkspace.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if prior.DraftVersion != 3 || prior.DraftDigest != digest || !bytes.Equal(priorRaw, original) {
			t.Fatal("Arango history overwrote original route-A revision")
		}
	}

	if compiled < 3 {
		t.Fatal("initial, proposed or restored route bypassed production compiler")
	}
}
