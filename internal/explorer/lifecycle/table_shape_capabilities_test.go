package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/tableshapecap"
)

type lifecycleTableShapeRepository struct {
	catalogs    map[string]tableshapecap.CatalogReceipt
	scans       map[string]tableshapecap.CategoryScanReceipt
	resolutions map[string]tableshapecap.ResolutionReceipt
}

func tableShapeTestPtr[T any](value T) *T { return &value }

func newLifecycleTableShapeRepository() *lifecycleTableShapeRepository {
	return &lifecycleTableShapeRepository{
		catalogs: make(map[string]tableshapecap.CatalogReceipt), scans: make(map[string]tableshapecap.CategoryScanReceipt),
		resolutions: make(map[string]tableshapecap.ResolutionReceipt),
	}
}

func (r *lifecycleTableShapeRepository) PutCatalog(_ context.Context, value tableshapecap.CatalogReceipt) (tableshapecap.CatalogReceipt, error) {
	if err := value.Validate(); err != nil {
		return tableshapecap.CatalogReceipt{}, err
	}
	r.catalogs[value.ID] = value
	return value, nil
}

func (r *lifecycleTableShapeRepository) GetCatalog(_ context.Context, binding tableshapecap.Binding, id string) (tableshapecap.CatalogReceipt, error) {
	value, ok := r.catalogs[id]
	if !ok || value.Binding != binding {
		return tableshapecap.CatalogReceipt{}, tableshapecap.ErrNotFound
	}
	return value, nil
}

func (r *lifecycleTableShapeRepository) GetCatalogForLookup(_ context.Context, lookup tableshapecap.CatalogLookup, id string) (tableshapecap.CatalogReceipt, error) {
	value, ok := r.catalogs[id]
	if !ok || !lookup.Matches(value.Binding) {
		return tableshapecap.CatalogReceipt{}, tableshapecap.ErrNotFound
	}
	return value, nil
}

func (r *lifecycleTableShapeRepository) PutCategoryScan(_ context.Context, value tableshapecap.CategoryScanReceipt) (tableshapecap.CategoryScanReceipt, error) {
	if err := value.Validate(); err != nil {
		return tableshapecap.CategoryScanReceipt{}, err
	}
	r.scans[value.ID] = value
	return value, nil
}

func (r *lifecycleTableShapeRepository) GetCategoryScan(_ context.Context, binding tableshapecap.Binding, catalogID, id string) (tableshapecap.CategoryScanReceipt, error) {
	value, ok := r.scans[id]
	if !ok || value.Binding != binding || value.ParentCatalogID != catalogID {
		return tableshapecap.CategoryScanReceipt{}, tableshapecap.ErrNotFound
	}
	return value, nil
}

func (r *lifecycleTableShapeRepository) PutResolution(_ context.Context, value tableshapecap.ResolutionReceipt) (tableshapecap.ResolutionReceipt, error) {
	if err := value.Validate(); err != nil {
		return tableshapecap.ResolutionReceipt{}, err
	}
	r.resolutions[value.ID] = value
	return value, nil
}

func (r *lifecycleTableShapeRepository) GetResolution(_ context.Context, binding tableshapecap.Binding, catalogID, id string) (tableshapecap.ResolutionReceipt, error) {
	value, ok := r.resolutions[id]
	if !ok || value.Binding != binding || value.ParentCatalogID != catalogID {
		return tableshapecap.ResolutionReceipt{}, tableshapecap.ErrNotFound
	}
	return value, nil
}

type lifecycleTableShapeColumn struct {
	name, label, logical string
	nullable             bool
	construction         string
}

func lifecycleTableShapeService(t *testing.T, saved *authoringv2.TableShape) (*Service, *fakeStore, capability.Snapshot, []byte, error) {
	t.Helper()
	service, store, snapshot, _ := rowProposalService(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []struct{ key, label string }{
		{"category", "Category"}, {"value", "Value"}, {"value_alt", "Alternate value"},
		{"category_number", "Numeric category"}, {"category_boolean", "Boolean category"},
		{"text_value", "Text value"}, {"overflow", "Overflow category"},
	} {
		workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, authoringv2.Column{
			Column: column.key, Label: column.label, LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: column.key, ProjectionMode: "VALUE"}},
		})
	}
	if saved != nil {
		workspace.Documents[0].TableShape = saved
	}
	store.created.DraftConfig, err = workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftDigest, err = workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	service.config.TableShapeCapabilities = newLifecycleTableShapeRepository()
	service.config.ScanCategories = func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, request dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		values := []dataframeexecution.CategoryValue{{Present: true, Value: ""}, {Present: true, Value: "alpha"}, {Present: true, Value: nil}, {Present: false}}
		switch request.Column {
		case "category_number":
			values = []dataframeexecution.CategoryValue{{Present: true, Value: int64(0)}}
		case "category_boolean":
			values = []dataframeexecution.CategoryValue{{Present: true, Value: false}}
		case "overflow":
			return dataframeexecution.CategoryScanResult{Complete: true, Overflow: true}, nil
		}
		proof := compiler.CategoryScanProof{
			Version: 1, Output: request.Output, Column: request.Column, Kind: "STRING", Cardinality: "optional_one",
			MaxValues: request.MaxValues, OutputSchemaDigest: "schema-proof", PlanFingerprint: "plan-proof",
			QueryFingerprint: "query-proof", Fingerprint: "scan-proof",
		}
		return dataframeexecution.CategoryScanResult{Values: values, Complete: true, Proof: proof}, nil
	}
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt := lifecycleTableShapeReceipt(snapshot, request.Workspace)
		store.receipt = receipt
		return receipt, nil
	}
	before := append([]byte(nil), store.created.DraftConfig...)
	return service, store, snapshot, before, nil
}

func lifecycleTableShapeReceipt(snapshot capability.Snapshot, workspace authoringv2.Workspace) *explorer.CompilationReceipt {
	receipt := nativeReceipt(snapshot)
	receipt.IntentDigest, _ = workspace.Digest()
	receipt.NormalizedBundle, _ = workspace.CanonicalJSON()
	doc := workspace.Documents[0]
	columns := []lifecycleTableShapeColumn{
		{name: "patient_id", label: "Patient ID", logical: "string"},
		{name: "category", label: "Category", logical: "string", nullable: true},
		{name: "value", label: "Value", logical: "decimal", nullable: true},
		{name: "value_alt", label: "Alternate value", logical: "decimal"},
		{name: "category_number", label: "Numeric category", logical: "integer"},
		{name: "category_boolean", label: "Boolean category", logical: "boolean"},
		{name: "text_value", label: "Text value", logical: "string"},
		{name: "overflow", label: "Overflow category", logical: "string"},
	}
	if shape := doc.TableShape; shape != nil && shape.Reshape != nil && shape.Reshape.Kind == "PIVOT" && shape.Reshape.Pivot != nil {
		base := columns
		columns = make([]lifecycleTableShapeColumn, 0, len(shape.Reshape.Pivot.GroupKeys)+len(shape.Reshape.Pivot.Categories)+len(shape.Derived))
		for _, key := range shape.Reshape.Pivot.GroupKeys {
			for _, column := range base {
				if column.name == key {
					columns = append(columns, column)
				}
			}
		}
		for _, category := range shape.Reshape.Pivot.Categories {
			columns = append(columns, lifecycleTableShapeColumn{name: category.Output.Column, label: category.Output.Label, logical: "decimal", nullable: shape.Reshape.Pivot.MissingCellPolicy == "NULL"})
		}
	} else if shape := doc.TableShape; shape != nil && shape.Reshape != nil && shape.Reshape.Kind == "UNPIVOT" && shape.Reshape.Unpivot != nil {
		columns = []lifecycleTableShapeColumn{{name: "patient_id", label: "Patient ID", logical: "string"},
			{name: shape.Reshape.Unpivot.KeyOutput.Column, label: shape.Reshape.Unpivot.KeyOutput.Label, logical: "string"},
			{name: shape.Reshape.Unpivot.ValueOutput.Column, label: shape.Reshape.Unpivot.ValueOutput.Label, logical: "decimal", nullable: shape.Reshape.Unpivot.NullRowPolicy == "PRESERVE"}}
	}
	if doc.TableShape != nil {
		for _, derived := range doc.TableShape.Derived {
			columns = append(columns, lifecycleTableShapeColumn{
				name: derived.Output.Column, label: derived.Output.Label, logical: "decimal",
				construction: string(derived.ConstructionID),
				nullable:     derived.MissingInputPolicy == "PROPAGATE_NULL" || derived.Operation == "DIVIDE" && derived.DivisionByZeroPolicy == "NULL",
			})
		}
	}
	public := explorer.PublicOutputContract{OutputID: "patients", Columns: make([]explorer.PublicOutputColumn, 0, len(columns))}
	receipt.EmittedColumns = make([]explorer.EmittedColumn, 0, len(columns))
	provenance := make(map[string]string, len(columns))
	for index, column := range columns {
		constructionID := column.construction
		public.Columns = append(public.Columns, explorer.PublicOutputColumn{
			Column: column.name, ConstructionID: constructionID, Label: column.label, LogicalType: column.logical,
			Cardinality: "optional_one", Nullable: column.nullable, Shape: "scalar",
		})
		receipt.EmittedColumns = append(receipt.EmittedColumns, explorer.EmittedColumn{
			EmissionID: fmt.Sprintf("em_%02d", index), OutputID: "patients", PublicColumn: column.name,
			ConstructionID: constructionID, Label: column.label, LogicalType: column.logical,
			Cardinality: "optional_one", Nullable: column.nullable, Shape: "scalar",
		})
		provenance[column.name] = "EXPLICIT"
	}
	contract, _ := json.Marshal(explorer.PublicOutputContracts{Outputs: []explorer.PublicOutputContract{public}})
	receipt.PublicOutputContract = contract
	receipt.OutputContractDigest, _ = explorer.CompilationArtifactDigest(contract)
	receipt.OutputFingerprints = map[string]string{"patients": "fingerprint-" + receipt.IntentDigest}
	receipt.OutputColumnProvenance = map[string]map[string]string{"patients": provenance}
	receipt.CompilationKey, _ = explorer.CompilationKey(*receipt)
	receipt.ID, _ = explorer.ReceiptID(*receipt)
	return receipt
}

func tableShapeCatalogRequest(owner *explorer.Explorer, snapshot capability.Snapshot) TableShapeCatalogRequest {
	return TableShapeCatalogRequest{Project: owner.Project, ExplorerID: owner.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest, OutputID: "patients"}
}

func catalogColumnChoice(t *testing.T, catalog tableshapecap.CatalogReceipt, role tableshapecap.ChoiceRole, key string) string {
	t.Helper()
	for _, choice := range catalog.Choices.Columns {
		if choice.Role == role && choice.ColumnKey == key {
			return choice.ID
		}
	}
	t.Fatalf("missing %s choice for %s", role, key)
	return ""
}

func catalogPolicyChoice(t *testing.T, catalog tableshapecap.CatalogReceipt, role tableshapecap.ChoiceRole, policy string) string {
	t.Helper()
	for _, choice := range catalog.Choices.Policies {
		if choice.Role == role && choice.PolicyID == policy {
			return choice.ID
		}
	}
	t.Fatalf("missing %s policy %s", role, policy)
	return ""
}

func catalogOperatorChoice(t *testing.T, catalog tableshapecap.CatalogReceipt, operator string) string {
	t.Helper()
	for _, choice := range catalog.Choices.Operators {
		if choice.Operator == operator {
			return choice.ID
		}
	}
	t.Fatalf("missing operator %s", operator)
	return ""
}

func lifecycleErrorCode(err error) string {
	var lifecycleErr *Error
	if errors.As(err, &lifecycleErr) {
		return lifecycleErr.Code
	}
	return ""
}

func TestTableShapeCatalogDiscoveryPivotAndDerivedReceipts(t *testing.T) {
	service, store, snapshot, before, _ := lifecycleTableShapeService(t, nil)
	request := tableShapeCatalogRequest(store.created, snapshot)
	result, err := service.GetTableShapeCatalog(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.CatalogID == "" || len(result.Columns) == 0 || len(result.Operands) < 2 {
		t.Fatalf("catalog omitted compiler-owned choices: %#v", result)
	}
	encoded, _ := json.Marshal(result)
	for _, raw := range []string{`"patient_id"`, `"category"`, `"value"`, `"ERROR"`} {
		if strings.Contains(string(encoded), raw) {
			t.Fatalf("catalog exposed raw compiler choice %s: %s", raw, encoded)
		}
	}
	catalog := service.config.TableShapeCapabilities.(*lifecycleTableShapeRepository).catalogs[result.CatalogID]
	pair := TableShapeCategoryDiscoveryRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest, OutputID: request.OutputID,
		CatalogID: result.CatalogID, CategoryColumnChoiceID: catalogColumnChoice(t, catalog, tableshapecap.RolePivotCategory, "category"),
		ValueColumnChoiceID: catalogColumnChoice(t, catalog, tableshapecap.RolePivotValue, "value"),
	}
	discovery, err := service.DiscoverTableShapeCategories(context.Background(), pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Categories) != 4 || discovery.Categories[0].Value.Kind != tableshapecap.ScalarString || *discovery.Categories[0].Value.String != "" || discovery.Categories[2].Value.Kind != tableshapecap.ScalarNull || discovery.Categories[3].Value.Kind != tableshapecap.ScalarMissing {
		t.Fatalf("category scan lost empty/null/missing values: %#v", discovery.Categories)
	}
	groupID := catalogColumnChoice(t, catalog, tableshapecap.RolePivotGroup, "patient_id")
	selection := TableShapePivotSelection{
		CategoryDiscoveryID: discovery.DiscoveryID, GroupColumnChoiceIDs: []string{groupID},
		CategoryColumnChoiceID: pair.CategoryColumnChoiceID, ValueColumnChoiceID: pair.ValueColumnChoiceID,
		DuplicatePolicyChoiceID: catalogPolicyChoice(t, catalog, tableshapecap.RolePolicyDuplicate, "ERROR"),
		MissingPolicyChoiceID:   catalogPolicyChoice(t, catalog, tableshapecap.RolePolicyMissing, "NULL"),
		UnlistedPolicyChoiceID:  catalogPolicyChoice(t, catalog, tableshapecap.RolePolicyUnlisted, "ERROR"),
	}
	for index, category := range discovery.Categories[:2] {
		selection.Categories = append(selection.Categories, TableShapePivotCategorySelection{ChoiceID: category.ID, OutputColumn: fmt.Sprintf("bucket_%d", index+1), OutputLabel: category.Label})
	}
	pivot, err := service.ResolveTableShape(context.Background(), TableShapeResolutionRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, CatalogID: result.CatalogID, Kind: tableshapecap.ResolutionPivot, Pivot: &selection,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pivot.ResolutionID == "" || len(pivot.DerivedOperands) != 2 {
		t.Fatalf("pivot did not expose typed post-reshape operands: %#v", pivot)
	}
	baseValueChoice := catalog.Choices.Operands[0].ID
	derivedSelection := TableShapeDerivedSelection{
		OutputColumn: "bucket_plus_zero", OutputLabel: "Bucket plus zero", PivotResolutionID: pivot.ResolutionID,
		OperatorChoiceID:      catalogOperatorChoice(t, catalog, "ADD"),
		Left:                  TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandCatalogChoice, ChoiceID: pivot.DerivedOperands[0].ID},
		Right:                 TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandLiteral, Literal: tableShapeTestPtr(tableshapecap.IntegerScalar(0))},
		MissingPolicyChoiceID: catalogPolicyChoice(t, catalog, tableshapecap.RolePolicyDerivedMissing, "ERROR"),
	}
	derived, err := service.ResolveTableShape(context.Background(), TableShapeResolutionRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, CatalogID: result.CatalogID, Kind: tableshapecap.ResolutionDerived, Derived: &derivedSelection,
	})
	if err != nil {
		t.Fatal(err)
	}
	if derived.Result == nil || derived.Result.Nullable || derived.Result.LogicalType != tableshapecap.LogicalDecimal {
		t.Fatalf("derived type does not match compiler policy semantics: %#v", derived.Result)
	}
	derivedSelection.OutputColumn, derivedSelection.OutputLabel = "next", "Next"
	derivedSelection.Left = TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandResolution, ResolutionID: derived.ResolutionID}
	derivedSelection.Right = TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandLiteral, Literal: tableShapeTestPtr(tableshapecap.IntegerScalar(1))}
	chained, err := service.ResolveTableShape(context.Background(), TableShapeResolutionRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, CatalogID: result.CatalogID, Kind: tableshapecap.ResolutionDerived, Derived: &derivedSelection,
	})
	if err != nil {
		t.Fatal(err)
	}
	if chained.ResolutionID == derived.ResolutionID {
		t.Fatal("derived outputs with distinct output names aliased to one receipt")
	}
	derivedSelection.OutputColumn, derivedSelection.OutputLabel = "invalid", "Invalid"
	derivedSelection.Left = TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandCatalogChoice, ChoiceID: baseValueChoice}
	if _, err := service.ResolveTableShape(context.Background(), TableShapeResolutionRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, CatalogID: result.CatalogID, Kind: tableshapecap.ResolutionDerived, Derived: &derivedSelection,
	}); lifecycleErrorCode(err) != "INVALID_CHOICE_ID" {
		t.Fatalf("accepted a base column removed by pivot: code=%s err=%v", lifecycleErrorCode(err), err)
	}
	if store.saveDraftCalls != 0 || !bytes.Equal(before, store.created.DraftConfig) {
		t.Fatalf("capability discovery/resolution mutated the draft: saves=%d", store.saveDraftCalls)
	}
}

func TestTableShapeSavedPivotReloadUsesDetachedBaseCatalog(t *testing.T) {
	shape := &authoringv2.TableShape{Reshape: &authoringv2.TableReshape{Kind: "PIVOT", Pivot: &authoringv2.PivotConstruction{
		ConstructionID: "pivot_saved", GroupKeys: []string{"patient_id"}, CategoryColumn: "category", ValueColumn: "value",
		Categories:      []authoringv2.PivotCategory{{Key: authoringv2.TableScalar{Kind: "STRING", String: tableShapeTestPtr("")}, Output: authoringv2.ColumnOutput{Column: "empty_bucket", Label: "Empty bucket"}}},
		DuplicatePolicy: "ERROR", MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "ERROR",
	}}}
	service, store, snapshot, _, _ := lifecycleTableShapeService(t, shape)
	result, err := service.GetTableShapeCatalog(context.Background(), tableShapeCatalogRequest(store.created, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if result.SavedProposal.Kind != "GROUPED_PIVOT" || result.SavedProposal.Support.State != tableshapecap.AvailabilitySupported || result.SavedProposal.Pivot == nil {
		t.Fatalf("saved pivot did not reload as editable intent: %#v", result.SavedProposal)
	}
	if result.SavedProposal.Pivot.PivotResolutionID == "" || len(result.SavedProposal.Pivot.Categories) != 1 || result.SavedProposal.Pivot.Categories[0].ChoiceID == "" {
		t.Fatalf("saved pivot reload lacks opaque category/context receipts: %#v", result.SavedProposal.Pivot)
	}
	foundRemovedBaseColumn := false
	for _, column := range result.Columns {
		for _, choice := range column.Choices {
			if choice.Role == tableshapecap.RolePivotCategory || choice.Role == tableshapecap.RolePivotValue {
				foundRemovedBaseColumn = true
			}
		}
	}
	if !foundRemovedBaseColumn {
		t.Fatalf("base catalog did not restore category/value columns omitted from final output: %#v", result.Columns)
	}
}

func TestTableScalarToCapabilityScalarPreservesSentinelKeys(t *testing.T) {
	for _, test := range []struct {
		name string
		in   authoringv2.TableScalar
		want tableshapecap.ScalarKind
	}{
		{name: "recorded null", in: authoringv2.TableScalar{Kind: authoringv2.TableScalarNull}, want: tableshapecap.ScalarNull},
		{name: "missing property", in: authoringv2.TableScalar{Kind: authoringv2.TableScalarMissing}, want: tableshapecap.ScalarMissing},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := tableScalarToCapabilityScalar(test.in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != test.want {
				t.Fatalf("kind = %q, want %q", got.Kind, test.want)
			}
		})
	}
}

func TestTableShapeDiscoveryRejectsStaleAndIncompleteBindings(t *testing.T) {
	service, store, snapshot, _, _ := lifecycleTableShapeService(t, nil)
	request := tableShapeCatalogRequest(store.created, snapshot)
	catalog, err := service.GetTableShapeCatalog(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	repository := service.config.TableShapeCapabilities.(*lifecycleTableShapeRepository)
	storedCatalog := repository.catalogs[catalog.CatalogID]
	pair := TableShapeCategoryDiscoveryRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, CatalogID: catalog.CatalogID,
		CategoryColumnChoiceID: catalogColumnChoice(t, storedCatalog, tableshapecap.RolePivotCategory, "category"),
		ValueColumnChoiceID:    catalogColumnChoice(t, storedCatalog, tableshapecap.RolePivotValue, "value"),
	}
	store.created.DraftVersion++
	if _, err := service.DiscoverTableShapeCategories(context.Background(), pair); lifecycleErrorCode(err) != "DRAFT_CONFLICT" {
		t.Fatalf("stale draft request code=%s err=%v", lifecycleErrorCode(err), err)
	}
	store.created.DraftVersion = request.ExpectedDraftVersion
	service.config.ScanCategories = func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, _ dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		return dataframeexecution.CategoryScanResult{Values: []dataframeexecution.CategoryValue{{Present: true, Value: "x"}}, Complete: false}, nil
	}
	if _, err := service.DiscoverTableShapeCategories(context.Background(), pair); lifecycleErrorCode(err) != "CATEGORY_SCAN_INCOMPLETE" {
		t.Fatalf("incomplete scan code=%s err=%v", lifecycleErrorCode(err), err)
	}
	service.config.ScanCategories = func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, _ dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		return dataframeexecution.CategoryScanResult{Values: []dataframeexecution.CategoryValue{{Present: true, Value: "x"}}, Complete: true, Overflow: true}, nil
	}
	if _, err := service.DiscoverTableShapeCategories(context.Background(), pair); lifecycleErrorCode(err) != "CATEGORY_SCAN_INCOMPLETE" {
		t.Fatalf("overflow scan code=%s err=%v", lifecycleErrorCode(err), err)
	}
}

func TestTableShapeRequestBoundaryRejectsRawDefinitionsAndTamperedChoices(t *testing.T) {
	var request TableShapeResolutionRequest
	if err := json.Unmarshal([]byte(`{"project":"p","explorerId":"e","snapshotToken":"s","expectedDraftVersion":1,"expectedDraftDigest":"d","outputId":"o","catalogId":"tsc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","kind":"DERIVED","derived":{"tableShape":{"reshape":null}}}`), &request); err == nil {
		t.Fatal("accepted raw authoring TableShape at capability boundary")
	}
	service, store, snapshot, _, _ := lifecycleTableShapeService(t, nil)
	catalogResult, err := service.GetTableShapeCatalog(context.Background(), tableShapeCatalogRequest(store.created, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	selection := TableShapeDerivedSelection{
		OutputColumn: "unsafe", OutputLabel: "Unsafe", OperatorChoiceID: "tsch_tampered",
		Left:                  TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandCatalogChoice, ChoiceID: "tsch_tampered"},
		Right:                 TableShapeOperandSelection{Kind: tableshapecap.ResolvedOperandLiteral, Literal: tableShapeTestPtr(tableshapecap.IntegerScalar(0))},
		MissingPolicyChoiceID: "tsch_tampered",
	}
	_, err = service.ResolveTableShape(context.Background(), TableShapeResolutionRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", CatalogID: catalogResult.CatalogID, Kind: tableshapecap.ResolutionDerived, Derived: &selection,
	})
	if lifecycleErrorCode(err) != "INVALID_CHOICE_ID" {
		t.Fatalf("tampered choice ID code=%s err=%v", lifecycleErrorCode(err), err)
	}
}

func TestTableShapeCategoryScalarConversionsPreserveFalseAndZero(t *testing.T) {
	for _, test := range []struct {
		column string
		want   tableshapecap.Scalar
	}{
		{column: "category_number", want: tableshapecap.IntegerScalar(0)},
		{column: "category_boolean", want: tableshapecap.BooleanScalar(false)},
	} {
		t.Run(test.column, func(t *testing.T) {
			service, store, snapshot, _, _ := lifecycleTableShapeService(t, nil)
			request := tableShapeCatalogRequest(store.created, snapshot)
			catalogResult, err := service.GetTableShapeCatalog(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			repository := service.config.TableShapeCapabilities.(*lifecycleTableShapeRepository)
			catalog := repository.catalogs[catalogResult.CatalogID]
			pair := TableShapeCategoryDiscoveryRequest{
				Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
				ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
				OutputID: request.OutputID, CatalogID: catalog.ID,
				CategoryColumnChoiceID: catalogColumnChoice(t, catalog, tableshapecap.RolePivotCategory, test.column),
				ValueColumnChoiceID:    catalogColumnChoice(t, catalog, tableshapecap.RolePivotValue, "value"),
			}
			discovery, err := service.DiscoverTableShapeCategories(context.Background(), pair)
			if err != nil {
				t.Fatal(err)
			}
			if len(discovery.Categories) != 1 || !reflect.DeepEqual(discovery.Categories[0].Value, test.want) {
				t.Fatalf("discovered category = %#v, want %#v", discovery.Categories, test.want)
			}
		})
	}
}

func TestTableShapeCategoryDiscoveryCanRunBeforeFinalPivotChoices(t *testing.T) {
	service, store, snapshot, _, _ := lifecycleTableShapeService(t, nil)
	request := tableShapeCatalogRequest(store.created, snapshot)
	catalogResult, err := service.GetTableShapeCatalog(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	catalog := service.config.TableShapeCapabilities.(*lifecycleTableShapeRepository).catalogs[catalogResult.CatalogID]
	_, err = service.DiscoverTableShapeCategories(context.Background(), TableShapeCategoryDiscoveryRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		ExpectedDraftVersion: request.ExpectedDraftVersion, ExpectedDraftDigest: request.ExpectedDraftDigest,
		OutputID: request.OutputID, CatalogID: catalog.ID,
		CategoryColumnChoiceID: catalogColumnChoice(t, catalog, tableshapecap.RolePivotCategory, "category"),
		ValueColumnChoiceID:    catalogColumnChoice(t, catalog, tableshapecap.RolePivotValue, "value"),
	})
	if err != nil {
		t.Fatalf("category scan required unrelated group/policy selection: %v", err)
	}
}

func TestTableShapeNoScannerReturnsExactRoleRefusal(t *testing.T) {
	service, store, snapshot, _, _ := lifecycleTableShapeService(t, nil)
	service.config.ScanCategories = nil
	result, err := service.GetTableShapeCatalog(context.Background(), tableShapeCatalogRequest(store.created, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	for _, availability := range result.Availability {
		if availability.Role == tableshapecap.RolePivotCategory && availability.State == tableshapecap.AvailabilityRefused && availability.ReasonCode == "CATEGORY_SCAN_UNAVAILABLE" {
			return
		}
	}
	t.Fatalf("missing scanner refusal: %#v", result.Availability)
}

func TestTableShapeAuthorizationScopeIsRevalidated(t *testing.T) {
	service, store, snapshot, _, _ := lifecycleTableShapeService(t, nil)
	request := tableShapeCatalogRequest(store.created, snapshot)
	if _, err := service.GetTableShapeCatalog(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	service.config.Capability.ForCompilation = func(_ context.Context, project, token string) (AuthorizedCapability, error) {
		changed := snapshot.Clone()
		changed.Identity.AuthorizationScopeDigest = "different-scope"
		return AuthorizedCapability{Snapshot: changed, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	if _, err := service.GetTableShapeCatalog(context.Background(), request); lifecycleErrorCode(err) != "STALE_AUTHORIZATION_SCOPE" {
		t.Fatalf("changed scope code=%s err=%v", lifecycleErrorCode(err), err)
	}
}

func TestTableShapeCatalogReuseValidatesCurrentDraftAndScope(t *testing.T) {
	service, store, snapshot, _, _ := lifecycleTableShapeService(t, nil)
	request := tableShapeCatalogRequest(store.created, snapshot)
	catalogResult, err := service.GetTableShapeCatalog(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}

	compile := service.config.CompileReceipt
	compileCalls := 0
	service.config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		compileCalls++
		return compile(ctx, request)
	}

	base, catalog, err := service.tableShapeCatalogForRequest(context.Background(), request, catalogResult.CatalogID)
	if err != nil {
		t.Fatalf("reuse current catalog: %v", err)
	}
	if compileCalls != 0 {
		t.Fatalf("unchanged base was recompiled %d times", compileCalls)
	}
	if base.binding != catalog.Binding || base.receipt == nil || base.receipt.ID != catalog.Binding.BaseCompilationReceiptID {
		t.Fatalf("reused base is not bound to persisted catalog receipt: base=%#v catalog=%#v", base.binding, catalog.Binding)
	}

	staleDraft := request
	staleDraft.ExpectedDraftVersion++
	if _, _, err := service.tableShapeCatalogForRequest(context.Background(), staleDraft, catalogResult.CatalogID); lifecycleErrorCode(err) != "DRAFT_CONFLICT" {
		t.Fatalf("stale draft code=%s err=%v", lifecycleErrorCode(err), err)
	}
	if compileCalls != 0 {
		t.Fatalf("stale draft entered compilation path %d times", compileCalls)
	}

	service.config.Capability.ForCompilation = func(_ context.Context, project, token string) (AuthorizedCapability, error) {
		changed := snapshot.Clone()
		changed.Identity.AuthorizationScopeDigest = "different-scope"
		return AuthorizedCapability{Snapshot: changed, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	if _, _, err := service.tableShapeCatalogForRequest(context.Background(), request, catalogResult.CatalogID); lifecycleErrorCode(err) != "STALE_AUTHORIZATION_SCOPE" {
		t.Fatalf("changed scope code=%s err=%v", lifecycleErrorCode(err), err)
	}
	if compileCalls != 0 {
		t.Fatalf("changed authorization scope entered compilation path %d times", compileCalls)
	}
}

var _ tableshapecap.Repository = (*lifecycleTableShapeRepository)(nil)
