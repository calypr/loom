package postgres

// Category presence/value scans (pg-sql-25): the PostgreSQL terminal for the
// existing typed evidence request (compiler.CompileCategoryScanOutput and
// compiler.CompileCategoryScanStage), dispatched from the compiler boundary
// exactly as population.go's RenderPopulationRootScan is — an exported entry
// the compiler calls, not a RenderPhysicalPlan door.
//
// Shape of the delivered statement. The inner read is rendered by
// RenderPhysicalPlan itself over a plan copy narrowed to two projections: the
// category column's own registered EXTRACT selector, and one PresenceOutput
// sidecar over the same (row variable, document path) evidence. There is no
// second SQL builder, no second path emitter, and no second presence emitter
// here: the selector arm of renderReturn and the presence arm of
// propertyPresenceExpression (null_semantics.go) produce those two columns, so
// the category statement can never drift from the read spine it summarizes.
// The outer relation is the distinct/ordered/bounded layer — the SQL twin of
// the AQL suffix in compiler/category_scan.go (categoryScanDistinctSuffix):
//
//	value  = CASE WHEN present THEN <selector value> END
//	                          -- AQL: present ? row[column] : null
//	group  = GROUP BY present, value  -- AQL: COLLECT present, value
//	order  = present, TYPENAME(value), string bytes (COLLATE "C"), value
//	                          -- AQL: categoryScanDistinctSuffix; the string
//	                             bucket is byte-pinned so the page order is
//	                             cluster-collation-independent, matching
//	                             AQL's byte comparison (see
//	                             categoryStringValueKeyExpression)
//	bound  = LIMIT maxValues+1        -- AQL: LIMIT @__loom_category_limit
//
// The three states null_semantics.go defines survive the aggregation because
// the value column stays jsonb and the presence column stays a total SQL
// boolean: an absent path contributes the group (false, SQL NULL), a stored
// JSON null contributes (true, jsonb 'null') under PRESERVE_NULL (SQL NULL
// under OMIT_NULLS, the behavior table's own readings), and a stored value —
// including "", false, 0, [] and [null] — contributes (true, itself). GROUP BY
// collapses duplicate contributors exactly as AQL COLLECT does, and an empty
// population contributes NO row at all: the statement has no aggregate over
// the outer relation, so a missing row can never be fabricated as an empty
// aggregate row.
//
// Closed surface. Everything outside this slice is a typed
// ErrUnsupportedPostgresPlan capability gap, never an AQL fallback and never
// an AQL-to-SQL translation: a plan with a construction stage sequence
// (composition, group, coded group, pivot, and graph slices keep their own
// registered contracts), any operation outside ROOT_SCAN / FILTER /
// canonical DERIVED_LET / SORT / RETURN (traversals, SET, UNNEST, UNPIVOT,
// grouped pivots, group rows), a plan-carried LIMIT (a bounded preview page
// must not be applied before the distinct), or a category column that is not
// exactly one non-hidden scalar-cardinality DIRECT_SCALAR EXTRACT selector
// over the root row document. Hidden projections, duplicate names, a name
// colliding with the presence marker, and a presence claim that disagrees with
// the selector all refuse.
//
// Identifier discipline: the only names interpolated into the outer statement
// are this file's own renderer-owned CTE/alias names and the positional
// "c<n>" aliases the spine assembler mints, selected by index after checking
// the declared column they stand for. Category column names, document paths,
// the scope binds, and the bound are parameters or never reach SQL text.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const (
	// CategoryScanPresentColumn and CategoryScanValueColumn are the declared
	// positional output contract of a category statement; the row port aligns
	// them with OutputColumns by index, and the executor reads exactly these
	// keys (execution/category_scan.go).
	CategoryScanPresentColumn = "present"
	CategoryScanValueColumn   = "value"

	// maxCategoryScanValues mirrors compiler.MaxCategoryScanValues: the
	// renderer re-checks the bound so no caller can drive an unbounded
	// DISTINCT scan through this entry point.
	maxCategoryScanValues = 256

	categorySourceCTE   = "__loom_category_source"
	categoryRowsCTE     = "__loom_category_rows"
	categoryDistinctCTE = "__loom_category_distinct"
	categoryRowAlias    = "__loom_category_row"
)

// RenderedCategoryScan is the executable PostgreSQL form of one category
// evidence request: the bounded distinct statement plus its independently
// bounded overflow confirmation, both with the same ["present","value"]
// positional contract.
type RenderedCategoryScan struct {
	// Values is the ordered category page: at most MaxValues+1 rows, so the
	// executor detects truncation without a second query.
	Values command.RenderedPostgresPhysicalPlan
	// Witness is the unordered bounded confirmation over the same distinct
	// relation with a wider bound. It carries no ORDER BY: its only contract
	// is the row count and the presence bits.
	Witness command.RenderedPostgresPhysicalPlan
	// PresentColumn and ValueColumn name the positional output columns.
	PresentColumn string
	ValueColumn   string
	// PresenceMarkerColumn names the projection this renderer added for the
	// presence proof, echoed for compiler diagnostics. It is never public
	// output: only present/value are declared.
	PresenceMarkerColumn string
}

// RenderCategoryScan renders one category presence/value scan (and its
// overflow witness) for a plan the read spine already renders, onto the
// trusted PostgreSQL storage binding. The result is deterministic: the same
// plan, binding, column, and bound produce byte-identical SQL and
// value-identical ordered arguments.
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
	// The witness bound is the AQL overflow-witness bound (category scan
	// stage compilation passes maxValues*4+1), so a confirmation page is
	// comparable across backends.
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

// categoryScanInnerPlan validates the closed category-scan surface and returns
// the plan copy the spine renders: SORT removed (the outer GROUP BY and ORDER
// BY own the order, and an inner order cannot survive the distinct), and the
// terminal RETURN narrowed to the category selector plus its presence sidecar.
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

	// SORT removal is safe for the delivered contract: every ORDER BY key of
	// the outer statement is also a GROUP BY key, so the page order is total
	// over the distinct groups and no inner order can survive the collapse.
	// The clone keeps this file's edits off the caller's plan entirely.
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

// categorySelectorEvidence is the closed reading of a category projection:
// exactly one scalar-cardinality DIRECT_SCALAR EXTRACT selector over the root
// row document. It re-reads, never trusts, the shape lowering emits
// (lower/root_projection.go lowerSemanticFieldProjection). The spine's own
// selector validator (classifyExtractionProjections) runs again during the
// inner render, so this is the surface gate that keeps the category slice
// closed, not a second contract.
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
	// An inline presence claim on the value projection is legal only when it
	// proves this very path, so a plan cannot carry a foreign presence claim
	// into a category scan whose sidecar this renderer mints itself.
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

// categoryOutputIndex locates the single positional alias the spine assembler
// minted for one declared column name. Only the returned index reaches SQL
// text, as a decimal in a "c<n>" alias this package owns.
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

// composeCategoryStatement wraps the rendered inner read in the distinct,
// ordered, bounded category layer. ordered selects the category page order;
// the witness shares every other clause so a confirmation and its page can
// never disagree about what a (present, value) group is.
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

// categoryValueRankExpression is the SQL twin of the AQL category order's
// middle key: TYPENAME(value) ASC (compiler/category_scan.go
// categoryScanDistinctSuffix), i.e. the jsonb type names in the same
// alphabetical order AQL's type names sort in. A SQL NULL value — the ABSENT
// reading — ranks with AQL's "null" so an absent group and a stored-JSON-null
// group keep the same relative rank and are separated by the presence key.
const categoryValueRankExpression = `CASE WHEN "value" IS NULL THEN 2 ELSE CASE COALESCE(jsonb_typeof("value"), 'null')` +
	` WHEN 'array' THEN 0 WHEN 'boolean' THEN 1 WHEN 'null' THEN 2 WHEN 'number' THEN 3` +
	` WHEN 'object' THEN 4 WHEN 'string' THEN 5 ELSE 6 END END`

// categoryStringValueKeyExpression is the byte-pin for the ORDER BY's string
// bucket (the coded_group.go / graph.go COLLATE "C" precedent). Raw
// "value" ASC compares jsonb strings with varstr_cmp under the database
// default collation, which is deployment-dependent and diverges from AQL's
// byte comparison for mixed-case or non-ASCII values. jsonb strings order by
// their raw UTF-8 bytes here; every non-string value yields SQL NULL in this
// key, so within the remaining type buckets the trailing "value" ASC stays
// the tiebreak and is collation-independent for scalars (jsonb numbers
// compare numerically, booleans by false/true, and SQL NULL ranks last).
// Array and object buckets keep jsonb's documented structural comparison.
const categoryStringValueKeyExpression = `CASE WHEN jsonb_typeof("value") = 'string' THEN "value" #>> '{}' END COLLATE "C"`
