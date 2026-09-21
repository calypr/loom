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
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/tableshapecap"
	"github.com/gofiber/fiber/v3"
)

type tableShapeRouteRepository struct {
	catalogs    map[string]tableshapecap.CatalogReceipt
	scans       map[string]tableshapecap.CategoryScanReceipt
	resolutions map[string]tableshapecap.ResolutionReceipt
}

func newTableShapeRouteRepository() *tableShapeRouteRepository {
	return &tableShapeRouteRepository{
		catalogs: make(map[string]tableshapecap.CatalogReceipt), scans: make(map[string]tableshapecap.CategoryScanReceipt),
		resolutions: make(map[string]tableshapecap.ResolutionReceipt),
	}
}

func (r *tableShapeRouteRepository) PutCatalog(_ context.Context, value tableshapecap.CatalogReceipt) (tableshapecap.CatalogReceipt, error) {
	if err := value.Validate(); err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	r.catalogs[value.ID] = value
	return value, nil
}

func (r *tableShapeRouteRepository) GetCatalog(_ context.Context, binding tableshapecap.Binding, id string) (tableshapecap.CatalogReceipt, error) {
	value, ok := r.catalogs[id]
	if !ok || value.Binding != binding {
		return tableshapecap.CatalogReceipt{}, tableshapecap.ErrNotFound
	}
	return value, nil
}

func (r *tableShapeRouteRepository) PutCategoryScan(_ context.Context, value tableshapecap.CategoryScanReceipt) (tableshapecap.CategoryScanReceipt, error) {
	if err := value.Validate(); err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	r.scans[value.ID] = value
	return value, nil
}

func (r *tableShapeRouteRepository) GetCategoryScan(_ context.Context, binding tableshapecap.Binding, catalogID, id string) (tableshapecap.CategoryScanReceipt, error) {
	value, ok := r.scans[id]
	if !ok || value.Binding != binding || value.ParentCatalogID != catalogID {
		return tableshapecap.CategoryScanReceipt{}, tableshapecap.ErrNotFound
	}
	return value, nil
}

func (r *tableShapeRouteRepository) PutResolution(_ context.Context, value tableshapecap.ResolutionReceipt) (tableshapecap.ResolutionReceipt, error) {
	if err := value.Validate(); err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	r.resolutions[value.ID] = value
	return value, nil
}

func (r *tableShapeRouteRepository) GetResolution(_ context.Context, binding tableshapecap.Binding, catalogID, id string) (tableshapecap.ResolutionReceipt, error) {
	value, ok := r.resolutions[id]
	if !ok || value.Binding != binding || value.ParentCatalogID != catalogID {
		return tableshapecap.ResolutionReceipt{}, tableshapecap.ErrNotFound
	}
	return value, nil
}

func TestTableShapeHTTPContractCatalogDiscoveryAndResolution(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []struct{ name, label, logical string }{
		{"group_number", "Group number", "integer"},
		{"status", "Status", "string"},
		{"measure", "Measurement", "decimal"},
		{"measure_alt", "Alternate measurement", "decimal"},
	} {
		workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, authoringv2.Column{
			Column: column.name, Label: column.label, LogicalType: column.logical, OccurrenceID: "base",
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: column.name, ProjectionMode: "VALUE"}},
		})
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
	if _, err := store.create(explorer.Explorer{Project: "project-a", ExplorerID: "custom", Title: "Patients", DraftConfig: draft, DraftVersion: 1, DraftDigest: digest}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	scopeMode := authscope.ReadScopeUnrestricted
	config := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: scopeMode}}, nil
			},
			Catalog: authoringV2Catalog,
		},
		TableShapeCapabilities: newTableShapeRouteRepository(),
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			receipt, err := persistTestNativeReceipt(ctx, t, service, request, snapshot)
			if err != nil {
				return nil, err
			}
			return tableShapeRouteReceipt(t, service, receipt)
		},
		ScanTableShapeCategories: func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, request dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
			if request.Output != "patients" || request.Column != "status" || request.MaxValues < 2 {
				t.Fatalf("unexpected category scan request: %#v", request)
			}
			return dataframeexecution.CategoryScanResult{
				Values:   []dataframeexecution.CategoryValue{{Present: true, Value: "active"}, {Present: true, Value: "inactive"}},
				Complete: true,
				Proof:    compiler.CategoryScanProof{Version: 1, Output: "patients", Column: "status", Kind: "STRING", Cardinality: "optional_one", MaxValues: request.MaxValues, OutputSchemaDigest: "schema-proof", PlanFingerprint: "plan-proof", QueryFingerprint: "query-proof", Fingerprint: "scan-proof"},
			}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"
	before, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	catalogRequest := fmt.Sprintf(`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients"}`, snapshot.Token, before.DraftVersion, before.DraftDigest)
	catalogHTTP := requestJSON(t, app, http.MethodPost, basePath+"/table-shape-capabilities", catalogRequest)
	if catalogHTTP.StatusCode != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", catalogHTTP.StatusCode, catalogHTTP.Body)
	}
	var catalog loomapi.TableShapeCapabilitiesResponse
	if err := json.Unmarshal([]byte(catalogHTTP.Body), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.CatalogId == "" || catalog.OutputId != "patients" || catalog.PivotCategoryDiscovery.Kind != "not-requested" || len(catalog.ReshapeModes) != 3 || len(catalog.GroupColumns) == 0 || len(catalog.CategoryColumns) == 0 || len(catalog.ValueColumns) == 0 {
		t.Fatalf("catalog did not expose the editor role model: %#v", catalog)
	}
	hasNone, hasDivisionPolicy := false, false
	for _, mode := range catalog.ReshapeModes {
		hasNone = hasNone || mode.Mode == "NONE" && mode.Availability.Kind == "supported"
	}
	for _, operator := range catalog.BinaryOperators {
		hasDivisionPolicy = hasDivisionPolicy || operator.Label == "Divide" && operator.RequiresDivisionByZeroPolicy
	}
	if !hasNone || !hasDivisionPolicy || catalog.DerivedAvailability.Kind != "supported" || catalog.UnpivotWithDerivedAvailability.Kind != "unsupported" {
		t.Fatalf("catalog omitted reshape, operator-policy, or availability facts: %#v", catalog)
	}
	findChoice := func(choices []loomapi.TableShapeEditorChoice, label string) string {
		t.Helper()
		for _, choice := range choices {
			if choice.Label == label && choice.Availability.Kind == "supported" {
				return choice.ChoiceId
			}
		}
		t.Fatalf("missing supported choice %q in %#v", label, choices)
		return ""
	}
	groupID := findChoice(catalog.GroupColumns, "Group number")
	categoryID := findChoice(catalog.CategoryColumns, "Status")
	valueID := findChoice(catalog.ValueColumns, "Measurement")
	duplicateID := findChoice(catalog.DuplicatePolicies, "Reject duplicate cells")
	missingID := findChoice(catalog.MissingCellPolicies, "Use null for missing cells")
	unlistedID := findChoice(catalog.UnlistedCategoryPolicies, "Reject new categories")
	for _, forbidden := range []string{"columnKey", "constructionId", "query", "resourceType", "sourcePath", "FHIRType", "schemaPath"} {
		if strings.Contains(catalogHTTP.Body, forbidden) {
			t.Fatalf("capability response leaked backend-only field %q: %s", forbidden, catalogHTTP.Body)
		}
	}
	unknown := requestJSON(t, app, http.MethodPost, basePath+"/table-shape-capabilities", strings.TrimSuffix(catalogRequest, "}")+`,"extra":true}`)
	if unknown.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown capabilities field status=%d body=%s", unknown.StatusCode, unknown.Body)
	}
	badTaggedUnion := fmt.Sprintf(`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","catalogId":%q,"kind":"PIVOT","pivot":{},"unpivot":{}}`, snapshot.Token, before.DraftVersion, before.DraftDigest, catalog.CatalogId)
	invalidUnion := requestJSON(t, app, http.MethodPost, basePath+"/table-shape-resolutions", badTaggedUnion)
	if invalidUnion.StatusCode != http.StatusBadRequest {
		t.Fatalf("mismatched tagged selection status=%d body=%s", invalidUnion.StatusCode, invalidUnion.Body)
	}
	discoveryRequest := fmt.Sprintf(`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","catalogId":%q,"categoryColumnChoiceId":%q,"valueColumnChoiceId":%q}`, snapshot.Token, before.DraftVersion, before.DraftDigest, catalog.CatalogId, categoryID, valueID)
	discoveryHTTP := requestJSON(t, app, http.MethodPost, basePath+"/table-shape-category-discoveries", discoveryRequest)
	if discoveryHTTP.StatusCode != http.StatusOK {
		t.Fatalf("discovery status=%d body=%s", discoveryHTTP.StatusCode, discoveryHTTP.Body)
	}
	var discovery loomapi.TableShapeCategoryDiscoveryResponse
	if err := json.Unmarshal([]byte(discoveryHTTP.Body), &discovery); err != nil {
		t.Fatal(err)
	}
	if discovery.Kind != "complete" || discovery.DiscoveryIdentity == "" || discovery.Pair.CategoryColumn.ChoiceId != categoryID || discovery.Pair.ValueColumn.ChoiceId != valueID || len(discovery.Categories) != 2 || discovery.Categories[0].Value.String == nil || *discovery.Categories[0].Value.String != "active" {
		t.Fatalf("discovery did not return typed category identities: %#v", discovery)
	}
	resolution := loomapi.TableShapeResolutionRequest{
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: int(before.DraftVersion), ExpectedDraftDigest: before.DraftDigest,
		OutputId: "patients", CatalogId: catalog.CatalogId, Kind: loomapi.TableShapeResolutionRequestKindPIVOT,
		Pivot: &loomapi.TableShapePivotSelection{
			CategoryDiscoveryId: discovery.DiscoveryIdentity, GroupColumnChoiceIds: []string{groupID}, CategoryColumnChoiceId: categoryID,
			ValueColumnChoiceId: valueID, DuplicatePolicyChoiceId: duplicateID, MissingPolicyChoiceId: missingID,
			UnlistedPolicyChoiceId: unlistedID,
			Categories:             []loomapi.TableShapePivotCategorySelection{{ChoiceId: discovery.Categories[0].ChoiceId, OutputColumn: "active_measure", OutputLabel: "Active measurement"}},
		},
	}
	resolutionBody, err := json.Marshal(resolution)
	if err != nil {
		t.Fatal(err)
	}
	resolutionHTTP := requestJSON(t, app, http.MethodPost, basePath+"/table-shape-resolutions", string(resolutionBody))
	if resolutionHTTP.StatusCode != http.StatusOK {
		t.Fatalf("resolution status=%d body=%s", resolutionHTTP.StatusCode, resolutionHTTP.Body)
	}
	var resolved loomapi.TableShapeResolutionResponse
	if err := json.Unmarshal([]byte(resolutionHTTP.Body), &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.ResolutionId == "" || len(resolved.OutputDescriptors) != 2 || len(resolved.PostPivotOperands) < 2 {
		t.Fatalf("resolution lacks output descriptors or post-pivot operands: %#v", resolved)
	}
	var groupDescriptor, categoryDescriptor *loomapi.TableShapeResolvedOutputDescriptor
	for index := range resolved.OutputDescriptors {
		descriptor := &resolved.OutputDescriptors[index]
		switch descriptor.Kind {
		case "group":
			groupDescriptor = descriptor
		case "category":
			categoryDescriptor = descriptor
		}
	}
	if groupDescriptor == nil || groupDescriptor.GroupColumn == nil || groupDescriptor.GroupColumn.ChoiceId != groupID || groupDescriptor.OperandChoiceId == nil || groupDescriptor.Type.LogicalType != "INTEGER" || categoryDescriptor == nil || categoryDescriptor.Category == nil || categoryDescriptor.Category.ChoiceId != discovery.Categories[0].ChoiceId || categoryDescriptor.OperandChoiceId == nil || categoryDescriptor.Type.LogicalType != "DECIMAL" {
		t.Fatalf("resolved output-to-operand bindings are incomplete: %#v", resolved.OutputDescriptors)
	}
	operandIDs := map[string]bool{}
	for _, operand := range resolved.PostPivotOperands {
		operandIDs[operand.ChoiceId] = true
	}
	if !operandIDs[*groupDescriptor.OperandChoiceId] || !operandIDs[*categoryDescriptor.OperandChoiceId] {
		t.Fatalf("output descriptor operand IDs do not resolve to the returned post-pivot operands: %#v / %#v", resolved.OutputDescriptors, resolved.PostPivotOperands)
	}
	for name, body := range map[string]string{"catalog": catalogHTTP.Body, "discovery": discoveryHTTP.Body, "resolution": resolutionHTTP.Body} {
		for _, forbidden := range []string{"columnKey", "constructionId", "query", "resourceType", "sourcePath", "FHIRType", "schemaPath"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s response leaked backend-only field %q: %s", name, forbidden, body)
			}
		}
	}
	after, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if after.DraftVersion != before.DraftVersion || after.DraftDigest != before.DraftDigest || string(after.DraftConfig) != string(before.DraftConfig) {
		t.Fatalf("catalog/discovery/resolution mutated the draft: before=%#v after=%#v", before, after)
	}
	scopeMode = authscope.ReadScopeRestricted
	wrongScope := requestJSON(t, app, http.MethodPost, basePath+"/table-shape-capabilities", catalogRequest)
	if wrongScope.StatusCode != http.StatusConflict || !strings.Contains(wrongScope.Body, "STALE_AUTHORIZATION_SCOPE") {
		t.Fatalf("wrong-scope capability request status=%d body=%s", wrongScope.StatusCode, wrongScope.Body)
	}
	scopeMode = authscope.ReadScopeUnrestricted
	staleResolution := resolution
	staleResolution.SnapshotToken = "stale-snapshot-token"
	staleBody, err := json.Marshal(staleResolution)
	if err != nil {
		t.Fatal(err)
	}
	stale := requestJSON(t, app, http.MethodPost, basePath+"/table-shape-resolutions", string(staleBody))
	if stale.StatusCode != http.StatusConflict || !strings.Contains(stale.Body, "STALE_CATALOG_SNAPSHOT") {
		t.Fatalf("stale resolution status=%d body=%s", stale.StatusCode, stale.Body)
	}
	afterFailures, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if afterFailures.DraftVersion != before.DraftVersion || afterFailures.DraftDigest != before.DraftDigest || string(afterFailures.DraftConfig) != string(before.DraftConfig) {
		t.Fatalf("failed requests mutated the draft: before=%#v after=%#v", before, afterFailures)
	}
}

func tableShapeRouteReceipt(t *testing.T, service *explorer.Service, receipt *explorer.CompilationReceipt) (*explorer.CompilationReceipt, error) {
	t.Helper()
	columns := []struct {
		name, label, logical string
		nullable             bool
	}{
		{"patient_id", "Patient ID", "string", false},
		{"group_number", "Group number", "integer", false},
		{"status", "Status", "string", true},
		{"measure", "Measurement", "decimal", true},
		{"measure_alt", "Alternate measurement", "decimal", false},
	}
	contract := explorer.PublicOutputContract{OutputID: "patients", Columns: make([]explorer.PublicOutputColumn, 0, len(columns))}
	receipt.EmittedColumns = make([]explorer.EmittedColumn, 0, len(columns))
	provenance := make(map[string]string, len(columns))
	for index, column := range columns {
		contract.Columns = append(contract.Columns, explorer.PublicOutputColumn{
			Column: column.name, Label: column.label, LogicalType: column.logical, Cardinality: "optional_one",
			Nullable: column.nullable, Shape: "scalar",
		})
		receipt.EmittedColumns = append(receipt.EmittedColumns, explorer.EmittedColumn{
			EmissionID: fmt.Sprintf("table_shape_%d", index), OutputID: "patients", PublicColumn: column.name,
			Label: column.label, LogicalType: column.logical, Cardinality: "optional_one", Nullable: column.nullable, Shape: "scalar",
		})
		provenance[column.name] = "EXPLICIT"
	}
	contractJSON, err := json.Marshal(explorer.PublicOutputContracts{Outputs: []explorer.PublicOutputContract{contract}})
	if err != nil {
		return nil, err
	}
	receipt.PublicOutputContract = contractJSON
	receipt.OutputContractDigest, err = explorer.CompilationArtifactDigest(contractJSON)
	if err != nil {
		return nil, err
	}
	receipt.OutputFingerprints = map[string]string{"patients": "table-shape-route-output"}
	receipt.OutputColumnProvenance = map[string]map[string]string{"patients": provenance}
	receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
	if err != nil {
		return nil, err
	}
	receipt.ID, err = explorer.ReceiptID(*receipt)
	if err != nil {
		return nil, err
	}
	return service.StoreCompilationReceipt(context.Background(), *receipt)
}
