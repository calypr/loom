package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func correlatedPhysicalBinding() *fhirschema.CorrelatedBinding {
	return &fhirschema.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"}
}

func TestCorrelatedPhysicalProjectionScopesCodingAndValueToOwner(t *testing.T) {
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{
			Name: "shared", Columns: []string{"shared"}, ColumnSelector: mustSelector(t, "code.coding[].code"), ValueSelector: mustSelector(t, "valueQuantity.value"), ValueKind: expression.KindDecimal,
			Correlation: correlatedPhysicalBinding(), CorrelationSystem: "urn:study:A", CorrelationCode: "shared",
		}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.BindVars["pivot_root_shared_columns_system"] != "urn:study:A" {
		t.Fatalf("system bind = %#v", rendered.BindVars)
	}
	if !strings.Contains(rendered.Query, "component") || !strings.Contains(rendered.Query, "valueQuantity") || !strings.Contains(rendered.Query, "correlation_pivot_coding") {
		t.Fatalf("correlated projection lost owner/coding/value scope:\n%s", rendered.Query)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("correlated projection plan invalid: %v", err)
	}
}

func TestCorrelatedPhysicalProjectionPreservesDeclaredReductionAndAlias(t *testing.T) {
	for _, test := range []struct {
		mode       string
		want       string
		forbid     string
		wantMarker string
	}{
		{mode: "ALL", want: ": __correlation_flat_values", forbid: "SORTED_UNIQUE(__correlation_flat_values)"},
		{mode: "DISTINCT", want: "SORTED_UNIQUE(__correlation_flat_values)"},
		{mode: "VALUE", want: "INVALID_MULTIPLE_VALUES"},
		{mode: "FIRST", want: "FIRST(SORTED(__correlation_flat_values))"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
				Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{
					Name: "correlated_family", Columns: []string{"shared"}, ColumnAliases: map[string]string{"shared": "height_cm"}, ProjectionMode: test.mode,
					Correlation: correlatedPhysicalBinding(), CorrelationSystem: "urn:study:A", CorrelationCode: "shared",
				}},
			}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			var pivot *ir.PhysicalPivotMap
			var outputName string
			operations := append(append([]ir.PhysicalOperation(nil), plan.DeferredExpressionLets...), plan.Operations...)
			for _, operation := range operations {
				if operation.ExpressionLet != nil && operation.ExpressionLet.Expression.Pivot != nil {
					pivot = operation.ExpressionLet.Expression.Pivot
				}
			}
			if pivot == nil || pivot.ColumnAliases["shared"] != "height_cm" || pivot.ProjectionMode != test.mode {
				t.Fatalf("physical pivot = %#v", pivot)
			}
			for _, operation := range operations {
				if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
					continue
				}
				for _, projection := range operation.Return.Projections {
					if projection.Name == "height_cm" {
						outputName = projection.Name
					}
				}
			}
			if outputName != "height_cm" {
				t.Fatalf("output projection name = %q, want height_cm", outputName)
			}
			rendered, err := aql.RenderPhysicalPlan(plan)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(rendered.Query, test.want) {
				t.Fatalf("query missing %q:\n%s", test.want, rendered.Query)
			}
			if test.forbid != "" && strings.Contains(rendered.Query, test.forbid) {
				t.Fatalf("query used forbidden reduction %q:\n%s", test.forbid, rendered.Query)
			}
		})
	}
}

func TestCorrelatedPhysicalProjectionMarksUnsupportedChoiceArm(t *testing.T) {
	binding := correlatedPhysicalBinding()
	binding.ChoiceArms = []string{"valueQuantity"}
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{
			Name: "correlated_family", Columns: []string{"shared"}, ColumnAliases: map[string]string{"shared": "height_cm"}, ProjectionMode: "VALUE",
			Correlation: binding, CorrelationSystem: "urn:study:A", CorrelationCode: "shared",
		}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "INVALID_CHOICE_ARM") || !strings.Contains(rendered.Query, "raw: __correlation_unsupported_values") {
		t.Fatalf("query lacks explicit choice-arm outcome:\n%s", rendered.Query)
	}
}

func TestCorrelatedPhysicalStringAllPreservesArrayAndDeduplicatesCodingAliases(t *testing.T) {
	binding := correlatedPhysicalBinding()
	binding.ValuePath = "valueString"
	binding.LogicalType = "string"
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{
			Name: "correlated_family", Columns: []string{"shared"}, ColumnAliases: map[string]string{"shared": "height_cm"}, ProjectionMode: "ALL", StringifyValue: true,
			Correlation: binding, CorrelationSystem: "urn:study:A", CorrelationCode: "shared",
		}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.Query, "TO_STRING(__correlation_flat_values)") {
		t.Fatalf("string ALL projection converted the whole array instead of preserving its elements:\n%s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, ": __correlation_flat_values") {
		t.Fatalf("string ALL projection does not return the value array:\n%s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "COLLECT __correlation_owner_key = __correlation_candidate.key INTO __correlation_owner_group") || !strings.Contains(rendered.Query, "UNIQUE(FLATTEN(__correlation_owner_group[*].__correlation_candidate.values))") {
		t.Fatalf("correlated projection does not collapse duplicate Coding aliases within an owner:\n%s", rendered.Query)
	}
}

func TestCorrelatedPhysicalFilterUsesSameCodingBinding(t *testing.T) {
	code := "shared"
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Filters: []spec.TypedFilter{{
			FieldRef: "Observation.component_code", Selector: "code.coding[].code", FieldKind: spec.FilterCode, Repeated: true, Quantifier: spec.QuantifierAny, Operator: spec.FilterEquals,
			Values: []spec.FilterValue{{Kind: spec.FilterCode, Code: &spec.CodeValue{System: "urn:study:B", Code: code}}}, Correlation: correlatedPhysicalBinding(),
		}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.BindVars["correlated_system_root_filter_1"] != "urn:study:B" || rendered.BindVars["correlated_code_root_filter_1"] != code {
		t.Fatalf("filter binds = %#v", rendered.BindVars)
	}
	if !strings.Contains(rendered.Query, "FILTER __correlation_system == @correlated_system_root_filter_1") || !strings.Contains(rendered.Query, "FILTER __correlation_code == @correlated_code_root_filter_1") {
		t.Fatalf("same-Coding filter constraints missing:\n%s", rendered.Query)
	}
}

func TestCorrelatedPhysicalVersionedFilterMatchesSameCodingVersion(t *testing.T) {
	binding := correlatedPhysicalBinding()
	binding.ValueScope = fhirschema.CorrelatedValueScopeCoding
	binding.VersionPath = "version"
	binding.ValuePath = "display"
	binding.LogicalType = "string"
	binding.ChoiceArms = nil
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Filters: []spec.TypedFilter{{
			FieldRef: "Observation.component_code", Selector: "code.coding[].code", FieldKind: spec.FilterCode, Repeated: true, Quantifier: spec.QuantifierAny, Operator: spec.FilterEquals,
			Values: []spec.FilterValue{{Kind: spec.FilterCode, Code: &spec.CodeValue{System: "urn:study:A", Code: "shared", Version: "v2"}}}, Correlation: binding,
		}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if got := rendered.BindVars["correlated_version_root_filter_1"]; got != "v2" {
		t.Fatalf("version bind = %#v, want v2; all binds: %#v", got, rendered.BindVars)
	}
	query := rendered.Query
	codeMatch := strings.Index(query, "FILTER __correlation_code == @correlated_code_root_filter_1")
	versionMatch := strings.Index(query, "FILTER __correlation_version == @correlated_version_root_filter_1")
	valueRead := strings.Index(query, "LET __correlation_values =")
	if codeMatch < 0 || versionMatch < codeMatch || valueRead < versionMatch {
		t.Fatalf("system/code/version must match one Coding before its value is read:\n%s", query)
	}
	if !strings.Contains(query, "LET __correlation_version = NOT_NULL(FIRST(") {
		t.Fatalf("missing/null Coding.version is not normalized to empty string:\n%s", query)
	}
}

func TestCorrelatedPhysicalProjectionReadsValueFromMatchedCoding(t *testing.T) {
	binding := correlatedPhysicalBinding()
	binding.ValueScope = fhirschema.CorrelatedValueScopeCoding
	binding.VersionPath = "version"
	binding.ValuePath = "display"
	binding.ValueFallback = []string{"code"}
	binding.FallbackOnBlankString = true
	binding.ChoiceArms = nil
	binding.LogicalType = "string"
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{
			Name: "shared", Columns: []string{"shared"}, ProjectionMode: "ALL",
			Correlation: binding, CorrelationSystem: "urn:study:A", CorrelationCode: "shared", CorrelationVersion: "v2",
		}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	query := rendered.Query
	match := strings.Index(query, "FILTER POSITION(@pivot_root_shared_columns, __correlation_code)")
	versionMatch := strings.Index(query, "FILTER __correlation_version == @pivot_root_shared_columns_version")
	values := strings.Index(query, "LET __correlation_values =")
	if got := rendered.BindVars["pivot_root_shared_columns_version"]; got != "v2" {
		t.Fatalf("version bind = %#v, want v2", got)
	}
	if match < 0 || versionMatch < match || values < versionMatch {
		t.Fatalf("selected system/code/version Coding must match before its value is read:\n%s", query)
	}
	valueEnd := strings.Index(query[values:], "LET __correlation_flat_values =")
	if valueEnd < 0 {
		t.Fatalf("query has no end for correlated value expression:\n%s", query)
	}
	valueExpression := query[values : values+valueEnd]
	for _, fragment := range []string{
		"FOR __root IN [__loom_physical_correlation_pivot_coding]",
		"LET __value = __root.display",
		"LET __value = __root.code",
		"TRIM(__loom_physical_correlation_nonblank_value) != ''",
	} {
		if !strings.Contains(valueExpression, fragment) {
			t.Fatalf("same-Coding display/code fallback is missing %q:\n%s", fragment, valueExpression)
		}
	}
}

func TestCorrelatedPhysicalVersionlessKeyMatchesOnlyMissingOrNullVersion(t *testing.T) {
	binding := correlatedPhysicalBinding()
	binding.ValueScope = fhirschema.CorrelatedValueScopeCoding
	binding.VersionPath = "version"
	binding.ValuePath = "display"
	binding.LogicalType = "string"
	binding.ChoiceArms = nil
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{
			Name: "shared", Columns: []string{"shared"}, Correlation: binding,
			CorrelationSystem: "urn:study:A", CorrelationCode: "shared", CorrelationVersion: "",
		}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := rendered.BindVars["pivot_root_shared_columns_version"]; !ok || value != "" {
		t.Fatalf("empty-version bind = %#v, present=%t; all binds: %#v", value, ok, rendered.BindVars)
	}
	if !strings.Contains(rendered.Query, `NOT_NULL(FIRST(`) || !strings.Contains(rendered.Query, `, "")`) || !strings.Contains(rendered.Query, "FILTER __correlation_version == @pivot_root_shared_columns_version") {
		t.Fatalf("empty selected version must match only missing/null Coding.version:\n%s", rendered.Query)
	}
}

func TestCorrelatedPhysicalTaskSingletonCodingPivotAndFilter(t *testing.T) {
	binding := &fhirschema.CorrelatedBinding{
		OwnerPath: "input[]", KeyPath: "input[].valueCoding", ValueScope: fhirschema.CorrelatedValueScopeCoding,
		SystemPath: "system", CodePath: "code", VersionPath: "version", ValuePath: "display",
		ValueFallback: []string{"code"}, FallbackOnBlankString: true, LogicalType: "string",
	}
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Task",
		Pivots: []semantic.SemanticPivot{{
			Name: "task_code", Columns: []string{"shared"}, ProjectionMode: "ALL", Correlation: binding,
			CorrelationSystem: "urn:study:A", CorrelationCode: "shared", CorrelationVersion: "v2",
		}},
		Filters: []spec.TypedFilter{{
			FieldRef: "Task.input_code", Selector: "input[].valueCoding.code", FieldKind: spec.FilterCode, Repeated: true, Quantifier: spec.QuantifierAny, Operator: spec.FilterEquals,
			Values: []spec.FilterValue{{Kind: spec.FilterCode, Code: &spec.CodeValue{System: "urn:study:A", Code: "shared", Version: "v2"}}}, Correlation: binding,
		}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, bind := range []string{"pivot_root_task_code_columns_version", "correlated_version_root_filter_1"} {
		if rendered.BindVars[bind] != "v2" {
			t.Fatalf("singleton Coding version bind %q = %#v; all binds: %#v", bind, rendered.BindVars[bind], rendered.BindVars)
		}
	}
	for _, codingVariable := range []string{"__loom_physical_correlation_pivot_coding", "__loom_physical_correlation_coding"} {
		loop := "FOR " + codingVariable + " IN "
		start := strings.Index(rendered.Query, loop)
		if start < 0 {
			t.Fatalf("query has no singleton Coding loop %q:\n%s", loop, rendered.Query)
		}
		collectionStart := start + len(loop)
		if !strings.HasPrefix(rendered.Query[collectionStart:], "(\n") {
			t.Fatalf("singular valueCoding must be iterated as the singleton result list, without array flattening:\n%s", rendered.Query)
		}
	}
	for _, fragment := range []string{
		"LET __value = __root.valueCoding",
		"FILTER __value != null",
		"FILTER __correlation_version == @pivot_root_task_code_columns_version",
		"FILTER __correlation_version == @correlated_version_root_filter_1",
		"LET __value = __root.display",
		"LET __value = __root.code",
	} {
		if !strings.Contains(rendered.Query, fragment) {
			t.Fatalf("singleton Coding correlation is missing %q:\n%s", fragment, rendered.Query)
		}
	}
}

func TestCorrelatedPhysicalFilterReadsValueFromMatchedCoding(t *testing.T) {
	binding := correlatedPhysicalBinding()
	binding.ValueScope = fhirschema.CorrelatedValueScopeCoding
	binding.ValuePath = "display"
	binding.ValueFallback = []string{"code"}
	binding.FallbackOnBlankString = true
	binding.ChoiceArms = nil
	binding.LogicalType = "string"
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Filters: []spec.TypedFilter{{
			FieldRef: "Observation.component_code", Selector: "code.coding[].code", FieldKind: spec.FilterCode, Repeated: true, Quantifier: spec.QuantifierAny, Operator: spec.FilterEquals,
			Values: []spec.FilterValue{{Kind: spec.FilterCode, Code: &spec.CodeValue{System: "urn:study:A", Code: "shared"}}}, Correlation: binding,
		}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	query := rendered.Query
	match := strings.Index(query, "FILTER __correlation_code == @correlated_code_root_filter_1")
	values := strings.Index(query, "LET __correlation_values =")
	if match < 0 || values < match {
		t.Fatalf("selected Coding must match before its value is read:\n%s", query)
	}
	valueEnd := strings.Index(query[values:], "LET __correlation_unsupported_values =")
	if valueEnd < 0 {
		t.Fatalf("query has no end for correlated value expression:\n%s", query)
	}
	valueExpression := query[values : values+valueEnd]
	for _, fragment := range []string{"FOR __root IN [__loom_physical_correlation_coding]", "LET __value = __root.display", "LET __value = __root.code"} {
		if !strings.Contains(valueExpression, fragment) {
			t.Fatalf("same-Coding display/code fallback is missing %q:\n%s", fragment, valueExpression)
		}
	}
}

func TestCorrelatedPhysicalCodingScopeValidatesValuesAgainstCoding(t *testing.T) {
	for _, test := range []struct {
		name        string
		withVersion bool
		mutate      func(*ir.PhysicalCorrelation)
	}{
		{name: "primary", mutate: func(correlation *ir.PhysicalCorrelation) {
			correlation.ValueSelector = mustSelector(t, "valueQuantity.value")
		}},
		{name: "fallback", mutate: func(correlation *ir.PhysicalCorrelation) {
			correlation.ValueFallbacks[0] = mustSelector(t, "valueQuantity.value")
		}},
		{name: "key shape", mutate: func(correlation *ir.PhysicalCorrelation) {
			correlation.KeyIsArray = false
		}},
		{name: "version selector", withVersion: true, mutate: func(correlation *ir.PhysicalCorrelation) {
			selector := mustSelector(t, "display")
			correlation.VersionSelector = &selector
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			binding := correlatedPhysicalBinding()
			binding.ValueScope = fhirschema.CorrelatedValueScopeCoding
			binding.ValuePath = "display"
			if test.withVersion {
				binding.VersionPath = "version"
			}
			binding.ValueFallback = []string{"code"}
			binding.ChoiceArms = nil
			binding.LogicalType = "string"
			plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
				Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{
					Name: "shared", Columns: []string{"shared"}, Correlation: binding, CorrelationSystem: "urn:study:A", CorrelationCode: "shared",
				}},
			}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			var correlation *ir.PhysicalCorrelation
			operations := append(append([]ir.PhysicalOperation(nil), plan.DeferredExpressionLets...), plan.Operations...)
			for _, operation := range operations {
				if operation.ExpressionLet != nil && operation.ExpressionLet.Expression.Pivot != nil {
					correlation = operation.ExpressionLet.Expression.Pivot.Correlation
				}
			}
			if correlation == nil {
				t.Fatal("correlated pivot has no physical correlation")
			}
			test.mutate(correlation)
			if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "Coding") {
				t.Fatalf("invalid Coding selector error = %v", err)
			}
		})
	}
}

func TestExtensionPhysicalProjectionKeepsNestedURLAndValueScope(t *testing.T) {
	binding := &fhirschema.ExtensionBinding{
		OwnerPath: "extension[].extension[]", URLPath: []string{"urn:parent:left", "urn:leaf"}, ValuePath: "valueString", LogicalType: "string", ChoiceArms: []string{"valueString"},
	}
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{
			Name: "extension_family", Columns: []string{"left_leaf"}, ColumnAliases: map[string]string{"left_leaf": "left_leaf"}, ProjectionMode: "ALL",
			ExtensionCorrelation: binding,
		}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("extension plan invalid: %v", err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.BindVars["pivot_root_extension_family_columns_url_0"] != "urn:parent:left" || rendered.BindVars["pivot_root_extension_family_columns_url_1"] != "urn:leaf" {
		t.Fatalf("extension URL binds = %#v", rendered.BindVars)
	}
	if !strings.Contains(rendered.Query, "extension") || !strings.Contains(rendered.Query, "valueString") || !strings.Contains(rendered.Query, "pivot_root_extension_family_columns_url_0") || !strings.Contains(rendered.Query, "pivot_root_extension_family_columns_url_1") {
		t.Fatalf("extension ancestry/value scope missing:\n%s", rendered.Query)
	}
	if strings.Contains(rendered.Query, "correlation_pivot_coding") || strings.Contains(rendered.Query, "match = leaf") {
		t.Fatalf("extension projection used coding or legacy leaf matching:\n%s", rendered.Query)
	}
}

func TestExtensionPhysicalProjectionMakesWrongArmExplicit(t *testing.T) {
	binding := &fhirschema.ExtensionBinding{
		OwnerPath: "extension[].extension[]", URLPath: []string{"urn:parent:left", "urn:leaf"}, ValuePath: "valueString", LogicalType: "string",
	}
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{Name: "extension_family", Columns: []string{"leaf"}, ColumnAliases: map[string]string{"leaf": "leaf"}, ExtensionCorrelation: binding}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "INVALID_CHOICE_ARM") || !strings.Contains(rendered.Query, "valueBoolean") {
		t.Fatalf("wrong extension choice arm is not explicit:\n%s", rendered.Query)
	}
}

func TestExtensionPhysicalProjectionReducesAllOwnersTogether(t *testing.T) {
	binding := &fhirschema.ExtensionBinding{OwnerPath: "extension[].extension[]", URLPath: []string{"urn:parent:left", "urn:leaf"}, ValuePath: "valueString", LogicalType: "string"}
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Pivots: []semantic.SemanticPivot{{Name: "extension_family", Columns: []string{"leaf"}, ProjectionMode: "VALUE", ExtensionCorrelation: binding}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "FLATTEN(__extension_pairs[*].values)") || !strings.Contains(rendered.Query, "INVALID_MULTIPLE_VALUES") {
		t.Fatalf("extension VALUE mode does not reduce all matching owners together:\n%s", rendered.Query)
	}
}

func TestCorrelatedPhysicalFiltersKeepIndependentTerminologyBinds(t *testing.T) {
	plan, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Filters: []spec.TypedFilter{
			{FieldRef: "first", Selector: "code.coding[].code", FieldKind: spec.FilterCode, Repeated: true, Quantifier: spec.QuantifierAny, Operator: spec.FilterEquals,
				Values: []spec.FilterValue{{Kind: spec.FilterCode, Code: &spec.CodeValue{System: "urn:study:A", Code: "shared"}}}, Correlation: correlatedPhysicalBinding()},
			{FieldRef: "second", Selector: "code.coding[].code", FieldKind: spec.FilterCode, Repeated: true, Quantifier: spec.QuantifierAny, Operator: spec.FilterEquals,
				Values: []spec.FilterValue{{Kind: spec.FilterCode, Code: &spec.CodeValue{System: "urn:study:B", Code: "shared"}}}, Correlation: correlatedPhysicalBinding()},
		},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"correlated_system_root_filter_1": "urn:study:A",
		"correlated_code_root_filter_1":   "shared",
		"correlated_system_root_filter_2": "urn:study:B",
		"correlated_code_root_filter_2":   "shared",
	} {
		if rendered.BindVars[key] != want {
			t.Fatalf("bind %s = %#v, want %q; all binds=%#v", key, rendered.BindVars[key], want, rendered.BindVars)
		}
	}
	if !strings.Contains(rendered.Query, "@correlated_system_root_filter_1") || !strings.Contains(rendered.Query, "@correlated_system_root_filter_2") {
		t.Fatalf("independent correlated predicates missing from query:\n%s", rendered.Query)
	}
}

func TestCorrelatedPhysicalFilterRejectsUnsupportedOperator(t *testing.T) {
	_, err := lower.BuildGenericPhysicalPlanWithPolicy(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Observation", Filters: []spec.TypedFilter{{
			FieldRef: "not_equals", Selector: "code.coding[].code", FieldKind: spec.FilterCode, Repeated: true, Quantifier: spec.QuantifierAny, Operator: spec.FilterNotEquals,
			Values: []spec.FilterValue{{Kind: spec.FilterCode, Code: &spec.CodeValue{System: "urn:study:A", Code: "shared"}}}, Correlation: correlatedPhysicalBinding(),
		}},
	}}, semantic.ExecutionContext{Project: "project"}, ir.DefaultPhysicalOptimizationPolicy())
	if err == nil || !strings.Contains(err.Error(), "supports only EQUALS") {
		t.Fatalf("unsupported correlated operator error = %v", err)
	}
}

func mustSelector(t *testing.T, path string) spec.Selector {
	t.Helper()
	selector, err := spec.ParseSelector(path)
	if err != nil {
		t.Fatal(err)
	}
	return selector
}
