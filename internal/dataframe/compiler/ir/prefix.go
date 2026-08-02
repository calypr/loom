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
	// UnnestScopeIdentity identifies the active cardinality-changing scope at
	// the point where this set is materialized. It is intentionally derived
	// from canonical PhysicalUnnest operations rather than renderer variable
	// names. Prefixes on opposite sides of an unnest barrier must never share,
	// even when they read the same root variable.
	UnnestScopeIdentity string
	NodeVariable        string
	EdgeVariable        string
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
	PhysicalPrefixUnnestBarrier        PhysicalTraversalPrefixRejectionReason = "UNNEST_SCOPE_BARRIER"
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
	for index, operation := range plan.Operations {
		if operation.Kind == PhysicalSetOp && operation.Set != nil && operation.Set.Variable == set.Variable {
			return DecomposePhysicalTraversalPrefixAt(plan, set, index)
		}
	}
	return DecomposePhysicalTraversalPrefixAt(plan, set, -1)
}

// DecomposePhysicalTraversalPrefixAt is the position-aware form used by the
// optimizer and diagnostics. The operation index is required because an
// unnest changes row cardinality for all operations that follow it while the
// source variable may remain the same root binding.
func DecomposePhysicalTraversalPrefixAt(plan PhysicalPlan, set PhysicalSet, setIndex int) (PhysicalTraversalPrefixDecomposition, error) {
	return decomposePhysicalTraversalPrefix(plan, set, setIndex)
}

func decomposePhysicalTraversalPrefix(plan PhysicalPlan, set PhysicalSet, setIndex int) (PhysicalTraversalPrefixDecomposition, error) {
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
	unnestScope, err := physicalUnnestScopeIdentityAt(plan, setIndex)
	if err != nil {
		return PhysicalTraversalPrefixDecomposition{}, rejectPhysicalTraversalPrefix(PhysicalPrefixUnnestBarrier, "%v", err)
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
		UnnestScopeIdentity:      unnestScope,
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

// physicalUnnestScopeIdentityAt returns a deterministic identity for every
// active top-level unnest before operationIndex. A set after an unnest still
// reads the original root binding, so source-variable equality alone is not a
// sufficient sharing key. The expression and join mode are part of the key so
// a rewrite cannot cross a different cardinality/null-preservation contract.
func physicalUnnestScopeIdentityAt(plan PhysicalPlan, operationIndex int) (string, error) {
	if operationIndex < 0 {
		return "", nil
	}
	if operationIndex > len(plan.Operations) {
		return "", fmt.Errorf("operation index %d is outside plan", operationIndex)
	}
	type unnestScope struct {
		InputVariable  string
		OutputVariable string
		Ordinality     string
		Expression     PhysicalExpression
		JoinMode       PhysicalUnnestJoinMode
	}
	active := make([]unnestScope, 0)
	for index := 0; index < operationIndex; index++ {
		operation := plan.Operations[index]
		if operation.Kind != PhysicalUnnestOp {
			continue
		}
		if operation.Unnest == nil {
			return "", fmt.Errorf("unnest operation %d has no payload", index)
		}
		unnest := operation.Unnest
		active = append(active, unnestScope{
			InputVariable:  unnest.InputVariable,
			OutputVariable: unnest.OutputVariable,
			Ordinality:     unnest.Ordinality,
			Expression:     unnest.Expression,
			JoinMode:       unnest.JoinMode,
		})
	}
	if len(active) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(active)
	if err != nil {
		return "", fmt.Errorf("encode active unnest scope: %w", err)
	}
	return string(encoded), nil
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
		UnnestScopeIdentity                               string
		ScopeOperationCount                               int
	}{prefix.SourceVariable, string(prefix.Direction), collection, label, prefix.EdgeTargetTypeField, project, generation, paths, unrestricted, allowed, prefix.UnnestScopeIdentity, prefix.ScopeOperationCount}
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
