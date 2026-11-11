package postgres

// This file registers unit-normalized Quantity extraction as an
// implemented, closed PostgreSQL RETURN projection (pg-sql-21). It is the
// renderer-facing capability registration point for
// ir.PhysicalExtract.UnitNormalization (ir/physical_values.go:159-166: the
// compiler-resolved, pinned conversion set carried beside one selector
// extraction). Conversions are keyed by exact system/code identity and the
// immutable rule set — FHIR Quantity.unit display text is deliberately
// never read here. Every accepted projection is built by this file; the
// classifier in selectors.go only recognizes the kind and proves row
// readability, and render.go remains the package's single statement
// builder. No second SQL builder exists: this file emits a jsonb-valued
// expression fragment and binds its parameters; render.go composes it into
// the SELECT list.
//
// AQL parity anchors. The AQL producer of the registered shape is
// render/aql/selectors.go renderUnitNormalizedExtract (selectors.go:164-239)
// dispatched from renderExtract (selectors.go:18-20), pinned by
// render/aql/call_test.go
// TestRenderUnitNormalizationConvertsPerMeasurementAndSkipsNullBeforeIdentityAssert
// (call_test.go:90-136). The
// plan producer is lower/root_projection.go:321-333
// (physicalAggregateExpression, the repository's only
// PhysicalUnitNormalization emitter), fed by
// semantic/recipe_rich_shaping.go:439-490 against the compiler-owned
// immutable registry in internal/dataframe/unit/unit.go
// (approvedUnitRules/approvedUnitPolicies/approvedUnitDimensions). Doc
// decisions follow the same official ArangoDB 3.9 sources as calls.go:
// operators.md 259-277 (invalid arithmetic such as Infinity or a
// non-numeric operand yields null without raising), fundamentals-
// data-types.md 72-73 (values are IEEE-754 doubles and arithmetic is
// hardware IEEE — the conversion therefore runs on real double-precision
// * and + inside arms whose magnitudes were pre-proven safe by raise-free
// numeric-domain band tests, the pg-sql-10 discipline), and
// functions-miscellaneous.md 504-517 (ASSERT raises with its message —
// reproduced through the population.go guardCast barrier with the same
// named tag).
//
// Registered surface (one RETURN projection kind; extractionUnitNormalized):
//
//	exact payload posture   ARRAY-cardinality EMPTY_ON_NULL EXTRACT with a
//	                        non-nil UnitNormalization — the single posture
//	                        lower/root_projection.go:334 emits. Every other
//	                        posture is a typed refusal: no producer emits
//	                        it, and ir.Validate keeps the payload legal
//	                        only inside aggregate expressions.
//	closed selector         the main sequence's closed direct-property
//	                        contract plus REGISTERED non-negative indexed
//	                        steps (semantic sanctions "select one indexed
//	                        Quantity first", recipe_rich_shaping.go:471;
//	                        #> text[] reads out-of-range and
//	                        non-array indexes as SQL NULL = AQL's missing
//	                        reads null). Repeated ([]) steps keep the
//	                        closed-slice array refusal; negative indexes
//	                        refuse as a named capability gap (ArangoDB
//	                        renders [n] verbatim, render/aql/selector.go:34,
//	                        47, so [-1] carries tail-index semantics with
//	                        no registered PostgreSQL equivalence — the
//	                        enabling array-rendering capability is
//	                        pg-sql-16, EXPAND/repeated items over typed
//	                        intermediate rows).
//	per-measurement rule    one rule per row: the row's system/code
//	identity                FIRST-sibling strings (ordinality 1 of the
//	                        one-item-normalized sibling list — array
//	                        sources take their first item, scalars their
//	                        own value, matching FIRST(FLATTEN(...))) match
//	                        exactly one bound rule on (source_system,
//	                        source_code). Only stored JSON strings are
//	                        identities: typeof-guarded, no trim, no case
//	                        fold, and Quantity.unit display text is never
//	                        consulted. Zero matches raises SQLSTATE 22P02
//	                        carrying UNIT_IDENTITY_UNKNOWN — byte-identical
//	                        to the AQL ASSERT message.
//	rule-set tie cell       the identity match is a function from an exact
//	                        source pair to at most one rule. A rule set
//	                        carrying two rules for one source pair is
//	                        REFUSED before any SQL exists
//	                        (unitRulesPairAmbiguity) — never resolved by
//	                        bind-array order. Registered divergence: AQL's
//	                        FIRST over candidate bindings silently takes
//	                        array order (render/aql/selectors.go:213) — a
//	                        row-order ambiguity the PostgreSQL register
//	                        refuses outright. The execution-time COUNT > 1
//	                        UNIT_RULE_AMBIGUOUS arm remains as defense in
//	                        depth (also never row order); with registry
//	                        re-verification it is unreachable from any
//	                        renderable plan, and the shape golden pins its
//	                        text. The deterministic side is pinned live:
//	                        every fixture identity resolves exactly one
//	                        rule, with mutant M6 as the alive control.
//	null drop before the    null-valued measurements (missing path, JSON
//	identity guard        null, null items inside a stored value array)
//	                        drop before the identity guard runs, exactly as
//	                        AQL's FILTER value != null precedes ASSERT
//	                        (call_test.go:120-123 pin). Pinned live:
//	                        violating rows PLAIN under EXPLAIN and
//	                        execute clean; TestSQL21LiveNullValuesSkip-
//	                        BeforeIdentityAssert is the named victim of
//	                        mutant M1.
//	conversion              output = input*scale + offset on IEEE doubles,
//	                        scale/offset read from one renderer-minted
//	                        jsonb rule binding (shortest-round-trip
//	                        float64 texts; NaN/Inf are refused before any
//	                        SQL exists). Non-numeric values (booleans,
//	                        arrays, objects, strings outside the pg-sql-10
//	                        decimal tiers) coerce to NULL and read a jsonb
//	                        null element — AQL's invalid-arithmetic null
//	                        (operators.md 259-262) — while still requiring
//	                        a valid identity; decimal-form strings coerce
//	                        through callNumericCoercion's registered tiers.
//	                        IEEE overflow (operand magnitude at or beyond
//	                        callDoubleOverflowBound, or an unregistered
//	                        arm) reads a jsonb null element; products
//	                        below callDoubleSubnormalFloor read the
//	                        registered 0+offset. Every double arm runs
//	                        only where the clamped numeric band tests
//	                        (callBandOperandClamp) prove the executed
//	                        operation cannot raise 22003; the addition
//	                        pass boundary unitAddPassUpper = DBL_MAX −
//	                        2^975 leaves margin ≥ 2^974 above the
//	                        documented tested-vs-executed error budget
//	                        (≈ 2^973 product + ½ ulp sum at the top), and
//	                        is pinned by two independent exact derivations
//	                        (Go big.Float and Python decimal; see
//	                        TestUnitAddPassUpperDoubleDerivation).
//	result                  COALESCE(jsonb_agg(converted ORDER BY
//	                        ordinality), '[]'::jsonb): a jsonb array per
//	                        row in document order, EMPTY_ON_NULL posture,
//	                        null elements preserved (never skipped by
//	                        jsonb_agg — every arm is jsonb-valued).
//
// Refusals (all ErrUnsupportedPostgresPlan, never an AQL fallback, byte-
// identical pre-existing messages elsewhere): every other UnitNormalization
// consumer keeps its legacy refusal untouched — CALL operands
// (calls.go:1667 and the selectors.go:416 operand gate, which this
// classifier intercepts only for the registered posture on the projection
// path), objects.go:405, predicates.go:704, traversal.go:803, unnest.go:438
// — and aggregate-expression projections keep the selectors.go:352 default
// refusal. Production lowering reaches the registered arm only through
// pg-sql-13 ("Render Group keys and aggregate reducers", named at
// coded_group.go:187): the repository's sole unit-normalization producer
// emits the extract exclusively inside PhysicalAggregateExpression
// (root_projection.go:379), whose projection kind the classifier still
// refuses, so this registration is the canonical hand-off shape for that
// ticket — the pg-sql-08 hop-only RELATED posture (sets.go:82-95). The
// production-compiled acceptance legs (unit_normalization_production_
// test.go P-1/P-2/P-3) attest exactly that: a real recipe through the real
// lowering stays refused at the aggregate gate, and the lowering's own
// byte-identical EXTRACT hoisted into the registered projection renders
// and executes on live PostgreSQL 18.
//
// Defense in depth: ir.Validate pins the normalization contract upstream
// (ir/physical_validation_refs.go:59-93); this file re-reads it
// authoritatively — sibling-pairing, Quantity ownership through the
// generated schema, per-rule unit.UnitConversionRule.Validate, and
// registry re-verification through unit.ResolveApprovedUnitRules (every
// bound rule must equal the immutable registry entry for its id/version,
// so a hand-authored plan cannot execute its own coefficients) — so a
// tampered rule set refuses with the same named diagnostics the ir test
// expects (UNIT_DIMENSION_INCOMPATIBLE via unit.ErrUnitDimensionIncom-
// patible; UNIT_IDENTITY_UNKNOWN via unit.ErrUnitIdentityUnknown for
// non-approvable rule material) before
// any SQL is assembled (the ir witness for these two sentinels is
// ir/unit_validation_test.go:41-44).
//
// Parameter discipline never changes: the whole pinned rule set crosses as
// ONE ordered jsonb $n (renderer-marshaled from the validated rules,
// deduplicated by its canonical text); selector property names cross as
// bound text and array positions as canonical decimal path elements
// null_semantics.go's extended step emitter — the package's single
// path-binding authority; guard tags are renderer-owned structural text
// inside population.go guardCast behind the constant-folding barrier; and
// no plan string ever becomes SQL text.

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

// unitGuardTags are the canonical ASSERT tags the AQL producer raises with
// (render/aql/selectors.go:214 spells "UNIT_IDENTITY_UNKNOWN" literally).
// They are renderer-owned structural constants, so they take the plain
// population.go guardCast (tag embedded in the 22P02 message text).
// UNIT_RULE_AMBIGUOUS has no AQL counterpart — the AQL FIRST-over-bindings
// reading silently resolves by array order; the PostgreSQL register
// refuses the ambiguity instead (registered divergence cell above).
const (
	unitGuardIdentityUnknown = "UNIT_IDENTITY_UNKNOWN"
	unitGuardRuleAmbiguous   = "UNIT_RULE_AMBIGUOUS"
)

// unitAddPassUpper is DBL_MAX − 2^975 in its exact decimal expansion: the
// strict pass boundary for the executed (double product + offset) sum.
// Derivation (mirrors the callDouble* margins in calls.go): each double
// operand parses within half an ulp of its text, so the executed product
// deviates from the tested numeric product by at most ~2^972 + ½ ulp
// (≈ 2^973) at the top of the range; the executed sum adds at most ½ ulp
// more. A tested magnitude strictly below DBL_MAX − 2^975 therefore keeps
// the executed sum strictly below DBL_MAX − 2^974, one margin doubling
// below the round-half-even overflow tie at DBL_MAX + 2^970
// (callDoubleOverflowBound), so the addition can never raise 22003. The
// constant is pinned by two independent exact derivations — big.Float in
// TestUnitAddPassUpperDoubleDerivation and Python's decimal module (banked
// in the plan artifact) — because the callDoubleNormalFloor lesson (a
// 17-digit rounded DBL_MIN spelling silently admits the maximum
// subnormal) shows single-derivation pins are how band bugs survive.
const unitAddPassUpper = "179769313486231251480077898176534491778436481631674425806715807129950620372469748806686859598516352578265980131802208169442826256090225147178389403728551798718562185003524030875337817916681086138040437410377184717767504109875384015236135739992861126057954874517079522029393077059952637103891167171142536396800"

// unitProjectionFacts are the plan-independent contract facts the
// classifier proves once and the render walk consumes without re-deriving.
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

// classifyUnitNormalizedProjection enforces the structural half of the
// registered projection contract (everything checkable without plan
// context) and returns the fully classified projection. It runs BEFORE
// validateSelectorExtraction, which keeps its legacy unit refusal for the
// operand paths (CALL/required-match/etc.) that do not register here.
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

	// Defense-in-depth re-read of the ir contract
	// (ir/physical_validation_refs.go:59-93). Every refusal keeps the
	// upstream sentinel wrapping so the named diagnostics survive %w
	// classification.
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
	// Registry re-verification: every rule must equal the immutable
	// registry entry for its (id, version) under the declared target
	// (identity-v1 is the target-relative synthesis
	// unit.go:207-208 produces). A plan cannot make arbitrary conversion
	// coefficients executable by picking its own numbers — the pg-sql-10
	// "callers cannot supply executable coefficients" posture carried
	// into the unit domain. Unapproved rule material refuses with the
	// upstream UNIT_IDENTITY_UNKNOWN sentinel; coefficient or dimension
	// drift refuses as a named tamper. Duplicate rule references are
	// refused here by the resolver; two DISTINCT rules sharing one source
	// pair survive to the execution-time tie cell (file header).
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

// unitProjectionSelectorSteps validates one of the three unit selectors
// into the registered step forms: direct properties and non-negative
// indexed steps. Repeated steps and negative indexes are named refusals
// (file header: the enabling array-rendering capability is pg-sql-16).
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
		case step.Index != nil && *step.Index < 0:
			return nil, fmt.Errorf("%w: projection %q: unit %s selector step %d uses negative index %d; ArangoDB renders [n] verbatim with tail-index semantics (render/aql/selector.go:34,47) and no PostgreSQL equivalence is registered (the enabling array-rendering capability is pg-sql-16)",
				ErrUnsupportedPostgresPlan, projectionName, role, index, *step.Index)
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

// unitRulesPairAmbiguity registers the tie cell (coordinator determinism
// condition): the identity match is a function from an exact source pair
// to at most one rule. A rule set carrying two rules for one normalized
// pair is REFUSED before any SQL exists — it must never resolve by
// bind-array order, which is what AQL's FIRST over candidate bindings
// silently does (render/aql/selectors.go:213; registered divergence).
// Registry re-verification already admits only pinned registry entries, so
// no current policy can construct such a set; the check is the
// future-registry guarantee, and the execution-time COUNT > 1
// UNIT_RULE_AMBIGUOUS arm stays as defense in depth (never row order).
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

// unitRulesBinding marshals the validated rule set into the one jsonb
// parameter the identity lookup reads. The struct marshal is deterministic
// (rendering the same plan twice yields byte-identical SQL and Args), and
// a non-finite coefficient — which unit.Validate already rejects, re-read
// here as a pre-SQL gate — has no JSON or jsonb representation and is
// refused before any SQL exists.
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

// unitValueListExpression normalizes one document-path reading to the
// 0..N-item contributing value list AQL's FLATTEN(renderSelectorArrayFrom
// Source(...)) builds: missing or JSON-null reads '[]', a stored array
// contributes its own items (one FLATTEN level, second-level arrays
// survive as items exactly as FLATTEN's single pass keeps them), and any
// other stored value contributes itself whole.
func unitValueListExpression(document string) string {
	return "(CASE WHEN " + document + " IS NULL OR jsonb_typeof(" + document + ") = 'null' THEN '[]'::jsonb" +
		" WHEN jsonb_typeof(" + document + ") = 'array' THEN " + document +
		" ELSE jsonb_build_array(" + document + ") END)"
}

// renderUnitNormalizedProjection renders one classified unit-normalized
// projection onto its jsonb-valued SELECT fragment. The fragment is a
// correlated scalar subquery over the row document's contributing values:
//
//	(SELECT COALESCE(jsonb_agg(converted ORDER BY ord), '[]'::jsonb)
//	   FROM (values expanded WITH ORDINALITY, nulls dropped) AS vals
//	   CROSS JOIN LATERAL (first-sibling system/code texts) AS identity
//	   CROSS JOIN LATERAL (rule match: exactly one, else guard raise) AS rule)
//
// The null drop sits INSIDE the value subquery so the identity lateral is
// reached only by surviving values — AQL's FILTER-before-ASSERT ordering
// (selectors.go:210-214), pinned live by the named M1 victim. The rule
// lateral always returns exactly one row: COUNT = 1 answers the matched
// rule (MIN over the single text, the distinct_present_one precedent at
// calls.go:1626-1633), COUNT = 0 raises the AQL-identical
// UNIT_IDENTITY_UNKNOWN guard, COUNT > 1 raises UNIT_RULE_AMBIGUOUS — the
// registered tie cell that refuses row-order resolution (file header).
// Every double arm runs inside band tests proven raise-free with the
// pg-sql-10 helpers; every invalid value reads a jsonb null element so
// jsonb_agg never skips it.
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
	// The dedup cache key embeds the raw marshaled rule text (and the
	// step emitter likewise keys path literals by their raw strings).
	// Raw plan data reaching a helper key is safe only because the
	// classification above re-verified every rule byte-equal to its
	// immutable registry entry: the bytes are registry-minted, so two
	// distinct rule sets can never collide on one key by picking texts,
	// and a tampered set never reaches this line. Without that
	// byte-equality gate, embedding plan strings in a dedup key would be
	// an unbound cross-plan channel, not a cache optimization.
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

	// Operands. The stored value crosses callNumericCoercion's registered
	// numeric-domain pre-pass (numbers by canonical text, decimal-form
	// strings through the oracle-gated tiers, everything else NULL); the
	// rule's scale/offset cross as renderer-minted shortest-round-trip
	// float64 texts inside the bound jsonb, so their ::numeric and
	// ::double precision reads parse exactly and never raise (a finite
	// float64 text is always storable in both domains).
	value := "loom_un_value.v"
	coerced := callNumericCoercion(value)
	clamped := callBandOperandClamp(coerced)
	scaleNumeric := "((loom_un_rule.r ->> 'scale')::numeric)"
	offsetNumeric := "((loom_un_rule.r ->> 'offset')::numeric)"
	scaleDouble := "((loom_un_rule.r ->> 'scale')::double precision)"
	offsetDouble := "((loom_un_rule.r ->> 'offset')::double precision)"
	productMagnitude := "abs((" + callBandOperandClamp(coerced) + " * " + callBandOperandClamp(scaleNumeric) + "))"
	sumMagnitude := "abs((" + callBandOperandClamp(coerced) + " * " + callBandOperandClamp(scaleNumeric) + " + " + callBandOperandClamp(offsetNumeric) + "))"

	// Conversion arms, evaluated in written order, each raise-free on
	// every input (CASE WHEN ordering is the executor's guarantee, the
	// same discipline as callDecimalTierArms):
	//   1. non-numeric stored value → jsonb null element (AQL invalid
	//      arithmetic; the identity guard above still required a valid
	//      identity, exactly as AQL asserts before converting).
	//   2. operand at or beyond the IEEE overflow tie → jsonb null
	//      element (Infinity is invalid → null per operators.md 259-262,
	//      and jsonb could not hold it). The clamp cap (1e400) sits above
	//      this boundary, so the verdict is unchanged by clamping. This
	//      arm also guarantees callDoubleOperand below is never NULL
	//      inside the pass arm (it reads the exact cast, or the
	//      registered 0 below the underflow floor).
	//   3. pass band: product strictly below the registered multiply pass
	//      boundary AND the sum strictly below unitAddPassUpper — the
	//      executed double * and + cannot raise 22003 here.
	//   4. product underflow band → the registered 0 + offset (IEEE's
	//      flush below the tie; the same reading their castable
	//      neighbours already take).
	//   5. everything else (pass-boundary gray bands, overflow sums) →
	//      the registered conservative jsonb null, never a raise and
	//      never a wrong number.
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
