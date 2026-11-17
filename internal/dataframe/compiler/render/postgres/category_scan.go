package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const (
	CategoryScanPresentColumn = "present"
	CategoryScanValueColumn   = "value"

	maxCategoryScanValues = 256

	categorySourceCTE   = "__loom_category_source"
	categoryRowsCTE     = "__loom_category_rows"
	categoryDistinctCTE = "__loom_category_distinct"
	categoryRowAlias    = "__loom_category_row"
)

type RenderedCategoryScan struct {
	Values               command.RenderedPostgresPhysicalPlan
	Witness              command.RenderedPostgresPhysicalPlan
	PresentColumn        string
	ValueColumn          string
	PresenceMarkerColumn string
}

func RenderCategoryScan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding, columnName, presenceMarkerColumn string, maxValues int) (RenderedCategoryScan, error) {
	if err := storage.Validate(); err != nil {
		return RenderedCategoryScan{}, err
	}
	if strings.TrimSpace(columnName) == "" {
		return RenderedCategoryScan{}, fmt.Errorf("%w: a category scan names the column whose distinct values it enumerates", ErrUnsupportedPostgresPlan)
	}
	if maxValues < 1 || maxValues > maxCategoryScanValues {
		return RenderedCategoryScan{}, fmt.Errorf("%w: category scan maxValues %d is outside the registered 1..%d bound",
			ErrUnsupportedPostgresPlan, maxValues, maxCategoryScanValues)
	}
	narrowed, err := categoryScanInnerPlan(plan, columnName, presenceMarkerColumn)
	if err != nil {
		return RenderedCategoryScan{}, err
	}
	inner, err := RenderPhysicalPlan(narrowed, storage)
	if err != nil {
		return RenderedCategoryScan{}, err
	}
	valueIndex, err := categoryOutputIndex(inner.OutputColumns, columnName)
	if err != nil {
		return RenderedCategoryScan{}, err
	}
	presenceIndex, err := categoryOutputIndex(inner.OutputColumns, presenceMarkerColumn)
	if err != nil {
		return RenderedCategoryScan{}, err
	}
	values, err := composeCategoryStatement(inner, valueIndex, presenceIndex, maxValues+1, true)
	if err != nil {
		return RenderedCategoryScan{}, err
	}
	witness, err := composeCategoryStatement(inner, valueIndex, presenceIndex, maxValues*4+1, false)
	if err != nil {
		return RenderedCategoryScan{}, err
	}
	return RenderedCategoryScan{
		Values: values, Witness: witness,
		PresentColumn: CategoryScanPresentColumn, ValueColumn: CategoryScanValueColumn,
		PresenceMarkerColumn: presenceMarkerColumn,
	}, nil
}

func categoryScanInnerPlan(plan ir.PhysicalPlan, columnName, presenceMarkerColumn string) (ir.PhysicalPlan, error) {
	if plan.StageSequence != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("%w: category scans over composed construction stages are not registered for PostgreSQL; only the read-spine shape has a rendered category terminal (pg-sql-25)",
			ErrUnsupportedPostgresPlan)
	}
	if len(plan.Operations) == 0 {
		return ir.PhysicalPlan{}, fmt.Errorf("%w: a category scan requires a root scan and a terminal RETURN", ErrUnsupportedPostgresPlan)
	}
	returnIndex := -1
	sortCount := 0
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 || operation.RootScan == nil {
				return ir.PhysicalPlan{}, fmt.Errorf("%w: a category scan requires exactly one leading ROOT_SCAN with its payload set", ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalFilterOp:
			if operation.Filter == nil {
				return ir.PhysicalPlan{}, fmt.Errorf("%w: operation %d: filter payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet == nil {
				return ir.PhysicalPlan{}, fmt.Errorf("%w: operation %d: derived LET payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalSortOp:
			if operation.Sort == nil {
				return ir.PhysicalPlan{}, fmt.Errorf("%w: operation %d: sort payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			sortCount++
			if sortCount > 1 {
				return ir.PhysicalPlan{}, fmt.Errorf("%w: at most one SORT is accepted; the category page order is owned by the distinct layer", ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalReturnOp:
			if operation.Return == nil || index != len(plan.Operations)-1 {
				return ir.PhysicalPlan{}, fmt.Errorf("%w: a category scan requires exactly one terminal RETURN", ErrUnsupportedPostgresPlan)
			}
			returnIndex = index
		case ir.PhysicalLimitOp:
			return ir.PhysicalPlan{}, fmt.Errorf("%w: a plan-carried LIMIT would bound the page before the distinct, which is not the category contract; category pages are bounded by maxValues+1",
				ErrUnsupportedPostgresPlan)
		default:
			return ir.PhysicalPlan{}, fmt.Errorf("%w: operation %q is outside the registered PostgreSQL category surface (root scan, filters, canonical auth LET, sort, return)",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if returnIndex < 0 {
		return ir.PhysicalPlan{}, fmt.Errorf("%w: a category scan requires a terminal RETURN", ErrUnsupportedPostgresPlan)
	}

	projections := plan.Operations[returnIndex].Return.Projections
	if strings.TrimSpace(presenceMarkerColumn) == "" || presenceMarkerColumn == columnName {
		return ir.PhysicalPlan{}, fmt.Errorf("%w: the presence marker column must be a distinct non-blank name", ErrUnsupportedPostgresPlan)
	}
	var category *ir.PhysicalProjection
	for index := range projections {
		projection := &projections[index]
		if projection.Name == presenceMarkerColumn {
			return ir.PhysicalPlan{}, fmt.Errorf("%w: projection %q collides with the renderer-owned presence marker column",
				ErrUnsupportedPostgresPlan, presenceMarkerColumn)
		}
		if projection.Name != columnName {
			continue
		}
		if category != nil {
			return ir.PhysicalPlan{}, fmt.Errorf("%w: %d projections are named %q; a category column must be projected exactly once",
				ErrUnsupportedPostgresPlan, 2, columnName)
		}
		if projection.Hidden {
			return ir.PhysicalPlan{}, fmt.Errorf("%w: category column %q is hidden executor-side metadata and is not a public category",
				ErrUnsupportedPostgresPlan, columnName)
		}
		category = projection
	}
	if category == nil {
		return ir.PhysicalPlan{}, fmt.Errorf("%w: the terminal RETURN does not project the category column %q", ErrUnsupportedPostgresPlan, columnName)
	}
	path, rowVariable, err := categorySelectorEvidence(*category)
	if err != nil {
		return ir.PhysicalPlan{}, err
	}

	out := ir.ClonePhysicalPlan(plan)
	operations := make([]ir.PhysicalOperation, 0, len(out.Operations))
	for _, operation := range out.Operations {
		if operation.Kind == ir.PhysicalSortOp {
			continue
		}
		operations = append(operations, operation)
	}
	returnIndex = len(operations) - 1
	returnOp := *operations[returnIndex].Return
	returnOp.Projections = []ir.PhysicalProjection{
		*category,
		{
			Name:           presenceMarkerColumn,
			PresenceOutput: true,
			Presence: &ir.PhysicalProjectionPresence{
				Source: ir.PhysicalValue{Variable: rowVariable, Path: []string{"payload"}},
				Paths:  [][]string{append([]string(nil), path...)},
			},
		},
	}
	operations[returnIndex].Return = &returnOp
	out.Operations = operations
	return out, nil
}

func categorySelectorEvidence(projection ir.PhysicalProjection) ([]string, string, error) {
	if projection.Expression == nil || projection.Expression.Kind != ir.PhysicalExtractExpression ||
		projection.Expression.Cardinality != ir.PhysicalScalarCardinality || projection.Expression.Extract == nil {
		return nil, "", fmt.Errorf("%w: category column %q must read the document through a scalar-cardinality EXTRACT selector; derived CALL, OBJECT, SLICE, unit-normalized, and typed-column reads have no registered category reading",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	extract := projection.Expression.Extract
	if extract.ExecutionMode != ir.PhysicalSelectorDirectScalar {
		return nil, "", fmt.Errorf("%w: category column %q uses selector execution mode %q; only the registered DIRECT_SCALAR selector has a category reading",
			ErrUnsupportedPostgresPlan, projection.Name, string(extract.ExecutionMode))
	}
	if len(extract.Fallbacks) != 0 || extract.Prepared != nil || extract.Distinct || extract.UnitNormalization != nil ||
		extract.Selector.Filter != nil {
		return nil, "", fmt.Errorf("%w: category column %q uses a selector shape outside the registered single-path direct scalar form",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if extract.Source.BindKey != "" || extract.Source.Variable == "" ||
		len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" {
		return nil, "", fmt.Errorf(`%w: category column %q must extract from the row document at ["payload"], the exact source shape lowering emits; got %v`,
			ErrUnsupportedPostgresPlan, projection.Name, extract.Source.Path)
	}
	if len(extract.Selector.Steps) == 0 {
		return nil, "", fmt.Errorf("%w: category column %q has an empty selector", ErrUnsupportedPostgresPlan, projection.Name)
	}
	path := make([]string, 0, len(extract.Selector.Steps))
	for index, step := range extract.Selector.Steps {
		if step.Field == "" || step.Iterate || step.Index != nil {
			return nil, "", fmt.Errorf("%w: category column %q: selector step %d is not a plain property; repeated and indexed steps have no category reading",
				ErrUnsupportedPostgresPlan, projection.Name, index)
		}
		if !payloadPathSegmentPattern.MatchString(step.Field) {
			return nil, "", fmt.Errorf("%w: category column %q: selector step %q is not renderer-safe",
				ErrUnsupportedPostgresPlan, projection.Name, step.Field)
		}
		path = append(path, step.Field)
	}
	if presence := projection.Presence; presence != nil {
		if presence.Source.Variable != extract.Source.Variable || presence.Source.BindKey != "" ||
			len(presence.Source.Path) != 1 || presence.Source.Path[0] != "payload" ||
			len(presence.Paths) != 1 || strings.Join(presence.Paths[0], "\x1f") != strings.Join(path, "\x1f") {
			return nil, "", fmt.Errorf("%w: category column %q: presence metadata must prove the selector's own path",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
	}
	return path, extract.Source.Variable, nil
}

func categoryOutputIndex(declared []string, name string) (int, error) {
	found := -1
	for index, candidate := range declared {
		if candidate == name {
			if found >= 0 {
				return 0, fmt.Errorf("%w: the inner read declares %q twice; a category column must be declared exactly once",
					ErrUnsupportedPostgresPlan, name)
			}
			found = index
		}
	}
	if found < 0 {
		return 0, fmt.Errorf("%w: the inner read did not declare %q; the category statement cannot be composed",
			ErrUnsupportedPostgresPlan, name)
	}
	return found, nil
}

func composeCategoryStatement(inner command.RenderedPostgresPhysicalPlan, valueIndex, presenceIndex, bound int, ordered bool) (command.RenderedPostgresPhysicalPlan, error) {
	if bound < 1 {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf("%w: category bound %d is not positive", ErrUnsupportedPostgresPlan, bound)
	}
	valueAlias := `"` + innerColumnAlias(valueIndex) + `"`
	presenceAlias := `"` + innerColumnAlias(presenceIndex) + `"`
	presentColumn := `"` + CategoryScanPresentColumn + `"`
	valueColumn := `"` + CategoryScanValueColumn + `"`

	args := append([]command.PostgreSQLParameter(nil), inner.Args...)
	args = append(args, command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(bound)})
	boundParameter := "$" + strconv.Itoa(len(args))

	statement := "WITH " + categorySourceCTE + " AS (" + inner.SQL + "), " +
		categoryRowsCTE + " AS (SELECT " + categoryRowAlias + "." + presenceAlias + " AS " + presentColumn +
		", CASE WHEN " + categoryRowAlias + "." + presenceAlias + " THEN " + categoryRowAlias + "." + valueAlias +
		" END AS " + valueColumn + " FROM " + categorySourceCTE + " AS " + categoryRowAlias + "), " +
		categoryDistinctCTE + " AS (SELECT " + presentColumn + ", " + valueColumn + " FROM " + categoryRowsCTE +
		" GROUP BY " + presentColumn + ", " + valueColumn + ") " +
		"SELECT " + presentColumn + " AS \"c0\", " + valueColumn + " AS \"c1\" FROM " + categoryDistinctCTE
	if ordered {
		statement += " ORDER BY " + presentColumn + " ASC, " + categoryValueRankExpression + " ASC, " +
			categoryStringValueKeyExpression + " ASC, " + valueColumn + " ASC"
	}
	statement += " LIMIT " + boundParameter
	result, err := command.NewRenderedPostgresPhysicalPlan(statement, args,
		[]string{CategoryScanPresentColumn, CategoryScanValueColumn})
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf("compose category statement: %w", err)
	}
	return result, nil
}

func innerColumnAlias(index int) string {
	return "c" + strconv.Itoa(index)
}

const categoryValueRankExpression = `CASE WHEN "value" IS NULL THEN 2 ELSE CASE COALESCE(jsonb_typeof("value"), 'null')` +
	` WHEN 'array' THEN 0 WHEN 'boolean' THEN 1 WHEN 'null' THEN 2 WHEN 'number' THEN 3` +
	` WHEN 'object' THEN 4 WHEN 'string' THEN 5 ELSE 6 END END`

const categoryStringValueKeyExpression = `CASE WHEN jsonb_typeof("value") = 'string' THEN "value" #>> '{}' END COLLATE "C"`
