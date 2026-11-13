package postgres

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func RenderRootScan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := validateRootScanOperation(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return RenderPhysicalPlan(plan, storage)
}

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
