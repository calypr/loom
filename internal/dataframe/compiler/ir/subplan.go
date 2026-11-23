package ir

import (
	"fmt"
	"reflect"
)

type PhysicalSubplan struct {
	Captures   []string
	Operations []PhysicalOperation
	Return     PhysicalExpression

	Sort   *PhysicalValue
	Unique bool

	DistinctBy *PhysicalValue
}

func validatePhysicalSubplan(subplan PhysicalSubplan, parent map[string]bool, bindVars map[string]any) error {
	return validatePhysicalSubplanWithCapturePolicy(subplan, parent, bindVars, false)
}

func validatePhysicalUncorrelatedSubplan(subplan PhysicalSubplan, bindVars map[string]any) error {
	if len(subplan.Captures) != 0 {
		return fmt.Errorf("uncorrelated subplan cannot declare captures")
	}
	return validatePhysicalSubplanWithCapturePolicy(subplan, nil, bindVars, true)
}

func validatePhysicalSubplanWithCapturePolicy(subplan PhysicalSubplan, parent map[string]bool, bindVars map[string]any, allowUncorrelated bool) error {
	if len(subplan.Captures) == 0 && !allowUncorrelated {
		return fmt.Errorf("subplan requires at least one explicit capture")
	}
	if len(subplan.Captures) > 0 && allowUncorrelated {
		return fmt.Errorf("uncorrelated subplan cannot declare captures")
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
		case PhysicalCollectionScanOp:
			if err := requireCollectionBind(bindVars, operation.CollectionScan.CollectionBindKey); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
			if err := definePhysicalVariable(defined, operation.CollectionScan.Variable); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
		case PhysicalKeySetLookupOp:
			lookup := operation.KeySetLookup
			if err := requireCollectionBind(bindVars, lookup.CollectionBindKey); err != nil {
				return fmt.Errorf("subplan operation %d collection: %w", index, err)
			}
			if err := validatePhysicalValue(lookup.Keys, defined, bindVars); err != nil {
				return fmt.Errorf("subplan operation %d key set: %w", index, err)
			}
			if err := definePhysicalVariable(defined, lookup.KeyVariable); err != nil {
				return fmt.Errorf("subplan operation %d key variable: %w", index, err)
			}
			if err := definePhysicalVariable(defined, lookup.Variable); err != nil {
				return fmt.Errorf("subplan operation %d resource variable: %w", index, err)
			}
		case PhysicalDocumentLookupOp:
			lookup := operation.DocumentLookup
			if err := requireCollectionBind(bindVars, lookup.CollectionBindKey); err != nil {
				return fmt.Errorf("subplan operation %d collection: %w", index, err)
			}
			if err := validatePhysicalValue(lookup.ExactID, defined, bindVars); err != nil {
				return fmt.Errorf("subplan operation %d exact document identity: %w", index, err)
			}
			if err := definePhysicalVariable(defined, lookup.Variable); err != nil {
				return fmt.Errorf("subplan operation %d: %w", index, err)
			}
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
		case PhysicalUnnestOp:
			if err := validatePhysicalUnnest(*operation.Unnest, defined, bindVars); err != nil {
				return fmt.Errorf("subplan operation %d unnest: %w", index, err)
			}
			if err := definePhysicalUnnestVariables(*operation.Unnest, defined); err != nil {
				return fmt.Errorf("subplan operation %d unnest bindings: %w", index, err)
			}
		default:
			return fmt.Errorf("subplan operation %d has unsupported kind %q", index, operation.Kind)
		}
	}
	if err := validatePhysicalExpression(subplan.Return, defined, bindVars); err != nil {
		return err
	}
	if subplan.Sort != nil {
		if err := validatePhysicalValue(*subplan.Sort, defined, bindVars); err != nil {
			return fmt.Errorf("subplan sort: %w", err)
		}
	}
	if subplan.DistinctBy != nil {
		if err := validatePhysicalValue(*subplan.DistinctBy, defined, bindVars); err != nil {
			return fmt.Errorf("subplan distinct key: %w", err)
		}
		if subplan.Unique || subplan.Sort == nil || !reflect.DeepEqual(*subplan.Sort, *subplan.DistinctBy) {
			return fmt.Errorf("distinct-key subplan requires the same stable sort key and cannot also use full-value uniqueness")
		}
	}
	if subplan.Unique && subplan.Sort == nil {
		return fmt.Errorf("unique subplan requires a stable sort value")
	}
	return nil
}
