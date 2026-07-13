package ir

import (
	"encoding/json"
	"fmt"
)

// PhysicalTraversalPrefix is the canonical, target-type-independent portion
// of one optional generic traversal set. It describes exactly the work that a
// future optimizer may materialize once for compatible siblings; it is not a
// renderer instruction and has no runtime effect by itself.
//
// NodeVariable and EdgeVariable are deliberately canonical names. Local
// variables from the source set live on PhysicalTraversalSubset, which makes
// alpha-equivalent prefixes compare without relying on lowering counters.
type PhysicalTraversalPrefix struct {
	SourceVariable           string
	Direction                PhysicalTraversalDirection
	EdgeCollectionBindKey    string
	EdgeLabelBindKey         string
	EdgeTargetTypeField      string
	ProjectBindKey           string
	DatasetGenerationBindKey string
	AuthPathsBindKey         string
	AuthUnrestrictedBindKey  string
	ScopeAllowedBindKey      string
	ScopeOperationCount      int
	NodeVariable             string
	EdgeVariable             string
}

const (
	physicalTraversalPrefixNodeVariable = "__loom_prefix_node"
	physicalTraversalPrefixEdgeVariable = "__loom_prefix_edge"
)

// PhysicalTraversalSubset contains the target-type specialization and work
// after the mandatory traversal scope. Its local variables are retained so a
// later rewrite can perform explicit alpha-renaming instead of guessing from
// AQL strings.
type PhysicalTraversalSubset struct {
	TargetTypeBindKey  string
	TargetVariable     string
	EdgeVariable       string
	ConsumerOperations []PhysicalOperation
}

// PhysicalTraversalPrefixDecomposition is the only B1 output consumed by a
// future sharing rewrite. PrefixKey is stable across generated local variable
// and target-type bind names, but differs for any scoped physical behavior.
type PhysicalTraversalPrefixDecomposition struct {
	Prefix    PhysicalTraversalPrefix
	Subset    PhysicalTraversalSubset
	PrefixKey string
}

type PhysicalTraversalPrefixRejectionReason string

const (
	PhysicalPrefixNotOptionalSet       PhysicalTraversalPrefixRejectionReason = "NOT_OPTIONAL_SET"
	PhysicalPrefixSharedSubset         PhysicalTraversalPrefixRejectionReason = "ALREADY_SHARED_SUBSET"
	PhysicalPrefixInvalidCapture       PhysicalTraversalPrefixRejectionReason = "INVALID_PARENT_CAPTURE"
	PhysicalPrefixMissingTraversal     PhysicalTraversalPrefixRejectionReason = "MISSING_TRAVERSAL"
	PhysicalPrefixUnsupportedDirection PhysicalTraversalPrefixRejectionReason = "UNSUPPORTED_DIRECTION"
	PhysicalPrefixInvalidRoute         PhysicalTraversalPrefixRejectionReason = "INVALID_ROUTE"
	PhysicalPrefixInvalidScope         PhysicalTraversalPrefixRejectionReason = "INVALID_SCOPE"
	PhysicalPrefixInvalidTarget        PhysicalTraversalPrefixRejectionReason = "INVALID_TARGET_SUBSET"
)

// PhysicalTraversalPrefixError makes rejection intentional and inspectable.
// Optimizers must retain this reason in diagnostics rather than treating an
// ineligible set as an accidental non-match.
type PhysicalTraversalPrefixError struct {
	Reason PhysicalTraversalPrefixRejectionReason
	Detail string
}

func (e *PhysicalTraversalPrefixError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("physical traversal prefix is not shareable: %s", e.Reason)
	}
	return fmt.Sprintf("physical traversal prefix is not shareable: %s: %s", e.Reason, e.Detail)
}

func rejectPhysicalTraversalPrefix(reason PhysicalTraversalPrefixRejectionReason, format string, args ...any) error {
	return &PhysicalTraversalPrefixError{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// DecomposePhysicalTraversalPrefix validates and canonically decomposes a
// generic optional traversal set. It does not alter the plan and deliberately
// rejects shared subsets, required EXISTS paths, non-proven directions, and
// sets whose tenant scope is not the exact generic edge/node scope block.
func DecomposePhysicalTraversalPrefix(plan PhysicalPlan, set PhysicalSet) (PhysicalTraversalPrefixDecomposition, error) {
	if set.SourceSetVariable != "" {
		return PhysicalTraversalPrefixDecomposition{}, rejectPhysicalTraversalPrefix(PhysicalPrefixSharedSubset, "set %q reads %q", set.Variable, set.SourceSetVariable)
	}
	if set.Variable == "" {
		return PhysicalTraversalPrefixDecomposition{}, rejectPhysicalTraversalPrefix(PhysicalPrefixNotOptionalSet, "set variable is empty")
	}
	if len(set.Subplan.Captures) != 1 {
		return PhysicalTraversalPrefixDecomposition{}, rejectPhysicalTraversalPrefix(PhysicalPrefixInvalidCapture, "set %q must have exactly one parent capture", set.Variable)
	}
	if len(set.Subplan.Operations) < 7 {
		return PhysicalTraversalPrefixDecomposition{}, rejectPhysicalTraversalPrefix(PhysicalPrefixMissingTraversal, "set %q requires traversal plus exact generic scope", set.Variable)
	}
	first := set.Subplan.Operations[0]
	if first.Kind != PhysicalTraversalOp || first.Traversal == nil {
		return PhysicalTraversalPrefixDecomposition{}, rejectPhysicalTraversalPrefix(PhysicalPrefixMissingTraversal, "set %q first operation is not TRAVERSAL", set.Variable)
	}
	traversal := *first.Traversal
	if traversal.SourceVariable != set.Subplan.Captures[0] {
		return PhysicalTraversalPrefixDecomposition{}, rejectPhysicalTraversalPrefix(PhysicalPrefixInvalidCapture, "traversal source %q does not match capture %q", traversal.SourceVariable, set.Subplan.Captures[0])
	}
	if traversal.Direction != PhysicalInbound && traversal.Direction != PhysicalOutbound {
		return PhysicalTraversalPrefixDecomposition{}, rejectPhysicalTraversalPrefix(PhysicalPrefixUnsupportedDirection, "direction %q", traversal.Direction)
	}
	if err := validateGenericNavigationTraversal(plan, traversal); err != nil {
		return PhysicalTraversalPrefixDecomposition{}, rejectPhysicalTraversalPrefix(PhysicalPrefixInvalidRoute, "%v", err)
	}

	// The prefix is exactly TRAVERSAL followed by edge/node project,
	// generation, and auth scope. Consumer predicates start only after this
	// block, so they cannot accidentally broaden the shared neighbor set.
	scope := set.Subplan.Operations[1:7]
	scopeVariable, err := validateGenericNavigationScopeBlock(scope, traversal.TargetVariable, traversal.EdgeVariable, traversal.TargetVariable)
	if err != nil {
		return PhysicalTraversalPrefixDecomposition{}, rejectPhysicalTraversalPrefix(PhysicalPrefixInvalidScope, "%v", err)
	}
	if scopeVariable == "" {
		return PhysicalTraversalPrefixDecomposition{}, rejectPhysicalTraversalPrefix(PhysicalPrefixInvalidScope, "auth scope variable is empty")
	}
	if traversal.TargetTypeBindKey == "" {
		return PhysicalTraversalPrefixDecomposition{}, rejectPhysicalTraversalPrefix(PhysicalPrefixInvalidTarget, "target type bind is empty")
	}
	if _, ok := plan.BindVars[traversal.TargetTypeBindKey].(string); !ok {
		return PhysicalTraversalPrefixDecomposition{}, rejectPhysicalTraversalPrefix(PhysicalPrefixInvalidTarget, "target type bind %q is not a string", traversal.TargetTypeBindKey)
	}

	prefix := PhysicalTraversalPrefix{
		SourceVariable:           traversal.SourceVariable,
		Direction:                traversal.Direction,
		EdgeCollectionBindKey:    traversal.EdgeCollectionBindKey,
		EdgeLabelBindKey:         traversal.EdgeLabelBindKey,
		EdgeTargetTypeField:      traversal.EdgeTargetTypeField,
		ProjectBindKey:           physicalScopeProjectBind,
		DatasetGenerationBindKey: physicalScopeDatasetGenerationBind,
		AuthPathsBindKey:         physicalScopeAuthPathsBind,
		AuthUnrestrictedBindKey:  physicalScopeAuthPathsUnrestrictedBind,
		ScopeAllowedBindKey:      physicalScopeAllowedBind,
		ScopeOperationCount:      len(scope),
		NodeVariable:             physicalTraversalPrefixNodeVariable,
		EdgeVariable:             physicalTraversalPrefixEdgeVariable,
	}
	key, err := physicalTraversalPrefixKey(plan, prefix)
	if err != nil {
		return PhysicalTraversalPrefixDecomposition{}, err
	}
	return PhysicalTraversalPrefixDecomposition{
		Prefix: prefix,
		Subset: PhysicalTraversalSubset{
			TargetTypeBindKey:  traversal.TargetTypeBindKey,
			TargetVariable:     traversal.TargetVariable,
			EdgeVariable:       traversal.EdgeVariable,
			ConsumerOperations: clonePhysicalOperations(set.Subplan.Operations[7:]),
		},
		PrefixKey: key,
	}, nil
}

func physicalTraversalPrefixKey(plan PhysicalPlan, prefix PhysicalTraversalPrefix) (string, error) {
	bind := func(key string) (string, error) {
		value, found := plan.BindVars[key]
		if !found {
			return "", rejectPhysicalTraversalPrefix(PhysicalPrefixInvalidScope, "bind %q is missing", key)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", rejectPhysicalTraversalPrefix(PhysicalPrefixInvalidScope, "bind %q cannot be canonically encoded: %v", key, err)
		}
		return string(encoded), nil
	}
	collection, err := bind(prefix.EdgeCollectionBindKey)
	if err != nil {
		return "", err
	}
	label, err := bind(prefix.EdgeLabelBindKey)
	if err != nil {
		return "", err
	}
	project, err := bind(prefix.ProjectBindKey)
	if err != nil {
		return "", err
	}
	generation, err := bind(prefix.DatasetGenerationBindKey)
	if err != nil {
		return "", err
	}
	paths, err := bind(prefix.AuthPathsBindKey)
	if err != nil {
		return "", err
	}
	unrestricted, err := bind(prefix.AuthUnrestrictedBindKey)
	if err != nil {
		return "", err
	}
	allowed, err := bind(prefix.ScopeAllowedBindKey)
	if err != nil {
		return "", err
	}
	key := struct {
		Source, Direction, Collection, Label, TargetField string
		Project, Generation, Paths, Unrestricted, Allowed string
		ScopeOperationCount                               int
	}{prefix.SourceVariable, string(prefix.Direction), collection, label, prefix.EdgeTargetTypeField, project, generation, paths, unrestricted, allowed, prefix.ScopeOperationCount}
	encoded, err := json.Marshal(key)
	if err != nil {
		return "", fmt.Errorf("encode physical traversal prefix key: %w", err)
	}
	return string(encoded), nil
}

func clonePhysicalOperations(operations []PhysicalOperation) []PhysicalOperation {
	if len(operations) == 0 {
		return nil
	}
	out := make([]PhysicalOperation, len(operations))
	for index, operation := range operations {
		out[index] = clonePhysicalOperation(operation)
	}
	return out
}
