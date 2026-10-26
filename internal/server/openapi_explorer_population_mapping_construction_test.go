package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestPopulationMappingRouteUsesReceiptBoundAuthoredFilterConstruction(t *testing.T) {
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
		ID:     "keep-present-patient-id",
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{
			Kind:   authoringv2.ConstructionOperationFilter,
			Filter: &authoringv2.ConstructionFilter{ColumnID: columns[0].ID, Operator: authoringv2.ConstructionFilterExists},
		},
		Outputs: columns,
	}}
	selectionID := "selection-filter-exists"
	document.Population = &authoringv2.Population{SelectionRevisionID: selectionID, Route: []authoringv2.PopulationRouteStep{}}
	workspace.Documents[0] = document
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringV2Catalog(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringV2Catalog(snapshot, "custom")).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate saved FILTER EXISTS workspace: %v", err)
	}
	rawDraft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	draftDigest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}

	const project, explorerID = "project-a", "custom"
	store := &populationProposalHTTPStore{
		testExplorerStore: newTestExplorerStore(),
		selections:        map[string]explorer.SelectionRevision{},
		members:           map[string][]explorer.SelectionMember{},
	}
	if _, err := store.create(explorer.Explorer{Project: project, ExplorerID: explorerID, Title: "Patients", DraftConfig: rawDraft, DraftVersion: 1, DraftDigest: draftDigest}); err != nil {
		t.Fatal(err)
	}
	members := []explorer.SelectionMember{
		{Ref: explorer.ResourceRef{Project: project, Generation: snapshot.Identity.Generation, ResourceType: "Patient", ID: "patient-1"}},
		{Ref: explorer.ResourceRef{Project: project, Generation: snapshot.Identity.Generation, ResourceType: "Patient", ID: "patient-2"}},
		{Ref: explorer.ResourceRef{Project: project, Generation: snapshot.Identity.Generation, ResourceType: "Patient", ID: "patient-3"}},
	}
	completedAt := time.Now().UTC()
	store.selections[selectionID] = explorer.SelectionRevision{
		ID: selectionID, Project: project, Generation: snapshot.Identity.Generation, ResourceType: "Patient",
		Rule: explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit}, Source: explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest: snapshot.Identity.AuthorizationScopeDigest, RuleDigest: "explicit-membership-rule",
		MembershipDigest: explorer.MembershipDigest(members), MemberCount: int64(len(members)), MemberBytes: 256,
		Complete: true, CreatedAt: completedAt, CompletedAt: &completedAt,
	}
	store.members[selectionID] = append([]explorer.SelectionMember(nil), members...)

	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	var mappingCalls, queryCalls int
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{},
		QueryRows: func(_ context.Context, query string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			queryCalls++
			if !strings.Contains(query, "FILTER") {
				return fmt.Errorf("population mapping query omitted the authored filter: %s", query)
			}
			for _, row := range []map[string]any{
				{ir.PhysicalPopulationMappingMemberField: "patient-1", ir.PhysicalPopulationMappingIdentityPartsField: []any{"patient-1"}, ir.PhysicalPopulationMappingExplicitIdentityField: "patient-1"},
				{ir.PhysicalPopulationMappingMemberField: "patient-2", ir.PhysicalPopulationMappingIdentityPartsField: []any{"patient-2"}, ir.PhysicalPopulationMappingExplicitIdentityField: "patient-2"},
			} {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	cursorCodec, err := lifecycle.NewHMACPopulationMappingCursorCodec("population-mapping-boundary-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	config := lifecycle.Config{
		SelectionMembersCollection:   "loom_explorer_selection_members",
		PopulationMappingCursorCodec: cursorCodec,
		Capability: lifecycle.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		ReceiptLookup: domain.CompilationReceiptForExplorer,
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, engine, domain, nil, nil)
		},
		PopulationMapping: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, output string, memberIDs []string, after string, limit int) (dataframeexecution.PopulationMappingResult, error) {
			mappingCalls++
			selectedIDs := append([]string(nil), memberIDs...)
			sort.Strings(selectedIDs)
			if receipt == nil || output != "patients" || !reflect.DeepEqual(selectedIDs, []string{"patient-1", "patient-2", "patient-3"}) {
				return dataframeexecution.PopulationMappingResult{}, fmt.Errorf("population mapping lost the receipt output or exact selected IDs: receipt=%#v output=%q members=%v", receipt, output, selectedIDs)
			}
			resolved, err := compileValidatedReceiptResolution(ctx, engine, receipt, bindings)
			if err != nil {
				return dataframeexecution.PopulationMappingResult{}, err
			}
			reader := dataframeexecution.PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
				for _, id := range memberIDs {
					if err := visit(id); err != nil {
						return err
					}
				}
				return nil
			})
			return engine.PopulationMapping(ctx, resolved, dataframeexecution.PopulationMappingRequest{Output: output, AfterMemberID: after, MaxUnmapped: limit}, reader)
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, domain, config)
	basePath := "/api/v1/projects/" + project + "/explorers/" + explorerID + "/authoring/v2"

	reconcile := requestJSON(t, app, http.MethodPost, basePath+"/reconcile", fmt.Sprintf(`{"snapshotToken":%q,"draftVersion":1,"draftDigest":%q}`, snapshot.Token, draftDigest))
	if reconcile.StatusCode != http.StatusOK {
		t.Fatalf("reconcile saved FILTER EXISTS construction status=%d body=%s", reconcile.StatusCode, reconcile.Body)
	}
	var compiled loomapi.CompileResponse
	if err := json.Unmarshal([]byte(reconcile.Body), &compiled); err != nil {
		t.Fatal(err)
	}
	if compiled.ReceiptId == "" {
		t.Fatalf("reconcile returned no persisted receipt: %s", reconcile.Body)
	}
	receipt, err := domain.CompilationReceiptForExplorer(context.Background(), project, explorerID, compiled.ReceiptId)
	if err != nil {
		t.Fatalf("load reconciled receipt: %v", err)
	}
	if len(receipt.Bundle.Outputs) != 1 || receipt.Bundle.Outputs[0].Construction == nil || len(receipt.Bundle.Outputs[0].Construction.Steps) != 1 ||
		receipt.Bundle.Outputs[0].Construction.Steps[0].Operation.Kind != recipe.ConstructionFilterOp ||
		receipt.Bundle.Outputs[0].Construction.Steps[0].Operation.Filter == nil ||
		receipt.Bundle.Outputs[0].Construction.Steps[0].Operation.Filter.Operator != recipe.FilterExists {
		t.Fatalf("receipt did not retain exactly one authored FILTER EXISTS: %#v", receipt.Bundle.Outputs)
	}

	wrongReceipt := requestJSON(t, app, http.MethodPost, basePath+"/population-mapping", `{"receiptId":"missing-receipt","outputId":"patients","limit":10}`)
	if wrongReceipt.StatusCode != http.StatusNotFound || mappingCalls != 0 || queryCalls != 0 {
		t.Fatalf("unknown receipt status=%d mapping calls=%d query calls=%d body=%s", wrongReceipt.StatusCode, mappingCalls, queryCalls, wrongReceipt.Body)
	}

	populationMapping := requestJSON(t, app, http.MethodPost, basePath+"/population-mapping", fmt.Sprintf(`{"receiptId":%q,"outputId":"patients","limit":10}`, receipt.ID))
	if populationMapping.StatusCode != http.StatusOK {
		t.Fatalf("receipt-bound authored-construction population mapping status=%d body=%s", populationMapping.StatusCode, populationMapping.Body)
	}
	var mapped loomapi.PopulationMappingResponse
	if err := json.Unmarshal([]byte(populationMapping.Body), &mapped); err != nil {
		t.Fatal(err)
	}
	if mapped.Binding.ReceiptId != receipt.ID || mapped.Binding.Project != project || mapped.Binding.ExplorerId != explorerID || mapped.Binding.OutputId != "patients" || mapped.Binding.Generation != snapshot.Identity.Generation || mapped.Binding.SelectionRevisionId != selectionID {
		t.Fatalf("population mapping response is not bound to the reconciled receipt and selection: %#v", mapped.Binding)
	}
	if mapped.Status != loomapi.PopulationMappingResponseStatusCOMPLETE || mapped.Counts == nil || mapped.Counts.Selected != 3 || mapped.Counts.Mapped != 2 || mapped.Counts.Unmapped != 1 {
		t.Fatalf("population mapping counts = %#v status=%q, want COMPLETE 3 selected / 2 mapped / 1 unmapped", mapped.Counts, mapped.Status)
	}
	if len(mapped.Unmapped) != 1 || mapped.Unmapped[0].Id != "patient-3" || mappingCalls != 1 || queryCalls != 1 {
		t.Fatalf("population mapping unmapped/call counts = %#v/%d/%d", mapped.Unmapped, mappingCalls, queryCalls)
	}
}
