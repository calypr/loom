package server

import (
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
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/gofiber/fiber/v3"
)

func TestRowDefinitionHTTPContractListsChoicesAndPreviewsWithoutDraftMutation(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: explorerScopeDigest(scope),
			SchemaDigest: "schema-digest", ResourceInventoryDigest: "inventory", RelationshipDigest: "relationships",
			FieldDigest: "fields", ShapeDigest: "shape-digest", ProtocolVersion: "protocol", CompilerVersion: "compiler",
			TraversalPolicyVersion: "route-policy", ProjectionPolicyVersion: "projection-policy",
		},
		capability.Policy{Route: capability.RoutePolicy{Version: "route-policy"}, Projection: capability.ProjectionPolicy{Version: "projection-policy"}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "node-root", ResourceType: "SignalPacket", RowRootEligible: true, Populated: true}}, nil,
		[]capability.Candidate{{ID: "candidate-values", NodeID: "node-root", ResourceType: "SignalPacket", FieldPath: "values[]", Label: "Values", LogicalType: "string", Cardinality: "MANY", ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst}, SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true}}, nil,
	)
	index, err := fhirschema.NewIndex([]fhirschema.Definition{{
		Name:     "SignalPacket",
		Elements: []fhirschema.Element{{Name: "values", JSONType: fhirschema.JSONTypeArray, ItemJSONType: "string", Title: "Values", Description: "Repeated values"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	rowChoices, err := lifecycle.NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		Explorer: authoringv2.ExplorerMetadata{Title: "Signals"},
		Documents: []authoringv2.Document{{
			Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Signals", RowLabel: "Signals"},
			RootResourceType: "SignalPacket", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "SignalPacket"},
			Rows: authoringv2.RecordsRowDefinition(), Columns: []authoringv2.Column{},
		}},
		Tabs: []authoringv2.Tab{{ID: "signals-tab", Title: "Signals", OutputID: "patients", Visible: true}},
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := newTestExplorerStore()
	if _, err := store.create(explorer.Explorer{Project: "project-a", ExplorerID: "custom", Title: "Signals", DraftConfig: draft, DraftVersion: 1, DraftDigest: digest}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	previews := 0
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
		RowChoiceResolver: rowChoices,
		RowChoicePlanner:  rowChoices,
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return persistTestNativeReceipt(ctx, t, service, request, snapshot)
		},
		PreviewReceipt: func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			previews++
			if receipt == nil || !bindings.IncludeRowIdentity || bindings.OutputNames[0] != "patients" {
				t.Fatalf("preview did not receive an exact output and row identity request: receipt=%#v bindings=%#v", receipt, bindings)
			}
			value := any(0)
			if receipt.RowDefinitionProposal != nil {
				value = false
			}
			if err := visit(map[string]any{"__loom_row_id": "row-1", "value": value}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"value"}, RowCount: 1, Complete: true}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"
	list := requestJSON(t, app, http.MethodGet, basePath+"/row-definition-choices?outputId=patients&snapshotToken="+snapshot.Token, "")
	if list.StatusCode != http.StatusOK {
		t.Fatalf("choice list status=%d body=%s", list.StatusCode, list.Body)
	}
	var listed lifecycle.RowDefinitionChoicesResponse
	if err := json.Unmarshal([]byte(list.Body), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Choices) != 1 || listed.Choices[0].Kind != lifecycle.RowChoiceExpanded || listed.Choices[0].Label != "Values" || listed.Choices[0].ValueType != "ARRAY" || listed.Choices[0].RouteSummary != "Root" || listed.ExplicitGroups == nil || len(listed.ExplicitGroups) != 0 {
		t.Fatalf("listed row-definition choices = %#v", listed)
	}
	for _, forbidden := range []string{"resourceType", "fieldPath", "FHIRType", "schemaPath"} {
		if strings.Contains(list.Body, forbidden) {
			t.Fatalf("choice response leaked schema detail %q: %s", forbidden, list.Body)
		}
	}
	before, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	proposalBody := func(choiceID string) string {
		return fmt.Sprintf(`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","selection":{"kind":"EXPANDED","expanded":{"rowChoiceId":%q,"emptyCollectionPolicy":"PRESERVE_PARENT"}}}`,
			snapshot.Token, before.DraftVersion, before.DraftDigest, choiceID)
	}
	proposed := requestJSON(t, app, http.MethodPost, basePath+"/row-definition-proposals", proposalBody(listed.Choices[0].ChoiceID))
	if proposed.StatusCode != http.StatusOK {
		t.Fatalf("proposal status=%d body=%s", proposed.StatusCode, proposed.Body)
	}
	var proposal lifecycle.RowDefinitionProposal
	if err := json.Unmarshal([]byte(proposed.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalID == "" || proposal.Comparison.Status != lifecycle.RowDefinitionComparisonAvailable || proposal.Comparison.Base == nil || proposal.Comparison.Candidate == nil || proposal.Comparison.Base.RowCount != 1 || proposal.Comparison.Candidate.RowCount != 1 || proposal.Comparison.Base.Sampled || proposal.Comparison.Candidate.Sampled || len(proposal.Comparison.AffectedColumns) != 1 || proposal.Comparison.AffectedColumns[0] != "value" || previews != 2 {
		t.Fatalf("proposal comparison=%#v preview calls=%d", proposal.Comparison, previews)
	}
	if !strings.Contains(proposed.Body, `"rowIdentity":"row-1"`) || strings.Contains(proposed.Body, `"value":`) {
		t.Fatalf("comparison omitted identity or exposed row cell values: %s", proposed.Body)
	}
	after, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if after.DraftVersion != before.DraftVersion || after.DraftDigest != before.DraftDigest || string(after.DraftConfig) != string(before.DraftConfig) {
		t.Fatalf("proposal mutated the draft before apply: before=%#v after=%#v", before, after)
	}
	staleChoiceID := listed.Choices[0].ChoiceID[:len(listed.Choices[0].ChoiceID)-1] + "0"
	if strings.HasSuffix(listed.Choices[0].ChoiceID, "0") {
		staleChoiceID = listed.Choices[0].ChoiceID[:len(listed.Choices[0].ChoiceID)-1] + "1"
	}
	stale := requestJSON(t, app, http.MethodPost, basePath+"/row-definition-proposals", proposalBody(staleChoiceID))
	if stale.StatusCode != http.StatusConflict || !strings.Contains(stale.Body, `"code":"STALE_ROW_CHOICE"`) {
		t.Fatalf("stale choice status=%d body=%s", stale.StatusCode, stale.Body)
	}
	afterStale, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if afterStale.DraftVersion != before.DraftVersion || afterStale.DraftDigest != before.DraftDigest || string(afterStale.DraftConfig) != string(before.DraftConfig) {
		t.Fatalf("stale choice mutated the draft: before=%#v after=%#v", before, afterStale)
	}
}
