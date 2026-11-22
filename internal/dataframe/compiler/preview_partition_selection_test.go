package compiler

import (
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestPreviewProjectionPlansLargeQueryWithFewerThan160Columns(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Fields: []recipeplan.SemanticField{{Name: "id", FieldRef: "Patient.id", Expr: recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionScalar}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	terminal := plan.Operations[len(plan.Operations)-1].Return
	root := plan.Operations[0].RootScan.Variable
	for i := 0; i < 30; i++ {
		path := make([]string, 401)
		path[0] = "payload"
		for j := 1; j < len(path); j++ {
			path[j] = "nested_field"
		}
		terminal.Projections = append(terminal.Projections, ir.PhysicalProjection{
			Name: fmt.Sprintf("nested_%d", i), Value: ir.PhysicalValue{Variable: root, Path: path},
		})
	}
	page, err := CompileRecipeOutputPageWithPolicy(lower.CompiledRecipeOutput{Name: "patients", RootResourceType: "Patient", Plan: plan}, recipe.RuntimeBindings{Project: "p", PreviewLimit: 25}, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(page.RowsQuery) < minimumPreviewPartitionQueryBytes || len(page.RowsPartitions) < 2 {
		t.Fatalf("query bytes=%d, partitions=%d; want a large narrow query split", len(page.RowsQuery), len(page.RowsPartitions))
	}
}

func TestPreviewProjectionGroupsSeparateHiddenMetadataAndStayDeterministic(t *testing.T) {
	columns := make([]ir.PhysicalProjection, 161)
	for i := range columns {
		columns[i] = ir.PhysicalProjection{Name: fmt.Sprintf("column_%03d", i), Value: ir.PhysicalValue{Variable: "root", Path: []string{"payload", fmt.Sprintf("column_%03d", i)}}}
	}
	first, ok := groupPreviewProjections(columns)
	if !ok {
		t.Fatal("groups failed")
	}
	second, ok := groupPreviewProjections(columns)
	if !ok {
		t.Fatal("second groups failed")
	}
	if len(first) != len(second) || len(first) < 2 {
		t.Fatalf("partition count first=%d second=%d", len(first), len(second))
	}
	for i := range first {
		if len(first[i]) > previewProjectionPartitionSize {
			t.Fatalf("partition %d has %d projections", i, len(first[i]))
		}
		for j := range first[i] {
			if first[i][j].Name != second[i][j].Name {
				t.Fatal("grouping is nondeterministic")
			}
		}
	}
}

func TestPreviewProjectionPlanEmitsMetadataByItself(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient", Fields: []recipeplan.SemanticField{{Name: "id", FieldRef: "Patient.id", Expr: recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionScalar}}}})
	if err != nil {
		t.Fatal(err)
	}
	terminal := plan.Operations[len(plan.Operations)-1].Return
	root := plan.Operations[0].RootScan.Variable
	hasKey := false
	for _, projection := range terminal.Projections {
		hasKey = hasKey || projection.Name == "_key"
	}
	if !hasKey {
		terminal.Projections = append(terminal.Projections, ir.PhysicalProjection{Name: "_key", Hidden: true, Value: ir.PhysicalValue{Variable: root, Path: []string{"_key"}}})
	}
	terminal.Projections = append(terminal.Projections, ir.PhysicalProjection{Name: "__loom_dynamic_runtime_keys", Hidden: true, Value: ir.PhysicalValue{Variable: root, Path: []string{"payload"}}})
	plan.BindVars[RootPageKeysBind] = []string{}
	for i := 0; i < 160; i++ {
		terminal.Projections = append(terminal.Projections, ir.PhysicalProjection{Name: fmt.Sprintf("wide_%03d", i), Value: ir.PhysicalValue{Variable: root, Path: []string{"payload", fmt.Sprintf("wide_%03d", i)}}})
	}
	rendered, err := aql.RenderPhysicalPlanWithRootPageKeys(plan, RootPageKeysBind, 25)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := renderSelectedRootProjectionPartitions(plan, rendered, RootPageKeysBind, 25)
	if err != nil {
		t.Fatal(err)
	}
	foundMetadata := false
	for i, part := range parts {
		hasMetadata := false
		public := 0
		for _, name := range part.ProjectionNames {
			if name == "__loom_dynamic_runtime_keys" {
				hasMetadata = true
			}
			if !strings.HasPrefix(name, "__loom") && name != "_key" {
				public++
			}
		}
		if hasMetadata {
			foundMetadata = true
			if public != 0 {
				t.Fatalf("metadata partition %d also carries %d public columns", i, public)
			}
		}
	}
	if !foundMetadata {
		t.Fatal("missing metadata-only partition")
	}
}

func TestPreviewProjectionPlanDeclinesExcessiveRetainedPrefix(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient", Fields: []recipeplan.SemanticField{{Name: "id", FieldRef: "Patient.id", Expr: recipeplan.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionScalar}}}})
	if err != nil {
		t.Fatal(err)
	}
	root := plan.Operations[0].RootScan.Variable
	terminal := plan.Operations[len(plan.Operations)-1].Return
	hasKey := false
	for _, projection := range terminal.Projections {
		hasKey = hasKey || projection.Name == "_key"
	}
	if !hasKey {
		terminal.Projections = append(terminal.Projections, ir.PhysicalProjection{Name: "_key", Hidden: true, Value: ir.PhysicalValue{Variable: root, Path: []string{"_key"}}})
	}
	for i := 0; i < 160; i++ {
		terminal.Projections = append(terminal.Projections, ir.PhysicalProjection{Name: fmt.Sprintf("wide_%03d", i), Value: ir.PhysicalValue{Variable: root, Path: []string{"payload", fmt.Sprintf("wide_%03d", i)}}})
	}
	var mandatory ir.PhysicalOperation
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalFilterOp {
			mandatory = operation
			break
		}
	}
	if mandatory.Filter == nil {
		t.Fatal("expected generic source plan to retain root scope filters")
	}
	for i := 0; i < 4000; i++ {
		plan.Operations = append(plan.Operations[:len(plan.Operations)-1], append([]ir.PhysicalOperation{mandatory}, plan.Operations[len(plan.Operations)-1:]...)...)
	}
	plan.BindVars[RootPageKeysBind] = []string{}
	full, err := aql.RenderPhysicalPlanWithRootPageKeys(plan, RootPageKeysBind, 25)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := renderSelectedRootProjectionPartitions(plan, full, RootPageKeysBind, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 0 {
		t.Fatalf("excessive common prefix produced %d partitions; want full query fallback", len(parts))
	}
}
