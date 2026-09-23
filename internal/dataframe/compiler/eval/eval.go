package eval

import (
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

var ErrUnsupportedPlan = errors.New("physical plan is outside the in-memory preview evaluator subset")

type Program struct {
	rootVariable string
	bindVars     map[string]any
	sets         []string
	lets         []compiledLet
	projections  []compiledProjection
	letNames     map[string]struct{}
	uses         map[string]int
}

type compiledLet struct {
	name       string
	expression expression
	dependsOn  []string
}

type compiledProjection struct {
	name       string
	hidden     bool
	expression expression
	dependsOn  []string
}

type setDefinition struct {
	name string
	set  ir.PhysicalSet
}

// Compile validates the plan and compiles its reachable return expressions
// once. Unsupported expression shapes return ErrUnsupportedPlan so callers can
// fall back to the normal query path.
func Compile(plan ir.PhysicalPlan) (*Program, error) {
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("validate physical plan: %w", err)
	}
	if len(plan.DeferredExpressionLets) != 0 {
		return nil, unsupported("deferred expression LETs are not supported")
	}

	rootVariable := ""
	sets := make([]setDefinition, 0, 8)
	lets := make([]compiledLet, 0, len(plan.Operations))
	letIndexes := make(map[string]int)
	var returnOp *ir.PhysicalReturn
	for index := range plan.Operations {
		operation := plan.Operations[index]
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			rootVariable = operation.RootScan.Variable
			if rootVariable != "root" {
				return nil, unsupported("root variable %q is not supported", rootVariable)
			}
		case ir.PhysicalFilterOp:
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet.Operator != "AUTH_RESOURCE_PATH_ALLOWED" {
				return nil, unsupported("derived LET operator %q is not supported", operation.DerivedLet.Operator)
			}
		case ir.PhysicalSetOp:
			if operation.Set.Kind != "" && operation.Set.Kind != ir.PhysicalNodeSetKind {
				return nil, unsupported("set %q has unsupported kind %q", operation.Set.Variable, operation.Set.Kind)
			}
			sets = append(sets, setDefinition{name: operation.Set.Variable, set: *operation.Set})
		case ir.PhysicalExpressionLetOp:
			compiled, err := compileExpression(operation.ExpressionLet.Expression, plan.BindVars)
			if err != nil {
				return nil, fmt.Errorf("compile LET %q: %w", operation.ExpressionLet.Variable, err)
			}
			if _, exists := letIndexes[operation.ExpressionLet.Variable]; exists {
				return nil, fmt.Errorf("duplicate expression LET %q", operation.ExpressionLet.Variable)
			}
			letIndexes[operation.ExpressionLet.Variable] = len(lets)
			lets = append(lets, compiledLet{name: operation.ExpressionLet.Variable, expression: compiled})
		case ir.PhysicalReturnOp:
			if returnOp != nil {
				return nil, unsupported("multiple RETURN operations")
			}
			returnOp = operation.Return
		default:
			return nil, unsupported("operation %q is not supported", operation.Kind)
		}
	}
	if rootVariable == "" || returnOp == nil {
		return nil, unsupported("plan must have a root scan and a RETURN")
	}

	projections := make([]compiledProjection, 0, len(returnOp.Projections))
	for _, projection := range returnOp.Projections {
		var compiled expression
		var err error
		if projection.Expression != nil {
			compiled, err = compileExpression(*projection.Expression, plan.BindVars)
		} else {
			compiled, err = compileValue(ir.PhysicalExpression{
				Kind:         ir.PhysicalValueExpression,
				Cardinality:  ir.PhysicalScalarCardinality,
				NullBehavior: ir.PhysicalPreserveNull,
				Value:        &projection.Value,
			}, plan.BindVars)
		}
		if err != nil {
			return nil, fmt.Errorf("compile projection %q: %w", projection.Name, err)
		}
		projections = append(projections, compiledProjection{name: projection.Name, hidden: projection.Hidden, expression: compiled})
	}

	allLetNames := make(map[string]struct{}, len(lets))
	for _, let := range lets {
		allLetNames[let.name] = struct{}{}
	}
	setByName := make(map[string]ir.PhysicalSet, len(sets))
	setOrder := make([]string, 0, len(sets))
	for _, set := range sets {
		setByName[set.name] = set.set
		setOrder = append(setOrder, set.name)
	}

	reachableLets := make(map[string]struct{}, len(lets))
	reachableSets := make(map[string]struct{}, len(sets))
	var visit func(map[string]struct{}) error
	visit = func(references map[string]struct{}) error {
		for name := range references {
			if _, ok := allLetNames[name]; ok {
				if _, seen := reachableLets[name]; seen {
					continue
				}
				reachableLets[name] = struct{}{}
				let := lets[letIndexes[name]]
				nested := make(map[string]struct{})
				let.expression.references(nested)
				if err := visit(nested); err != nil {
					return err
				}
				continue
			}
			if name == rootVariable {
				continue
			}
			if _, ok := setByName[name]; ok {
				reachableSets[name] = struct{}{}
				continue
			}
			return unsupported("expression references unavailable variable %q", name)
		}
		return nil
	}
	for _, projection := range projections {
		references := make(map[string]struct{})
		projection.expression.references(references)
		if err := visit(references); err != nil {
			return nil, err
		}
	}

	program := &Program{
		rootVariable: rootVariable,
		bindVars:     cloneBindVars(plan.BindVars),
		lets:         make([]compiledLet, 0, len(reachableLets)),
		projections:  projections,
		letNames:     reachableLets,
		uses:         make(map[string]int, len(reachableLets)),
	}
	for _, setName := range setOrder {
		if _, needed := reachableSets[setName]; !needed {
			continue
		}
		set := setByName[setName]
		if set.Output != nil && !containsSetOutputField(set.Output.Fields, ir.PhysicalSetPayloadField) {
			return nil, unsupported("terminal set %q does not retain its payload", setName)
		}
		if set.Projection != nil || set.Prepared != nil {
			return nil, unsupported("terminal set %q uses projected or prepared rows", setName)
		}
		program.sets = append(program.sets, setName)
	}

	for _, let := range lets {
		if _, needed := reachableLets[let.name]; !needed {
			continue
		}
		let.dependsOn = referencedNames(let.expression, reachableLets)
		program.lets = append(program.lets, let)
	}
	for index := range program.projections {
		program.projections[index].dependsOn = referencedNames(program.projections[index].expression, reachableLets)
	}
	for _, let := range program.lets {
		for _, dependency := range let.dependsOn {
			program.uses[dependency]++
		}
	}
	for _, projection := range program.projections {
		for _, dependency := range projection.dependsOn {
			program.uses[dependency]++
		}
	}
	return program, nil
}

// Eligible reports whether Compile accepts the physical plan subset.
func Eligible(plan ir.PhysicalPlan) bool {
	_, err := Compile(plan)
	return err == nil
}

// SourceSetVariables returns the terminal SET variables reachable from the
// RETURN projections, in physical-plan order. The returned slice is owned by
// the caller.
func (p *Program) SourceSetVariables() []string {
	if p == nil {
		return nil
	}
	return append([]string(nil), p.sets...)
}

// EvaluateRow evaluates one already-extracted root row and its requested child
// sets. It does not retain or mutate source documents between calls.
func (p *Program) EvaluateRow(variables map[string]any) (map[string]any, error) {
	if p == nil {
		return nil, errors.New("nil in-memory preview program")
	}
	rootValue, ok := variables[p.rootVariable]
	if !ok {
		return nil, fmt.Errorf("missing root variable %q", p.rootVariable)
	}
	root, ok := rootValue.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("root variable %q must be an object, got %T", p.rootVariable, rootValue)
	}
	if _, ok := root["payload"]; !ok {
		return nil, fmt.Errorf("root variable %q is missing payload", p.rootVariable)
	}

	scope := &evaluationScope{
		rootVariable: p.rootVariable,
		variables:    make(map[string]any, 1+len(p.sets)+len(p.lets)),
		bindVars:     p.bindVars,
		sets:         make(map[string]struct{}, len(p.sets)),
		uses:         make(map[string]int, len(p.uses)),
	}
	scope.variables[p.rootVariable] = root
	for name, count := range p.uses {
		scope.uses[name] = count
	}
	for _, name := range p.sets {
		value, ok := variables[name]
		if !ok {
			return nil, fmt.Errorf("missing source set %q", name)
		}
		rows, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("source set %q must be an array, got %T", name, value)
		}
		for index, value := range rows {
			row, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("source set %q row %d must be an object, got %T", name, index, value)
			}
			if _, ok := row["payload"]; !ok {
				return nil, fmt.Errorf("source set %q row %d is missing payload", name, index)
			}
		}
		scope.variables[name] = rows
		scope.sets[name] = struct{}{}
	}

	for _, let := range p.lets {
		value, err := let.expression.evaluate(scope)
		if err != nil {
			return nil, fmt.Errorf("evaluate LET %q: %w", let.name, err)
		}
		scope.variables[let.name] = value
		scope.consume(let.dependsOn)
	}

	output := make(map[string]any, len(p.projections))
	for _, projection := range p.projections {
		value, err := projection.expression.evaluate(scope)
		if err != nil {
			return nil, fmt.Errorf("evaluate projection %q: %w", projection.name, err)
		}
		if isMissing(value) {
			value = nil
		}
		output[projection.name] = value
		scope.consume(projection.dependsOn)
	}
	return output, nil
}

func (s *evaluationScope) consume(names []string) {
	for _, name := range names {
		s.uses[name]--
		if s.uses[name] == 0 {
			delete(s.variables, name)
		}
	}
}

func referencedNames(value expression, names map[string]struct{}) []string {
	references := make(map[string]struct{})
	value.references(references)
	result := make([]string, 0, len(references))
	for name := range references {
		if _, ok := names[name]; ok {
			result = append(result, name)
		}
	}
	return result
}

func containsSetOutputField(fields []ir.PhysicalSetOutputField, field ir.PhysicalSetOutputField) bool {
	for _, candidate := range fields {
		if candidate == field {
			return true
		}
	}
	return false
}

func cloneBindVars(source map[string]any) map[string]any {
	copy := make(map[string]any, len(source))
	for key, value := range source {
		switch typed := value.(type) {
		case []string:
			copy[key] = append([]string(nil), typed...)
		case []any:
			copy[key] = append([]any(nil), typed...)
		default:
			copy[key] = value
		}
	}
	return copy
}

func unsupported(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupportedPlan, fmt.Sprintf(format, args...))
}
