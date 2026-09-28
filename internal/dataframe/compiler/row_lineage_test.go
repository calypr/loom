package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileRowLineageUsesBoundedCanonicalGroupTerminal(t *testing.T) {
	output := lowerConstructionOutput(t, constructionMissingKeyPolicyOutput(recipe.ConstructionGroupMissingKeyGroup), recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	capability := RowLineageCapabilityForOutput(output)
	if !capability.Available {
		t.Fatalf("unsupported source operation: %s", capability.Operation)
	}
	compiled, err := CompileRowLineageOutput(output, "opaque-group-row", 25, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"root.payload.id", "root._key", "CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH",
		"TO_STRING([[\"construction\"", "SORT __loom_physical_construction_row_lineage_contributor",
		"LIMIT @row_lineage_offset, @row_lineage_fetch_limit", "hasMore:", "found:",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("row lineage query is missing %q:\n%s", want, compiled.Query)
		}
	}
	if strings.Contains(compiled.Query, "COLLECT ") && strings.Contains(compiled.Query, " INTO ") {
		t.Fatalf("row lineage query materialized unbounded Group contributors:\n%s", compiled.Query)
	}
	if compiled.BindVars[rowLineageRowIDBind] != "opaque-group-row" || compiled.BindVars[rowLineageOffsetBind] != 25 ||
		compiled.BindVars[rowLineageLimitBind] != 10 || compiled.BindVars[rowLineageFetchLimitBind] != 11 {
		t.Fatalf("row lineage page bindings = %#v", compiled.BindVars)
	}
}

func TestCompileKeylessEmptyGroupHasExplicitEmptyContributorPage(t *testing.T) {
	output := lowerConstructionOutput(t, constructionCountRowsOnlyOutput(), recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	if capability := RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("keyless Group capability = %#v, want available", capability)
	}
	compiled, err := CompileRowLineageOutput(output, "opaque-empty-group-row", 0, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiled.Query, "COLLECT WITH COUNT INTO") || !strings.Contains(compiled.Query, "contributors: SLICE") || strings.Contains(compiled.Query, "COLLECT "+"__loom_physical_row_lineage_group_key") {
		t.Fatalf("keyless Group lineage terminal does not preserve the empty Group row and bounded empty page:\n%s", compiled.Query)
	}
	if compiled.Limit != DefaultRowLineageLimit || compiled.BindVars[rowLineageFetchLimitBind] != DefaultRowLineageLimit+1 {
		t.Fatalf("default page = limit %d, bindings %#v", compiled.Limit, compiled.BindVars)
	}
}

func TestRowLineageCapabilityRejectsEarlierConstructionOperations(t *testing.T) {
	output := constructionOracleOutput()
	compiled := lowerConstructionOutput(t, output, recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	capability := RowLineageCapabilityForOutput(compiled)
	if capability.Available || capability.ReasonCode == "" || capability.Operation == "" {
		t.Fatalf("multi-stage construction capability = %#v, want a specific unsupported state", capability)
	}
}
