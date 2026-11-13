package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/dataframe/unit"
)

func sql21CentimeterFacts(t *testing.T) (unit.UnitIdentity, unit.UnitDimension, []unit.UnitConversionRule) {
	t.Helper()
	policy, err := unit.ResolveApprovedUnitPolicy("to-centimeters", "1")
	if err != nil {
		t.Fatalf("ResolveApprovedUnitPolicy(to-centimeters): %v", err)
	}
	dimension, rules, err := unit.ResolveApprovedUnitRules(policy.Rules, policy.Target)
	if err != nil {
		t.Fatalf("ResolveApprovedUnitRules(to-centimeters): %v", err)
	}
	return policy.Target, dimension, rules
}

func sql21FahrenheitFacts(t *testing.T) (unit.UnitIdentity, unit.UnitDimension, []unit.UnitConversionRule) {
	t.Helper()
	policy, err := unit.ResolveApprovedUnitPolicy("to-fahrenheit", "1")
	if err != nil {
		t.Fatalf("ResolveApprovedUnitPolicy(to-fahrenheit): %v", err)
	}
	dimension, rules, err := unit.ResolveApprovedUnitRules(policy.Rules, policy.Target)
	if err != nil {
		t.Fatalf("ResolveApprovedUnitRules(to-fahrenheit): %v", err)
	}
	return policy.Target, dimension, rules
}

func sql21Steps(fields ...string) spec.Selector {
	steps := make([]spec.SelectorStep, 0, len(fields))
	for _, field := range fields {
		steps = append(steps, spec.SelectorStep{Field: field})
	}
	return spec.Selector{Steps: steps}
}

func sql21UnitExtract(t *testing.T, mutate func(*ir.PhysicalExtract)) *ir.PhysicalExtract {
	t.Helper()
	target, dimension, rules := sql21CentimeterFacts(t)
	valueSelector := sql21Steps("valueQuantity", "value")
	extract := &ir.PhysicalExtract{
		Source:        ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
		ResourceType:  "Observation",
		Selector:      valueSelector,
		ExecutionMode: ir.PhysicalSelectorGeneric,
		UnitNormalization: &ir.PhysicalUnitNormalization{
			OriginalValue: valueSelector,
			SourceSystem:  sql21Steps("valueQuantity", "system"),
			SourceCode:    sql21Steps("valueQuantity", "code"),
			Target:        target,
			Dimension:     dimension,
			Rules:         rules,
		},
	}
	if mutate != nil {
		mutate(extract)
	}
	return extract
}

func sql21UnitProjection(t *testing.T, mutate func(*ir.PhysicalExtract)) ir.PhysicalProjection {
	t.Helper()
	expression := &ir.PhysicalExpression{
		Kind:         ir.PhysicalExtractExpression,
		Cardinality:  ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalEmptyOnNull,
		Extract:      sql21UnitExtract(t, mutate),
	}
	return ir.PhysicalProjection{Name: "length_cm", Expression: expression}
}

func sql21Plan(projections ...ir.PhysicalProjection) ir.PhysicalPlan {
	return ir.PhysicalPlan{
		Version: 1,
		Engine:  ir.PhysicalEngineAQL,
		BindVars: map[string]any{
			"root_collection":                  "Observation",
			"project":                          "sql21-offline",
			"dataset_generation":               "gen-1",
			"auth_resource_paths":              []string{"FHIR/Observation"},
			"auth_resource_paths_unrestricted": false,
			"scope_allowed":                    true,
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}},
		},
	}
}

func sql21Render(t *testing.T, plan ir.PhysicalPlan) string {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding("loom_offline")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	rendered, err := RenderPhysicalPlan(plan, binding)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	if strings.Contains(rendered.SQL, "@") {
		t.Fatalf("unit projection SQL carries AQL bind syntax: %s", rendered.SQL)
	}
	return rendered.SQL
}

func sql21RenderError(t *testing.T, projection ir.PhysicalProjection) error {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding("loom_offline")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	_, err = RenderPhysicalPlan(sql21Plan(projection), binding)
	if err == nil {
		t.Fatalf("unit projection accepted, want refusal")
	}
	if !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("refusal error = %v, want ErrUnsupportedPostgresPlan", err)
	}
	return err
}

func sql21RulesParameters(t *testing.T, args []command.PostgreSQLParameter) [][]map[string]any {
	t.Helper()
	var all [][]map[string]any
	for _, argument := range args {
		raw, ok := argument.Value.(json.RawMessage)
		if !ok {
			continue
		}
		if argument.Type != command.PostgreSQLParameterJSONB || !strings.Contains(string(raw), "source_system") {
			continue
		}
		var rules []map[string]any
		if err := json.Unmarshal(raw, &rules); err != nil {
			t.Fatalf("rules parameter is not valid JSON: %v", err)
		}
		all = append(all, rules)
	}
	return all
}

func sql21RulesParameter(t *testing.T, args []command.PostgreSQLParameter) []map[string]any {
	t.Helper()
	all := sql21RulesParameters(t, args)
	if len(all) != 1 {
		t.Fatalf("rules-shaped jsonb binds = %d, want exactly 1 (one renderer-minted shared bind per distinct rule set)", len(all))
	}
	return all[0]
}

func sql21RulesBindIndexes(t *testing.T, sql string) []string {
	t.Helper()
	var indexes []string
	for _, match := range sql21RulesBindPattern.FindAllStringSubmatch(sql, -1) {
		indexes = append(indexes, match[1])
	}
	return indexes
}

var sql21RulesBindPattern = regexp.MustCompile(`jsonb_array_elements\(\$(\d+)::jsonb\)`)

func TestSQL21IdenticalRuleSetsShareOneBind(t *testing.T) {
	first := sql21UnitProjection(t, nil)
	first.Name = "length_cm"
	second := sql21UnitProjection(t, nil)
	second.Name = "width_cm"
	binding, err := ir.NewPostgresStorageBinding("loom_offline")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	rendered, err := RenderPhysicalPlan(sql21Plan(first, second), binding)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	if all := sql21RulesParameters(t, rendered.Args); len(all) != 1 {
		t.Fatalf("identical rule sets minted %d rules binds, want one shared bind", len(all))
	}
	indexes := sql21RulesBindIndexes(t, rendered.SQL)
	if len(indexes) != 2 {
		t.Fatalf("rules-lateral references = %d, want one per projection:\n%s", len(indexes), rendered.SQL)
	}
	if indexes[0] != indexes[1] {
		t.Fatalf("identical rule sets read separate binds $%s and $%s:\n%s", indexes[0], indexes[1], rendered.SQL)
	}
	position, err := strconv.Atoi(indexes[0])
	if err != nil || position < 1 || position > len(rendered.Args) ||
		rendered.Args[position-1].Type != command.PostgreSQLParameterJSONB {
		t.Fatalf("shared rules bind $%s does not name a jsonb argument", indexes[0])
	}
}

func TestSQL21DistinctRuleSetsGetSeparateBinds(t *testing.T) {
	binding, err := ir.NewPostgresStorageBinding("loom_offline")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	rendered, err := RenderPhysicalPlan(sql21Plan(sql21UnitProjection(t, nil), sql21FahrenheitProjection(t)), binding)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	if all := sql21RulesParameters(t, rendered.Args); len(all) != 2 {
		t.Fatalf("distinct rule sets minted %d binds, want two", len(all))
	}
	indexes := sql21RulesBindIndexes(t, rendered.SQL)
	if len(indexes) != 2 || indexes[0] == indexes[1] {
		t.Fatalf("distinct rule sets must read two separate binds, got %v:\n%s", indexes, rendered.SQL)
	}
}

func TestSQL21UnitProjectionRendersRegisteredShape(t *testing.T) {
	binding, err := ir.NewPostgresStorageBinding("loom_offline")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	rendered, err := RenderPhysicalPlan(sql21Plan(sql21UnitProjection(t, nil)), binding)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	sql := rendered.SQL
	for _, want := range []string{
		"jsonb_array_elements(", "WITH ORDINALITY AS loom_un_item(v, ord)",
		"WHERE loom_un_item.v IS NOT NULL AND loom_un_item.v <> 'null'::jsonb",
		`loom_un_candidate.r ->> 'source_system' = loom_un_identity.system_text`,
		`loom_un_candidate.r ->> 'source_code' = loom_un_identity.code_text`,
		"UNIT_IDENTITY_UNKNOWN", "UNIT_RULE_AMBIGUOUS",
		"COALESCE(jsonb_agg(", "ORDER BY loom_un_value.v_ord), '[]'::jsonb)",
		"#> $", "::text[]",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
	dropIndex := strings.Index(sql, "loom_un_item.v IS NOT NULL")
	guardIndex := strings.Index(sql, "UNIT_IDENTITY_UNKNOWN")
	if dropIndex < 0 || guardIndex < 0 || dropIndex > guardIndex {
		t.Fatalf("null drop must precede the identity guard:\n%s", sql)
	}
	if strings.Contains(sql, `->> 'unit'`) || strings.Contains(sql, `#>> '{unit}`) {
		t.Fatalf("display label consulted:\n%s", sql)
	}
	for _, leaked := range []string{"valueQuantity", "value", "system", "code"} {
		if strings.Contains(sql, "'"+leaked+"'") {
			t.Fatalf("selector segment %q leaked into SQL text:\n%s", leaked, sql)
		}
	}
	rules := sql21RulesParameter(t, rendered.Args)
	if len(rules) != 2 {
		t.Fatalf("rules binding = %d entries, want 2 (identity + ucum:m-to-cm): %#v", len(rules), rules)
	}
	if rules[0]["id"] != "ucum:m-to-cm" || rules[0]["scale"] != float64(100) || rules[0]["offset"] != float64(0) ||
		rules[0]["source_system"] != "http://unitsofmeasure.org" || rules[0]["source_code"] != "m" {
		t.Fatalf("linear rule binding = %#v", rules[0])
	}
	if rules[1]["id"] != "identity-v1" || rules[1]["scale"] != float64(1) || rules[1]["offset"] != float64(0) ||
		rules[1]["source_system"] != "http://unitsofmeasure.org" || rules[1]["source_code"] != "cm" {
		t.Fatalf("identity rule binding = %#v", rules[1])
	}
	for _, arm := range []string{"COUNT(*) > 1", "UNIT_RULE_AMBIGUOUS", callDoubleMulDivPassUpper[:24], unitAddPassUpper[:24]} {
		if !strings.Contains(sql, arm) {
			t.Fatalf("fragment missing registered arm %q", arm)
		}
	}
	second, err := RenderPhysicalPlan(sql21Plan(sql21UnitProjection(t, nil)), binding)
	if err != nil || second.SQL != rendered.SQL || !reflect.DeepEqual(second.Args, rendered.Args) {
		t.Fatalf("unit projection render is not deterministic")
	}
}

func TestSQL21AffineProjectionRegistersOffset(t *testing.T) {
	target, dimension, rules := sql21FahrenheitFacts(t)
	valueSelector := sql21Steps("valueQuantity", "value")
	expression := &ir.PhysicalExpression{
		Kind:         ir.PhysicalExtractExpression,
		Cardinality:  ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalEmptyOnNull,
		Extract: &ir.PhysicalExtract{
			Source:        ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
			ResourceType:  "Observation",
			Selector:      valueSelector,
			ExecutionMode: ir.PhysicalSelectorGeneric,
			UnitNormalization: &ir.PhysicalUnitNormalization{
				OriginalValue: valueSelector,
				SourceSystem:  sql21Steps("valueQuantity", "system"),
				SourceCode:    sql21Steps("valueQuantity", "code"),
				Target:        target, Dimension: dimension, Rules: rules,
			},
		},
	}
	binding, err := ir.NewPostgresStorageBinding("loom_offline")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	rendered, err := RenderPhysicalPlan(sql21Plan(ir.PhysicalProjection{Name: "temp_f", Expression: expression}), binding)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	bound := sql21RulesParameter(t, rendered.Args)
	var cToF map[string]any
	for _, rule := range bound {
		if rule["id"] == "ucum:c-to-f" {
			cToF = rule
		}
	}
	if cToF == nil {
		t.Fatalf("ucum:c-to-f missing from binding %#v", bound)
	}
	if cToF["scale"] != 1.8 || cToF["offset"] != float64(32) || cToF["kind"] != "AFFINE" ||
		cToF["source_code"] != "Cel" || cToF["target_code"] != "[degF]" {
		t.Fatalf("affine binding = %#v", cToF)
	}
}

func TestSQL21IndexedQuantityRegistersBoundIndex(t *testing.T) {
	projection := sql21UnitProjection(t, func(x *ir.PhysicalExtract) {
		index := 2
		valueSelector := spec.Selector{Steps: []spec.SelectorStep{
			{Field: "valueQuantity", Index: &index}, {Field: "value"},
		}}
		systemSelector := spec.Selector{Steps: []spec.SelectorStep{
			{Field: "valueQuantity", Index: &index}, {Field: "system"},
		}}
		codeSelector := spec.Selector{Steps: []spec.SelectorStep{
			{Field: "valueQuantity", Index: &index}, {Field: "code"},
		}}
		x.Selector = valueSelector
		x.UnitNormalization.OriginalValue = valueSelector
		x.UnitNormalization.SourceSystem = systemSelector
		x.UnitNormalization.SourceCode = codeSelector
	})
	binding, err := ir.NewPostgresStorageBinding("loom_offline")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	rendered, err := RenderPhysicalPlan(sql21Plan(projection), binding)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	var boundIndexPath bool
	for _, argument := range rendered.Args {
		if argument.Type != command.PostgreSQLParameterText {
			continue
		}
		if text, ok := argument.Value.(string); ok && strings.HasPrefix(text, "{valueQuantity,2,") {
			boundIndexPath = true
		}
	}
	if !boundIndexPath {
		t.Fatalf("indexed step did not cross as a bound text[] path:\n%s", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, "[2]") || strings.Contains(rendered.SQL, "-> 2") ||
		strings.Contains(rendered.SQL, "valueQuantity,2") {
		t.Fatalf("index leaked into SQL text:\n%s", rendered.SQL)
	}
}

func TestSQL21NegativeIndexTailArm(t *testing.T) {
	projection := sql21UnitProjection(t, func(x *ir.PhysicalExtract) {
		index := -1
		valueSelector := spec.Selector{Steps: []spec.SelectorStep{
			{Field: "valueQuantity", Index: &index}, {Field: "value"},
		}}
		systemSelector := spec.Selector{Steps: []spec.SelectorStep{
			{Field: "valueQuantity", Index: &index}, {Field: "system"},
		}}
		codeSelector := spec.Selector{Steps: []spec.SelectorStep{
			{Field: "valueQuantity", Index: &index}, {Field: "code"},
		}}
		x.Selector = valueSelector
		x.UnitNormalization.OriginalValue = valueSelector
		x.UnitNormalization.SourceSystem = systemSelector
		x.UnitNormalization.SourceCode = codeSelector
	})
	binding, err := ir.NewPostgresStorageBinding("loom_offline")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	rendered, err := RenderPhysicalPlan(sql21Plan(projection), binding)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	sql := rendered.SQL
	for _, fragment := range []string{
		"CASE WHEN jsonb_typeof(",
		"THEN (CASE WHEN $",
		"< 0 THEN ",
		"-> (jsonb_array_length(",
		") + $",
		") END) ELSE ",
		"#> $",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("tail-arm SQL lost fragment %q:\n%s", fragment, sql)
		}
	}
	var boundIndex bool
	for _, argument := range rendered.Args {
		if argument.Type == command.PostgreSQLParameterBigint {
			if value, ok := argument.Value.(int64); ok && value == -1 {
				boundIndex = true
			}
		}
	}
	if !boundIndex {
		t.Fatalf("negative index did not cross as a bound bigint parameter:\n%s", sql)
	}
	if strings.Contains(sql, "[-1]") || strings.Contains(sql, "- 1)") || strings.Contains(sql, ",-1}") ||
		strings.Contains(sql, "{-1}") {
		t.Fatalf("negative index leaked into SQL text:\n%s", sql)
	}
}

func TestSQL21RefusalTable(t *testing.T) {
	for _, caseItem := range []struct {
		name    string
		mutate  func(*ir.PhysicalExtract)
		mutate2 func(*ir.PhysicalProjection)
		want    string
	}{
		{"scalar posture", func(x *ir.PhysicalExtract) {}, func(p *ir.PhysicalProjection) {
			p.Expression.Cardinality = ir.PhysicalScalarCardinality
		}, "ARRAY-cardinality EMPTY_ON_NULL"},
		{"preserve-null posture", func(x *ir.PhysicalExtract) {}, func(p *ir.PhysicalProjection) {
			p.Expression.NullBehavior = ir.PhysicalPreserveNull
		}, "ARRAY-cardinality EMPTY_ON_NULL"},
		{"distinct", func(x *ir.PhysicalExtract) { x.Distinct = true }, nil, "DISTINCT"},
		{"fallbacks", func(x *ir.PhysicalExtract) {
			x.Fallbacks = []ir.PhysicalSelectorFallback{{Source: x.Source, ResourceType: "Observation", Selector: sql21Steps("status")}}
		}, nil, "fallback selectors"},
		{"prepared", func(x *ir.PhysicalExtract) {
			x.Prepared = &ir.PhysicalPreparedReference{SetVariable: "prepared", Field: "value"}
		}, nil, "prepared-set"},
		{"selector filter", func(x *ir.PhysicalExtract) {
			x.Selector.Filter = &spec.ContainsFilter{Field: "status", Needle: "x"}
		}, nil, "CONTAINS filters"},
		{"conditional array mode", func(x *ir.PhysicalExtract) {
			x.ExecutionMode = ir.PhysicalSelectorConditionalArray
		}, nil, "CONDITIONAL_ARRAY"},
		{"unknown mode", func(x *ir.PhysicalExtract) { x.ExecutionMode = "TELEPORT" }, nil, "unknown selector execution mode"},
		{"source bind", func(x *ir.PhysicalExtract) { x.Source = ir.PhysicalValue{BindKey: "doc"} }, nil, "not a bind value"},
		{"source path", func(x *ir.PhysicalExtract) { x.Source.Path = []string{"data"} }, nil, `["payload"]`},
		{"empty resource type", func(x *ir.PhysicalExtract) { x.ResourceType = "" }, nil, "schema resource type"},
		{"original mismatch", func(x *ir.PhysicalExtract) {
			x.UnitNormalization.OriginalValue = sql21Steps("valueQuantity", "comparison")
		}, nil, "original value must match extract selector"},
		{"original not value", func(x *ir.PhysicalExtract) {
			x.Selector = sql21Steps("valueQuantity", "unit")
			x.UnitNormalization.OriginalValue = sql21Steps("valueQuantity", "unit")
		}, nil, "FHIR Quantity value"},
		{"system not sibling", func(x *ir.PhysicalExtract) {
			x.UnitNormalization.SourceSystem = sql21Steps("status")
		}, nil, "must be siblings"},
		{"code not sibling", func(x *ir.PhysicalExtract) {
			x.UnitNormalization.SourceCode = sql21Steps("status")
		}, nil, "must be siblings"},
		{"iterated value selector", func(x *ir.PhysicalExtract) {
			steps := []spec.SelectorStep{{Field: "valueQuantity", Iterate: true}, {Field: "value"}}
			x.Selector = spec.Selector{Steps: steps}
			x.UnitNormalization.OriginalValue = spec.Selector{Steps: steps}
		}, nil, "scalar or indexed Quantity"},
		{"iterated value step", func(x *ir.PhysicalExtract) {
			steps := []spec.SelectorStep{{Field: "component", Iterate: true}, {Field: "valueQuantity"}, {Field: "value"}}
			x.Selector = spec.Selector{Steps: steps}
			x.UnitNormalization.OriginalValue = spec.Selector{Steps: steps}
			x.UnitNormalization.SourceSystem = spec.Selector{Steps: []spec.SelectorStep{{Field: "component", Iterate: true}, {Field: "valueQuantity"}, {Field: "system"}}}
			x.UnitNormalization.SourceCode = spec.Selector{Steps: []spec.SelectorStep{{Field: "component", Iterate: true}, {Field: "valueQuantity"}, {Field: "code"}}}
		}, nil, "scalar or indexed Quantity"},
		{"iterated system step", func(x *ir.PhysicalExtract) {
			x.UnitNormalization.SourceSystem = spec.Selector{Steps: []spec.SelectorStep{{Field: "valueQuantity"}, {Field: "system", Iterate: true}}}
		}, nil, "scalar or indexed Quantity"},
		{"empty selector", func(x *ir.PhysicalExtract) { x.Selector = spec.Selector{} }, nil, "original value must match"},
		{"empty value steps", func(x *ir.PhysicalExtract) {
			x.Selector = sql21Steps("valueQuantity", "value")
			x.UnitNormalization.OriginalValue = spec.Selector{}
		}, nil, "original value must match"},
		{"invalid target", func(x *ir.PhysicalExtract) {
			x.UnitNormalization.Target = unit.UnitIdentity{System: "http://unitsofmeasure.org"}
		}, nil, "target and approved rules are required"},
		{"empty rules", func(x *ir.PhysicalExtract) { x.UnitNormalization.Rules = nil }, nil, "target and approved rules are required"},
		{"not a quantity owner", func(x *ir.PhysicalExtract) { x.ResourceType = "Patient" }, nil, "owned by a FHIR Quantity"},
		{"unknown rule material", func(x *ir.PhysicalExtract) {
			x.UnitNormalization.Rules[0] = unit.UnitConversionRule{ID: "ucum:not-approved", Version: "1",
				Kind: unit.UnitConversionLinear, Source: unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "m"},
				Target:    unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"},
				Dimension: unit.UnitDimension{Length: 1}, Scale: 100}
		}, nil, "is not approved"},
		{"tampered scale", func(x *ir.PhysicalExtract) {
			x.UnitNormalization.Rules[0].Scale = 101
		}, nil, "does not match the pinned registry entry"},
		{"tampered kind", func(x *ir.PhysicalExtract) {
			x.UnitNormalization.Rules[0].Kind = unit.UnitConversionAffine
		}, nil, "does not match the pinned registry entry"},
		{"tampered dimension", func(x *ir.PhysicalExtract) {
			x.UnitNormalization.Rules[0].Dimension = unit.UnitDimension{Mass: 1}
		}, nil, "UNIT_DIMENSION_INCOMPATIBLE"},
		{"tampered policy dimension", func(x *ir.PhysicalExtract) {
			x.UnitNormalization.Dimension = unit.UnitDimension{Mass: 1}
		}, nil, "UNIT_DIMENSION_INCOMPATIBLE"},
		{"duplicate rule reference", func(x *ir.PhysicalExtract) {
			x.UnitNormalization.Rules = append(x.UnitNormalization.Rules, x.UnitNormalization.Rules[1])
		}, nil, "duplicates rule reference"},
	} {
		t.Run(caseItem.name, func(t *testing.T) {
			projection := sql21UnitProjection(t, caseItem.mutate)
			if caseItem.mutate2 != nil {
				caseItem.mutate2(&projection)
			}
			err := sql21ContractError(t, projection, nil)
			if !strings.Contains(err.Error(), caseItem.want) {
				t.Fatalf("refusal %q does not name %q: %v", err.Error(), caseItem.want, err)
			}
			if caseItem.name == "unknown rule material" && !errors.Is(err, unit.ErrUnitIdentityUnknown) {
				t.Fatalf("unapproved rule material must keep the UNIT_IDENTITY_UNKNOWN sentinel: %v", err)
			}
			if strings.Contains(caseItem.want, "UNIT_DIMENSION_INCOMPATIBLE") && !errors.Is(err, unit.ErrUnitDimensionIncompatible) {
				t.Fatalf("dimension refusal must keep the UNIT_DIMENSION_INCOMPATIBLE sentinel: %v", err)
			}
		})
	}
	for _, caseItem := range []struct {
		name    string
		mutate  func(*ir.PhysicalExtract)
		mutate2 func(*ir.PhysicalProjection)
		want    string
	}{
		{"scalar posture", func(x *ir.PhysicalExtract) {}, func(p *ir.PhysicalProjection) {
			p.Expression.Cardinality = ir.PhysicalScalarCardinality
		}, "ARRAY-cardinality EMPTY_ON_NULL"},
		{"preserve-null posture", func(x *ir.PhysicalExtract) {}, func(p *ir.PhysicalProjection) {
			p.Expression.NullBehavior = ir.PhysicalPreserveNull
		}, "ARRAY-cardinality EMPTY_ON_NULL"},
		{"distinct", func(x *ir.PhysicalExtract) { x.Distinct = true }, nil, "DISTINCT"},
	} {
		t.Run("entrypoint/"+caseItem.name, func(t *testing.T) {
			projection := sql21UnitProjection(t, caseItem.mutate)
			if caseItem.mutate2 != nil {
				caseItem.mutate2(&projection)
			}
			err := sql21RenderError(t, projection)
			if !strings.Contains(err.Error(), caseItem.want) {
				t.Fatalf("entry refusal %q does not name %q: %v", err.Error(), caseItem.want, err)
			}
		})
	}
	t.Run("unknown source variable", func(t *testing.T) {
		projection := sql21UnitProjection(t, nil)
		projection.Expression.Extract.Source.Variable = "elsewhere"
		projection.Expression.Extract.UnitNormalization.OriginalValue.Steps[0].Field = "valueQuantity"
		err := sql21ContractError(t, projection, nil)
		if !strings.Contains(err.Error(), "does not bind a readable row") {
			t.Fatalf("unknown-variable refusal misdiagnosed: %v", err)
		}
	})
	t.Run("derived row source", func(t *testing.T) {
		err := sql21ContractError(t, sql21UnitProjection(t, nil), &rowScope{derivedValue: "x", derivedJSON: true})
		if !strings.Contains(err.Error(), "does not bind a readable stored row") {
			t.Fatalf("derived-row refusal misdiagnosed: %v", err)
		}
	})
	t.Run("resource type contradiction", func(t *testing.T) {
		err := sql21ContractError(t, sql21UnitProjection(t, nil), &rowScope{alias: "r0", resourceType: "Patient"})
		if !strings.Contains(err.Error(), "validates unit selectors against resource type") {
			t.Fatalf("resource-type refusal misdiagnosed: %v", err)
		}
	})
}

func sql21ContractError(t *testing.T, projection ir.PhysicalProjection, override *rowScope) error {
	t.Helper()
	resourceType := "Observation"
	if override != nil && override.resourceType != "" {
		resourceType = override.resourceType
	}
	classified, err := classifyExtractionProjections([]ir.PhysicalProjection{projection},
		func(variable string) (string, bool) {
			if variable == "root" {
				return resourceType, true
			}
			return "", false
		})
	if err != nil {
		return err
	}
	binding, err := ir.NewPostgresStorageBinding("loom_offline")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	scope := rowScope{alias: "r0", resourceType: "Observation"}
	if override != nil {
		scope = *override
		if scope.resourceType == "" {
			scope.resourceType = "Observation"
		}
	}
	r := &planRenderer{storage: binding, parameters: map[string]int{},
		rows: map[string]rowScope{"root": scope}}
	if _, err := r.renderUnitNormalizedProjection(classified[0]); err != nil {
		return err
	}
	t.Fatalf("unit projection accepted, want refusal")
	return nil
}

func TestSQL21PresenceRefusedOnUnitProjection(t *testing.T) {
	projection := sql21UnitProjection(t, nil)
	projection.Presence = &ir.PhysicalProjectionPresence{
		Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
		Paths:  [][]string{{"valueQuantity", "value"}},
	}
	err := sql21RenderError(t, projection)
	if !strings.Contains(err.Error(), "presence metadata") {
		t.Fatalf("presence on unit projection accepted or misdiagnosed: %v", err)
	}
}

func TestSQL21ClosedSliceRefusesUnitProjection(t *testing.T) {
	binding, err := ir.NewPostgresStorageBinding("loom_offline")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	_, err = RenderSelectorExtraction(sql21Plan(sql21UnitProjection(t, nil)), binding)
	if err == nil || !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("closed slice accepted a unit projection: %v", err)
	}
	if !strings.Contains(err.Error(), "read spine registration (unit_normalization.go)") {
		t.Fatalf("closed slice refusal misdiagnosed: %v", err)
	}
}

func TestSQL21OperandPathsKeepLegacyRefusal(t *testing.T) {
	unitExtract := sql21UnitExtract(t, nil)
	call := &ir.PhysicalCall{Name: "distinct_present_one", TargetKind: "DECIMAL", Args: []ir.PhysicalExpression{{
		Kind:         ir.PhysicalExtractExpression,
		Cardinality:  ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalEmptyOnNull,
		Extract:      unitExtract,
	}}}
	projection := ir.PhysicalProjection{Name: "one_value", Expression: &ir.PhysicalExpression{
		Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Call: call,
	}}
	err := sql21RenderError(t, projection)
	if !strings.Contains(err.Error(), "unit normalization") || !strings.Contains(err.Error(), "distinct_present_one") {
		t.Fatalf("distinct_present_one unit refusal drifted: %v", err)
	}

	scalarUnit := sql21UnitExtract(t, nil)
	scalarUnit.Selector = sql21Steps("valueQuantity", "value")
	callOperand := &ir.PhysicalCall{Name: "add", Args: []ir.PhysicalExpression{
		{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Value: &ir.PhysicalValue{Variable: "root", Path: []string{"meta", "versionId"}}},
		{Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Extract: scalarUnit},
	}}
	operandProjection := ir.PhysicalProjection{Name: "sum", Expression: &ir.PhysicalExpression{
		Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Call: callOperand,
	}}
	const legacy = "unit-normalized extraction is not implemented for PostgreSQL"
	err = sql21RenderError(t, operandProjection)
	if !strings.Contains(err.Error(), legacy) {
		t.Fatalf("operand-path unit refusal must stay byte-identical to %q, got: %v", legacy, err)
	}
}

func TestSQL21UnitAddPassUpperDoubleDerivation(t *testing.T) {
	dblMax := new(big.Float).SetPrec(2048)
	twoPow971 := new(big.Float).SetPrec(2048).SetInt(new(big.Int).Lsh(big.NewInt(1), 971))
	mantissa := new(big.Int).Lsh(big.NewInt(1), 53)
	mantissa.Sub(mantissa, big.NewInt(1))
	dblMax.SetInt(mantissa)
	dblMax.Mul(dblMax, twoPow971)
	power975 := new(big.Float).SetPrec(2048).SetInt(new(big.Int).Lsh(big.NewInt(1), 975))
	want := new(big.Float).SetPrec(2048).Sub(dblMax, power975)
	if want.Text('f', 0) != unitAddPassUpper {
		t.Fatalf("unitAddPassUpper drifted from the big.Float DBL_MAX - 2^975 expansion")
	}
	got, _, err := big.ParseFloat(unitAddPassUpper, 10, 2048, big.ToNearestEven)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	overBound, _, _ := big.ParseFloat(callDoubleOverflowBound, 10, 2048, big.ToNearestEven)
	if got.Cmp(overBound) >= 0 {
		t.Fatalf("unitAddPassUpper must sit below the overflow tie")
	}
	higher := new(big.Float).SetPrec(2048).Sub(dblMax, new(big.Float).SetPrec(2048).SetInt(new(big.Int).Lsh(big.NewInt(1), 974)))
	lower := new(big.Float).SetPrec(2048).Sub(dblMax, new(big.Float).SetPrec(2048).SetInt(new(big.Int).Lsh(big.NewInt(1), 976)))
	if got.Cmp(higher) >= 0 || got.Cmp(lower) <= 0 {
		t.Fatalf("unitAddPassUpper outside the registered margin band")
	}
}

func TestSQL21SelectorStepValidatorDirect(t *testing.T) {
	if _, err := unitProjectionSelectorSteps("p", "code",
		spec.Selector{Steps: []spec.SelectorStep{{Field: "valueQuantity"}, {Field: "c de"}}}); err == nil ||
		!strings.Contains(err.Error(), "not renderer-safe") {
		t.Fatalf("unsafe segment accepted: %v", err)
	}
	if _, err := unitProjectionSelectorSteps("p", "code", spec.Selector{}); err == nil ||
		!strings.Contains(err.Error(), "selector is empty") {
		t.Fatalf("empty selector accepted: %v", err)
	}
	if _, err := unitProjectionSelectorSteps("p", "code",
		spec.Selector{Steps: []spec.SelectorStep{{Field: "valueQuantity"}, {Field: "code"}}}); err != nil {
		t.Fatalf("legal steps refused: %v", err)
	}
}

func TestSQL21RulesBindingDeterminismAndFinitenessGate(t *testing.T) {
	_, _, rules := sql21CentimeterFacts(t)
	first, err := unitRulesBindingJSON(rules)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	second, err := unitRulesBindingJSON(rules)
	if err != nil || string(first) != string(second) {
		t.Fatalf("rule binding is not deterministic")
	}
	tampered := append([]unit.UnitConversionRule(nil), rules...)
	tampered[0].Scale = math.Inf(1)
	_, tamperErr := unitRulesBindingJSON(tampered)
	if tamperErr == nil || !strings.Contains(tamperErr.Error(), "non-finite scale or offset") {
		t.Fatalf("non-finite coefficient accepted: %v", tamperErr)
	}
	if !errors.Is(tamperErr, ErrUnsupportedPostgresPlan) {
		t.Fatalf("non-finite refusal must be typed: %v", tamperErr)
	}
}

var (
	sql21FixturesMu      sync.Mutex
	sql21TrackedFixtures = map[string]bool{}
)

func runVerifyNamespaceDeltaAudit(prefix string, mu *sync.Mutex, tracked map[string]bool, inner func() int) int {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return inner()
	}
	escaped := strings.ReplaceAll(prefix, `_`, `\_`)
	like := "'" + escaped + "%' ESCAPE '\\'"
	snapshot := func(ctx context.Context) ([]string, error) {
		pool, err := pgxpool.New(ctx, raw)
		if err != nil {
			return nil, err
		}
		defer pool.Close()
		queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var names []string
		for _, query := range []string{
			`SELECT datname FROM pg_database WHERE datname LIKE ` + like,
			`SELECT nspname FROM pg_namespace WHERE nspname LIKE ` + like,
		} {
			rows, err := pool.Query(queryCtx, query)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					rows.Close()
					return nil, err
				}
				names = append(names, name)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			rows.Close()
		}
		return names, nil
	}
	beforeList, err := snapshot(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s delta audit: snapshot before run: %v\n", prefix, err)
		return 1
	}
	before := make(map[string]bool, len(beforeList))
	for _, name := range beforeList {
		before[name] = true
	}
	code := inner()
	after, err := snapshot(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s delta audit: snapshot after run: %v\n", prefix, err)
		code = 1
	}
	mu.Lock()
	defer mu.Unlock()
	for _, name := range after {
		if before[name] {
			continue
		}
		if tracked[name] {
			fmt.Fprintf(os.Stderr, "%s delta audit: %q was created and tracked by this run and still survives\n", prefix, name)
			code = 1
			continue
		}
		fmt.Fprintf(os.Stderr, "%s delta audit: note — %q was born during this run but is not owned by it (sibling lane?)\n", prefix, name)
	}
	return code
}

func sql21RunDeltaAudit(inner func() int) int {
	return runVerifyNamespaceDeltaAudit("verify_sql21_", &sql21FixturesMu, sql21TrackedFixtures, inner)
}
