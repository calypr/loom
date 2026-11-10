package postgres

// Canonical scope enforcement for the PostgreSQL renderer (pg-sql-02).
//
// This file is the single definition in this package of how the plan's
// canonical scope predicates are mapped into root and edge SQL, before any
// projection is assembled. The canonical contract is the one the IR proves
// for navigation plans in ir.ValidateGenericPhysicalPlanScope
// (internal/dataframe/compiler/ir/scope.go) and the AQL renderer demands in
// render/aql (validateGenericNavigationScopeBlock): for every resource row a
// plan reads — the root row, an edge row, and the reached-target row of a
// traversal — the scope is exactly, and in order,
//
//	project            == @project                      (non-empty string)
//	dataset_generation == @dataset_generation           (string; "" is the
//	                                                      legacy namespace)
//	AUTH_RESOURCE_PATH_ALLOWED over <row>.auth_resource_path,
//	@auth_resource_paths, @auth_resource_paths_unrestricted
//	followed by the equality against @scope_allowed
//
// PostgreSQL renders that same block as typed WHERE/ON predicates instead
// of FILTER clauses, with one important renderer-side rule: the renderer
// re-reads the scope binds from the plan and never trusts plan-authored
// scope operations. loadScope below is therefore the only place scope is
// established, every scope value becomes an ordered typed $n parameter, and
// every defect is a typed ErrInvalidPostgresPlanScope refusal that yields no
// SQL. There is no implicit auth scope, no AQL fallback, and no AQL-to-SQL
// conversion here: scope defects fail closed.
//
// Authorization is deny-by-default in the SQL mapping:
//
//   - Restricted mode (auth_resource_paths_unrestricted bound false) binds
//     the exact authorized path set as one JSONB array parameter and emits
//     auth_resource_path = ANY (SELECT jsonb_array_elements_text($n::jsonb))
//     for every row of the scope, edge rows included. A path outside the
//     bound set never reads.
//   - An empty (or nil) restricted set is a legitimate authorization state
//     that reads nothing: it binds the JSON empty array literally, never a
//     SQL or JSON null, so the ANY probe is false for every row rather than
//     widening or erroring.
//   - Unrestricted mode must be requested explicitly; it then adds no auth
//     predicate at all and must not carry paths.
//   - The block's closing equality against @scope_allowed is the fifth
//     canonical bind and is re-read like the other four. It must be present
//     as a bool: true renders the allowed shapes above unchanged; false is
//     the repository-wide canonical deny (ir/prefix.go binds it as the fifth
//     required scope key, render/aql and ir demand the closing FILTER,
//     ir/whole_relation_scope_evidence.go and compiler/category_scan_related.go
//     treat anything but true as denied) and maps to a literal zero-row
//     predicate: every row source — root, edge, and reached target — ends
//     its scope chain with AND FALSE, mirroring AQL reading zero rows through
//     FILTER <var> == @scope_allowed. The deny is a shape fixed by a
//     validated bind state, not plan data: it never becomes a parameter and
//     never copies a value into SQL text. Absence or a mistyped value is a
//     plan defect refused before any parameter is bound.
//
// The statement-shape authority stays where it is: root_scan.go plus
// RenderPhysicalPlan assemble statements, and root_scan_test.go plus
// render_test.go pin their literals. The functions here are the reusable
// predicate mapping those authorities call — for the root row and the
// reached-target row through scopedResourcePredicate, and for edge-occurrence
// rows through scopedEdgePredicate, which the edge-join renderer and the
// later traversal slice (pg-sql-06, which depends on this ticket) build on.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// Plan-level bind keys shared with the canonical lowering path
// (internal/dataframe/compiler/lower): every scoped plan carries these
// scope binds, and the PostgreSQL renderer re-reads them instead of trusting
// plan-authored predicates.
const (
	projectBindKey        = "project"
	datasetGenerationBind = "dataset_generation"
	authPathsBindKey      = "auth_resource_paths"
	authUnrestrictedBind  = "auth_resource_paths_unrestricted"
	scopeAllowedBind      = "scope_allowed"
)

// planScope is the immutable, re-read scope of one plan: the placeholder
// indices of the three canonical scope parameters established by loadScope
// plus the canonical deny state of the fifth bind. An index of 0 means
// "predicate not emitted", which for authPathsIndex is reachable only
// through explicitly requested unrestricted mode. denied is set when
// scope_allowed was bound false, the canonical zero-row state.
type planScope struct {
	projectIndex    int
	generationIndex int
	authPathsIndex  int // 0 when unrestricted mode was explicit
	denied          bool
}

// loadScope binds the explicit plan scope. Scope binds must be present with
// their canonical types; nothing is defaulted. This is the package's single
// scope gate: RenderPhysicalPlan and every registered slice enter through
// it before a single predicate is emitted, so a plan cannot reach assembly
// with a missing, mistyped, or contradictory scope.
func (r *planRenderer) loadScope(plan ir.PhysicalPlan) error {
	project, ok := plan.BindVars[projectBindKey].(string)
	if !ok || strings.TrimSpace(project) == "" {
		return fmt.Errorf("%w: %q bind must be a non-empty string", ErrInvalidPostgresPlanScope, projectBindKey)
	}
	generation, ok := plan.BindVars[datasetGenerationBind].(string)
	if !ok {
		return fmt.Errorf("%w: %q bind must be a string (the empty string is the legacy namespace)",
			ErrInvalidPostgresPlanScope, datasetGenerationBind)
	}
	unrestricted, ok := plan.BindVars[authUnrestrictedBind].(bool)
	if !ok {
		return fmt.Errorf("%w: %q bind must be set explicitly; there is no implicit auth scope",
			ErrInvalidPostgresPlanScope, authUnrestrictedBind)
	}
	paths, ok := plan.BindVars[authPathsBindKey].([]string)
	if !ok {
		return fmt.Errorf("%w: %q bind must be a []string", ErrInvalidPostgresPlanScope, authPathsBindKey)
	}
	if unrestricted && len(paths) != 0 {
		return fmt.Errorf("%w: unrestricted mode must not carry auth resource paths (%d given)",
			ErrInvalidPostgresPlanScope, len(paths))
	}
	// The canonical block closes with the equality against @scope_allowed
	// (ir/scope.go findAuthScopeEquality), so it is re-read here like the
	// other four binds, after them and still before any parameter is bound.
	// false is a legitimate plan state — the repository-wide canonical deny
	// that reads zero rows — while absence or a mistyped value is a plan
	// defect: there is no implicit allow.
	allowed, ok := plan.BindVars[scopeAllowedBind].(bool)
	if !ok {
		return fmt.Errorf("%w: %q bind must be set explicitly to a boolean; false is the canonical deny state (zero rows) and absence or a mistyped value is a plan defect",
			ErrInvalidPostgresPlanScope, scopeAllowedBind)
	}

	r.scope = planScope{
		projectIndex:    r.textParameter(project, "text:"+project),
		generationIndex: r.textParameter(generation, "text:"+generation),
		denied:          !allowed,
	}
	if !unrestricted {
		copied := append([]string(nil), paths...)
		raw, err := json.Marshal(copied)
		if err != nil {
			return fmt.Errorf("%w: auth resource paths are not JSON-encodable: %v", ErrInvalidPostgresPlanScope, err)
		}
		// A restricted scope with no authorized paths must read nothing, not
		// error and not widen: an empty Go slice marshals to JSON null when
		// nil, which is not an array, and jsonb_array_elements_text over a
		// jsonb null scalar raises "cannot extract elements from a scalar".
		// Binding the empty array literally makes the ANY probe false for
		// every row, so deny-all is zero rows.
		if string(raw) == "null" {
			raw = json.RawMessage("[]")
		}
		r.scope.authPathsIndex = r.jsonbParameter(raw)
	}
	return nil
}

// scopedResourcePredicate renders the scope equality chain for one resource
// row, including the explicit auth mode. Restricted mode binds the exact
// authorized path set (an empty set reads nothing and never widens);
// unrestricted mode was explicitly requested and adds no path predicate. A
// canonical deny (scope_allowed bound false) closes the chain with a literal
// zero-row conjunct, so no resource row ever reads. This is the one
// definition used for both the root row and the reached target row of an
// edge join. A statement whose root carries the cohort guards must use
// scopedResourcePredicateDenyConjunct instead: a literal FALSE in the shared
// top-level AND tree folds the whole tree away and prunes the guard
// InitPlans (population.go explains the stable-false deny conjunct it
// passes).
func (r *planRenderer) scopedResourcePredicate(alias string, typeIndex int) string {
	return r.scopedResourcePredicateDenyConjunct(alias, typeIndex, " AND FALSE")
}

// scopedResourcePredicateDenyConjunct renders the same canonical chain with
// the deny arm supplied by the caller. denyConjunct is used only when
// scope_allowed was bound false and must be a literal always-false
// expression -- never a bind parameter: the deny is shape-fixed.
func (r *planRenderer) scopedResourcePredicateDenyConjunct(alias string, typeIndex int, denyConjunct string) string {
	var builder strings.Builder
	builder.WriteString(alias + "." + r.storage.Resource.MustColumn("project").SQLText() + " = $" + strconv.Itoa(r.scope.projectIndex))
	builder.WriteString(" AND " + alias + "." + r.storage.Resource.MustColumn("dataset_generation").SQLText() + " = $" + strconv.Itoa(r.scope.generationIndex))
	if r.scope.authPathsIndex != 0 {
		builder.WriteString(" AND " + alias + "." + r.storage.Resource.MustColumn("auth_resource_path").SQLText())
		builder.WriteString(" = ANY (SELECT jsonb_array_elements_text($" + strconv.Itoa(r.scope.authPathsIndex) + "::jsonb))")
	}
	if typeIndex != 0 {
		builder.WriteString(" AND " + alias + "." + r.storage.Resource.MustColumn("resource_type").SQLText() + " = $" + strconv.Itoa(typeIndex))
	}
	if r.scope.denied {
		builder.WriteString(denyConjunct)
	}
	return builder.String()
}

// scopedEdgePredicate renders the canonical scope chain for one
// edge-occurrence row: the same project, dataset-generation, and auth-mode
// predicates as a resource row, over the edge table's own typed scope
// columns. An edge outside the scope never joins, regardless of whether its
// endpoints are authorized — the canonical block puts AUTH_RESOURCE_PATH_-
// ALLOWED over the edge variable as well as the target variable
// (ir/physicalScopeResourceForOperation, PhysicalTraversalOp case), and the
// AQL renderer enforces the same edge auth (render/aql
// validateGenericNavigationScopeBlock).
//
// Traversal-payload predicates (label, anchored/reached endpoint identity)
// are not canonical scope and are appended by the edge-join renderer itself;
// this function is the whole canonical edge scope, so the later traversal
// slice (pg-sql-06) must start its edge join here rather than re-deriving
// these predicates. A canonical deny (scope_allowed bound false) closes the
// chain with a literal zero-row conjunct, so no edge occurrence ever joins.
func (r *planRenderer) scopedEdgePredicate(alias string) string {
	columns := r.storage.Edge
	predicate := alias + "." + columns.MustColumn("project").SQLText() + " = $" + strconv.Itoa(r.scope.projectIndex)
	predicate += " AND " + alias + "." + columns.MustColumn("dataset_generation").SQLText() + " = $" + strconv.Itoa(r.scope.generationIndex)
	if r.scope.authPathsIndex != 0 {
		predicate += " AND " + alias + "." + columns.MustColumn("auth_resource_path").SQLText() +
			" = ANY (SELECT jsonb_array_elements_text($" + strconv.Itoa(r.scope.authPathsIndex) + "::jsonb))"
	}
	if r.scope.denied {
		predicate += " AND FALSE"
	}
	return predicate
}
