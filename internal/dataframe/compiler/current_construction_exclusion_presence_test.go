package compiler

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func currentNullMissingExclusionOutput() recipe.Output {
	output := currentOraclePivotOutput(recipe.PivotUnlistedCategoryExcludeWithEvidence)
	output.Name = "current_null_missing_exclusions"
	output.Construction.Steps[0].Operation.Pivot.Categories = []recipe.ConstructionPivotCategory{
		{Key: recipe.TableScalar{Kind: recipe.TableScalarNull}, OutputColumnID: "null_value"},
		{Key: recipe.TableScalar{Kind: recipe.TableScalarMissing}, OutputColumnID: "missing_value"},
	}
	output.Construction.Steps[0].Outputs = []recipe.StageColumn{
		{ID: "group_text", Name: "group_text"},
		{ID: "group_number", Name: "group_number"},
		{ID: "null_value", Name: "null_value"},
		{ID: "missing_value", Name: "missing_value"},
	}
	return output
}

func TestCurrentPivotExclusionsPreserveSourceCategoryPresence(t *testing.T) {
	for _, test := range []struct {
		name      string
		output    recipe.Output
		fromInput bool
	}{
		{name: "literal categories", output: currentOraclePivotOutput(recipe.PivotUnlistedCategoryExcludeWithEvidence)},
		{name: "NULL and MISSING categories", output: currentNullMissingExclusionOutput(), fromInput: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, _, err := compileCurrentConstructionOracle(test.output, "private-project", "generation", 100)
			if err != nil {
				t.Fatal(err)
			}
			if err := compiled.Plan.Validate(); err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(compiled.Plan)
			if err != nil {
				t.Fatal(err)
			}
			original := compiled.Plan.StageSequence.Stages[0].GroupedPivot
			if original.CategoryPresenceFromInput != test.fromInput {
				t.Fatalf("category-presence path = %t, want %t", original.CategoryPresenceFromInput, test.fromInput)
			}
			source := compiled.Plan.Operations[len(compiled.Plan.Operations)-1].Return.Projections
			var originalCategory *ir.PhysicalProjection
			for i := range source {
				if source[i].Name == original.CategoryColumn {
					originalCategory = &source[i]
				}
			}
			if originalCategory == nil {
				t.Fatal("source category projection is missing")
			}
			replayed, err := sourceStagePivotForExclusions(ir.ClonePhysicalPlan(compiled.Plan))
			if err != nil {
				t.Fatal(err)
			}
			if err := replayed.Validate(); test.fromInput && err != nil {
				t.Fatalf("replayed exclusion input invalid: %v", err)
			}
			var pivot *ir.PhysicalGroupedPivot
			for _, operation := range replayed.Operations {
				if operation.GroupedPivot != nil {
					pivot = operation.GroupedPivot
				}
			}
			if pivot == nil {
				t.Fatal("replayed Pivot is missing")
			}
			if test.fromInput && (len(pivot.Categories) != 2 || pivot.Categories[0].MatchKind != ir.PhysicalPivotCategoryNullMatch || pivot.Categories[1].MatchKind != ir.PhysicalPivotCategoryMissingMatch) {
				t.Fatal("replay changed the typed NULL/MISSING partition")
			}
			if pivot.CategoryPresenceColumn != original.CategoryPresenceColumn || pivot.CategoryPresenceFromInput != original.CategoryPresenceFromInput {
				t.Fatal("replay changed the compiler-owned presence companion")
			}
			categoryFound, companionFound := false, false
			for _, projection := range pivot.InputProjections {
				if projection.Name == pivot.CategoryPresenceColumn {
					companionFound = true
				}
				if projection.Name != pivot.CategoryColumn {
					continue
				}
				categoryFound = true
				if test.fromInput {
					if originalCategory.Presence == nil {
						t.Fatal("fixture did not exercise duplicate source-presence metadata")
					}
					if projection.Presence != nil {
						t.Fatal("category projection duplicates presence already supplied by the companion")
					}
				} else if !reflect.DeepEqual(projection.Presence, originalCategory.Presence) {
					t.Fatal("literal category replay changed ordinary source-presence metadata")
				}
			}
			if !categoryFound || test.fromInput && !companionFound {
				t.Fatal("source category or presence companion was dropped")
			}
			query, err := CompileTableShapeExclusionsWithPolicy(compiled, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if query.Query == "" || query.BindVars["table_shape_exclusion_fetch_limit"] != 11 {
				t.Fatal("exclusion query did not retain bounded paging")
			}
			after, err := json.Marshal(compiled.Plan)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("exclusion replay mutated the current source plan")
			}
		})
	}
}
