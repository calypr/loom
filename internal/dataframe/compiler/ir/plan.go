package ir

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/spec"
	"regexp"
	"strings"

	"github.com/calypr/loom/fhirschema"
)

// PhysicalPlan is the renderer-independent AQL operation graph produced after
// semantic planning. Operations are ordered because AQL variables have lexical
// scope: an operation may reference only variables introduced before it.
type PhysicalPlan struct {
	Version    int
	Source     PhysicalSource
	BindVars   map[string]any
	Operations []PhysicalOperation
	// AppliedRules records physical rewrites without exposing renderer
	// implementation details to callers.
	AppliedRules         []string
	SharedTraversalCount int
	// OptimizationPolicy records every optional rewrite decision made by the
	// physical optimizer, including conservative rejections.
	OptimizationPolicy PhysicalOptimizationReport
	// RequiredMatchReuseCount records duplicate required EXISTS predicates
	// removed during physical lowering. It is deliberately separate from
	// shared traversal count: required predicates remain pre-window
	// semi-joins, while optional sets are post-window materializations.
	RequiredMatchReuseCount int
}

// PhysicalSource retains semantic provenance through physical optimization so
// explain output and compiler errors can point back to user intent.
type PhysicalSource struct {
	RecipeID      string
	TemplateID    string
	SemanticNode  string
	SemanticField string
	ResourceType  string
	Relationship  string
}

type PhysicalOperationKind string

const (
	PhysicalRootScanOp   PhysicalOperationKind = "ROOT_SCAN"
	PhysicalTraversalOp  PhysicalOperationKind = "TRAVERSAL"
	PhysicalFilterOp     PhysicalOperationKind = "FILTER"
	PhysicalDerivedLetOp PhysicalOperationKind = "DERIVED_LET"
	// PhysicalSetOp materializes a correlated, array-valued subplan. It is the
	// only operation that can introduce a set variable; selectors, aggregates,
	// pivots, and slices consume that variable through typed expressions.
	PhysicalSetOp PhysicalOperationKind = "SET"
	// PhysicalSortOp and PhysicalLimitOp describe the root execution window.
	// They are intentionally typed so preview ordering and bounds cannot be
	// smuggled into an AQL string by a caller.
	PhysicalSortOp   PhysicalOperationKind = "SORT"
	PhysicalLimitOp  PhysicalOperationKind = "LIMIT"
	PhysicalReturnOp PhysicalOperationKind = "RETURN"
)

// PhysicalOperation is a tagged union. Exactly one payload matching Kind must
// be set. Source can be more specific than the plan-level provenance.
type PhysicalOperation struct {
	Kind       PhysicalOperationKind
	Source     PhysicalSource
	RootScan   *PhysicalRootScan
	Traversal  *PhysicalTraversal
	Filter     *PhysicalFilter
	DerivedLet *PhysicalDerivedLet
	Set        *PhysicalSet
	Sort       *PhysicalSort
	Limit      *PhysicalLimit
	Return     *PhysicalReturn
}

type PhysicalRootScan struct {
	Variable          string
	CollectionBindKey string
}

type PhysicalTraversalDirection string

const (
	PhysicalOutbound PhysicalTraversalDirection = "OUTBOUND"
	PhysicalInbound  PhysicalTraversalDirection = "INBOUND"
	PhysicalAny      PhysicalTraversalDirection = "ANY"
)

// PhysicalTraversalStrategy selects the execution shape for a validated
// depth-one relationship. Native graph traversal is the conservative
// fallback. EndpointLookup is only legal when storage-route metadata proves
// the endpoint/discriminator fields and their compound index contract.
type PhysicalTraversalStrategy string

const (
	PhysicalTraversalNative         PhysicalTraversalStrategy = "NATIVE"
	PhysicalTraversalEndpointLookup PhysicalTraversalStrategy = "ENDPOINT_LOOKUP"
)

type PhysicalTraversal struct {
	SourceVariable        string
	TargetVariable        string
	EdgeVariable          string
	Direction             PhysicalTraversalDirection
	EdgeCollectionBindKey string
	EdgeLabelBindKey      string
	TargetTypeBindKey     string
	// EdgeTargetTypeField is a compiler-owned fhir_edge discriminator used
	// alongside TargetTypeBindKey. For a parent-to-child INBOUND route it is
	// from_type; for a proven forward OUTBOUND route it is to_type. The node
	// resourceType check remains independently mandatory.
	EdgeTargetTypeField string
	// Strategy is deliberately typed rather than an AQL fragment. Endpoint
	// fields are supplied by resolveStorageRoute and validated against the
	// direction before the renderer can use them.
	Strategy            PhysicalTraversalStrategy
	EndpointField       string
	EndpointJoinField   string
	EndpointIndexFields []string
}

// PhysicalValue is either a variable/path reference or a bind variable. A
// renderer must never interpret Path segments as AQL source text.
type PhysicalValue struct {
	Variable string
	Path     []string
	BindKey  string
}

// PhysicalCardinality and PhysicalNullBehavior are deliberately explicit on
// every rich expression. A renderer must not infer array/scalar/null behavior
// from where an expression appears in an AQL template.
type PhysicalCardinality string

const (
	PhysicalScalarCardinality PhysicalCardinality = "SCALAR"
	PhysicalArrayCardinality  PhysicalCardinality = "ARRAY"
	PhysicalObjectCardinality PhysicalCardinality = "OBJECT"
)

type PhysicalNullBehavior string

const (
	PhysicalPreserveNull PhysicalNullBehavior = "PRESERVE_NULL"
	PhysicalOmitNulls    PhysicalNullBehavior = "OMIT_NULLS"
	PhysicalEmptyOnNull  PhysicalNullBehavior = "EMPTY_ON_NULL"
)

type PhysicalExpressionKind string

const (
	PhysicalValueExpression     PhysicalExpressionKind = "VALUE"
	PhysicalExtractExpression   PhysicalExpressionKind = "EXTRACT"
	PhysicalAggregateExpression PhysicalExpressionKind = "AGGREGATE"
	PhysicalPivotExpression     PhysicalExpressionKind = "PIVOT_MAP"
	PhysicalSliceExpression     PhysicalExpressionKind = "SLICE"
	PhysicalObjectExpression    PhysicalExpressionKind = "OBJECT"
)

// PhysicalSelectorExecutionMode records a schema-proven selector lowering.
// Generic is the safe fallback for predicates, fallbacks, choice paths, and
// unknown cardinality.
type PhysicalSelectorExecutionMode string

const (
	PhysicalSelectorGeneric          PhysicalSelectorExecutionMode = "GENERIC"
	PhysicalSelectorDirectScalar     PhysicalSelectorExecutionMode = "DIRECT_SCALAR"
	PhysicalSelectorConditionalArray PhysicalSelectorExecutionMode = "CONDITIONAL_ARRAY"
)

// PhysicalExpression is a closed, renderer-independent value tree. It carries
// no AQL source text: selector paths have already been parsed and all literals
// are represented by bind values.
type PhysicalExpression struct {
	Kind         PhysicalExpressionKind
	Cardinality  PhysicalCardinality
	NullBehavior PhysicalNullBehavior
	Value        *PhysicalValue
	Extract      *PhysicalExtract
	Aggregate    *PhysicalAggregate
	Pivot        *PhysicalPivotMap
	Slice        *PhysicalSlice
	Object       *PhysicalObject
}

// PhysicalExtract obtains one FHIR selector from a variable or prior set
// element. ResourceType keeps schema validation available after semantic
// lowering; fallbacks preserve the existing FIRST_NON_NULL behavior.
type PhysicalExtract struct {
	Source       PhysicalValue
	ResourceType string
	Selector     Selector
	Fallbacks    []Selector
	// Distinct preserves the explicit DISTINCT projection mode after semantic
	// lowering. It is meaningful only for an array-valued expression.
	Distinct      bool
	ExecutionMode PhysicalSelectorExecutionMode
	// Prepared points at a selector value projected by a prepared child set.
	// Source remains the owning set for scope validation and diagnostics.
	Prepared *PhysicalPreparedReference
}

// PhysicalPreparedReference identifies one selector column in a prepared set.
type PhysicalPreparedReference struct {
	SetVariable string
	Field       string
}

// PhysicalPreparedSet describes selector projections shared by rich
// consumers over one materialized relationship set. Preparation stays
// attached to the correlated set so root row grain cannot change.
type PhysicalPreparedSet struct {
	Variable          string
	SourceSetVariable string
	Fields            []PhysicalPreparedField
}

type PhysicalPreparedField struct {
	Name         string
	ResourceType string
	Selector     Selector
}

type PhysicalAggregateOperation string

const (
	PhysicalCountAggregate          PhysicalAggregateOperation = "COUNT"
	PhysicalCountDistinctAggregate  PhysicalAggregateOperation = "COUNT_DISTINCT"
	PhysicalExistsAggregate         PhysicalAggregateOperation = "EXISTS"
	PhysicalDistinctValuesAggregate PhysicalAggregateOperation = "DISTINCT_VALUES"
	PhysicalMinAggregate            PhysicalAggregateOperation = "MIN"
	PhysicalMaxAggregate            PhysicalAggregateOperation = "MAX"
	PhysicalFirstAggregate          PhysicalAggregateOperation = "FIRST"
)

type PhysicalAggregate struct {
	Source    PhysicalValue
	Operation PhysicalAggregateOperation
	Value     *PhysicalExpression
	Predicate *PhysicalPredicateExpression
}

type PhysicalPivotMap struct {
	Source         PhysicalValue
	ResourceType   string
	KeySelector    Selector
	ValueSelector  Selector
	ColumnsBindKey string
	PreparedKey    *PhysicalPreparedReference
	PreparedValue  *PhysicalPreparedReference
}

// PhysicalSlice is a bounded, stable, nested projection over a prior set.
// Limit remains a bind variable so callers cannot synthesize query literals.
type PhysicalSlice struct {
	Source       PhysicalValue
	Predicate    *PhysicalPredicateExpression
	Sort         *PhysicalExpression
	LimitBindKey string
	Projections  []PhysicalExpressionProjection
}

type PhysicalExpressionProjection struct {
	Name       string
	Expression PhysicalExpression
}

type PhysicalObject struct {
	Fields []PhysicalExpressionProjection
}

// PhysicalSet is a correlated, array-valued subplan. Captures are declared
// explicitly so validation can prevent accidental references to parent or
// future variables. The set variable becomes visible only after its subplan
// has validated successfully.
type PhysicalSet struct {
	Variable string
	Subplan  PhysicalSubplan
	Unique   bool
	// Output describes a compact, identity-safe projection of each set item.
	// A nil output preserves the full stored document for shared traversal or
	// other consumers that have not proved a smaller contract.
	Output *PhysicalSetOutput
	// Projection describes selector values computed in the original child-set
	// subquery. It replaces the payload-bearing second prepared array when all
	// downstream consumers have a projection-safe selector contract.
	Projection *PhysicalSetProjection
	// SourceSetVariable is set for a typed subset over an already materialized
	// shared traversal. Such a set does not begin with TRAVERSAL; ItemVariable
	// is bound by the renderer while iterating SourceSetVariable.
	SourceSetVariable string
	ItemVariable      string
	// SortByKey makes the set's node order part of physical semantics. Optional
	// relationship materialization must not rely on Arango traversal order.
	SortByKey bool
	Prepared  *PhysicalPreparedSet
}

// PhysicalSetProjection is a single-materialization selector projection. The
// fields are arrays because selector evaluation preserves repeated FHIR
// values; scalar consumers apply their normal FIRST/FLATTEN semantics when
// reading the projected field.
type PhysicalSetProjection struct {
	Fields []PhysicalSetProjectionField
}

type PhysicalSetProjectionField struct {
	Name          string
	ResourceType  string
	Selector      Selector
	ExecutionMode PhysicalSelectorExecutionMode
}

// PhysicalSetOutputField names the only stored properties that may survive a
// compact set projection. The graph identity fields preserve nested traversal
// and duplicate-edge semantics; payload is retained only when a downstream
// selector or rich consumer needs it.
type PhysicalSetOutputField string

const (
	PhysicalSetGraphIDField      PhysicalSetOutputField = "_id"
	PhysicalSetKeyField          PhysicalSetOutputField = "_key"
	PhysicalSetIDField           PhysicalSetOutputField = "id"
	PhysicalSetResourceTypeField PhysicalSetOutputField = "resourceType"
	PhysicalSetPayloadField      PhysicalSetOutputField = "payload"
)

type PhysicalSetOutput struct {
	Fields []PhysicalSetOutputField
}

type PhysicalSubplan struct {
	Captures   []string
	Operations []PhysicalOperation
	Return     PhysicalExpression
}

type PhysicalPredicate struct {
	Operator string
	Left     PhysicalValue
	// LeftExpression lets a comparison consume a typed selector extraction.
	// Exactly one of Left and LeftExpression is present. This keeps user
	// filters out of AQL-shaped strings while the scope predicates can retain
	// their compact value-only form.
	LeftExpression *PhysicalExpression
	Right          *PhysicalValue
	Quantifier     ArrayQuantifier
	ValueKind      FilterValueKind
}

type PhysicalPredicateKind string

const (
	PhysicalComparisonPredicate PhysicalPredicateKind = "COMPARISON"
	PhysicalAllPredicate        PhysicalPredicateKind = "ALL"
	PhysicalAnyPredicate        PhysicalPredicateKind = "ANY"
	PhysicalNotPredicate        PhysicalPredicateKind = "NOT"
	PhysicalExistsPredicate     PhysicalPredicateKind = "EXISTS"
)

// PhysicalPredicateExpression is the typed predicate tree used by rich
// physical operations. Exists contains a bounded correlated subplan; it is
// not a string-shaped LENGTH(FOR ...) compatibility escape hatch.
type PhysicalPredicateExpression struct {
	Kind       PhysicalPredicateKind
	Comparison *PhysicalPredicate
	Children   []PhysicalPredicateExpression
	Exists     *PhysicalSubplan
}

type PhysicalFilter struct {
	// Predicate is retained for the frozen navigation plan. New lowering must
	// use Expression so compound and existence predicates stay typed.
	Predicate  PhysicalPredicate
	Expression *PhysicalPredicateExpression
}

// PhysicalDerivedLet names a derived value. Operator is a compiler-owned
// symbolic operation (for example UNIQUE or LENGTH), never raw AQL.
type PhysicalDerivedLet struct {
	Variable   string
	Operator   string
	Inputs     []PhysicalValue
	Expression *PhysicalExpression
}

// PhysicalSort is the deliberately small ordering primitive currently needed
// by generic root-grain previews. Additional sort keys and directions should
// be added only with a corresponding semantic ordering contract.
type PhysicalSort struct {
	Value PhysicalValue
}

// PhysicalLimit references a positive integer bind value. Keeping the value
// in BindVars retains the same parameterized execution boundary as filters.
type PhysicalLimit struct {
	BindKey string
}

type PhysicalProjection struct {
	Name       string
	Value      PhysicalValue
	Expression *PhysicalExpression
}

type PhysicalReturn struct {
	Projections []PhysicalProjection
}

var (
	physicalVariablePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	physicalBindKeyPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	physicalPathPartPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Validate enforces the frozen physical-plan invariants without rendering AQL.
func (p PhysicalPlan) Validate() error {
	if p.Version <= 0 {
		return fmt.Errorf("physical plan version must be positive")
	}
	for key := range p.BindVars {
		if !physicalBindKeyPattern.MatchString(key) {
			return fmt.Errorf("unsafe bind key %q", key)
		}
	}
	defined := map[string]bool{}
	rootScans := 0
	returns := 0
	for i, operation := range p.Operations {
		if returns > 0 {
			return fmt.Errorf("operation %d appears after RETURN", i)
		}
		if err := operation.validatePayload(); err != nil {
			return fmt.Errorf("operation %d (%s): %w", i, operation.Kind, err)
		}
		switch operation.Kind {
		case PhysicalRootScanOp:
			rootScans++
			if rootScans > 1 {
				return fmt.Errorf("operation %d: physical plan has multiple root scans", i)
			}
			if err := requireBind(p.BindVars, operation.RootScan.CollectionBindKey); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			if err := requireCollectionBind(p.BindVars, operation.RootScan.CollectionBindKey); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			if err := definePhysicalVariable(defined, operation.RootScan.Variable); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
		case PhysicalTraversalOp:
			traversal := operation.Traversal
			if !defined[traversal.SourceVariable] {
				return fmt.Errorf("operation %d: traversal source variable %q is out of scope", i, traversal.SourceVariable)
			}
			if traversal.Direction != PhysicalOutbound && traversal.Direction != PhysicalInbound && traversal.Direction != PhysicalAny {
				return fmt.Errorf("operation %d: invalid traversal direction %q", i, traversal.Direction)
			}
			if traversal.EdgeTargetTypeField != "" && !physicalPathPartPattern.MatchString(traversal.EdgeTargetTypeField) {
				return fmt.Errorf("operation %d: unsafe traversal edge type field %q", i, traversal.EdgeTargetTypeField)
			}
			if err := validatePhysicalTraversalStrategy(*traversal); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			for _, key := range []string{traversal.EdgeCollectionBindKey, traversal.EdgeLabelBindKey, traversal.TargetTypeBindKey} {
				if key != "" {
					if err := requireBind(p.BindVars, key); err != nil {
						return fmt.Errorf("operation %d: %w", i, err)
					}
				}
			}
			if traversal.EdgeCollectionBindKey == "" {
				return fmt.Errorf("operation %d: traversal edge collection bind key is required", i)
			}
			if err := requireCollectionBind(p.BindVars, traversal.EdgeCollectionBindKey); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			if err := definePhysicalVariable(defined, traversal.TargetVariable); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			if traversal.EdgeVariable != "" {
				if err := definePhysicalVariable(defined, traversal.EdgeVariable); err != nil {
					return fmt.Errorf("operation %d: %w", i, err)
				}
			}
		case PhysicalFilterOp:
			if err := validatePhysicalFilter(*operation.Filter, defined, p.BindVars); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
		case PhysicalDerivedLetOp:
			derived := operation.DerivedLet
			if err := validatePhysicalDerivedLet(*derived, defined, p.BindVars); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			if err := definePhysicalVariable(defined, derived.Variable); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
		case PhysicalSetOp:
			set := operation.Set
			if err := validatePhysicalSet(*set, defined, p.BindVars); err != nil {
				return fmt.Errorf("operation %d set %q: %w", i, set.Variable, err)
			}
			if err := definePhysicalVariable(defined, set.Variable); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			if set.Prepared != nil {
				if err := definePhysicalVariable(defined, set.Prepared.Variable); err != nil {
					return fmt.Errorf("operation %d prepared set: %w", i, err)
				}
			}
		case PhysicalSortOp:
			if err := validatePhysicalValue(operation.Sort.Value, defined, p.BindVars); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
		case PhysicalLimitOp:
			if err := requireBind(p.BindVars, operation.Limit.BindKey); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
			limit, ok := p.BindVars[operation.Limit.BindKey].(int)
			if !ok || limit <= 0 {
				return fmt.Errorf("operation %d: limit bind %q must be a positive int", i, operation.Limit.BindKey)
			}
		case PhysicalReturnOp:
			returns++
			seenNames := map[string]bool{}
			for _, projection := range operation.Return.Projections {
				if strings.TrimSpace(projection.Name) == "" || seenNames[projection.Name] {
					return fmt.Errorf("operation %d: return projection name %q is empty or duplicated", i, projection.Name)
				}
				seenNames[projection.Name] = true
				if err := validatePhysicalProjection(projection, defined, p.BindVars); err != nil {
					return fmt.Errorf("operation %d projection %q: %w", i, projection.Name, err)
				}
			}
		}
	}
	if rootScans != 1 {
		return fmt.Errorf("physical plan requires exactly one root scan")
	}
	if returns != 1 {
		return fmt.Errorf("physical plan requires exactly one RETURN")
	}
	return nil
}

func validatePhysicalTraversalStrategy(traversal PhysicalTraversal) error {
	strategy := traversal.Strategy
	if strategy == "" || strategy == PhysicalTraversalNative {
		return nil
	}
	if strategy != PhysicalTraversalEndpointLookup {
		return fmt.Errorf("unsupported traversal strategy %q", strategy)
	}
	if traversal.Direction != PhysicalInbound && traversal.Direction != PhysicalOutbound {
		return fmt.Errorf("endpoint lookup requires INBOUND or OUTBOUND direction")
	}
	if !physicalPathPartPattern.MatchString(traversal.EndpointField) || !physicalPathPartPattern.MatchString(traversal.EndpointJoinField) {
		return fmt.Errorf("endpoint lookup requires safe endpoint and join fields")
	}
	if len(traversal.EndpointIndexFields) == 0 {
		return fmt.Errorf("endpoint lookup requires declared compound index fields")
	}
	for _, field := range traversal.EndpointIndexFields {
		if !physicalPathPartPattern.MatchString(field) {
			return fmt.Errorf("endpoint lookup has unsafe index field %q", field)
		}
	}
	return nil
}

func validatePhysicalSet(set PhysicalSet, parent map[string]bool, bindVars map[string]any) error {
	if set.Projection != nil {
		if len(set.Projection.Fields) == 0 {
			return fmt.Errorf("set %q projection requires at least one field", set.Variable)
		}
		seenProjectionFields := map[string]bool{}
		for _, field := range set.Projection.Fields {
			if !physicalVariablePattern.MatchString(field.Name) || seenProjectionFields[field.Name] {
				return fmt.Errorf("set %q projection field %q is unsafe or duplicated", set.Variable, field.Name)
			}
			seenProjectionFields[field.Name] = true
			if strings.TrimSpace(field.ResourceType) == "" || !fhirschema.HasResource(field.ResourceType) {
				return fmt.Errorf("set %q projection field %q has invalid resource type %q", set.Variable, field.Name, field.ResourceType)
			}
			if err := validatePhysicalSelector(field.ResourceType, field.Selector); err != nil {
				return fmt.Errorf("set %q projection field %q selector: %w", set.Variable, field.Name, err)
			}
		}
	}
	if set.Output != nil {
		if len(set.Output.Fields) == 0 {
			return fmt.Errorf("set %q compact output requires at least one retained field", set.Variable)
		}
		seenOutputFields := map[PhysicalSetOutputField]bool{}
		for _, field := range set.Output.Fields {
			switch field {
			case PhysicalSetGraphIDField, PhysicalSetKeyField, PhysicalSetIDField, PhysicalSetResourceTypeField, PhysicalSetPayloadField:
			default:
				return fmt.Errorf("set %q compact output field %q is unsupported", set.Variable, field)
			}
			if seenOutputFields[field] {
				return fmt.Errorf("set %q compact output field %q is duplicated", set.Variable, field)
			}
			seenOutputFields[field] = true
		}
		if !seenOutputFields[PhysicalSetGraphIDField] || !seenOutputFields[PhysicalSetKeyField] {
			return fmt.Errorf("set %q compact output must retain _id and _key", set.Variable)
		}
	}
	if set.Prepared != nil {
		prepared := set.Prepared
		if !physicalVariablePattern.MatchString(prepared.Variable) || !physicalVariablePattern.MatchString(prepared.SourceSetVariable) {
			return fmt.Errorf("prepared set variables must be safe")
		}
		if prepared.SourceSetVariable != set.Variable {
			return fmt.Errorf("prepared set source %q must equal owning set %q", prepared.SourceSetVariable, set.Variable)
		}
		if len(prepared.Fields) == 0 {
			return fmt.Errorf("prepared set %q requires at least one field", prepared.Variable)
		}
		seen := map[string]bool{}
		for _, field := range prepared.Fields {
			if !physicalVariablePattern.MatchString(field.Name) || seen[field.Name] {
				return fmt.Errorf("prepared set field %q is unsafe or duplicated", field.Name)
			}
			seen[field.Name] = true
			if strings.TrimSpace(field.ResourceType) == "" || !fhirschema.HasResource(field.ResourceType) {
				return fmt.Errorf("prepared set field %q has invalid resource type %q", field.Name, field.ResourceType)
			}
			if err := validatePhysicalSelector(field.ResourceType, field.Selector); err != nil {
				return fmt.Errorf("prepared set field %q selector: %w", field.Name, err)
			}
		}
	}
	if set.SourceSetVariable == "" {
		return validatePhysicalSubplan(set.Subplan, parent, bindVars)
	}
	if !physicalVariablePattern.MatchString(set.ItemVariable) {
		return fmt.Errorf("shared subset %q has unsafe item variable", set.ItemVariable)
	}
	if !parent[set.SourceSetVariable] {
		return fmt.Errorf("shared subset source %q is out of scope", set.SourceSetVariable)
	}
	if len(set.Subplan.Captures) != 1 || set.Subplan.Captures[0] != set.SourceSetVariable {
		return fmt.Errorf("shared subset %q must capture exactly its source set", set.Variable)
	}
	defined := map[string]bool{set.SourceSetVariable: true, set.ItemVariable: true}
	for index, operation := range set.Subplan.Operations {
		if operation.Kind != PhysicalFilterOp && operation.Kind != PhysicalDerivedLetOp {
			return fmt.Errorf("shared subset operation %d has unsupported kind %q", index, operation.Kind)
		}
		if operation.Kind == PhysicalFilterOp {
			if err := validatePhysicalFilter(*operation.Filter, defined, bindVars); err != nil {
				return err
			}
		} else {
			if err := validatePhysicalDerivedLet(*operation.DerivedLet, defined, bindVars); err != nil {
				return err
			}
			if err := definePhysicalVariable(defined, operation.DerivedLet.Variable); err != nil {
				return err
			}
		}
	}
	return validatePhysicalExpression(set.Subplan.Return, defined, bindVars)
}

func (operation PhysicalOperation) validatePayload() error {
	payloads := 0
	if operation.RootScan != nil {
		payloads++
	}
	if operation.Traversal != nil {
		payloads++
	}
	if operation.Filter != nil {
		payloads++
	}
	if operation.DerivedLet != nil {
		payloads++
	}
	if operation.Set != nil {
		payloads++
	}
	if operation.Sort != nil {
		payloads++
	}
	if operation.Limit != nil {
		payloads++
	}
	if operation.Return != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("operation must contain exactly one payload")
	}
	valid := (operation.Kind == PhysicalRootScanOp && operation.RootScan != nil) ||
		(operation.Kind == PhysicalTraversalOp && operation.Traversal != nil) ||
		(operation.Kind == PhysicalFilterOp && operation.Filter != nil) ||
		(operation.Kind == PhysicalDerivedLetOp && operation.DerivedLet != nil) ||
		(operation.Kind == PhysicalSetOp && operation.Set != nil) ||
		(operation.Kind == PhysicalSortOp && operation.Sort != nil) ||
		(operation.Kind == PhysicalLimitOp && operation.Limit != nil) ||
		(operation.Kind == PhysicalReturnOp && operation.Return != nil)
	if !valid {
		return fmt.Errorf("payload does not match operation kind")
	}
	return nil
}

func definePhysicalVariable(defined map[string]bool, variable string) error {
	if !physicalVariablePattern.MatchString(variable) {
		return fmt.Errorf("unsafe variable name %q", variable)
	}
	if defined[variable] {
		return fmt.Errorf("variable %q is already defined", variable)
	}
	defined[variable] = true
	return nil
}

func requireBind(bindVars map[string]any, key string) error {
	if !physicalBindKeyPattern.MatchString(key) {
		return fmt.Errorf("unsafe bind key %q", key)
	}
	if _, ok := bindVars[key]; !ok {
		return fmt.Errorf("bind key %q is not defined", key)
	}
	return nil
}

func requireCollectionBind(bindVars map[string]any, key string) error {
	value, ok := bindVars[key]
	if !ok {
		return fmt.Errorf("bind key %q is not defined", key)
	}
	collection, ok := value.(string)
	if !ok || strings.TrimSpace(collection) == "" {
		return fmt.Errorf("collection bind key %q must have a non-empty string value", key)
	}
	return nil
}

func validatePhysicalPredicate(predicate PhysicalPredicate, defined map[string]bool, bindVars map[string]any) error {
	operator := strings.ToUpper(strings.TrimSpace(predicate.Operator))
	switch operator {
	case "EQUALS", "NOT_EQUALS", "IN", "EXISTS", "MISSING", "CONTAINS_TEXT", "GT", "GTE", "LT", "LTE":
	default:
		return fmt.Errorf("unknown physical filter operator %q", predicate.Operator)
	}
	hasLeftValue := predicate.Left.Variable != "" || predicate.Left.BindKey != "" || len(predicate.Left.Path) != 0
	hasLeftExpression := predicate.LeftExpression != nil
	if hasLeftValue == hasLeftExpression {
		return fmt.Errorf("physical filter predicate requires exactly one left value or expression")
	}
	if hasLeftExpression {
		if err := validatePhysicalExpression(*predicate.LeftExpression, defined, bindVars); err != nil {
			return fmt.Errorf("physical filter predicate left expression: %w", err)
		}
		if predicate.LeftExpression.Cardinality != PhysicalArrayCardinality {
			return fmt.Errorf("physical filter predicate left expression must be array-valued")
		}
		if !predicate.ValueKind.Valid() {
			return fmt.Errorf("physical filter predicate value kind %q is invalid", predicate.ValueKind)
		}
		if predicate.Quantifier != "" && !predicate.Quantifier.Valid() {
			return fmt.Errorf("physical filter predicate quantifier %q is invalid", predicate.Quantifier)
		}
	} else if err := validatePhysicalValue(predicate.Left, defined, bindVars); err != nil {
		return err
	}
	requiresRight := operator != "EXISTS" && operator != "MISSING"
	if requiresRight != (predicate.Right != nil) {
		return fmt.Errorf("physical filter operator %s right value presence is invalid", operator)
	}
	if predicate.Right != nil {
		if err := validatePhysicalValue(*predicate.Right, defined, bindVars); err != nil {
			return err
		}
	}
	return nil
}

func validatePhysicalFilter(filter PhysicalFilter, defined map[string]bool, bindVars map[string]any) error {
	legacy := strings.TrimSpace(filter.Predicate.Operator) != ""
	rich := filter.Expression != nil
	if legacy == rich {
		return fmt.Errorf("filter requires exactly one legacy predicate or predicate expression")
	}
	if legacy {
		return validatePhysicalPredicate(filter.Predicate, defined, bindVars)
	}
	return validatePhysicalPredicateExpression(*filter.Expression, defined, bindVars)
}

func validatePhysicalDerivedLet(derived PhysicalDerivedLet, defined map[string]bool, bindVars map[string]any) error {
	legacy := strings.TrimSpace(derived.Operator) != "" || len(derived.Inputs) != 0
	rich := derived.Expression != nil
	if legacy == rich {
		return fmt.Errorf("derived LET requires exactly one legacy operation or expression")
	}
	if rich {
		return validatePhysicalExpression(*derived.Expression, defined, bindVars)
	}
	if strings.TrimSpace(derived.Operator) == "" {
		return fmt.Errorf("derived LET operator is required")
	}
	for _, input := range derived.Inputs {
		if err := validatePhysicalValue(input, defined, bindVars); err != nil {
			return err
		}
	}
	return nil
}

func validatePhysicalProjection(projection PhysicalProjection, defined map[string]bool, bindVars map[string]any) error {
	hasValue := projection.Value.Variable != "" || projection.Value.BindKey != "" || len(projection.Value.Path) != 0
	hasExpression := projection.Expression != nil
	if hasValue == hasExpression {
		return fmt.Errorf("projection requires exactly one value or expression")
	}
	if hasExpression {
		return validatePhysicalExpression(*projection.Expression, defined, bindVars)
	}
	return validatePhysicalValue(projection.Value, defined, bindVars)
}

func validatePhysicalExpression(expression PhysicalExpression, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalExpressionObjectCycles(expression); err != nil {
		return err
	}
	if !expression.Cardinality.valid() {
		return fmt.Errorf("expression has invalid cardinality %q", expression.Cardinality)
	}
	if !expression.NullBehavior.valid() {
		return fmt.Errorf("expression has invalid null behavior %q", expression.NullBehavior)
	}
	payloads := 0
	if expression.Value != nil {
		payloads++
	}
	if expression.Extract != nil {
		payloads++
	}
	if expression.Aggregate != nil {
		payloads++
	}
	if expression.Pivot != nil {
		payloads++
	}
	if expression.Slice != nil {
		payloads++
	}
	if expression.Object != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("expression must contain exactly one payload")
	}
	switch expression.Kind {
	case PhysicalValueExpression:
		if expression.Value == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalValue(*expression.Value, defined, bindVars)
	case PhysicalExtractExpression:
		if expression.Extract == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalExtract(*expression.Extract, defined, bindVars)
	case PhysicalAggregateExpression:
		if expression.Aggregate == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalAggregate(*expression.Aggregate, defined, bindVars)
	case PhysicalPivotExpression:
		if expression.Pivot == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalPivot(*expression.Pivot, defined, bindVars)
	case PhysicalSliceExpression:
		if expression.Slice == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalSlice(*expression.Slice, defined, bindVars)
	case PhysicalObjectExpression:
		if expression.Object == nil {
			return fmt.Errorf("expression payload does not match kind")
		}
		return validatePhysicalObject(*expression.Object, defined, bindVars)
	default:
		return fmt.Errorf("unknown expression kind %q", expression.Kind)
	}
}

// validatePhysicalExpressionObjectCycles protects the recursive expression
// validator from a malformed in-memory plan containing a cycle of
// PhysicalObject pointers. JSON decoding cannot produce such a cycle, but
// plans are also assembled by compiler stages and tests, where pointers can
// be wired directly. The active/visited split permits shared (DAG) objects
// while rejecting only true recursion.
func validatePhysicalExpressionObjectCycles(expression PhysicalExpression) error {
	active := map[*PhysicalObject]bool{}
	visited := map[*PhysicalObject]bool{}
	var visitExpression func(PhysicalExpression) error
	var visitObject func(*PhysicalObject) error
	var visitPredicate func(*PhysicalPredicateExpression) error
	var visitSubplan func(PhysicalSubplan) error
	visitExpression = func(current PhysicalExpression) error {
		if current.Object != nil {
			if err := visitObject(current.Object); err != nil {
				return err
			}
		}
		if current.Aggregate != nil && current.Aggregate.Value != nil {
			if err := visitExpression(*current.Aggregate.Value); err != nil {
				return err
			}
		}
		if current.Aggregate != nil && current.Aggregate.Predicate != nil {
			if err := visitPredicate(current.Aggregate.Predicate); err != nil {
				return err
			}
		}
		if current.Slice != nil {
			if current.Slice.Predicate != nil {
				if err := visitPredicate(current.Slice.Predicate); err != nil {
					return err
				}
			}
			if current.Slice.Sort != nil {
				if err := visitExpression(*current.Slice.Sort); err != nil {
					return err
				}
			}
			for _, projection := range current.Slice.Projections {
				if err := visitExpression(projection.Expression); err != nil {
					return err
				}
			}
		}
		return nil
	}
	visitPredicate = func(predicate *PhysicalPredicateExpression) error {
		if predicate == nil {
			return nil
		}
		if predicate.Comparison != nil && predicate.Comparison.LeftExpression != nil {
			if err := visitExpression(*predicate.Comparison.LeftExpression); err != nil {
				return err
			}
		}
		for index := range predicate.Children {
			if err := visitPredicate(&predicate.Children[index]); err != nil {
				return err
			}
		}
		if predicate.Exists != nil {
			return visitSubplan(*predicate.Exists)
		}
		return nil
	}
	visitSubplan = func(subplan PhysicalSubplan) error {
		for _, operation := range subplan.Operations {
			switch operation.Kind {
			case PhysicalFilterOp:
				if operation.Filter != nil && operation.Filter.Expression != nil {
					if err := visitPredicate(operation.Filter.Expression); err != nil {
						return err
					}
				}
			case PhysicalDerivedLetOp:
				if operation.DerivedLet != nil && operation.DerivedLet.Expression != nil {
					if err := visitExpression(*operation.DerivedLet.Expression); err != nil {
						return err
					}
				}
			case PhysicalSetOp:
				if operation.Set != nil {
					if err := visitSubplan(operation.Set.Subplan); err != nil {
						return err
					}
				}
			}
		}
		return visitExpression(subplan.Return)
	}
	visitObject = func(object *PhysicalObject) error {
		if active[object] {
			return fmt.Errorf("physical object expression contains a recursive cycle")
		}
		if visited[object] {
			return nil
		}
		active[object] = true
		for _, field := range object.Fields {
			if err := visitExpression(field.Expression); err != nil {
				return err
			}
		}
		delete(active, object)
		visited[object] = true
		return nil
	}
	return visitExpression(expression)
}

func (cardinality PhysicalCardinality) valid() bool {
	return cardinality == PhysicalScalarCardinality || cardinality == PhysicalArrayCardinality || cardinality == PhysicalObjectCardinality
}

func (behavior PhysicalNullBehavior) valid() bool {
	return behavior == PhysicalPreserveNull || behavior == PhysicalOmitNulls || behavior == PhysicalEmptyOnNull
}

func validatePhysicalExtract(extract PhysicalExtract, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalValue(extract.Source, defined, bindVars); err != nil {
		return err
	}
	if strings.TrimSpace(extract.ResourceType) == "" || !fhirschema.HasResource(extract.ResourceType) {
		return fmt.Errorf("extract resource type %q is not represented by the active generated FHIR schema", extract.ResourceType)
	}
	if err := validatePhysicalSelector(extract.ResourceType, extract.Selector); err != nil {
		return fmt.Errorf("extract selector: %w", err)
	}
	if extract.ExecutionMode != "" && extract.ExecutionMode != PhysicalSelectorGeneric && extract.ExecutionMode != PhysicalSelectorDirectScalar && extract.ExecutionMode != PhysicalSelectorConditionalArray {
		return fmt.Errorf("unknown selector execution mode %q", extract.ExecutionMode)
	}
	if extract.ExecutionMode == PhysicalSelectorDirectScalar && (len(extract.Fallbacks) != 0 || extract.Selector.Filter != nil || !selectorHasNoArrays(extract.Selector)) {
		return fmt.Errorf("direct scalar selector mode requires one fallback-free non-repeated selector")
	}
	if extract.ExecutionMode == PhysicalSelectorConditionalArray && (len(extract.Fallbacks) != 0 || extract.Selector.Filter != nil || !selectorHasIteratedArray(extract.Selector)) {
		return fmt.Errorf("conditional array selector mode requires one fallback-free repeated selector")
	}
	for index, fallback := range extract.Fallbacks {
		if err := validatePhysicalSelector(extract.ResourceType, fallback); err != nil {
			return fmt.Errorf("extract fallback %d: %w", index, err)
		}
	}
	if extract.Prepared != nil {
		if err := validatePhysicalPreparedReference(*extract.Prepared, defined); err != nil {
			return err
		}
		if len(extract.Fallbacks) != 0 {
			return fmt.Errorf("prepared extract cannot use fallback selectors")
		}
	}
	return nil
}

func validatePhysicalPreparedReference(reference PhysicalPreparedReference, defined map[string]bool) error {
	if !physicalVariablePattern.MatchString(reference.SetVariable) || !defined[reference.SetVariable] {
		return fmt.Errorf("prepared set variable %q is out of scope", reference.SetVariable)
	}
	if !physicalVariablePattern.MatchString(reference.Field) {
		return fmt.Errorf("prepared field %q is unsafe", reference.Field)
	}
	return nil
}

func validatePhysicalSelector(resourceType string, selector Selector) error {
	if len(selector.Steps) == 0 {
		return fmt.Errorf("selector is required")
	}
	if _, _, err := spec.SelectorCardinality(resourceType, selector); err != nil {
		return err
	}
	return nil
}

func validatePhysicalAggregate(aggregate PhysicalAggregate, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalValue(aggregate.Source, defined, bindVars); err != nil {
		return err
	}
	switch aggregate.Operation {
	case PhysicalCountAggregate, PhysicalCountDistinctAggregate, PhysicalExistsAggregate, PhysicalDistinctValuesAggregate, PhysicalMinAggregate, PhysicalMaxAggregate, PhysicalFirstAggregate:
	default:
		return fmt.Errorf("unknown aggregate operation %q", aggregate.Operation)
	}
	needsValue := aggregate.Operation != PhysicalCountAggregate && aggregate.Operation != PhysicalExistsAggregate
	if needsValue != (aggregate.Value != nil) {
		return fmt.Errorf("aggregate operation %q value presence is invalid", aggregate.Operation)
	}
	if aggregate.Value != nil {
		if err := validatePhysicalExpression(*aggregate.Value, defined, bindVars); err != nil {
			return fmt.Errorf("aggregate value: %w", err)
		}
	}
	if aggregate.Predicate != nil {
		if err := validatePhysicalPredicateExpression(*aggregate.Predicate, defined, bindVars); err != nil {
			return fmt.Errorf("aggregate predicate: %w", err)
		}
	}
	return nil
}

func validatePhysicalPivot(pivot PhysicalPivotMap, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalValue(pivot.Source, defined, bindVars); err != nil {
		return err
	}
	if strings.TrimSpace(pivot.ResourceType) == "" || !fhirschema.HasResource(pivot.ResourceType) {
		return fmt.Errorf("pivot resource type %q is not represented by the active generated FHIR schema", pivot.ResourceType)
	}
	if err := validatePhysicalSelector(pivot.ResourceType, pivot.KeySelector); err != nil {
		return fmt.Errorf("pivot key selector: %w", err)
	}
	if err := validatePhysicalSelector(pivot.ResourceType, pivot.ValueSelector); err != nil {
		return fmt.Errorf("pivot value selector: %w", err)
	}
	if err := requireBind(bindVars, pivot.ColumnsBindKey); err != nil {
		return err
	}
	columns, ok := bindVars[pivot.ColumnsBindKey].([]string)
	if !ok || len(columns) == 0 {
		return fmt.Errorf("pivot columns bind %q must be a non-empty []string", pivot.ColumnsBindKey)
	}
	for _, column := range columns {
		if strings.TrimSpace(column) == "" {
			return fmt.Errorf("pivot columns bind %q contains an empty column", pivot.ColumnsBindKey)
		}
	}
	if pivot.PreparedKey != nil {
		if err := validatePhysicalPreparedReference(*pivot.PreparedKey, defined); err != nil {
			return fmt.Errorf("prepared pivot key: %w", err)
		}
	}
	if pivot.PreparedValue != nil {
		if err := validatePhysicalPreparedReference(*pivot.PreparedValue, defined); err != nil {
			return fmt.Errorf("prepared pivot value: %w", err)
		}
	}
	return nil
}

func validatePhysicalSlice(slice PhysicalSlice, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalValue(slice.Source, defined, bindVars); err != nil {
		return err
	}
	if slice.Predicate != nil {
		if err := validatePhysicalPredicateExpression(*slice.Predicate, defined, bindVars); err != nil {
			return fmt.Errorf("slice predicate: %w", err)
		}
	}
	if slice.Sort == nil {
		return fmt.Errorf("slice requires a stable sort expression")
	}
	if err := validatePhysicalExpression(*slice.Sort, defined, bindVars); err != nil {
		return fmt.Errorf("slice sort: %w", err)
	}
	if err := requireBind(bindVars, slice.LimitBindKey); err != nil {
		return err
	}
	limit, ok := bindVars[slice.LimitBindKey].(int)
	if !ok || limit <= 0 {
		return fmt.Errorf("slice limit bind %q must be a positive int", slice.LimitBindKey)
	}
	return validatePhysicalExpressionProjections(slice.Projections, defined, bindVars, "slice")
}

func validatePhysicalObject(object PhysicalObject, defined map[string]bool, bindVars map[string]any) error {
	return validatePhysicalExpressionProjections(object.Fields, defined, bindVars, "object")
}

func validatePhysicalExpressionProjections(projections []PhysicalExpressionProjection, defined map[string]bool, bindVars map[string]any, owner string) error {
	if len(projections) == 0 {
		return fmt.Errorf("%s requires at least one projection", owner)
	}
	seen := map[string]bool{}
	for _, projection := range projections {
		if strings.TrimSpace(projection.Name) == "" || seen[projection.Name] {
			return fmt.Errorf("%s projection name %q is empty or duplicated", owner, projection.Name)
		}
		seen[projection.Name] = true
		if err := validatePhysicalExpression(projection.Expression, defined, bindVars); err != nil {
			return fmt.Errorf("%s projection %q: %w", owner, projection.Name, err)
		}
	}
	return nil
}

func validatePhysicalPredicateExpression(predicate PhysicalPredicateExpression, defined map[string]bool, bindVars map[string]any) error {
	switch predicate.Kind {
	case PhysicalComparisonPredicate:
		if predicate.Comparison == nil || len(predicate.Children) != 0 || predicate.Exists != nil {
			return fmt.Errorf("comparison predicate requires exactly one comparison")
		}
		return validatePhysicalPredicate(*predicate.Comparison, defined, bindVars)
	case PhysicalAllPredicate, PhysicalAnyPredicate:
		if predicate.Comparison != nil || predicate.Exists != nil || len(predicate.Children) == 0 {
			return fmt.Errorf("%s predicate requires one or more child predicates", predicate.Kind)
		}
		for index, child := range predicate.Children {
			if err := validatePhysicalPredicateExpression(child, defined, bindVars); err != nil {
				return fmt.Errorf("predicate child %d: %w", index, err)
			}
		}
		return nil
	case PhysicalNotPredicate:
		if predicate.Comparison != nil || predicate.Exists != nil || len(predicate.Children) != 1 {
			return fmt.Errorf("NOT predicate requires exactly one child predicate")
		}
		return validatePhysicalPredicateExpression(predicate.Children[0], defined, bindVars)
	case PhysicalExistsPredicate:
		if predicate.Comparison != nil || len(predicate.Children) != 0 || predicate.Exists == nil {
			return fmt.Errorf("EXISTS predicate requires exactly one subplan")
		}
		return validatePhysicalSubplan(*predicate.Exists, defined, bindVars)
	default:
		return fmt.Errorf("unknown predicate kind %q", predicate.Kind)
	}
}

func validatePhysicalSubplan(subplan PhysicalSubplan, parent map[string]bool, bindVars map[string]any) error {
	if len(subplan.Captures) == 0 {
		return fmt.Errorf("subplan requires at least one explicit capture")
	}
	defined := make(map[string]bool, len(subplan.Captures))
	for _, capture := range subplan.Captures {
		if !parent[capture] {
			return fmt.Errorf("subplan capture %q is out of scope", capture)
		}
		if err := definePhysicalVariable(defined, capture); err != nil {
			return fmt.Errorf("subplan capture: %w", err)
		}
	}
	if len(subplan.Operations) == 0 {
		return fmt.Errorf("subplan requires at least one operation")
	}
	for index, operation := range subplan.Operations {
		if operation.Kind == PhysicalRootScanOp || operation.Kind == PhysicalReturnOp || operation.Kind == PhysicalSortOp || operation.Kind == PhysicalLimitOp {
			return fmt.Errorf("subplan operation %d cannot be %s", index, operation.Kind)
		}
		if err := operation.validatePayload(); err != nil {
			return fmt.Errorf("subplan operation %d (%s): %w", index, operation.Kind, err)
		}
		switch operation.Kind {
		case PhysicalTraversalOp:
			traversal := operation.Traversal
			if !defined[traversal.SourceVariable] {
				return fmt.Errorf("subplan operation %d: traversal source variable %q is out of scope", index, traversal.SourceVariable)
			}
			if traversal.Direction != PhysicalOutbound && traversal.Direction != PhysicalInbound && traversal.Direction != PhysicalAny {
				return fmt.Errorf("subplan operation %d: invalid traversal direction %q", index, traversal.Direction)
			}
			if traversal.EdgeTargetTypeField != "" && !physicalPathPartPattern.MatchString(traversal.EdgeTargetTypeField) {
				return fmt.Errorf("subplan operation %d: unsafe traversal edge type field %q", index, traversal.EdgeTargetTypeField)
			}
			if err := validatePhysicalTraversalStrategy(*traversal); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
			if err := requireCollectionBind(bindVars, traversal.EdgeCollectionBindKey); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
			for _, key := range []string{traversal.EdgeLabelBindKey, traversal.TargetTypeBindKey} {
				if key != "" {
					if err := requireBind(bindVars, key); err != nil {
						return fmt.Errorf("subplan operation %d: %w", index, err)
					}
				}
			}
			if err := definePhysicalVariable(defined, traversal.TargetVariable); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
			if traversal.EdgeVariable != "" {
				if err := definePhysicalVariable(defined, traversal.EdgeVariable); err != nil {
					return fmt.Errorf("subplan operation %d: %w", index, err)
				}
			}
		case PhysicalFilterOp:
			if err := validatePhysicalFilter(*operation.Filter, defined, bindVars); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
		case PhysicalDerivedLetOp:
			if err := validatePhysicalDerivedLet(*operation.DerivedLet, defined, bindVars); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
			if err := definePhysicalVariable(defined, operation.DerivedLet.Variable); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
		case PhysicalSetOp:
			if err := validatePhysicalSet(*operation.Set, defined, bindVars); err != nil {
				return fmt.Errorf("subplan operation %d set %q: %w", index, operation.Set.Variable, err)
			}
			if err := definePhysicalVariable(defined, operation.Set.Variable); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
		default:
			return fmt.Errorf("subplan operation %d has unsupported kind %q", index, operation.Kind)
		}
	}
	return validatePhysicalExpression(subplan.Return, defined, bindVars)
}

func validatePhysicalValue(value PhysicalValue, defined map[string]bool, bindVars map[string]any) error {
	hasVariable := value.Variable != ""
	hasBind := value.BindKey != ""
	if hasVariable == hasBind {
		return fmt.Errorf("physical value must reference exactly one variable or bind key")
	}
	if hasBind {
		if len(value.Path) != 0 {
			return fmt.Errorf("bind value %q cannot have a path", value.BindKey)
		}
		return requireBind(bindVars, value.BindKey)
	}
	if !defined[value.Variable] {
		return fmt.Errorf("variable %q is out of scope", value.Variable)
	}
	for _, part := range value.Path {
		if !physicalPathPartPattern.MatchString(part) {
			return fmt.Errorf("unsafe path segment %q", part)
		}
	}
	return nil
}
