package dataframe

import (
	"fmt"
	"regexp"
	"strings"
)

// PhysicalPlan is the renderer-independent AQL operation graph produced after
// semantic planning. Operations are ordered because AQL variables have lexical
// scope: an operation may reference only variables introduced before it.
type PhysicalPlan struct {
	Version    int
	Source     PhysicalSource
	BindVars   map[string]any
	Operations []PhysicalOperation
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
}

// PhysicalValue is either a variable/path reference or a bind variable. A
// renderer must never interpret Path segments as AQL source text.
type PhysicalValue struct {
	Variable string
	Path     []string
	BindKey  string
}

type PhysicalPredicate struct {
	Operator string
	Left     PhysicalValue
	Right    *PhysicalValue
}

type PhysicalFilter struct {
	Predicate PhysicalPredicate
}

// PhysicalDerivedLet names a derived value. Operator is a compiler-owned
// symbolic operation (for example UNIQUE or LENGTH), never raw AQL.
type PhysicalDerivedLet struct {
	Variable string
	Operator string
	Inputs   []PhysicalValue
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
	Name  string
	Value PhysicalValue
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
			for _, key := range []string{traversal.EdgeCollectionBindKey, traversal.EdgeLabelBindKey, traversal.TargetTypeBindKey} {
				if key != "" {
					if err := requireBind(p.BindVars, key); err != nil {
						return fmt.Errorf("operation %d: %w", i, err)
					}
				}
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
			if err := validatePhysicalPredicate(operation.Filter.Predicate, defined, p.BindVars); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
			}
		case PhysicalDerivedLetOp:
			derived := operation.DerivedLet
			if strings.TrimSpace(derived.Operator) == "" {
				return fmt.Errorf("operation %d: derived LET operator is required", i)
			}
			for _, input := range derived.Inputs {
				if err := validatePhysicalValue(input, defined, p.BindVars); err != nil {
					return fmt.Errorf("operation %d: %w", i, err)
				}
			}
			if err := definePhysicalVariable(defined, derived.Variable); err != nil {
				return fmt.Errorf("operation %d: %w", i, err)
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
				if err := validatePhysicalValue(projection.Value, defined, p.BindVars); err != nil {
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

func validatePhysicalPredicate(predicate PhysicalPredicate, defined map[string]bool, bindVars map[string]any) error {
	if strings.TrimSpace(predicate.Operator) == "" {
		return fmt.Errorf("filter operator is required")
	}
	if err := validatePhysicalValue(predicate.Left, defined, bindVars); err != nil {
		return err
	}
	if predicate.Right != nil {
		return validatePhysicalValue(*predicate.Right, defined, bindVars)
	}
	return nil
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
