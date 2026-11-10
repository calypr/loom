package postgres

// window.go registers the deterministic execution window as the closed final
// operation slice of the PostgreSQL renderer (pg-sql-12): the one SORT and one
// LIMIT that follow every cardinality-changing operator, rendered over the
// COMPLETE typed row identity, not only the root ID. The AQL behavior being
// matched is the root execution window of render/aql/navigation_layout.go
// (lines 140-165: UNNEST is the cardinality boundary kept before the window
// "so a row-grain-aware compiler can put a database-side LIMIT after
// expansion"; SORT then LIMIT captured as rootWindow; LIMIT without SORT
// refused) as emitted by renderRootWindowOperation in render/aql/render.go
// (every key ASC, LIMIT @bind). The stable ordering contract being matched is
// the compiler-owned tuple ir.PhysicalUnnestSortKeys
// (ir/physical_operations.go:322-339), whose documentation fixes the rule
// this file renders: "expanded rows include their occurrence and owner-relative
// item witnesses after the root identity", enforced by
// validateGenericNavigationRootSort (navigation_layout.go:440-466: "SORT keys
// do not match the stable row identity tuple").
//
// Two window shapes are registered, and this file is their single contract
// owner — every SORT/LIMIT decision in the package flows through the entry
// points here (window discipline):
//
//   - W1, the spine window (no UNNEST): the merged pg-sql-02 contract, kept
//     byte-identical. At most one SORT of single typed-column keys from the
//     filterable column policy, ASC only, optionally followed by one bound
//     LIMIT. LIMIT without SORT stays accepted here exactly as merged: the
//     renderer-appended identity total order makes such pages deterministic,
//     so the hazard the AQL layout guard defends against (an unbounded
//     non-deterministic page) cannot arise on this path. The guard is not
//     widened onto W1 in this ticket; W2 requires SORT before LIMIT.
//
//   - W2, the final window after UNNEST: the plan may carry at most one SORT
//     and one LIMIT only after the last UNNEST (the closed sequence
//     [ROOT_SCAN, UNNEST+, SORT?, LIMIT?, RETURN] is enforced by
//     validateUnnestPlanSurface in unnest.go). When the plan carries UNNEST,
//     SORT keys must exactly equal — in order, key by key — the concatenation
//     over the plan's UNNEST operations in plan order of
//     ir.PhysicalUnnestSortKeys, and a LIMIT requires that SORT to be present
//     first. Each tuple position maps onto its storage identity expression:
//
//     * {row, ["_key"]} over a stored resource row  -> alias."logical_id" ASC
//       (the PostgreSQL counterpart of an Arango _key: unique per
//       (project, dataset_generation, resource_type) — migration 000002's
//       UNIQUE constraint — and the scan pins all three),
//     * {edge, ["_key"]} over a route edge variable -> alias."edge_seq" ASC,
//       the storage occurrence witness (edge_seq is the fhir_edge_occurrence
//       primary key; logical_edge_key is nullable and cannot be a total
//       order),
//     * {hasItem} (bare) -> (u_k.rn IS NOT NULL) ASC, the two-valued item
//       discriminator, false exactly on the OUTER pad row. It is placed
//       before the ordinality witness so a LIMIT that cuts an owner group
//       pages the pad row before real items, matching the AQL tuple, which
//       sorts the has-item witness immediately before ordinality and renders
//       every key ASC;
//     * {ordinality} (bare) -> u_k.rn ASC, the renderer-derived stable
//       zero-based item position (unnest.go), ordered by the WITH-ORDINALITY
//       positions of the expansion chain.
//
//     Nothing on this path reads plan strings into SQL: the plan names
//     variables, the renderer supplies every column from the trusted storage
//     allowlist and every lateral alias from its own counter. Sorting on an
//     expansion-derived variable (item, ancestors) is refused: derived scopes
//     are never orderable, and the exact-tuple check rejects them by
//     position. The plan-side _key spelling is accepted only as part of the
//     exact W2 tuple, never as a W1 key; resource_seq/edge_seq stay
//     renderer-owned ordering columns that no plan path can address.
//
// Total-order construction (assemble delegates here): the mapped plan keys
// first (plan order), then the renderer completeness witnesses — root
// resource_seq, every join edge_seq in join order, then per expansion in plan
// order the has-item witness (only when the window is open; a plan without a
// window keeps its merged pg-sql-09 statement byte-identical) and the rn.
// Exact-duplicate terms are deduplicated, which collapses the overlap between
// plan-mapped witnesses and renderer witnesses without changing meaning.
//
// Determinism argument: a final row is (root row, one edge row per join, one
// rn per expansion). resource_seq (primary key) distinguishes root rows;
// edge_seq (primary key) distinguishes occurrences within each join; rn is
// unique within its expansion group and (rn IS NOT NULL) separates the single
// OUTER pad row of a group from its items. The witness chain is therefore
// injective on the final row set, hence a strict total order, so every page
// cut by the bound LIMIT repeats identically across executions. The plan-side
// identity columns add the AQL-matching semantic order; they are redundant to
// injectivity, never load-bearing for it.
//
// Collation is contract-declared: logical_id sorts under the column
// collation of the source store while Arango sorts _key byte-wise. Page
// membership is guaranteed stable across PostgreSQL executions; byte-order
// agreement with an Arango page under collation-sensitive logical_id values is
// not claimed by this contract (the acceptance fixtures use deterministic
// ASCII identities, where the two orders coincide). Edge-granularity page
// order uses the storage edge_seq and does not claim to reproduce an Arango
// edge _key lexicographic page.
//
// The PreviewSourceWindowByRootID source window (windowing at the scan by
// root ID before expansion, render/aql/navigation_layout.go:440-449) is
// deliberately NOT implemented here: the PostgreSQL window is the final-row
// window, and the flag keeps its typed refusal in render.go. Everything
// outside the two registered shapes keeps failing closed with
// ErrUnsupportedPostgresPlan; there is no fallback to AQL and no AQL-to-SQL
// conversion.

import (
	"fmt"
	"strconv"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// executionWindow is the renderer-owned SORT/LIMIT state for one plan render.
// Every field is written only through the entry points in this file; no plan
// string reaches SQL text through them.
type executionWindow struct {
	// keys holds the mapped plan sort keys in plan order, each term already
	// suffixed " ASC" and built solely from storage-allowlisted columns and
	// renderer-minted lateral aliases.
	keys []string
	// limitClause is " LIMIT $n" once the bound LIMIT is accepted; empty
	// until then.
	limitClause string
	sortSeen    bool
	limitSeen   bool
	// hasUnnest is set when a registered UNNEST has rendered, so the window
	// entry points know whether they are completing W1 or W2.
	hasUnnest bool
	// edgeVars maps a plan edge variable to the renderer-minted alias of its
	// join, so the route occurrence witness can resolve the variable the plan
	// names without ever trusting a plan-supplied identifier.
	edgeVars map[string]string
}

// rememberEdge records the variable-to-alias binding of one typed-identity
// join as renderTraversal mints it. Re-binding one variable would silently
// split the occurrence witness, so a duplicate is refused.
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

// renderWindowSort is the single SORT entry point. Dispatch (renderOperations
// in render.go) has already proved the arms (at most one SORT, before LIMIT,
// inside the phase machine); this method enforces the key contract of the
// window shape the rendered operations define: W2's exact stable row identity
// tuple once an UNNEST has rendered, otherwise the merged W1 typed-column
// contract, byte-for-byte.
func (r *planRenderer) renderWindowSort(plan ir.PhysicalPlan, sort ir.PhysicalSort) error {
	if r.window.sortSeen {
		return fmt.Errorf("%w: at most one SORT", ErrUnsupportedPostgresPlan)
	}
	if r.window.hasUnnest {
		if err := r.renderFinalWindowSort(plan, sort); err != nil {
			return err
		}
	} else {
		for _, key := range sort.Keys {
			scope, ok := r.rows[key.Variable]
			if !ok {
				return fmt.Errorf("%w: sort variable %q does not bind a readable row", ErrUnsupportedPostgresPlan, key.Variable)
			}
			if scope.optional {
				// NULL-extended child columns sort as a distinct witness a plan
				// never declared; deterministic row order belongs to the root
				// identity plus the renderer-owned edge witnesses.
				return fmt.Errorf("%w: sort key on %q, a row materialized by an OPTIONAL traversal LEFT JOIN LATERAL, is not registered; sorting expanded child rows is a later capability",
					ErrUnsupportedPostgresPlan, key.Variable)
			}
			if key.BindKey != "" || len(key.Path) != 1 || !resourceFilterColumns[key.Path[0]] {
				return fmt.Errorf("%w: sort keys must be single typed columns; payload-path sorting is not yet implemented",
					ErrUnsupportedPostgresPlan)
			}
			r.window.keys = append(r.window.keys, scope.alias+"."+r.storage.Resource.MustColumn(key.Path[0]).SQLText()+" ASC")
		}
	}
	r.window.sortSeen = true
	return nil
}

// renderFinalWindowSort renders the W2 SORT. It walks the plan's UNNEST
// operations in plan order and, for each, derives the expected
// ir.PhysicalUnnestSortKeys segment positionally: every position must equal
// the plan's key (variable, bind, path) exactly before that position's
// storage term is emitted, so a mismatched or truncated tuple can never
// render a partially trusted ORDER BY.
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

// renderIdentitySegment compares and maps one expansion's identity segment
// (root, route edges in route order, owner when distinct, has-item,
// ordinality when set) and returns how many plan keys it consumed.
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

// windowRowIdentityTerm maps a stored-row identity key onto its logical_id
// column. A variable that does not bind a stored row (unknown, derived, or
// join-less) can never anchor the total order and is refused.
func (r *planRenderer) windowRowIdentityTerm(variable, role string) (string, error) {
	scope, ok := r.rows[variable]
	if !ok || scope.derivedValue != "" || scope.alias == "" {
		return "", fmt.Errorf("%w: stable row identity %s variable %q does not bind a readable stored row",
			ErrUnsupportedPostgresPlan, role, variable)
	}
	return scope.alias + "." + r.storage.Resource.MustColumn("logical_id").SQLText() + " ASC", nil
}

// lateralAliasAt reports the renderer-minted alias of the expansionIndex-th
// lateral expansion (plan order). The aliases are appended by renderUnnest as
// each expansion renders, so an out-of-range index means the window references
// an expansion that never bound.
func (r *planRenderer) lateralAliasAt(expansionIndex int) (string, bool) {
	if expansionIndex < 0 || expansionIndex >= len(r.lateralAliases) {
		return "", false
	}
	return r.lateralAliases[expansionIndex], true
}

// sameWindowIdentityValue is the exact plan-key comparison of the AQL tuple
// guard (render/aql/navigation_layout.go sameRenderPhysicalValue): variable,
// bind, and path element by element.
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

// renderWindowLimit is the single LIMIT entry point. W2 requires the stable
// row identity SORT first — the counterpart of the AQL layout guard
// (navigation_layout.go:161-163, "LIMIT requires deterministic root SORT")
// and of the reshaped-window guard (ir/physical_validation.go:125-129,
// "LIMIT requires an immediately preceding stable row SORT"). W1 keeps the
// merged spine behavior, where the renderer-appended total order already
// bounds the page deterministically (window.go header, W1 note).
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
	index := r.addParameter(compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterBigint, Value: int64(value)}, "")
	r.window.limitClause = " LIMIT $" + strconv.Itoa(index)
	r.window.limitSeen = true
	return nil
}

// windowOrder assembles the complete ORDER BY term chain: mapped plan keys in
// plan order, then the renderer completeness witnesses in contract order
// (root resource_seq, join edge_seq, per-expansion has-item — only while a
// window is open — and rn).
//
// Exact-term dedup runs only when the plan carries expansions
// (len(r.lateralAliases) > 0), where a plan-mapped key and a renderer-minted
// witness legitimately name the same column (for example the shared root
// logical_id term across two expansions) and must contribute one term. With
// no expansions (the W1 spine) the renderer witnesses — resource_seq and
// edge_seq, both outside resourceFilterColumns — can never collide with a
// typed-column plan key, so plan keys are emitted verbatim: a plan with
// duplicate SORT keys keeps its duplicate ORDER BY terms, exactly as the
// merged pg-sql-02 renderer emitted them.
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
	return terms
}
