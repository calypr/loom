package arango

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/tableshapecap"
	storepkg "github.com/calypr/loom/internal/store/arango"
)

func tableShapeTestCatalog(t *testing.T, binding tableshapecap.Binding) tableshapecap.CatalogReceipt {
	t.Helper()
	roles := []tableshapecap.ChoiceRole{
		tableshapecap.RolePivotGroup, tableshapecap.RolePivotCategory, tableshapecap.RolePivotValue,
		tableshapecap.RoleUnpivotInput, tableshapecap.RoleDerivedOperand, tableshapecap.RoleDerivedOperator,
		tableshapecap.RolePolicyDuplicate, tableshapecap.RolePolicyMissing, tableshapecap.RolePolicyUnlisted,
		tableshapecap.RolePolicyUnpivotNull, tableshapecap.RolePolicyDerivedMissing, tableshapecap.RolePolicyDivisionByZero,
	}
	availability := make([]tableshapecap.RoleAvailability, 0, len(roles))
	for _, role := range roles {
		availability = append(availability, tableshapecap.RoleAvailability{Role: role, State: tableshapecap.AvailabilitySupported})
	}
	columns := []tableshapecap.PublicColumn{
		{Key: "group", Label: "Group", LogicalType: tableshapecap.LogicalString},
		{Key: "category", Label: "Category", LogicalType: tableshapecap.LogicalString},
		{Key: "value", Label: "Value", LogicalType: tableshapecap.LogicalInteger},
	}
	choices := tableshapecap.CatalogChoices{
		Columns: []tableshapecap.ColumnChoice{
			{Role: tableshapecap.RolePivotGroup, ColumnKey: "group"},
			{Role: tableshapecap.RolePivotCategory, ColumnKey: "category"},
			{Role: tableshapecap.RolePivotValue, ColumnKey: "value"},
			{Role: tableshapecap.RoleUnpivotInput, ColumnKey: "category"},
			{Role: tableshapecap.RoleUnpivotInput, ColumnKey: "value"},
		},
		Operators: []tableshapecap.OperatorChoice{{Role: tableshapecap.RoleDerivedOperator, Operator: "ADD"}},
		Policies: []tableshapecap.PolicyChoice{
			{Role: tableshapecap.RolePolicyDuplicate, PolicyID: "ERROR"},
			{Role: tableshapecap.RolePolicyMissing, PolicyID: "NULL"},
			{Role: tableshapecap.RolePolicyUnlisted, PolicyID: "ERROR"},
			{Role: tableshapecap.RolePolicyUnpivotNull, PolicyID: "PRESERVE"},
			{Role: tableshapecap.RolePolicyDerivedMissing, PolicyID: "ERROR"},
			{Role: tableshapecap.RolePolicyDivisionByZero, PolicyID: "ERROR"},
		},
		Operands: []tableshapecap.OperandChoice{{Role: tableshapecap.RoleDerivedOperand, Operand: tableshapecap.OperandRef{Kind: tableshapecap.OperandColumn, ColumnKey: "value"}}},
	}
	catalog, err := tableshapecap.NewCatalogReceipt(binding, columns, availability, choices, tableshapecap.SavedShapeSummary{}, "2026-09-19T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func tableShapeTestBinding() tableshapecap.Binding {
	return tableshapecap.Binding{Project: "project-a", ExplorerID: "explorer-a", OutputID: "output-a", SnapshotToken: "snapshot-a", AuthorizationScope: "scope-a", SourceGeneration: "generation-a", DraftVersion: 4, DraftDigest: "draft-a", BaseDocumentDigest: "document-a", BaseCompilationReceiptID: "compile-a", OutputFingerprint: "fingerprint-a", CompilerSchemaDigest: "schema-a"}
}

type tableShapeCapabilityClient struct {
	*capabilitySnapshotClient
	rows map[string]map[string]any
}

func (c *tableShapeCapabilityClient) QueryRows(_ context.Context, query string, _ int, binds map[string]any, visit storepkg.RowVisitor) error {
	c.capabilitySnapshotClient.calls = append(c.capabilitySnapshotClient.calls, queryCall{query: query, binds: binds})
	kind, _ := binds["kind"].(string)
	id, _ := binds["id"].(string)
	if kind == "" {
		if doc, ok := binds["doc"].(map[string]any); ok {
			kind, _ = doc["kind"].(string)
			id, _ = doc["id"].(string)
		}
	}
	if row := c.rows[kind+":"+id]; row != nil {
		return visit(row)
	}
	if row := c.rows[kind]; row != nil {
		return visit(row)
	}
	return nil
}

func tableShapeTestResolution(t *testing.T, catalog tableshapecap.CatalogReceipt) (tableshapecap.ResolutionReceipt, tableshapecap.CategoryScanReceipt) {
	t.Helper()
	columns := map[tableshapecap.ChoiceRole]string{}
	for _, choice := range catalog.Choices.Columns {
		if _, ok := columns[choice.Role]; !ok {
			columns[choice.Role] = choice.ID
		}
	}
	policies := map[tableshapecap.ChoiceRole]string{}
	for _, choice := range catalog.Choices.Policies {
		policies[choice.Role] = choice.ID
	}
	categoryID, valueID := columns[tableshapecap.RolePivotCategory], columns[tableshapecap.RolePivotValue]
	values := []tableshapecap.Scalar{tableshapecap.StringScalar("north")}
	valuesDigest, err := tableshapecap.CategoryValuesDigest(values)
	if err != nil {
		t.Fatal(err)
	}
	proof := tableshapecap.CategoryProof{
		Complete: true, DistinctCount: 1, MaxCategories: 256, ValuesDigest: valuesDigest,
		SourceGeneration: catalog.Binding.SourceGeneration, OutputFingerprint: catalog.Binding.OutputFingerprint,
		ScanFingerprint: "scan-fingerprint-v1", QueryProof: "query-proof-v1",
	}
	scan, err := tableshapecap.NewCategoryScanReceipt(catalog.Binding, catalog.ID, categoryID, valueID, values, proof, "2026-09-19T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	pivot := &tableshapecap.PivotResolution{
		GroupColumnChoiceIDs: []string{columns[tableshapecap.RolePivotGroup]}, CategoryColumnChoiceID: columns[tableshapecap.RolePivotCategory], ValueColumnChoiceID: columns[tableshapecap.RolePivotValue],
		CategoryDiscoveryID:     scan.ID,
		Categories:              []tableshapecap.FrozenCategory{{ChoiceID: scan.Categories[0].ChoiceID, Value: scan.Categories[0].Value, OutputColumn: "category_north", OutputLabel: "North"}},
		DuplicatePolicyChoiceID: policies[tableshapecap.RolePolicyDuplicate], MissingPolicyChoiceID: policies[tableshapecap.RolePolicyMissing], UnlistedPolicyChoiceID: policies[tableshapecap.RolePolicyUnlisted],
		CategoryProof: scan.Proof,
	}
	resolution, err := tableshapecap.NewResolutionReceipt(catalog.Binding, catalog.ID, tableshapecap.ResolutionPivot, pivot, nil, nil, "2026-09-19T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return resolution, scan
}

func TestTableShapeCapabilityRepositoryIsCreateOnceAndTenantScoped(t *testing.T) {
	client := &capabilitySnapshotClient{}
	repository, err := NewTableShapeCapabilityRepository(client)
	if err != nil {
		t.Fatal(err)
	}
	binding := tableShapeTestBinding()
	catalog := tableShapeTestCatalog(t, binding)
	row, err := catalogDocument(catalog)
	if err != nil {
		t.Fatal(err)
	}
	client.row = row
	stored, err := repository.PutCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID != catalog.ID {
		t.Fatalf("stored ID=%s want %s", stored.ID, catalog.ID)
	}
	put := client.calls[len(client.calls)-1]
	if !strings.Contains(put.query, `overwriteMode: "ignore"`) || strings.Contains(strings.ToUpper(put.query), "UPDATE") {
		t.Fatalf("write is not insert-only:\n%s", put.query)
	}
	if len(put.binds) != 2 || put.binds["@c"] != TableShapeCapabilitiesCollection {
		t.Fatalf("unexpected insert binds: %#v", put.binds)
	}
	if _, ok := put.binds["doc"]; !ok {
		t.Fatal("insert document missing")
	}

	got, err := repository.GetCatalog(context.Background(), binding, catalog.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != catalog.ID {
		t.Fatalf("read ID=%s", got.ID)
	}
	get := client.calls[len(client.calls)-1]
	for _, field := range []string{"project", "explorerId", "outputId", "snapshotToken", "authorizationScope", "sourceGeneration", "draftVersion", "draftDigest", "baseDocumentDigest", "baseCompilationReceiptId", "outputFingerprint", "compilerSchemaDigest"} {
		if !strings.Contains(get.query, "d.binding."+map[string]string{"project": "project", "explorerId": "explorerId", "outputId": "outputId", "snapshotToken": "snapshotToken", "authorizationScope": "authorizationScope", "sourceGeneration": "sourceGeneration", "draftVersion": "draftVersion", "draftDigest": "draftDigest", "baseDocumentDigest": "baseDocumentDigest", "baseCompilationReceiptId": "baseCompilationReceiptId", "outputFingerprint": "outputFingerprint", "compilerSchemaDigest": "compilerSchemaDigest"}[field]+" == @"+field) {
			t.Errorf("tenant read query missing binding field %s", field)
		}
		if _, ok := get.binds[field]; !ok {
			t.Errorf("tenant read bind missing %s", field)
		}
	}

	otherOutput := binding
	otherOutput.OutputID = "output-b"
	if _, err := repository.GetCatalog(context.Background(), otherOutput, catalog.ID); !errors.Is(err, tableshapecap.ErrNotFound) {
		t.Fatalf("cross-output read returned %v", err)
	}

	other := tableShapeTestCatalog(t, binding)
	other.Columns[0].Label = "Different label"
	// Simulate an existing short-key collision after insert-ignore by storing a
	// different valid receipt under the incoming outer key.
	other, err = tableshapecap.NewCatalogReceipt(binding, other.Columns, other.Availability, other.Choices, tableshapecap.SavedShapeSummary{}, other.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	collisionDoc, err := catalogDocument(other)
	if err != nil {
		t.Fatal(err)
	}
	collisionDoc["id"] = catalog.ID
	collisionDoc["_key"] = catalog.ID
	client.row = collisionDoc
	if _, err := repository.PutCatalog(context.Background(), catalog); !errors.Is(err, tableshapecap.ErrIdentityClash) {
		t.Fatalf("content collision accepted: %v", err)
	}

	tampered, err := catalogDocument(catalog)
	if err != nil {
		t.Fatal(err)
	}
	tampered["catalog"].(map[string]any)["contentDigest"] = "sha256:tampered"
	client.row = tampered
	if _, err := repository.GetCatalog(context.Background(), binding, catalog.ID); err == nil {
		t.Fatal("tampered persisted record passed read validation")
	}
}

func TestTableShapeCapabilityRepositoryScopesResolutionByParent(t *testing.T) {
	baseClient := &capabilitySnapshotClient{}
	client := &tableShapeCapabilityClient{capabilitySnapshotClient: baseClient, rows: map[string]map[string]any{}}
	repository, err := NewTableShapeCapabilityRepository(client)
	if err != nil {
		t.Fatal(err)
	}
	binding := tableShapeTestBinding()
	catalog := tableShapeTestCatalog(t, binding)
	resolution, scan := tableShapeTestResolution(t, catalog)
	catalogRow, err := catalogDocument(catalog)
	if err != nil {
		t.Fatal(err)
	}
	resolutionRow, err := resolutionDocument(resolution)
	if err != nil {
		t.Fatal(err)
	}
	client.rows["CATALOG"] = catalogRow
	scanRow, err := categoryScanDocument(scan)
	if err != nil {
		t.Fatal(err)
	}
	client.rows["CATEGORY_SCAN:"+scan.ID] = scanRow
	client.rows["RESOLUTION"] = resolutionRow
	if _, err := repository.PutCategoryScan(context.Background(), scan); err != nil {
		t.Fatalf("persist category scan: %v", err)
	}
	if again, err := repository.PutCategoryScan(context.Background(), scan); err != nil || again.ID != scan.ID {
		t.Fatalf("idempotent category scan put = %#v err=%v", again, err)
	}
	gotScan, err := repository.GetCategoryScan(context.Background(), binding, catalog.ID, scan.ID)
	if err != nil || gotScan.ID != scan.ID {
		t.Fatalf("read category scan = %#v err=%v", gotScan, err)
	}
	var categoryLookup *queryCall
	for index := len(client.calls) - 1; index >= 0; index-- {
		if client.calls[index].binds["kind"] == "CATEGORY_SCAN" {
			categoryLookup = &client.calls[index]
			break
		}
	}
	if categoryLookup == nil || !strings.Contains(categoryLookup.query, `d.kind == @kind`) || !strings.Contains(categoryLookup.query, `d.parentCatalogId == @parentCatalogId`) || categoryLookup.binds["parentCatalogId"] != catalog.ID {
		t.Fatalf("category scan lookup is not kind/parent scoped: %#v", categoryLookup)
	}
	otherBinding := binding
	otherBinding.OutputID = "other-output"
	if _, err := repository.GetCategoryScan(context.Background(), otherBinding, catalog.ID, scan.ID); !errors.Is(err, tableshapecap.ErrNotFound) {
		t.Fatalf("cross-output category scan read returned %v", err)
	}
	if _, err := repository.GetCategoryScan(context.Background(), binding, "tsc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", scan.ID); !errors.Is(err, tableshapecap.ErrNotFound) {
		t.Fatalf("cross-parent category scan read returned %v", err)
	}
	if _, err := repository.PutResolution(context.Background(), resolution); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetResolution(context.Background(), binding, catalog.ID, resolution.ID); err != nil {
		t.Fatal(err)
	}
	call := client.calls[len(client.calls)-2]
	if !strings.Contains(call.query, "d.parentCatalogId == @parentCatalogId") || call.binds["parentCatalogId"] != catalog.ID {
		t.Fatalf("resolution lookup is not parent-scoped: %#v", call)
	}

	otherBinding = binding
	otherBinding.OutputID = "other-output"
	if _, err := repository.GetResolution(context.Background(), otherBinding, catalog.ID, resolution.ID); !errors.Is(err, tableshapecap.ErrNotFound) {
		t.Fatalf("cross-output resolution read returned %v", err)
	}
	if _, err := repository.GetResolution(context.Background(), binding, "tsc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", resolution.ID); !errors.Is(err, tableshapecap.ErrNotFound) {
		t.Fatalf("cross-parent read returned %v", err)
	}

	wrongParent := resolution
	wrongParent, err = tableshapecap.NewResolutionReceipt(binding, "tsc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", resolution.Kind, resolution.Pivot, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PutResolution(context.Background(), wrongParent); !errors.Is(err, tableshapecap.ErrNotFound) {
		t.Fatalf("wrong parent receipt was persisted: %v", err)
	}

	otherCatalog := tableShapeTestCatalog(t, binding)
	otherColumns := append([]tableshapecap.PublicColumn(nil), otherCatalog.Columns...)
	otherColumns[0].Label = "Other group label"
	otherCatalog, err = tableshapecap.NewCatalogReceipt(binding, otherColumns, otherCatalog.Availability, otherCatalog.Choices, otherCatalog.SavedShape, "")
	if err != nil {
		t.Fatal(err)
	}
	foreignChoiceResolution, _ := tableShapeTestResolution(t, otherCatalog)
	foreignChoiceResolution, err = tableshapecap.NewResolutionReceipt(binding, catalog.ID, foreignChoiceResolution.Kind, foreignChoiceResolution.Pivot, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PutResolution(context.Background(), foreignChoiceResolution); !errors.Is(err, tableshapecap.ErrInvalid) {
		t.Fatalf("cross-receipt choices were persisted: %v", err)
	}
}

func TestTableShapeCapabilityRepositoryLoadsNestedDerivedParents(t *testing.T) {
	client := &tableShapeCapabilityClient{capabilitySnapshotClient: &capabilitySnapshotClient{}, rows: map[string]map[string]any{}}
	repository, err := NewTableShapeCapabilityRepository(client)
	if err != nil {
		t.Fatal(err)
	}
	binding := tableShapeTestBinding()
	catalog := tableShapeTestCatalog(t, binding)
	catalogRow, err := catalogDocument(catalog)
	if err != nil {
		t.Fatal(err)
	}
	client.rows["CATALOG"] = catalogRow
	operatorID := catalog.Choices.Operators[0].ID
	operandID := catalog.Choices.Operands[0].ID
	missingPolicyID := ""
	for _, choice := range catalog.Choices.Policies {
		if choice.Role == tableshapecap.RolePolicyDerivedMissing {
			missingPolicyID = choice.ID
		}
	}
	first, err := tableshapecap.NewResolutionReceipt(binding, catalog.ID, tableshapecap.ResolutionDerived, nil, nil, &tableshapecap.DerivedResolution{
		Output:           tableshapecap.NamedOutput{Name: "first", Label: "First"},
		OperatorChoiceID: operatorID, Left: tableshapecap.ResolvedOperand{Kind: tableshapecap.ResolvedOperandCatalogChoice, ChoiceID: operandID}, Right: tableshapecap.ResolvedOperand{Kind: tableshapecap.ResolvedOperandLiteral, Literal: scalarPtr(tableshapecap.IntegerScalar(1))},
		Result: tableshapecap.TypeFact{LogicalType: tableshapecap.LogicalInteger}, MissingPolicyChoiceID: missingPolicyID,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	firstRow, err := resolutionDocument(first)
	if err != nil {
		t.Fatal(err)
	}
	client.rows["RESOLUTION:"+first.ID] = firstRow
	if _, err := repository.PutResolution(context.Background(), first); err != nil {
		t.Fatalf("persist first derived receipt: %v", err)
	}
	zero := 0
	second, err := tableshapecap.NewResolutionReceipt(binding, catalog.ID, tableshapecap.ResolutionDerived, nil, nil, &tableshapecap.DerivedResolution{
		Output:           tableshapecap.NamedOutput{Name: "second", Label: "Second"},
		OperatorChoiceID: operatorID, Left: tableshapecap.ResolvedOperand{Kind: tableshapecap.ResolvedOperandResolution, ResolutionID: first.ID, OutputIndex: &zero}, Right: tableshapecap.ResolvedOperand{Kind: tableshapecap.ResolvedOperandCatalogChoice, ChoiceID: operandID},
		Result: tableshapecap.TypeFact{LogicalType: tableshapecap.LogicalInteger}, MissingPolicyChoiceID: missingPolicyID,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	secondRow, err := resolutionDocument(second)
	if err != nil {
		t.Fatal(err)
	}
	client.rows["RESOLUTION:"+second.ID] = secondRow
	if _, err := repository.PutResolution(context.Background(), second); err != nil {
		t.Fatalf("persist nested derived receipt: %v", err)
	}
	if _, err := repository.GetResolution(context.Background(), binding, catalog.ID, second.ID); err != nil {
		t.Fatalf("read nested derived receipt: %v", err)
	}
	foundPriorLookup := false
	for _, call := range client.calls {
		if call.binds["id"] == first.ID && strings.Contains(call.query, "d.binding.outputId == @outputId") {
			foundPriorLookup = true
		}
	}
	if !foundPriorLookup {
		t.Fatal("nested receipt validation did not perform tenant-scoped lookup of its prior resolution")
	}
}

func scalarPtr(value tableshapecap.Scalar) *tableshapecap.Scalar { return &value }
