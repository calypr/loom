package postgres

import (
	"fmt"
	"strconv"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type executionWindow struct {
	keys            []string
	limitClause     string
	sortSeen        bool
	limitSeen       bool
	hasUnnest       bool
	hasUnpivot      bool
	unpivotOrdinals []string
	edgeVars        map[string]string
}

func (w *executionWindow) rememberEdge(edgeVariable, edgeAlias string) error {
	if edgeVariable == "" {
		return nil
	}
	if w.edgeVars == nil {
		w.edgeVars = map[string]string{}
	}
	if previous, taken := w.edgeVars[edgeVariable]; taken {
		return fmt.Errorf("%w: edge variable %q was already joined as %q; a stable occurrence witness needs one join per edge variable",
			ErrUnsupportedPostgresPlan, edgeVariable, previous)
	}
	w.edgeVars[edgeVariable] = edgeAlias
	return nil
}

func (r *planRenderer) renderWindowSort(plan ir.PhysicalPlan, sort ir.PhysicalSort) error {
	if r.window.sortSeen {
		return fmt.Errorf("%w: at most one SORT", ErrUnsupportedPostgresPlan)
	}
	if r.window.hasUnnest {
		if err := r.renderFinalWindowSort(plan, sort); err != nil {
			return err
		}
	} else if r.window.hasUnpivot {
		if err := r.renderFinalWindowSortUnpivot(sort); err != nil {
			return err
		}
	} else {
		for _, key := range sort.Keys {
			scope, ok := r.rows[key.Variable]
			if !ok {
				return fmt.Errorf("%w: sort variable %q does not bind a readable row", ErrUnsupportedPostgresPlan, key.Variable)
			}
			if scope.optional {
				return fmt.Errorf("%w: sort key on %q, a row materialized by an OPTIONAL traversal LEFT JOIN LATERAL, is not registered; sorting expanded child rows is a later capability",
					ErrUnsupportedPostgresPlan, key.Variable)
			}
			if key.BindKey != "" || len(key.Path) != 1 {
				return fmt.Errorf("%w: sort keys must be single typed columns; payload-path sorting is not yet implemented",
					ErrUnsupportedPostgresPlan)
			}
			column := canonicalKeyColumn(key.Path[0])
			if !resourceFilterColumns[column] {
				return fmt.Errorf("%w: sort keys must be single typed columns; payload-path sorting is not yet implemented",
					ErrUnsupportedPostgresPlan)
			}
			r.window.keys = append(r.window.keys, scope.alias+"."+r.storage.Resource.MustColumn(column).SQLText()+" ASC")
		}
	}
	r.window.sortSeen = true
	return nil
}

func (r *planRenderer) renderFinalWindowSort(plan ir.PhysicalPlan, sort ir.PhysicalSort) error {
	position := 0
	expansion := 0
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalUnnestOp {
			continue
		}
		unnest := operation.Unnest
		if unnest == nil {
			return fmt.Errorf("%w: UNNEST payload is missing while the window orders the expansion set", ErrUnsupportedPostgresPlan)
		}
		lateral, ok := r.lateralAliasAt(expansion)
		if !ok {
			return fmt.Errorf("%w: stable row identity references expansion %d before its lateral bound", ErrUnsupportedPostgresPlan, expansion+1)
		}
		expansion++
		consumed, err := r.renderIdentitySegment(*unnest, lateral, sort.Keys, position)
		if err != nil {
			return err
		}
		position += consumed
	}
	if position != len(sort.Keys) {
		return fmt.Errorf("%w: SORT after UNNEST must carry the complete stable row identity tuple and nothing else: %d keys given for %d expected",
			ErrUnsupportedPostgresPlan, len(sort.Keys), position)
	}
	return nil
}

func (r *planRenderer) renderIdentitySegment(unnest ir.PhysicalUnnest, lateral string, keys []ir.PhysicalValue, position int) (int, error) {
	want := ir.PhysicalUnnestSortKeys(unnest)
	if position+len(want) > len(keys) {
		return 0, fmt.Errorf("%w: SORT after UNNEST must carry the complete stable row identity tuple and nothing else: %d keys given for %d expected",
			ErrUnsupportedPostgresPlan, len(keys), position+len(want))
	}
	consumed := 0
	check := func(wanted ir.PhysicalValue) error {
		got := keys[position+consumed]
		if !sameWindowIdentityValue(got, wanted) {
			return fmt.Errorf("%w: SORT key %d does not match the stable row identity tuple", ErrUnsupportedPostgresPlan, position+consumed)
		}
		consumed++
		return nil
	}
	rootTerm, err := r.windowRowIdentityTerm(unnest.Owner.RootVariable, "root")
	if err != nil {
		return 0, err
	}
	if err := check(want[consumed]); err != nil {
		return 0, err
	}
	r.window.keys = append(r.window.keys, rootTerm)
	for _, step := range unnest.Owner.Route {
		if step.Traversal.EdgeVariable == "" {
			continue
		}
		if err := check(want[consumed]); err != nil {
			return 0, err
		}
		edgeAlias, ok := r.window.edgeVars[step.Traversal.EdgeVariable]
		if !ok {
			return 0, fmt.Errorf("%w: stable row identity edge %q was not joined", ErrUnsupportedPostgresPlan, step.Traversal.EdgeVariable)
		}
		r.window.keys = append(r.window.keys, edgeAlias+"."+r.storage.Edge.MustColumn("edge_seq").SQLText()+" ASC")
	}
	if unnest.Owner.OwnerVariable != unnest.Owner.RootVariable {
		ownerTerm, err := r.windowRowIdentityTerm(unnest.Owner.OwnerVariable, "owner")
		if err != nil {
			return 0, err
		}
		if err := check(want[consumed]); err != nil {
			return 0, err
		}
		r.window.keys = append(r.window.keys, ownerTerm)
	}
	if err := check(want[consumed]); err != nil {
		return 0, err
	}
	r.window.keys = append(r.window.keys, "("+lateral+".rn IS NOT NULL) ASC")
	if unnest.Ordinality != "" {
		if err := check(want[consumed]); err != nil {
			return 0, err
		}
		r.window.keys = append(r.window.keys, lateral+".rn ASC")
	}
	return consumed, nil
}

func (r *planRenderer) windowRowIdentityTerm(variable, role string) (string, error) {
	scope, ok := r.rows[variable]
	if !ok || scope.derivedValue != "" || scope.alias == "" {
		return "", fmt.Errorf("%w: stable row identity %s variable %q does not bind a readable stored row",
			ErrUnsupportedPostgresPlan, role, variable)
	}
	return scope.alias + "." + r.storage.Resource.MustColumn("logical_id").SQLText() + " ASC", nil
}

func (r *planRenderer) lateralAliasAt(expansionIndex int) (string, bool) {
	if expansionIndex < 0 || expansionIndex >= len(r.lateralAliases) {
		return "", false
	}
	return r.lateralAliases[expansionIndex], true
}

func sameWindowIdentityValue(got, wanted ir.PhysicalValue) bool {
	if got.Variable != wanted.Variable || got.BindKey != wanted.BindKey || len(got.Path) != len(wanted.Path) {
		return false
	}
	for index := range wanted.Path {
		if got.Path[index] != wanted.Path[index] {
			return false
		}
	}
	return true
}

func (r *planRenderer) renderWindowLimit(plan ir.PhysicalPlan, limit ir.PhysicalLimit) error {
	if r.window.limitSeen {
		return fmt.Errorf("%w: at most one LIMIT", ErrUnsupportedPostgresPlan)
	}
	if r.window.hasUnnest && !r.window.sortSeen {
		return fmt.Errorf("%w: a LIMIT after UNNEST requires the complete stable row identity SORT first", ErrUnsupportedPostgresPlan)
	}
	value, ok := plan.BindVars[limit.BindKey].(int)
	if !ok || value <= 0 {
		return fmt.Errorf("%w: limit bind %q must be a positive int", ErrUnsupportedPostgresPlan, limit.BindKey)
	}
	index := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(value)}, "")
	r.window.limitClause = " LIMIT $" + strconv.Itoa(index)
	r.window.limitSeen = true
	return nil
}

func (r *planRenderer) windowOrder() []string {
	terms := make([]string, 0, len(r.window.keys)+2+len(r.edgeAliases)+2*len(r.lateralAliases))
	seen := make(map[string]bool, cap(terms))
	dedupe := len(r.lateralAliases) > 0
	add := func(term string) {
		if dedupe && seen[term] {
			return
		}
		seen[term] = true
		terms = append(terms, term)
	}
	for _, key := range r.window.keys {
		add(key)
	}
	add(r.rootAlias + "." + r.storage.Resource.MustColumn("resource_seq").SQLText() + " ASC")
	for _, edgeAlias := range r.edgeAliases {
		add(edgeAlias + "." + r.storage.Edge.MustColumn("edge_seq").SQLText() + " ASC")
	}
	for _, lateral := range r.lateralAliases {
		if r.window.sortSeen {
			add("(" + lateral + ".rn IS NOT NULL) ASC")
		}
		add(lateral + ".rn ASC")
	}
	for _, ordinal := range r.window.unpivotOrdinals {
		terms = append(terms, ordinal+" ASC")
	}
	return terms
}
