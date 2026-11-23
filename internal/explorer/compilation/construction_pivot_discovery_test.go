package compilation

import (
	"errors"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/viewer"
)

func TestRecipeConstructionPreservesPivotDiscoveryAndRefusesUnresolvedCompilation(t *testing.T) {
	authoredColumns := []ws.Column{
		{ColumnID: "group_id", Column: "group", Label: "Group", LogicalType: "string"},
		{ColumnID: "category_id", Column: "category", Label: "Category", LogicalType: "string"},
		{ColumnID: "amount_id", Column: "amount", Label: "Amount", LogicalType: "decimal"},
	}
	authored := &ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{{
			ID: "pivot", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
				ConstructionID: "pivot", GroupKeyIDs: []string{"group_id"},
				CategoryColumnID: "category_id", ValueColumnID: "amount_id",
				Discovery:              &ws.ConstructionPivotDiscovery{MaxCategories: 4},
				DuplicatePolicy:        ws.ConstructionPivotDuplicateError,
				MissingCellPolicy:      ws.ConstructionPivotMissingNull,
				UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
			}},
			Outputs: []ws.StageColumn{{ID: "group_id", Name: "group", Label: "Group", Type: "string"}},
		}},
	}
	if err := authored.Validate(authoredColumns); err != nil {
		t.Fatalf("validate authored discovery Pivot: %v", err)
	}
	emitted := []viewer.EmittedColumn{
		{EmissionID: "emission-group", AuthoredColumns: []string{"group"}, PublicColumn: "resolved_group"},
		{EmissionID: "emission-category", AuthoredColumns: []string{"category"}, PublicColumn: "resolved_category"},
		{EmissionID: "emission-amount", AuthoredColumns: []string{"amount"}, PublicColumn: "resolved_amount"},
	}
	operation, err := recipeConstructionOperation(authored.Steps[0].Operation)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Pivot == nil || operation.Pivot.Discovery == nil || operation.Pivot.Discovery.MaxCategories != 4 {
		t.Fatalf("mapped discovery declaration = %#v", operation.Pivot)
	}
	if len(operation.Pivot.Categories) != 0 {
		t.Fatalf("unresolved discovery unexpectedly produced categories: %#v", operation.Pivot.Categories)
	}

	_, err = recipeConstruction(authored, authoredColumns, emitted)
	var compileErr *Error
	if !errors.As(err, &compileErr) || compileErr.Code != "PIVOT_DISCOVERY_UNRESOLVED" {
		t.Fatalf("unresolved discovery compilation error = %v, want PIVOT_DISCOVERY_UNRESOLVED", err)
	}
	if !strings.Contains(compileErr.Message, "must be resolved to concrete categories") {
		t.Fatalf("unresolved discovery message = %q", compileErr.Message)
	}
}
