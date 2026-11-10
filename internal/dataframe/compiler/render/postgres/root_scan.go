package postgres

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// This file registers the scoped direct root collection scan as an
// implemented, closed PostgreSQL operation (pg-sql-01). It is the
// renderer-facing capability registration point for the operation named by
// the physical IR's PhysicalRootScan: one direct scan of the agreed
// resource relation (ir.PostgresResourceLogicalTable, migration
// 000002_fhir_resource.sql) narrowed by the explicit project, dataset
// generation, and auth scope binds, filtered to one resource type, ordered
// by the immutable storage identity, and returned through positional
// projections of typed scope columns.
//
// The slice is deliberately closed. Traversal, filters, sorts, limits,
// selector extraction, population/cohort/candidate-seed roots, expression
// projections, and every other plan shape are outside this operation and
// fail closed with a typed ErrUnsupportedPostgresPlan; callers must surface
// them as capability gaps. Nothing here falls back to the AQL renderer and
// there is no AQL-to-SQL conversion: a rejected plan yields no SQL, and the
// only way to PostgreSQL statement text is through the identifier registry
// plus $n parameters.
//
// Planner-facing command registration (the compiler's target-aware PostgreSQL compile path)
// stays fail-closed for PostgreSQL until a PostgreSQL command adapter is
// registered by an execution slice; until then capability discovery must
// keep reporting PostgreSQL as unsupported at that gate, and this entry
// point — not command dispatch — is where the root-scan operation is
// implemented and registered.
//
// Deterministic statement and argument order, storage-binding trust, and
// the scope re-reading rules are inherited unchanged from
// RenderPhysicalPlan, which this entry validates the closed shape against
// and delegates to. That makes this file the single authoritative
// definition of the root-scan SQL shape downstream slices build on.

// RenderRootScan renders exactly one scoped direct root collection scan of
// plan onto the trusted PostgreSQL storage binding.
//
// The accepted plan shape is exactly two operations: a leading ROOT_SCAN
// with a direct scan payload (no population, cohort, or candidate-seed
// source) followed by a terminal RETURN. Every RETURN projection must read
// exactly one direct typed scope column of the root row
// (ir.PhysicalValue{Variable: <root variable>, Path: [<one typed column>]});
// selector extraction, expression projections, presence sidecars, and
// hidden projections are outside this slice. Plan scope binds are re-read,
// never trusted downstream: "project" and "dataset_generation" must be
// present strings and the auth mode must be explicit
// ("auth_resource_paths_unrestricted" bool plus "auth_resource_paths"
// []string), with restricted mode binding the exact authorized path set and
// an empty restricted set reading nothing rather than widening. The root
// collection bind must resolve to one non-empty resource type string.
//
// Everything the plan carries as data — project, generation, auth paths,
// and resource type — becomes an ordered typed $n parameter; only
// registry-validated identifiers appear in SQL text. The result is
// deterministic: the same plan and binding always yield byte-identical SQL
// and value-identical ordered arguments. Failures are typed:
// ErrUnsupportedPostgresPlan for shapes outside this slice,
// ErrInvalidPostgresPlanScope for missing or contradictory scope binds, and
// ir.ErrUnregisteredPostgresIdentifier for a binding that did not come from
// ir.NewPostgresStorageBinding. A rejected plan never produces SQL and
// never routes to AQL.
func RenderRootScan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := validateRootScanOperation(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	// The closed shape passed, so RenderPhysicalPlan's own gates (binding
	// trust, plan validation, engine/ClickHouse/stage refusals, scope
	// re-reading, deterministic assembly) all operate over a plan this
	// slice has already pinned to one direct root scan with typed
	// projections. There is no second SQL builder here by design: one
	// statement-shape authority keeps this registration and the full read
	// spine byte-identical for the shapes they share.
	return RenderPhysicalPlan(plan, storage)
}

// validateRootScanOperation enforces the closed root-scan slice before any
// rendering work begins. Every refusal is an ErrUnsupportedPostgresPlan so
// callers classify the whole family as a capability gap, and the root-scan
// payload checks run first so a malformed plan can never reach a payload
// dereference downstream.
func validateRootScanOperation(plan ir.PhysicalPlan) error {
	if len(plan.Operations) != 2 {
		return fmt.Errorf(
			"%w: the root-scan slice is exactly one ROOT_SCAN followed by one RETURN, got %d operation(s); filters, sorts, limits, traversals, and every other operation belong to the full read spine or to a later capability, not to RenderRootScan",
			ErrUnsupportedPostgresPlan, len(plan.Operations))
	}
	rootOperation, returnOperation := plan.Operations[0], plan.Operations[1]
	if rootOperation.Kind != ir.PhysicalRootScanOp || rootOperation.RootScan == nil {
		return fmt.Errorf("%w: the leading operation must be ROOT_SCAN with its payload set; the root-scan slice never renders a traversal, a collection scan, or a payload-less ROOT_SCAN",
			ErrUnsupportedPostgresPlan)
	}
	if returnOperation.Kind != ir.PhysicalReturnOp || returnOperation.Return == nil {
		return fmt.Errorf("%w: the terminal operation must be RETURN with its payload set", ErrUnsupportedPostgresPlan)
	}

	scan := rootOperation.RootScan
	if scan.Population != nil || scan.CohortSource != nil || scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: population, cohort, and candidate-seed roots are not registered for PostgreSQL; only the direct root collection scan has a rendered contract",
			ErrUnsupportedPostgresPlan)
	}
	if scan.Variable == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name its row variable", ErrUnsupportedPostgresPlan)
	}
	if scan.CollectionBindKey == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name the bind carrying the single resource type", ErrUnsupportedPostgresPlan)
	}
	// The resource type is runtime data and is re-read here, at the slice
	// boundary, exactly as the renderer will bind it: anything that is not
	// one non-empty string is a capability gap, never a coercion.
	if resourceType, ok := plan.BindVars[scan.CollectionBindKey].(string); !ok || strings.TrimSpace(resourceType) == "" {
		return fmt.Errorf("%w: root collection bind %q must name one resource type as a non-empty string",
			ErrUnsupportedPostgresPlan, scan.CollectionBindKey)
	}

	if len(returnOperation.Return.Projections) == 0 {
		return fmt.Errorf("%w: the root-scan RETURN must project at least one typed scope column", ErrUnsupportedPostgresPlan)
	}
	for _, projection := range returnOperation.Return.Projections {
		if strings.TrimSpace(projection.Name) == "" {
			return fmt.Errorf("%w: projection names are required output metadata (never SQL text) and must be set in the root-scan slice",
				ErrUnsupportedPostgresPlan)
		}
		if projection.Expression != nil {
			return fmt.Errorf("%w: projection %q uses an expression form; selector and expression projections belong to the extraction capability, not to the root-scan slice",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		if projection.Presence != nil || projection.PresenceOutput || projection.Hidden {
			return fmt.Errorf("%w: projection %q carries presence or hidden metadata; sidecars are outside the root-scan slice",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		value := projection.Value
		if value.BindKey != "" {
			return fmt.Errorf("%w: projection %q must read a row column, not a bind value", ErrUnsupportedPostgresPlan, projection.Name)
		}
		if value.Variable != scan.Variable {
			return fmt.Errorf("%w: projection %q reads variable %q; the root-scan slice may only read the root row variable %q",
				ErrUnsupportedPostgresPlan, projection.Name, value.Variable, scan.Variable)
		}
		if len(value.Path) != 1 || !resourceFilterColumns[value.Path[0]] {
			return fmt.Errorf("%w: projection %q must read exactly one typed scope column of the root row; payload-path extraction is a separate capability",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
	}
	return nil
}
