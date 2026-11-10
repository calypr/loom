package postgres

// Accepting the canonical scope operations that ir.ValidateGenericPhysical-
// PlanScope has already proven (internal/dataframe/compiler/ir/scope.go).
//
// Lowering emits the canonical block — project FILTER, dataset_generation
// FILTER, AUTH_RESOURCE_PATH_ALLOWED LET, and the closing equality against
// @scope_allowed — for every resource row the plan reads. The AQL renderer
// replays that block as FILTER clauses; the PostgreSQL renderer never does,
// because loadScope (scope.go) re-reads the five canonical binds and emits
// the scope itself as typed WHERE/ON predicates. Plan-authored scope
// operations therefore carry no additional PostgreSQL meaning: they are
// verified for exact canonical shape here and dropped, so a plan that
// satisfies the shared IR scope gate renders. Any scope-flavored operation
// that is not byte-canonical (wrong field, wrong bind key, an auth LET over
// a non-registered row) is refused as a scope defect before any parameter
// is bound — this file widens acceptance only for shapes the shared
// validator has already proven, never for plan-supplied scope data.

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const (
	scopeFieldNameProject    = "project"
	scopeFieldNameGeneration = "dataset_generation"
	scopeFieldNameAuthPath   = "auth_resource_path"
	canonicalAuthLetOperator = "AUTH_RESOURCE_PATH_ALLOWED"
	scopeComparisonOperator  = "EQUALS"
)

// acceptCanonicalScopeFilter reports whether the FILTER is one of the
// canonical scope FILTERs for a row this renderer already enforces. A
// canonical match drops the operation; a scope-flavored near-miss fails
// closed with ErrInvalidPostgresPlanScope.
func (r *planRenderer) acceptCanonicalScopeFilter(filter *ir.PhysicalFilter) (bool, error) {
	if filter == nil || filter.Expression != nil {
		return false, nil
	}
	predicate := filter.Predicate
	if predicate.Operator != scopeComparisonOperator || predicate.LeftExpression != nil || predicate.Right == nil {
		return false, nil
	}
	if predicate.Right.Variable != "" || len(predicate.Right.Path) != 0 {
		return false, nil
	}
	switch predicate.Right.BindKey {
	case projectBindKey:
		return r.acceptCanonicalRowField(predicate, scopeFieldNameProject, projectBindKey)
	case datasetGenerationBind:
		return r.acceptCanonicalRowField(predicate, scopeFieldNameGeneration, datasetGenerationBind)
	case scopeAllowedBind:
		if predicate.Left.Variable == "" || len(predicate.Left.Path) != 0 {
			return false, fmt.Errorf("%w: scope_allowed equality must bind a canonical auth LET variable directly", ErrInvalidPostgresPlanScope)
		}
		if _, ok := r.canonicalAuthLets[predicate.Left.Variable]; !ok {
			return false, fmt.Errorf("%w: scope_allowed equality names %q, which no canonical AUTH_RESOURCE_PATH_ALLOWED LET established",
				ErrInvalidPostgresPlanScope, predicate.Left.Variable)
		}
		return true, nil
	default:
		return false, nil
	}
}

func (r *planRenderer) acceptCanonicalRowField(predicate ir.PhysicalPredicate, fieldName, bindKey string) (bool, error) {
	if _, ok := r.rows[predicate.Left.Variable]; !ok {
		return false, nil
	}
	if len(predicate.Left.Path) != 1 || predicate.Left.Path[0] != fieldName {
		return false, fmt.Errorf("%w: %q scope filter must compare %s.%s",
			ErrInvalidPostgresPlanScope, bindKey, predicate.Left.Variable, fieldName)
	}
	return true, nil
}

// acceptCanonicalAuthLet verifies an AUTH_RESOURCE_PATH_ALLOWED derived LET
// over a registered scope row against the two auth binds and registers its
// variable for the closing equality. Non-auth operators return false with no
// error so the operation reaches the unsupported-surface refusal.
func (r *planRenderer) acceptCanonicalAuthLet(derivedLet *ir.PhysicalDerivedLet) (bool, error) {
	if derivedLet == nil || derivedLet.Operator != canonicalAuthLetOperator {
		return false, nil
	}
	refuse := func(reason string) (bool, error) {
		return false, fmt.Errorf("%w: canonical AUTH_RESOURCE_PATH_ALLOWED LET %s", ErrInvalidPostgresPlanScope, reason)
	}
	if derivedLet.Variable == "" {
		return refuse("must name a variable")
	}
	if _, taken := r.rows[derivedLet.Variable]; taken {
		return refuse("shadows a scope row variable")
	}
	if _, claimed := r.canonicalAuthLets[derivedLet.Variable]; claimed {
		return refuse("redefines an established auth variable")
	}
	var overRow string
	sawPaths, sawUnrestricted := false, false
	for _, input := range derivedLet.Inputs {
		switch {
		case input.BindKey == authPathsBindKey && input.Variable == "" && len(input.Path) == 0:
			sawPaths = true
		case input.BindKey == authUnrestrictedBind && input.Variable == "" && len(input.Path) == 0:
			sawUnrestricted = true
		case input.BindKey == "":
			if _, ok := r.rows[input.Variable]; !ok {
				return refuse(fmt.Sprintf("inputs %q, which is not a registered scope row", input.Variable))
			}
			if len(input.Path) != 1 || input.Path[0] != scopeFieldNameAuthPath {
				return refuse("must read <row>.auth_resource_path from its registered scope row")
			}
			overRow = input.Variable
		default:
			return refuse("carries an unknown input")
		}
	}
	if overRow == "" || !sawPaths || !sawUnrestricted {
		return refuse("must read <row>.auth_resource_path, @auth_resource_paths, and @auth_resource_paths_unrestricted")
	}
	r.canonicalAuthLets[derivedLet.Variable] = overRow
	return true, nil
}
