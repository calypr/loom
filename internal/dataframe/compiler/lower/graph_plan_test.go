package lower

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestBuildGraphPhysicalPlanRootOnly(t *testing.T) {
	plan, err := BuildGraphPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}}, semantic.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("BuildGraphPhysicalPlan() error = %v", err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	if rendered.Query == "" {
		t.Fatal("expected graph query")
	}
}

func TestBuildGraphPhysicalPlanOneHopRequired(t *testing.T) {
	plan, err := BuildGraphPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []semantic.SemanticNode{{Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient", MatchMode: spec.TraversalMatchRequired}}}}, semantic.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("BuildGraphPhysicalPlan() error = %v", err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	if !strings.Contains(rendered.Query, "GRAPH_RETURN") && !strings.Contains(rendered.Query, "COLLECT") {
		t.Fatalf("expected path union query, got %s", rendered.Query)
	}
}

func TestBuildGraphPhysicalPlanRootFallbackOnlyForOptionalBranches(t *testing.T) {
	base := semantic.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []semantic.SemanticNode{{Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient", MatchMode: spec.TraversalMatchOptional}}}
	optional, err := BuildGraphPhysicalPlan(semantic.OutputPlan{Root: base}, semantic.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if got := optional.Operations[len(optional.Operations)-1].GraphReturn.PathSets[0]; got != "path_root" {
		t.Fatalf("optional path sets = %#v, want root fallback", optional.Operations[len(optional.Operations)-1].GraphReturn.PathSets)
	}
	base.Children[0].MatchMode = spec.TraversalMatchRequired
	required, err := BuildGraphPhysicalPlan(semantic.OutputPlan{Root: base}, semantic.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range required.Operations[len(required.Operations)-1].GraphReturn.PathSets {
		if set == "path_root" {
			t.Fatal("required graph unexpectedly returns root-only path")
		}
	}
}

func graphPlanTestRoot(children ...semantic.SemanticNode) semantic.SemanticNode {
	return semantic.SemanticNode{Alias: "root", ResourceType: "Patient", Children: children}
}

func graphPlanTestCondition(alias string, mode spec.TraversalMatchMode, children ...semantic.SemanticNode) semantic.SemanticNode {
	return semantic.SemanticNode{Alias: alias, ResourceType: "Condition", EdgeLabel: "subject_Patient", MatchMode: mode, Children: children}
}

func graphPlanTestObservation(alias string, mode spec.TraversalMatchMode) semantic.SemanticNode {
	return semantic.SemanticNode{Alias: alias, ResourceType: "Observation", EdgeLabel: "subject_Patient", MatchMode: mode}
}

func graphPlanTestFocusedObservation(alias string, mode spec.TraversalMatchMode) semantic.SemanticNode {
	return semantic.SemanticNode{Alias: alias, ResourceType: "Observation", EdgeLabel: "focus_Condition", MatchMode: mode}
}

func graphPlanTestObservationSiblings(count int, mode spec.TraversalMatchMode) []semantic.SemanticNode {
	children := make([]semantic.SemanticNode, 0, count)
	for index := 0; index < count; index++ {
		children = append(children, graphPlanTestObservation(fmt.Sprintf("observation_%d", index), mode))
	}
	return children
}

func graphPlanTestBuild(t *testing.T, root semantic.SemanticNode) ir.PhysicalPlan {
	t.Helper()
	plan, err := BuildGraphPhysicalPlan(semantic.OutputPlan{Root: root}, semantic.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("BuildGraphPhysicalPlan() error = %v", err)
	}
	return plan
}

func graphPlanTestPathSets(t *testing.T, plan ir.PhysicalPlan) []string {
	t.Helper()
	last := plan.Operations[len(plan.Operations)-1]
	if last.GraphReturn == nil {
		t.Fatalf("final operation = %#v, want graph return", last)
	}
	return last.GraphReturn.PathSets
}

func graphPlanTestRequiredMatchKeys(plan ir.PhysicalPlan) []string {
	keys := []string{}
	for key := range plan.BindVars {
		if strings.HasPrefix(key, "required_") && strings.HasSuffix(key, "_result") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func graphPlanTestRequiredFiltersPrecedePathSeed(t *testing.T, plan ir.PhysicalPlan) {
	t.Helper()
	seed := -1
	for index, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalPathSeedOp {
			seed = index
			break
		}
	}
	if seed < 0 {
		t.Fatal("plan has no path seed operation")
	}
	for index := seed; index < len(plan.Operations); index++ {
		operation := plan.Operations[index]
		if operation.Kind == ir.PhysicalFilterOp && operation.Filter != nil && operation.Filter.Expression != nil && operation.Filter.Expression.Kind == ir.PhysicalExistsPredicate {
			t.Fatalf("required semi-join filter at %d must precede the path seed at %d", index, seed)
		}
	}
}

func TestBuildGraphPhysicalPlanOmittedMatchModeDefaultsToRequired(t *testing.T) {
	explicit := graphPlanTestBuild(t, graphPlanTestRoot(graphPlanTestCondition("condition", spec.TraversalMatchRequired)))
	for _, omitted := range []spec.TraversalMatchMode{"", " "} {
		plan := graphPlanTestBuild(t, graphPlanTestRoot(graphPlanTestCondition("condition", omitted)))
		if !reflect.DeepEqual(plan, explicit) {
			t.Fatalf("omitted match mode %q compiled differently from REQUIRED", omitted)
		}
	}
	if got := graphPlanTestPathSets(t, explicit); !reflect.DeepEqual(got, []string{"path_1"}) {
		t.Fatalf("required path sets = %v, want [path_1] without the root fallback", got)
	}
	for _, operation := range explicit.Operations {
		if operation.PathExtend != nil && operation.PathExtend.MatchMode != string(spec.TraversalMatchRequired) {
			t.Fatalf("path extend match mode = %q, want REQUIRED", operation.PathExtend.MatchMode)
		}
	}
}

func TestBuildGraphPhysicalPlanRequiredPrefixPathSensitivity(t *testing.T) {
	cases := []struct {
		name             string
		root             semantic.SemanticNode
		wantPathSets     []string
		wantRequiredKeys []string
	}{
		{
			name:             "required then required",
			root:             graphPlanTestRoot(graphPlanTestCondition("condition", spec.TraversalMatchRequired, graphPlanTestFocusedObservation("observation", spec.TraversalMatchRequired))),
			wantPathSets:     []string{"path_1", "path_2"},
			wantRequiredKeys: []string{"required_0_result", "required_1_result"},
		},
		{
			name:             "required under optional",
			root:             graphPlanTestRoot(graphPlanTestCondition("condition", spec.TraversalMatchOptional, graphPlanTestFocusedObservation("observation", spec.TraversalMatchRequired))),
			wantPathSets:     []string{"path_root", "path_1", "path_2"},
			wantRequiredKeys: []string{},
		},
		{
			name:             "required then optional",
			root:             graphPlanTestRoot(graphPlanTestCondition("condition", spec.TraversalMatchRequired, graphPlanTestFocusedObservation("observation", spec.TraversalMatchOptional))),
			wantPathSets:     []string{"path_1", "path_2"},
			wantRequiredKeys: []string{"required_0_result"},
		},
		{
			name: "branching required prefix",
			root: graphPlanTestRoot(
				graphPlanTestCondition("condition", spec.TraversalMatchRequired, graphPlanTestFocusedObservation("observation", spec.TraversalMatchRequired)),
				graphPlanTestObservation("screening", spec.TraversalMatchOptional),
			),
			wantPathSets:     []string{"path_1", "path_2", "path_3"},
			wantRequiredKeys: []string{"required_0_result", "required_1_result"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			plan := graphPlanTestBuild(t, test.root)
			if got := graphPlanTestPathSets(t, plan); !reflect.DeepEqual(got, test.wantPathSets) {
				t.Fatalf("path sets = %v, want %v", got, test.wantPathSets)
			}
			if got := graphPlanTestRequiredMatchKeys(plan); !reflect.DeepEqual(got, test.wantRequiredKeys) {
				t.Fatalf("required semi-join bindings = %v, want %v", got, test.wantRequiredKeys)
			}
			graphPlanTestRequiredFiltersPrecedePathSeed(t, plan)
		})
	}
}

func TestBuildGraphPhysicalPlanTraversalDeclarationLimitBoundary(t *testing.T) {
	atLimit := graphPlanTestBuild(t, graphPlanTestRoot(graphPlanTestObservationSiblings(32, spec.TraversalMatchOptional)...))
	wantAtLimit := []string{"path_root"}
	for index := 1; index <= 32; index++ {
		wantAtLimit = append(wantAtLimit, fmt.Sprintf("path_%d", index))
	}
	if got := graphPlanTestPathSets(t, atLimit); !reflect.DeepEqual(got, wantAtLimit) {
		t.Fatalf("path sets at the declaration limit = %v, want %v", got, wantAtLimit)
	}
	_, err := BuildGraphPhysicalPlan(semantic.OutputPlan{Root: graphPlanTestRoot(graphPlanTestObservationSiblings(33, spec.TraversalMatchOptional)...)}, semantic.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err == nil || err.Error() != "graph traversal declaration count exceeds 32" {
		t.Fatalf("over-limit error = %v, want the declaration count error", err)
	}
	shaping := graphPlanTestObservationSiblings(33, spec.TraversalMatchOptional)
	shaping[0].Fields = []semantic.SemanticField{{Name: "status"}}
	_, err = BuildGraphPhysicalPlan(semantic.OutputPlan{Root: graphPlanTestRoot(shaping...)}, semantic.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err == nil || err.Error() != "graph traversal declaration count exceeds 32" {
		t.Fatalf("count check must precede shaping validation, got %v", err)
	}
	invalid := graphPlanTestObservationSiblings(33, spec.TraversalMatchOptional)
	invalid[0].MatchMode = "SMALL"
	_, err = BuildGraphPhysicalPlan(semantic.OutputPlan{Root: graphPlanTestRoot(invalid...)}, semantic.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err == nil || !strings.Contains(err.Error(), `unsupported traversal match mode "SMALL"`) {
		t.Fatalf("semantic validation must precede the count check, got %v", err)
	}
}

func TestBuildGraphPhysicalPlanMatchModeAndShapingValidation(t *testing.T) {
	_, err := BuildGraphPhysicalPlan(semantic.OutputPlan{Root: graphPlanTestRoot(graphPlanTestObservation("observation", "SMALL"))}, semantic.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err == nil || err.Error() != `semantic traversal Patient -> Observation (subject_Patient) at Patient.children[0]: unsupported traversal match mode "SMALL"` {
		t.Fatalf("invalid match mode error = %v", err)
	}
	shapingChild := graphPlanTestObservation("observation", spec.TraversalMatchOptional)
	shapingChild.Fields = []semantic.SemanticField{{Name: "status"}}
	_, err = BuildGraphPhysicalPlan(semantic.OutputPlan{Root: graphPlanTestRoot(shapingChild)}, semantic.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err == nil || err.Error() != `graph traversal "observation" cannot declare dataframe shaping fields` {
		t.Fatalf("shaping field error = %v", err)
	}
	rootMode := graphPlanTestRoot(graphPlanTestObservation("observation", spec.TraversalMatchOptional))
	rootMode.MatchMode = spec.TraversalMatchRequired
	_, err = BuildGraphPhysicalPlan(semantic.OutputPlan{Root: rootMode}, semantic.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err == nil || err.Error() != `semantic graph root must not declare traversal match mode "REQUIRED"` {
		t.Fatalf("root match mode error = %v", err)
	}
	bothChild := graphPlanTestObservation("observation", "SMALL")
	bothChild.Fields = []semantic.SemanticField{{Name: "status"}}
	_, err = BuildGraphPhysicalPlan(semantic.OutputPlan{Root: graphPlanTestRoot(bothChild)}, semantic.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err == nil || !strings.Contains(err.Error(), `unsupported traversal match mode "SMALL"`) {
		t.Fatalf("semantic validation must precede shaping validation, got %v", err)
	}
}

func TestBuildGraphPhysicalPlanLeavesCallerGraphUnmodified(t *testing.T) {
	input := graphPlanTestRoot(
		graphPlanTestCondition("condition", "", graphPlanTestFocusedObservation("observation", spec.TraversalMatchRequired)),
		graphPlanTestObservation("screening", spec.TraversalMatchOptional),
	)
	template := graphPlanTestRoot(
		graphPlanTestCondition("condition", "", graphPlanTestFocusedObservation("observation", spec.TraversalMatchRequired)),
		graphPlanTestObservation("screening", spec.TraversalMatchOptional),
	)
	first := graphPlanTestBuild(t, input)
	if !reflect.DeepEqual(input, template) {
		t.Fatal("BuildGraphPhysicalPlan mutated the caller-provided graph")
	}
	second := graphPlanTestBuild(t, input)
	if !reflect.DeepEqual(input, template) {
		t.Fatal("second BuildGraphPhysicalPlan mutated the caller-provided graph")
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("plan output is not deterministic for an identical caller graph")
	}
	if input.Children[0].MatchMode != "" {
		t.Fatalf("omitted match mode was mutated to %q", input.Children[0].MatchMode)
	}
}
