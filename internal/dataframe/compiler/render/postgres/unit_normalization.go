package postgres

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/dataframe/unit"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const (
	unitGuardIdentityUnknown = "UNIT_IDENTITY_UNKNOWN"
	unitGuardRuleAmbiguous   = "UNIT_RULE_AMBIGUOUS"
)

const unitAddPassUpper = "179769313486231251480077898176534491778436481631674425806715807129950620372469748806686859598516352578265980131802208169442826256090225147178389403728551798718562185003524030875337817916681086138040437410377184717767504109875384015236135739992861126057954874517079522029393077059952637103891167171142536396800"

type unitProjectionFacts struct {
	sourceVariable string
	resourceType   string
	valueSteps     []documentPathStep
	systemSteps    []documentPathStep
	codeSteps      []documentPathStep
	rules          []unit.UnitConversionRule
	target         unit.UnitIdentity
	dimension      unit.UnitDimension
}

func classifyUnitNormalizedProjection(projection ir.PhysicalProjection) (classifiedExtractionProjection, error) {
	expression := projection.Expression
	extract := expression.Extract
	if expression.Cardinality != ir.PhysicalArrayCardinality || expression.NullBehavior != ir.PhysicalEmptyOnNull {
		return classifiedExtractionProjection{}, fmt.Errorf(
			"%w: projection %q: unit-normalized extraction is registered for the exact ARRAY-cardinality EMPTY_ON_NULL posture lower/root_projection.go:334 emits; got cardinality %q, null behavior %q",
			ErrUnsupportedPostgresPlan, projection.Name, string(expression.Cardinality), string(expression.NullBehavior))
	}
	if projection.Presence != nil {
		return classifiedExtractionProjection{}, fmt.Errorf(
			"%w: projection %q carries presence metadata on a unit-normalized expression; presence proofs pair with selector extractions only",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if len(extract.Fallbacks) != 0 {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: ordered fallback selectors are a separate capability", ErrUnsupportedPostgresPlan, projection.Name)
	}
	if extract.Prepared != nil {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: prepared-set selector references are not implemented for PostgreSQL", ErrUnsupportedPostgresPlan, projection.Name)
	}
	if extract.Distinct {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: DISTINCT unit-normalized projection has no producer (the lowering never sets it) and no registered SORTED_UNIQUE reading", ErrUnsupportedPostgresPlan, projection.Name)
	}
	if extract.Selector.Filter != nil {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: selector CONTAINS filters are not implemented for PostgreSQL", ErrUnsupportedPostgresPlan, projection.Name)
	}
	switch extract.ExecutionMode {
	case "", ir.PhysicalSelectorGeneric, ir.PhysicalSelectorDirectScalar:
	case ir.PhysicalSelectorConditionalArray:
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: CONDITIONAL_ARRAY selector mode requires repeated-step rendering, which is not registered for PostgreSQL", ErrUnsupportedPostgresPlan, projection.Name)
	default:
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: unknown selector execution mode %q", ErrUnsupportedPostgresPlan, projection.Name, string(extract.ExecutionMode))
	}
	if extract.Source.BindKey != "" {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: the selector source must be a row document, not a bind value", ErrUnsupportedPostgresPlan, projection.Name)
	}
	if len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" {
		return classifiedExtractionProjection{}, fmt.Errorf(`%w: projection %q: the selector source must be the row document at ["payload"], the exact source shape lowering emits (lower/root_projection.go); got %v`,
			ErrUnsupportedPostgresPlan, projection.Name, extract.Source.Path)
	}
	if strings.TrimSpace(extract.ResourceType) == "" {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: unit-normalized extraction must name the schema resource type it was validated against", ErrUnsupportedPostgresPlan, projection.Name)
	}

	normalization := extract.UnitNormalization
	if !normalization.Target.Valid() || len(normalization.Rules) == 0 {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: unit normalization target and approved rules are required", ErrUnsupportedPostgresPlan, projection.Name)
	}
	if normalization.OriginalValue.CanonicalPath() != extract.Selector.CanonicalPath() {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: unit normalization original value must match extract selector", ErrUnsupportedPostgresPlan, projection.Name)
	}
	valuePath := normalization.OriginalValue.CanonicalPath()
	if !strings.HasSuffix(valuePath, ".value") {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: unit normalization original value must select a FHIR Quantity value", ErrUnsupportedPostgresPlan, projection.Name)
	}
	quantityPath := strings.TrimSuffix(valuePath, ".value")
	if unitSelectorIterates(normalization.OriginalValue) || unitSelectorIterates(normalization.SourceSystem) || unitSelectorIterates(normalization.SourceCode) {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: unit normalization requires one scalar or indexed Quantity", ErrUnsupportedPostgresPlan, projection.Name)
	}
	if normalization.SourceSystem.CanonicalPath() != quantityPath+".system" || normalization.SourceCode.CanonicalPath() != quantityPath+".code" {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: unit normalization system and code selectors must be siblings of the original Quantity value", ErrUnsupportedPostgresPlan, projection.Name)
	}
	quantity, ok := fhirschema.ResolveFieldSemantics(extract.ResourceType, quantityPath)
	if !ok || quantity.Kind != fhirschema.FieldKindObject || quantity.Reference != "Quantity" {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: unit normalization original value must be owned by a FHIR Quantity", ErrUnsupportedPostgresPlan, projection.Name)
	}
	for index, rule := range normalization.Rules {
		if err := rule.Validate(normalization.Target, normalization.Dimension); err != nil {
			return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: unit normalization rule %d: %w", ErrUnsupportedPostgresPlan, projection.Name, index, err)
		}
	}
	references := make([]unit.UnitRuleReference, 0, len(normalization.Rules))
	for _, rule := range normalization.Rules {
		references = append(references, unit.UnitRuleReference{ID: rule.ID, Version: rule.Version})
	}
	resolvedDimension, resolvedRules, err := unit.ResolveApprovedUnitRules(references, normalization.Target)
	if err != nil {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: unit normalization rules: %w", ErrUnsupportedPostgresPlan, projection.Name, err)
	}
	if !resolvedDimension.Equal(normalization.Dimension) {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: unit normalization dimension %v does not match the pinned registry dimension %v for target %s/%s: %w",
			ErrUnsupportedPostgresPlan, projection.Name, normalization.Dimension, resolvedDimension, normalization.Target.System, normalization.Target.Code, unit.ErrUnitDimensionIncompatible)
	}
	if len(resolvedRules) != len(normalization.Rules) {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: unit normalization rule count %d does not match the resolved registry count %d",
			ErrUnsupportedPostgresPlan, projection.Name, len(normalization.Rules), len(resolvedRules))
	}
	for index := range normalization.Rules {
		if normalization.Rules[index] != resolvedRules[index] {
			return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: unit normalization rule %d (%q@%s) does not match the pinned registry entry (tampered coefficient, kind, identity, or dimension)",
				ErrUnsupportedPostgresPlan, projection.Name, index, normalization.Rules[index].ID, normalization.Rules[index].Version)
		}
	}
	if err := unitRulesPairAmbiguity(normalization.Rules); err != nil {
		return classifiedExtractionProjection{}, fmt.Errorf("%w: projection %q: %w", ErrUnsupportedPostgresPlan, projection.Name, err)
	}

	valueSteps, err := unitProjectionSelectorSteps(projection.Name, "original value", normalization.OriginalValue)
	if err != nil {
		return classifiedExtractionProjection{}, err
	}
	systemSteps, err := unitProjectionSelectorSteps(projection.Name, "system", normalization.SourceSystem)
	if err != nil {
		return classifiedExtractionProjection{}, err
	}
	codeSteps, err := unitProjectionSelectorSteps(projection.Name, "code", normalization.SourceCode)
	if err != nil {
		return classifiedExtractionProjection{}, err
	}
	return classifiedExtractionProjection{
		kind:       extractionUnitNormalized,
		projection: projection,
		unit: &unitProjectionFacts{
			sourceVariable: extract.Source.Variable,
			resourceType:   extract.ResourceType,
			valueSteps:     valueSteps,
			systemSteps:    systemSteps,
			codeSteps:      codeSteps,
			rules:          append([]unit.UnitConversionRule(nil), normalization.Rules...),
			target:         normalization.Target,
			dimension:      normalization.Dimension,
		},
	}, nil
}

func unitSelectorIterates(selector spec.Selector) bool {
	for _, step := range selector.Steps {
		if step.Iterate {
			return true
		}
	}
	return false
}

func unitProjectionSelectorSteps(projectionName, role string, selector spec.Selector) ([]documentPathStep, error) {
	if len(selector.Steps) == 0 {
		return nil, fmt.Errorf("%w: projection %q: the unit %s selector is empty", ErrUnsupportedPostgresPlan, projectionName, role)
	}
	steps := make([]documentPathStep, 0, len(selector.Steps))
	for index, step := range selector.Steps {
		switch {
		case step.Field == "":
			return nil, fmt.Errorf("%w: projection %q: unit %s selector step %d has no property", ErrUnsupportedPostgresPlan, projectionName, role, index)
		case step.Iterate:
			return nil, fmt.Errorf("%w: projection %q: unit %s selector repeated ([]) step %d requires array rendering, which is not registered (the enabling capability is pg-sql-16)", ErrUnsupportedPostgresPlan, projectionName, role, index)
		}
		if !payloadPathSegmentPattern.MatchString(step.Field) {
			return nil, fmt.Errorf("%w: projection %q: unit %s selector step %q is not renderer-safe", ErrUnsupportedPostgresPlan, projectionName, role, step.Field)
		}
		projectionStep := documentPathStep{field: step.Field}
		if step.Index != nil {
			projectionStep.indexed = true
			projectionStep.index = *step.Index
		}
		steps = append(steps, projectionStep)
	}
	return steps, nil
}

func unitRulesPairAmbiguity(rules []unit.UnitConversionRule) error {
	seen := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		key := rule.Source.System + "\x00" + rule.Source.Code
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("unit rule set carries two rules for source %s/%s; the registered identity match admits at most one rule per exact source pair (ambiguity must never resolve by bind-array order)",
				rule.Source.System, rule.Source.Code)
		}
		seen[key] = struct{}{}
	}
	return nil
}

type unitRulesBinding struct {
	ID           string  `json:"id"`
	Version      string  `json:"version"`
	Kind         string  `json:"kind"`
	SourceSystem string  `json:"source_system"`
	SourceCode   string  `json:"source_code"`
	TargetSystem string  `json:"target_system"`
	TargetCode   string  `json:"target_code"`
	Scale        float64 `json:"scale"`
	Offset       float64 `json:"offset"`
}

func unitRulesBindingJSON(rules []unit.UnitConversionRule) (json.RawMessage, error) {
	bindings := make([]unitRulesBinding, 0, len(rules))
	for _, rule := range rules {
		if math.IsNaN(rule.Scale) || math.IsInf(rule.Scale, 0) || math.IsNaN(rule.Offset) || math.IsInf(rule.Offset, 0) {
			return nil, fmt.Errorf("%w: unit rule %q carries a non-finite scale or offset (the unknown call/type pair gate before execution)", ErrUnsupportedPostgresPlan, rule.ID)
		}
		bindings = append(bindings, unitRulesBinding{
			ID: rule.ID, Version: rule.Version, Kind: string(rule.Kind),
			SourceSystem: rule.Source.System, SourceCode: rule.Source.Code,
			TargetSystem: rule.Target.System, TargetCode: rule.Target.Code,
			Scale: rule.Scale, Offset: rule.Offset,
		})
	}
	raw, err := json.Marshal(bindings)
	if err != nil {
		return nil, fmt.Errorf("%w: unit rule set is not JSON-encodable: %v", ErrUnsupportedPostgresPlan, err)
	}
	return json.RawMessage(raw), nil
}

func unitValueListExpression(document string) string {
	return "(CASE WHEN " + document + " IS NULL OR jsonb_typeof(" + document + ") = 'null' THEN '[]'::jsonb" +
		" WHEN jsonb_typeof(" + document + ") = 'array' THEN " + document +
		" ELSE jsonb_build_array(" + document + ") END)"
}

func (r *planRenderer) renderUnitNormalizedProjection(projection classifiedExtractionProjection) (string, error) {
	facts := projection.unit
	scope, ok := r.rows[facts.sourceVariable]
	if !ok || scope.alias == "" || scope.derivedValue != "" || scope.derivedJSON {
		return "", fmt.Errorf("%w: projection %q: unit normalization extracts from variable %q, which does not bind a readable stored row",
			ErrUnsupportedPostgresPlan, projection.projection.Name, facts.sourceVariable)
	}
	if facts.resourceType != scope.resourceType {
		return "", fmt.Errorf("%w: projection %q: unit normalization validates selectors against resource type %q, but the row is scanned as %q",
			ErrUnsupportedPostgresPlan, projection.projection.Name, facts.resourceType, scope.resourceType)
	}
	rulesRaw, err := unitRulesBindingJSON(facts.rules)
	if err != nil {
		return "", fmt.Errorf("projection %q: %w", projection.projection.Name, err)
	}
	rulesIndex := r.addParameter(command.PostgreSQLParameter{
		Type:  command.PostgreSQLParameterJSONB,
		Value: append(json.RawMessage(nil), rulesRaw...),
	}, "unit_rules:"+string(rulesRaw))

	valueDocument, err := r.documentPathStepsExpression(scope.alias, facts.valueSteps)
	if err != nil {
		return "", fmt.Errorf("projection %q: unit original value selector: %w", projection.projection.Name, err)
	}
	systemDocument, err := r.documentPathStepsExpression(scope.alias, facts.systemSteps)
	if err != nil {
		return "", fmt.Errorf("projection %q: unit system selector: %w", projection.projection.Name, err)
	}
	codeDocument, err := r.documentPathStepsExpression(scope.alias, facts.codeSteps)
	if err != nil {
		return "", fmt.Errorf("projection %q: unit code selector: %w", projection.projection.Name, err)
	}

	valueList := unitValueListExpression(valueDocument)
	systemFirst := "(" + unitValueListExpression(systemDocument) + ") -> 0"
	codeFirst := "(" + unitValueListExpression(codeDocument) + ") -> 0"

	value := "loom_un_value.v"
	coerced := callNumericCoercion(value)
	clamped := callBandOperandClamp(coerced)
	scaleNumeric := "((loom_un_rule.r ->> 'scale')::numeric)"
	offsetNumeric := "((loom_un_rule.r ->> 'offset')::numeric)"
	scaleDouble := "((loom_un_rule.r ->> 'scale')::double precision)"
	offsetDouble := "((loom_un_rule.r ->> 'offset')::double precision)"
	productMagnitude := "abs((" + callBandOperandClamp(coerced) + " * " + callBandOperandClamp(scaleNumeric) + "))"
	sumMagnitude := "abs((" + callBandOperandClamp(coerced) + " * " + callBandOperandClamp(scaleNumeric) + " + " + callBandOperandClamp(offsetNumeric) + "))"

	converted := "(CASE" +
		" WHEN " + coerced + " IS NULL THEN 'null'::jsonb" +
		" WHEN abs(" + clamped + ") >= '" + callDoubleOverflowBound + "'::numeric THEN 'null'::jsonb" +
		" WHEN " + productMagnitude + " < '" + callDoubleMulDivPassUpper + "'::numeric" +
		" AND " + sumMagnitude + " < '" + unitAddPassUpper + "'::numeric" +
		" THEN to_jsonb(" + callDoubleOperand(coerced) + " * " + scaleDouble + " + " + offsetDouble + ")" +
		" WHEN " + productMagnitude + " < '" + callDoubleSubnormalFloor + "'::numeric" +
		" THEN to_jsonb(0::double precision + " + offsetDouble + ")" +
		" ELSE 'null'::jsonb END)"

	fragment := "(SELECT COALESCE(jsonb_agg(" + converted + " ORDER BY loom_un_value.v_ord), '[]'::jsonb)" +
		" FROM (SELECT loom_un_item.v, loom_un_item.ord AS v_ord" +
		" FROM jsonb_array_elements(" + valueList + ") WITH ORDINALITY AS loom_un_item(v, ord)" +
		" WHERE loom_un_item.v IS NOT NULL AND loom_un_item.v <> 'null'::jsonb) AS loom_un_value" +
		" CROSS JOIN LATERAL (SELECT" +
		" CASE WHEN jsonb_typeof(" + systemFirst + ") = 'string' THEN " + systemFirst + " #>> '{}' END AS system_text," +
		" CASE WHEN jsonb_typeof(" + codeFirst + ") = 'string' THEN " + codeFirst + " #>> '{}' END AS code_text) AS loom_un_identity" +
		" CROSS JOIN LATERAL (SELECT CASE" +
		" WHEN COUNT(*) = 0 THEN to_jsonb(" + guardCast(unitGuardIdentityUnknown) + ")" +
		" WHEN COUNT(*) > 1 THEN to_jsonb(" + guardCast(unitGuardRuleAmbiguous) + ")" +
		" ELSE MIN(loom_un_candidate.r::text)::jsonb END AS r" +
		" FROM jsonb_array_elements($" + strconv.Itoa(rulesIndex) + "::jsonb) AS loom_un_candidate(r)" +
		" WHERE loom_un_candidate.r ->> 'source_system' = loom_un_identity.system_text" +
		" AND loom_un_candidate.r ->> 'source_code' = loom_un_identity.code_text) AS loom_un_rule)"
	if err := checkCallFragment(fragment); err != nil {
		return "", fmt.Errorf("projection %q: %w", projection.projection.Name, err)
	}
	return "(" + fragment + ")", nil
}
