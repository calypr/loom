package published

import (
	"fmt"
	"sort"
	"strings"
)

// Aggregate statements can contain a large number of grouping sets. Keep the
// query from competing with the rest of the Loom process for all pod memory:
// ClickHouse spills aggregation/sort state before the container limit is hit,
// and a small thread cap avoids one state arena per source worker. These are
// statement-local settings and do not change the server-wide profile.
const aggregateExternalMemoryBytes = 256 * 1024 * 1024

const aggregateMaxThreads = 2

func aggregateSettings() string {
	return fmt.Sprintf(
		"SETTINGS group_by_use_nulls = 0, max_bytes_before_external_group_by = %d, max_bytes_before_external_sort = %d, max_threads = %d",
		aggregateExternalMemoryBytes, aggregateExternalMemoryBytes, aggregateMaxThreads,
	)
}

type aggregateSQL struct {
	query         string
	columns       []string
	args          []any
	sourceColumns []string
	groupingSets  int
}

func buildAggregateSQL(dataset FederatedDataset, access map[string]SourceAccess, statement aggregateStatementPlan) (aggregateSQL, error) {
	switch statement.kind {
	case statementLegacy:
		return buildGroupingSetSQL(dataset, access, statement, false)
	case statementTerms:
		return buildGroupingSetSQL(dataset, access, statement, true)
	case statementBucket:
		return buildBucketSQL(dataset, access, statement)
	case statementScalar:
		return buildScalarSQL(dataset, access, statement)
	default:
		return aggregateSQL{}, fmt.Errorf("unknown aggregate statement kind %q", statement.kind)
	}
}

func aggregateUnion(dataset FederatedDataset, access map[string]SourceAccess, statement aggregateStatementPlan) (string, []any, []string, error) {
	set := make(map[string]struct{})
	for _, job := range statement.jobs {
		for _, column := range job.job.GroupBy {
			set[column] = struct{}{}
		}
		if job.job.Column != "" {
			set[job.job.Column] = struct{}{}
		}
	}
	for _, filter := range statement.filters {
		set[filter.Column] = struct{}{}
	}
	columns := make([]string, 0, len(set))
	for column := range set {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	union, args, err := federatedNormalizedUnion(dataset, columns, access)
	return union, args, columns, err
}

func buildGroupingSetSQL(dataset FederatedDataset, access map[string]SourceAccess, statement aggregateStatementPlan, terms bool) (aggregateSQL, error) {
	if !terms {
		return buildLegacyGroupingSetSQL(dataset, access, statement)
	}
	return buildTermsGroupingSetSQL(dataset, access, statement)
}

// buildLegacyGroupingSetSQL keeps the aggregate state narrow even when an
// operation contains hundreds of one-column facets. ClickHouse expands a
// large GROUPING SETS clause into one aggregation state per set; on the
// production 156-facet shape that alone can exceed the server's memory cap.
// We fan out the logical facet slot/key pairs once with arrayJoin and perform
// one physical GROUPING SETS aggregation over the narrow pair. The source
// union is still built once per statement, and every logical job remains
// represented by its slot in the grouped key.
func buildLegacyGroupingSetSQL(dataset FederatedDataset, access map[string]SourceAccess, statement aggregateStatementPlan) (aggregateSQL, error) {
	union, args, sourceColumns, err := aggregateUnion(dataset, access, statement)
	if err != nil {
		return aggregateSQL{}, err
	}
	allowed := aggregateAllowedColumns(dataset)
	where, whereArgs, err := buildWhere(statement.filters, allowed)
	if err != nil {
		return aggregateSQL{}, err
	}
	args = append(args, whereArgs...)

	keyAliases := make([]string, len(statement.jobs))
	keyExpressions := make([]string, 0, len(statement.jobs)+1)
	for index, planned := range statement.jobs {
		if len(planned.job.GroupBy) == 0 {
			continue
		}
		alias := fmt.Sprintf("__loom_group_key_%d", index)
		keyAliases[index] = alias
		keyExpressions = append(keyExpressions, "toJSONString(tuple("+quotedColumns(planned.job.GroupBy)+")) AS `"+alias+"`")
	}
	metricColumn := strings.TrimSpace(statement.jobs[0].job.Column)
	if metricColumn != "" {
		keyExpressions = append(keyExpressions, "`"+metricColumn+"` AS `__loom_metric_value`")
	}
	sourceProjection := strings.Join(keyExpressions, ", ")
	if sourceProjection == "" {
		sourceProjection = "*"
	}
	source := "SELECT " + sourceProjection + " FROM (" + union + ") AS __loom_aggregate_source"
	if len(where) > 0 {
		source += " WHERE " + strings.Join(where, " AND ")
	}
	pairs := make([]string, 0, len(statement.jobs))
	for index, planned := range statement.jobs {
		if len(planned.job.GroupBy) == 0 {
			pairs = append(pairs, fmt.Sprintf("tuple(%d, '[]')", index))
		} else {
			pairs = append(pairs, fmt.Sprintf("tuple(%d, `%s`)", index, keyAliases[index]))
		}
	}
	fanout := "SELECT tupleElement(__loom_pair, 1) AS `__loom_slot`, tupleElement(__loom_pair, 2) AS `__loom_group_json`"
	if metricColumn != "" {
		fanout += ", `__loom_metric_value`"
	}
	fanout += " FROM (SELECT arrayJoin([" + strings.Join(pairs, ", ") + "]) AS __loom_pair"
	if metricColumn != "" {
		fanout += ", `__loom_metric_value`"
	}
	fanout += " FROM (" + source + ") AS __loom_aggregate_source) AS __loom_aggregate_fanout"
	metric := legacyMetricOnColumn(statement.jobs[0].job, "__loom_metric_value")
	query := fmt.Sprintf("SELECT `__loom_slot`, `__loom_group_json`, %s AS `__loom_metric` FROM (%s) AS __loom_group_source GROUP BY GROUPING SETS ((`__loom_slot`, `__loom_group_json`)) %s", metric, fanout, aggregateSettings())
	return aggregateSQL{
		query: query, columns: []string{"__loom_slot", "__loom_group_json", "__loom_metric"}, args: args,
		sourceColumns: sourceColumns, groupingSets: len(statement.jobs),
	}, nil
}

func buildTermsGroupingSetSQL(dataset FederatedDataset, access map[string]SourceAccess, statement aggregateStatementPlan) (aggregateSQL, error) {
	union, args, sourceColumns, err := aggregateUnion(dataset, access, statement)
	if err != nil {
		return aggregateSQL{}, err
	}
	allowed := aggregateAllowedColumns(dataset)
	where, whereArgs, err := buildWhere(statement.filters, allowed)
	if err != nil {
		return aggregateSQL{}, err
	}
	args = append(args, whereArgs...)
	allGroups := make([]string, 0)
	for _, job := range statement.jobs {
		groups := []string{job.job.Column}
		for _, column := range groups {
			if !contains(allGroups, column) {
				allGroups = append(allGroups, column)
			}
		}
	}
	sort.Strings(allGroups)

	innerSelect := make([]string, 0, len(allGroups)+4)
	for _, column := range allGroups {
		innerSelect = append(innerSelect, fmt.Sprintf("`%s`", column))
	}
	maskNames := make([]string, 0)
	for start := 0; start < len(allGroups); start += 63 {
		end := start + 63
		if end > len(allGroups) {
			end = len(allGroups)
		}
		name := fmt.Sprintf("__loom_mask_%d", len(maskNames))
		maskNames = append(maskNames, name)
		innerSelect = append(innerSelect, fmt.Sprintf("%s AS `%s`", groupingMaskExpression(allGroups[start:end]), name))
	}
	metric := "count()"
	innerSelect = append(innerSelect, metric+" AS `__loom_metric`")

	sets := make([]string, len(statement.jobs))
	conditions := make([]string, len(statement.jobs))
	for i, job := range statement.jobs {
		groups := []string{job.job.Column}
		sets[i] = "(" + quotedColumns(groups) + ")"
		conditions[i] = groupingCondition(allGroups, groups, maskNames)
	}
	slot := "0"
	if len(statement.jobs) > 1 {
		parts := make([]string, 0, len(statement.jobs)*2+1)
		for i, condition := range conditions {
			parts = append(parts, condition, fmt.Sprintf("%d", i))
		}
		parts = append(parts, "-1")
		slot = "multiIf(" + strings.Join(parts, ", ") + ")"
	}
	groupJSONValues := make([]string, len(statement.jobs))
	for i, job := range statement.jobs {
		groups := []string{job.job.Column}
		groupJSONValues[i] = "'[]'"
		if len(groups) > 0 {
			groupJSONValues[i] = "toJSONString(tuple(" + quotedColumns(groups) + "))"
		}
	}
	groupJSON := groupJSONValues[0]
	if len(statement.jobs) > 1 {
		parts := make([]string, 0, len(statement.jobs)*2+1)
		for i, condition := range conditions {
			parts = append(parts, condition, groupJSONValues[i])
		}
		parts = append(parts, "'[]'")
		groupJSON = "multiIf(" + strings.Join(parts, ", ") + ")"
	}
	rawProjection := ""
	if len(allGroups) > 0 {
		rawProjection = ", " + quotedColumns(allGroups)
	}
	query := fmt.Sprintf("SELECT %s AS `__loom_slot`, %s AS `__loom_group_json`, `__loom_metric`%s FROM (SELECT %s FROM (%s) AS __loom_aggregate_source", slot, groupJSON, rawProjection, strings.Join(innerSelect, ", "), union)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	// ClickHouse 25.7's analyzer rejects GROUPING() with
	// group_by_use_nulls=1 (even when every argument is a grouping key).
	// Keep the setting explicit at 0 and use the grouping masks to distinguish
	// rolled-up defaults from real NULL values.
	query += " GROUP BY GROUPING SETS (" + strings.Join(sets, ", ") + ") " + aggregateSettings() + ") AS __loom_grouped"
	limitParts := make([]string, 0, len(statement.jobs)*2+1)
	for i, planned := range statement.jobs {
		limitParts = append(limitParts, fmt.Sprintf("`__loom_slot` = %d", i), fmt.Sprintf("%d", planned.job.Size+1))
	}
	limitParts = append(limitParts, "0")
	limitExpr := "multiIf(" + strings.Join(limitParts, ", ") + ")"
	orderKey := "`" + statement.jobs[0].job.Column + "`"
	if len(statement.jobs) > 1 {
		parts := make([]string, 0, len(statement.jobs)*2+1)
		for i, planned := range statement.jobs {
			parts = append(parts, fmt.Sprintf("`__loom_slot` = %d", i), "`"+planned.job.Column+"`")
		}
		parts = append(parts, "NULL")
		orderKey = "multiIf(" + strings.Join(parts, ", ") + ")"
	}
	query = fmt.Sprintf("SELECT `__loom_slot`, `__loom_group_json`, `__loom_metric` FROM (SELECT `__loom_slot`, `__loom_group_json`, `__loom_metric`, `__loom_order_key`, row_number() OVER (PARTITION BY `__loom_slot` ORDER BY `__loom_metric` DESC, `__loom_order_key` ASC) AS `__loom_rank` FROM (SELECT `__loom_slot`, `__loom_group_json`, `__loom_metric`, %s AS `__loom_order_key` FROM (%s) AS __loom_terms_projected) AS __loom_terms WHERE `__loom_group_json` != '[null]') AS __loom_ranked WHERE `__loom_rank` <= %s ORDER BY `__loom_slot`, `__loom_metric` DESC, `__loom_order_key` ASC", orderKey, query, limitExpr)
	return aggregateSQL{
		query: query, columns: []string{"__loom_slot", "__loom_group_json", "__loom_metric"}, args: args,
		sourceColumns: sourceColumns, groupingSets: len(statement.jobs),
	}, nil
}

func groupingCondition(all, groups, masks []string) string {
	present := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		present[group] = struct{}{}
	}
	conditions := make([]string, 0, len(masks))
	for start, index := 0, 0; start < len(all); start, index = start+63, index+1 {
		end := start + 63
		if end > len(all) {
			end = len(all)
		}
		var mask uint64
		for offset, column := range all[start:end] {
			if _, ok := present[column]; !ok {
				mask |= uint64(1) << uint(end-start-1-offset)
			}
		}
		conditions = append(conditions, fmt.Sprintf("`%s` = %d", masks[index], mask))
	}
	if len(conditions) == 0 {
		return "1"
	}
	return strings.Join(conditions, " AND ")
}

// groupingMaskExpression deliberately calls GROUPING with one column at a
// time. ClickHouse's analyzer has a compatibility bug for the variadic form
// (GROUPING(a, b, ...)) when GROUPING SETS contains sparse sets: it can reject
// a valid key as not being part of GROUP BY. Combining the scalar GROUPING
// results into the same bit mask is semantically identical and works across
// the ClickHouse versions supported by Loom.
func groupingMaskExpression(columns []string) string {
	if len(columns) == 0 {
		return "toUInt64(0)"
	}
	terms := make([]string, 0, len(columns))
	for index, column := range columns {
		shift := len(columns) - 1 - index
		terms = append(terms, fmt.Sprintf("bitShiftLeft(toUInt64(grouping(`%s`)), %d)", column, shift))
	}
	return strings.Join(terms, " + ")
}

func legacyMetricOnColumn(job AggregateJob, column string) string {
	switch job.Operation {
	case "COUNT_DISTINCT":
		return fmt.Sprintf("uniqExact(`%s`)", column)
	case "SUM":
		return fmt.Sprintf("sum(`%s`)", column)
	case "AVG":
		return fmt.Sprintf("avg(`%s`)", column)
	case "MIN":
		return fmt.Sprintf("min(`%s`)", column)
	case "MAX":
		return fmt.Sprintf("max(`%s`)", column)
	default:
		return "count()"
	}
}

func buildBucketSQL(dataset FederatedDataset, access map[string]SourceAccess, statement aggregateStatementPlan) (aggregateSQL, error) {
	union, unionArgs, sourceColumns, err := aggregateUnion(dataset, access, statement)
	if err != nil {
		return aggregateSQL{}, err
	}
	where, whereArgs, err := buildWhere(statement.filters, aggregateAllowedColumns(dataset))
	if err != nil {
		return aggregateSQL{}, err
	}
	expressionArgs := make([]any, 0, len(statement.jobs)*2)
	expressions := make([]string, len(statement.jobs))
	bucketColumns := make([]string, len(statement.jobs))
	sets := make([]string, len(statement.jobs))
	for slot, planned := range statement.jobs {
		job := planned.job
		bucket := fmt.Sprintf("__loom_bucket_%d", slot)
		bucketColumns[slot] = bucket
		sets[slot] = "(`" + bucket + "`)"
		if job.ResponseMode == AggregateResponseHistogram {
			expressions[slot] = fmt.Sprintf("floor(toFloat64(`%s`) / ?)*? AS `%s`", job.Column, bucket)
			expressionArgs = append(expressionArgs, job.Interval, job.Interval)
		} else {
			expressions[slot] = fmt.Sprintf("toStartOfInterval(`%s`, toIntervalSecond(?)) AS `%s`", job.Column, bucket)
			expressionArgs = append(expressionArgs, job.DateInterval)
		}
	}
	args := append(expressionArgs, unionArgs...)
	args = append(args, whereArgs...)
	source := fmt.Sprintf("SELECT %s FROM (%s) AS __loom_aggregate_source", strings.Join(expressions, ", "), union)
	if len(where) > 0 {
		source += " WHERE " + strings.Join(where, " AND ")
	}
	maskNames := make([]string, 0)
	masks := make([]string, 0)
	for start := 0; start < len(bucketColumns); start += 63 {
		end := start + 63
		if end > len(bucketColumns) {
			end = len(bucketColumns)
		}
		name := fmt.Sprintf("__loom_mask_%d", len(maskNames))
		maskNames = append(maskNames, name)
		masks = append(masks, fmt.Sprintf("%s AS `%s`", groupingMaskExpression(bucketColumns[start:end]), name))
	}
	conditions := make([]string, len(statement.jobs))
	for slot, bucket := range bucketColumns {
		conditions[slot] = groupingCondition(bucketColumns, []string{bucket}, maskNames)
	}
	slotExpr := "0"
	keyExpr := "toJSONString(tuple(`" + bucketColumns[0] + "`))"
	orderKeyExpr := "`" + bucketColumns[0] + "`"
	if len(statement.jobs) > 1 {
		slotParts := make([]string, 0, len(statement.jobs)*2+1)
		keyParts := make([]string, 0, len(statement.jobs)*2+1)
		orderParts := make([]string, 0, len(statement.jobs)*2+1)
		for slot, condition := range conditions {
			slotParts = append(slotParts, condition, fmt.Sprintf("%d", slot))
			keyParts = append(keyParts, condition, "toJSONString(tuple(`"+bucketColumns[slot]+"`))")
			orderParts = append(orderParts, condition, "`"+bucketColumns[slot]+"`")
		}
		slotParts = append(slotParts, "-1")
		keyParts = append(keyParts, "'[]'")
		orderParts = append(orderParts, "NULL")
		slotExpr = "multiIf(" + strings.Join(slotParts, ", ") + ")"
		keyExpr = "multiIf(" + strings.Join(keyParts, ", ") + ")"
		orderKeyExpr = "multiIf(" + strings.Join(orderParts, ", ") + ")"
	}
	groupedSelect := append(append([]string(nil), bucketColumns...), masks...)
	for index := range bucketColumns {
		groupedSelect[index] = "`" + bucketColumns[index] + "`"
	}
	groupedSelect = append(groupedSelect, "count() AS `__loom_metric`")
	grouped := fmt.Sprintf("SELECT %s FROM (%s) AS __loom_bucket_source GROUP BY GROUPING SETS (%s) %s", strings.Join(groupedSelect, ", "), source, strings.Join(sets, ", "), aggregateSettings())
	projected := fmt.Sprintf("SELECT %s AS `__loom_slot`, %s AS `__loom_group_json`, %s AS `__loom_order_key`, `__loom_metric` FROM (%s) AS __loom_bucket_grouped", slotExpr, keyExpr, orderKeyExpr, grouped)
	sizeParts := make([]string, 0, len(statement.jobs)*2+1)
	for slot, planned := range statement.jobs {
		sizeParts = append(sizeParts, fmt.Sprintf("`__loom_slot` = %d", slot), fmt.Sprintf("%d", planned.job.Size))
	}
	sizeParts = append(sizeParts, "0")
	sizeExpr := "multiIf(" + strings.Join(sizeParts, ", ") + ")"
	query := fmt.Sprintf("SELECT `__loom_slot`, `__loom_group_json`, `__loom_metric` FROM (SELECT *, row_number() OVER (PARTITION BY `__loom_slot` ORDER BY `__loom_order_key` ASC) AS `__loom_rank` FROM (%s) AS __loom_bucket_projected WHERE `__loom_group_json` != '[null]') AS __loom_bucket_ranked WHERE `__loom_rank` <= %s ORDER BY `__loom_slot`, `__loom_order_key` ASC", projected, sizeExpr)
	return aggregateSQL{
		query: query, columns: []string{"__loom_slot", "__loom_group_json", "__loom_metric"}, args: args,
		sourceColumns: sourceColumns, groupingSets: len(statement.jobs),
	}, nil
}

func buildScalarSQL(dataset FederatedDataset, access map[string]SourceAccess, statement aggregateStatementPlan) (aggregateSQL, error) {
	union, args, sourceColumns, err := aggregateUnion(dataset, access, statement)
	if err != nil {
		return aggregateSQL{}, err
	}
	where, whereArgs, err := buildWhere(statement.filters, aggregateAllowedColumns(dataset))
	if err != nil {
		return aggregateSQL{}, err
	}
	args = append(args, whereArgs...)
	expressions := make([]string, 0)
	columns := make([]string, 0)
	for slot, planned := range statement.jobs {
		column := planned.job.Column
		if planned.job.ResponseMode == AggregateResponseStats {
			for _, value := range []struct{ name, expression string }{
				{"count", "count()"}, {"value_count", fmt.Sprintf("countIf(`%s` IS NOT NULL)", column)},
				{"distinct_count", fmt.Sprintf("uniqExactIf(`%s`, `%s` IS NOT NULL)", column, column)},
				{"min", fmt.Sprintf("min(`%s`)", column)}, {"max", fmt.Sprintf("max(`%s`)", column)},
				{"sum", fmt.Sprintf("sum(`%s`)", column)}, {"avg", fmt.Sprintf("avg(`%s`)", column)},
			} {
				alias := fmt.Sprintf("__loom_%d_%s", slot, value.name)
				expressions = append(expressions, value.expression+" AS `"+alias+"`")
				columns = append(columns, alias)
			}
		} else {
			alias := fmt.Sprintf("__loom_%d_missing_count", slot)
			expressions = append(expressions, fmt.Sprintf("countIf(`%s` IS NULL) AS `%s`", column, alias))
			columns = append(columns, alias)
		}
	}
	query := fmt.Sprintf("SELECT %s FROM (%s) AS __loom_aggregate_source", strings.Join(expressions, ", "), union)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	return aggregateSQL{query: query, columns: columns, args: args, sourceColumns: sourceColumns}, nil
}

func aggregateAllowedColumns(dataset FederatedDataset) map[string]struct{} {
	allowed := make(map[string]struct{}, len(dataset.Columns))
	for _, column := range dataset.Columns {
		if !internalAggregateColumn(column.Name) {
			allowed[column.Name] = struct{}{}
		}
	}
	return allowed
}
