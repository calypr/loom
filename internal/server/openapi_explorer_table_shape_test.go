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

func (r *tableShapeRouteRepository) GetCatalogForLookup(_ context.Context, lookup tableshapecap.CatalogLookup, id string) (tableshapecap.CatalogReceipt, error) {
	value, ok := r.catalogs[id]
	if !ok || !lookup.Matches(value.Binding) {
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
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringV2Catalog(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringV2Catalog(snapshot, "custom")).NormalizePresentationOrders()
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
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
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
		ScanCategories: func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, request dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
			if request.Output != "patients" || request.Column != "status" || request.MaxValues < 2 {
				t.Fatalf("unexpected category scan request: %#v", request)
			}
			return dataframeexecution.CategoryScanResult{
				Values:   []dataframeexecution.CategoryValue{{Present: true, Value: "active"}, {Present: true, Value: "inactive"}},
				Complete: true,
				Proof:    compiler.CategoryScanProof{Version: 1, Output: "patients", Column: "status", Kind: "STRING", Cardinality: "optional_one", MaxValues: request.MaxValues, OutputSchemaDigest: "schema-proof", PlanFingerprint: "plan-proof", QueryFingerprint: "query-proof", Fingerprint: "scan-proof"},
			}, nil
		},
		PreviewReceipt: func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			workspace, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
			if err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			if workspace.Documents[0].TableShape == nil {
				if err := visit(map[string]any{"__loom_row_id": "row-1", "patient_id": "patient-1", "group_number": int64(7), "status": "active", "measure": 2.5, "measure_alt": 4.5}); err != nil {
					return dataframeexecution.PreviewSummary{}, err
				}
				return dataframeexecution.PreviewSummary{Columns: []string{"patient_id", "group_number", "status", "measure", "measure_alt"}, RowCount: 1, Complete: true}, nil
			}
			if err := visit(map[string]any{"__loom_row_id": "row-1", "group_number": int64(7), "active_measure": 2.5}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Columns: []string{"group_number", "active_measure"}, RowCount: 1, Complete: true}, nil
		},
		CellTrace: func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
			if receipt.TableShapeProposal == nil || request.RowID != "row-1" || request.Column != "active_measure" {
				t.Fatalf("unexpected table-shape cell trace request: receipt=%#v request=%#v", receipt.TableShapeProposal, request)
			}
			return dataframeexecution.CellTraceResult{
				RowID: request.RowID, Column: request.Column, Status: dataframeexecution.CellTraceValue,
				Contributions: []dataframeexecution.CellTraceContribution{{ResourceType: "Observation", ResourceID: "obs-1", Value: 2.5}},
				Complete:      true,
			}, nil
		},
		TableShapeExclusions: func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.TableShapeExclusionRequest) (dataframeexecution.TableShapeExclusionResult, error) {
			if receipt == nil || receipt.TableShapeProposal == nil || receipt.TableShapeProposal.OutputID != "patients" || request.Output != "patients" || request.Offset != 0 || request.Limit != 25 || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "patients" {
				t.Fatalf("unexpected table-shape exclusion request: receipt=%#v bindings=%#v request=%#v", receipt, bindings, request)
			}
			return dataframeexecution.TableShapeExclusionResult{
				Status: dataframeexecution.TableShapeExclusionIncomplete,
				Exclusions: []dataframeexecution.TableShapeExclusion{
					{SourceIdentity: &dataframeexecution.TableShapeSourceIdentity{ResourceType: "Observation", ResourceID: "obs-false"}, Category: dataframeexecution.CategoryValue{Present: true, Value: false}, CategoryType: "BOOLEAN", OutputRowID: "row-false", Reason: "UNLISTED_CATEGORY"},
					{SourceIdentity: &dataframeexecution.TableShapeSourceIdentity{ResourceType: "Observation", ResourceID: "obs-zero"}, Category: dataframeexecution.CategoryValue{Present: true, Value: int64(0)}, CategoryType: "INTEGER", OutputRowID: "row-zero", Reason: "UNLISTED_CATEGORY"},
					{SourceIdentity: &dataframeexecution.TableShapeSourceIdentity{ResourceType: "Observation", ResourceID: "obs-empty"}, Category: dataframeexecution.CategoryValue{Present: true, Value: ""}, CategoryType: "STRING", OutputRowID: "row-empty", Reason: "UNLISTED_CATEGORY"},
					{Category: dataframeexecution.CategoryValue{Present: false, Value: nil}, CategoryType: "STRING", OutputRowID: "row-omitted", Reason: "UNLISTED_CATEGORY", OmissionCode: "TABLE_SHAPE_SOURCE_IDENTITY_UNAVAILABLE"},
					{SourceIdentity: &dataframeexecution.TableShapeSourceIdentity{ResourceType: "Observation", ResourceID: "obs-null"}, Category: dataframeexecution.CategoryValue{Present: true, Value: nil}, CategoryType: "NULL", OutputRowID: "row-null", Reason: "UNLISTED_CATEGORY"},
				},
				Complete: false, HasMore: true, NextOffset: 25,
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
	proposalBody := fmt.Sprintf(`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","mode":"ADD","catalogId":%q,"reshapeResolutionId":%q,"limit":25}`,
		snapshot.Token, before.DraftVersion, before.DraftDigest, catalog.CatalogId, resolved.ResolutionId)
	proposalHTTP := requestJSON(t, app, http.MethodPost, basePath+"/table-shape-proposals", proposalBody)
	if proposalHTTP.StatusCode != http.StatusOK {
		t.Fatalf("table-shape proposal status=%d body=%s", proposalHTTP.StatusCode, proposalHTTP.Body)
	}
	serverTiming := proposalHTTP.Headers.Get("Server-Timing")
	for _, metric := range []string{
		"candidate-compile;dur=", "base-preview;dur=", "candidate-preview;dur=", "row-diff;dur=",
		"cell-trace;dur=", "receipt-evidence;dur=", "comparison;dur=",
	} {
		if !strings.Contains(serverTiming, metric) {
			t.Fatalf("proposal Server-Timing=%q; missing %q", serverTiming, metric)
		}
	}
	var proposal loomapi.TableShapeProposal
	if err := json.Unmarshal([]byte(proposalHTTP.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalId == "" || proposal.BaseReceiptId == "" || proposal.OutputId != "patients" || proposal.Mode != "ADD" {
		t.Fatalf("proposal identity is incomplete: %#v", proposal)
	}
	comparisonKind, err := proposal.Comparison.Discriminator()
	if err != nil || comparisonKind != "AVAILABLE" {
		t.Fatalf("proposal comparison discriminator=%q err=%v; want AVAILABLE", comparisonKind, err)
	}
	comparison, err := proposal.Comparison.AsTableShapeComparisonAvailable()
	if err != nil || comparison.Base.RowCount != 1 || comparison.Candidate.RowCount != 1 || comparison.Base.Sampled || comparison.Candidate.Sampled || comparison.ChangedRowCount != 1 || comparison.ChangedRowsSampled {
		t.Fatalf("proposal comparison summaries=%#v err=%v", comparison, err)
	}
	if len(comparison.ChangedRows) != 1 || comparison.ChangedRows[0].RowIdentity != "row-1" || !comparison.ChangedRows[0].BasePresent || !comparison.ChangedRows[0].CandidatePresent {
		t.Fatalf("proposal changed-row evidence=%#v", comparison.ChangedRows)
	}
	var changedCell *loomapi.TableShapeChangedCell
	for index := range comparison.ChangedRows[0].ChangedCells {
		if comparison.ChangedRows[0].ChangedCells[index].Column == "active_measure" {
			changedCell = &comparison.ChangedRows[0].ChangedCells[index]
			break
		}
	}
	if changedCell == nil || changedCell.Before.Present || changedCell.Before.Value != nil || !changedCell.After.Present || changedCell.After.Value != 2.5 {
		t.Fatalf("proposal cell evidence=%#v; want missing before and present 2.5 after", changedCell)
	}
	traceContributors := changedCell.Trace.Contributors
	if changedCell.Trace.State != "AVAILABLE" || changedCell.Trace.CellStatus == nil || *changedCell.Trace.CellStatus != "VALUE" || !changedCell.Trace.Complete || changedCell.Trace.Sampled || len(traceContributors) != 1 || traceContributors[0].ResourceType != "Observation" || traceContributors[0].ResourceId != "obs-1" || traceContributors[0].Value != 2.5 {
		t.Fatalf("proposal cell trace=%#v", changedCell.Trace)
	}
	if len(comparison.Contributors) != 1 || comparison.Contributors[0].ResourceType != "Observation" || comparison.Contributors[0].ResourceId != "obs-1" || comparison.ContributorsSampled || len(comparison.EvidenceLimitations) != 2 || comparison.Notices == nil {
		t.Fatalf("proposal contributor and limitation evidence=%#v", comparison)
	}
	if comparison.Exclusions.Status != "INCOMPLETE" || comparison.Exclusions.Complete || !comparison.Exclusions.Sampled || len(comparison.Exclusions.Records) != 5 {
		t.Fatalf("proposal exclusion evidence=%#v", comparison.Exclusions)
	}
	if comparison.Exclusions.Records[0].Category.Value != false || comparison.Exclusions.Records[1].Category.Value != float64(0) || comparison.Exclusions.Records[2].Category.Value != "" {
		t.Fatalf("proposal exclusion category values lost false, zero, or empty string: %#v", comparison.Exclusions.Records)
	}
	if comparison.Exclusions.Records[3].Category.Present || comparison.Exclusions.Records[3].SourceIdentity != nil || comparison.Exclusions.Records[3].OmissionCode == nil || *comparison.Exclusions.Records[3].OmissionCode != "TABLE_SHAPE_SOURCE_IDENTITY_UNAVAILABLE" || !comparison.Exclusions.Records[4].Category.Present || comparison.Exclusions.Records[4].Category.Value != nil {
		t.Fatalf("proposal exclusion omission evidence=%#v", comparison.Exclusions.Records[3])
	}
	if comparison.Exclusions.Records[0].SourceIdentity == nil || comparison.Exclusions.Records[0].SourceIdentity.ResourceType != "Observation" || comparison.Exclusions.Records[0].SourceIdentity.ResourceId != "obs-false" || comparison.Exclusions.Records[0].CategoryType != "BOOLEAN" || comparison.Exclusions.Records[0].OutputRowId != "row-false" || comparison.Exclusions.Records[0].Reason != "UNLISTED_CATEGORY" {
		t.Fatalf("proposal exact exclusion fields=%#v", comparison.Exclusions.Records[0])
	}
	if comparison.DeclaredInformationLoss.Status != "COMPLETE" || len(comparison.DeclaredInformationLoss.Items) != 1 || comparison.DeclaredInformationLoss.Items[0].Code != "GROUPED_PIVOT_DROPS_NON_GROUP_OUTPUT_COLUMNS" {
		t.Fatalf("proposal declared information loss=%#v", comparison.DeclaredInformationLoss)
	}
	if comparison.EvidenceLimitations[0].Code != "TABLE_SHAPE_EXCLUSIONS_SAMPLED" || comparison.EvidenceLimitations[1].Code != "TABLE_SHAPE_SOURCE_IDENTITY_UNAVAILABLE" {
		t.Fatalf("proposal evidence limitations=%#v", comparison.EvidenceLimitations)
	}
	for _, forbidden := range []string{"tableShape", "constructionId", "columnKey", "query", "sourcePath", "FHIRType", "schemaPath", "__loom_", "phaseTimings"} {
		if strings.Contains(proposalHTTP.Body, forbidden) {
			t.Fatalf("proposal response leaked durable or compiler detail %q: %s", forbidden, proposalHTTP.Body)
		}
	}
	proposalUnknown := requestJSON(t, app, http.MethodPost, basePath+"/table-shape-proposals", strings.TrimSuffix(proposalBody, "}")+`,"extra":true}`)
	if proposalUnknown.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown proposal field status=%d body=%s", proposalUnknown.StatusCode, proposalUnknown.Body)
	}
	proposalRawShape := requestJSON(t, app, http.MethodPost, basePath+"/table-shape-proposals", strings.TrimSuffix(proposalBody, "}")+`,"tableShape":{"derived":[]}}`)
	if proposalRawShape.StatusCode != http.StatusBadRequest {
		t.Fatalf("raw durable tableShape status=%d body=%s", proposalRawShape.StatusCode, proposalRawShape.Body)
	}
	staleCatalogBody := strings.Replace(proposalBody, catalog.CatalogId, "tsc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1)
	staleCatalog := requestJSON(t, app, http.MethodPost, basePath+"/table-shape-proposals", staleCatalogBody)
	if staleCatalog.StatusCode != http.StatusConflict {
		t.Fatalf("stale catalog status=%d body=%s", staleCatalog.StatusCode, staleCatalog.Body)
	}
	staleResolutionID := resolved.ResolutionId[:len(resolved.ResolutionId)-1] + "0"
	if strings.HasSuffix(resolved.ResolutionId, "0") {
		staleResolutionID = resolved.ResolutionId[:len(resolved.ResolutionId)-1] + "1"
	}
	staleResolutionBody := strings.Replace(proposalBody, resolved.ResolutionId, staleResolutionID, 1)
	staleResolution := requestJSON(t, app, http.MethodPost, basePath+"/table-shape-proposals", staleResolutionBody)
	if staleResolution.StatusCode != http.StatusConflict {
		t.Fatalf("stale resolution status=%d body=%s", staleResolution.StatusCode, staleResolution.Body)
	}
	modeMismatchBody := strings.Replace(proposalBody, `"mode":"ADD"`, `"mode":"REPLACE"`, 1)
	modeMismatch := requestJSON(t, app, http.MethodPost, basePath+"/table-shape-proposals", modeMismatchBody)
	if modeMismatch.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("mode mismatch status=%d body=%s", modeMismatch.StatusCode, modeMismatch.Body)
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
		t.Fatalf("catalog/discovery/resolution/proposal mutated the draft: before=%#v after=%#v", before, after)
	}
	scopeMode = authscope.ReadScopeRestricted
	wrongScope := requestJSON(t, app, http.MethodPost, basePath+"/table-shape-capabilities", catalogRequest)
	if wrongScope.StatusCode != http.StatusConflict || !strings.Contains(wrongScope.Body, "STALE_AUTHORIZATION_SCOPE") {
		t.Fatalf("wrong-scope capability request status=%d body=%s", wrongScope.StatusCode, wrongScope.Body)
	}
	scopeMode = authscope.ReadScopeUnrestricted
	staleResolutionRequest := resolution
	staleResolutionRequest.SnapshotToken = "stale-snapshot-token"
	staleBody, err := json.Marshal(staleResolutionRequest)
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
	var workspace authoringv2.Workspace
	if err := json.Unmarshal(receipt.NormalizedBundle, &workspace); err != nil {
		return nil, err
	}
	for _, document := range workspace.Documents {
		if document.Output.ID != "patients" || document.TableShape == nil || document.TableShape.Reshape == nil || document.TableShape.Reshape.Pivot == nil {
			continue
		}
		for _, category := range document.TableShape.Reshape.Pivot.Categories {
			column := explorer.PublicOutputColumn{
				Column: category.Output.Column, Label: category.Output.Label, LogicalType: "decimal",
				Cardinality: "optional_one", Nullable: true, Shape: "scalar",
			}
			contract.Columns = append(contract.Columns, column)
			receipt.EmittedColumns = append(receipt.EmittedColumns, explorer.EmittedColumn{
				EmissionID: "table_shape_" + category.Output.Column, OutputID: "patients", PublicColumn: column.Column,
				Label: column.Label, LogicalType: column.LogicalType, Cardinality: column.Cardinality, Nullable: column.Nullable, Shape: column.Shape,
			})
			provenance[column.Column] = "EXPLICIT"
		}
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
