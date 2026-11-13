package postgres

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
