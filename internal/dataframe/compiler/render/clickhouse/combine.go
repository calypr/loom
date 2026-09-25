package clickhouse

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type RenderedCombine struct {
	Query   string
	Args    []any
	Columns []string
}

// RenderCombine turns a typed compiler plan and its exact, authorized table
// resolutions into one ClickHouse SELECT. It never accepts SQL fragments from
// recipe input and never consults a moving publication pointer.
func RenderCombine(plan ir.PhysicalClickHouseCombine, inputs []ir.ResolvedClickHouseTable, project string) (RenderedCombine, error) {
	return RenderCombineWithLimit(plan, inputs, project, 0)
}

// RenderCombineWithLimit applies a bounded preview limit to the typed
// ClickHouse plan. A zero limit streams the complete result.
func RenderCombineWithLimit(plan ir.PhysicalClickHouseCombine, inputs []ir.ResolvedClickHouseTable, project string, limit int) (RenderedCombine, error) {
	if limit < 0 {
		return RenderedCombine{}, fmt.Errorf("ClickHouse combine limit cannot be negative")
	}
	if err := plan.Validate(); err != nil {
		return RenderedCombine{}, fmt.Errorf("validate ClickHouse combine plan: %w", err)
	}
	if err := validateResolvedInputs(plan, inputs, project); err != nil {
		return RenderedCombine{}, err
	}
	if err := validateResolvedSchema(plan, inputs); err != nil {
		return RenderedCombine{}, err
	}
	columns := combineQueryColumns(plan)
	var query string
	var args []any
	switch plan.Kind {
	case ir.PhysicalCombineKeyJoin:
		query, args = renderKeyJoin(plan, inputs)
	case ir.PhysicalCombineAppend:
		query, args = renderAppend(plan, inputs)
	case ir.PhysicalCombineMembership:
		query, args = renderMembership(plan, inputs)
	default:
		return RenderedCombine{}, fmt.Errorf("unsupported ClickHouse combine kind %q", plan.Kind)
	}
	if limit > 0 {
		query = applyQueryLimit(query, limit)
	}
	return RenderedCombine{Query: query, Args: args, Columns: columns}, nil
}

func applyQueryLimit(query string, limit int) string {
	if settings := strings.LastIndex(query, " SETTINGS "); settings >= 0 {
		return query[:settings] + fmt.Sprintf(" LIMIT %d", limit) + query[settings:]
	}
	return query + fmt.Sprintf(" LIMIT %d", limit)
}

func validateResolvedInputs(plan ir.PhysicalClickHouseCombine, inputs []ir.ResolvedClickHouseTable, project string) error {
	if len(inputs) != len(plan.Inputs) {
		return fmt.Errorf("resolved ClickHouse input count %d does not match plan count %d", len(inputs), len(plan.Inputs))
	}
	if strings.TrimSpace(project) == "" {
		return fmt.Errorf("ClickHouse combine project is required")
	}
	var firstScope *ir.ResolvedClickHouseTable
	for index, input := range inputs {
		ref := plan.Inputs[index]
		if input.TableID != ref.TableID || input.RevisionID != ref.RevisionID || input.OutputID != ref.OutputID {
			return fmt.Errorf("resolved ClickHouse input %d does not match its exact table/revision/output reference", index)
		}
		if input.Project != project || input.Project == "" {
			return fmt.Errorf("resolved ClickHouse input %d belongs to a different project", index)
		}
		if input.RevisionID == "" || input.OutputID == "" || input.SchemaDigest == "" || input.ReceiptID == "" || input.ScopeDigest == "" {
			return fmt.Errorf("resolved ClickHouse input %d is missing immutable publication identity", index)
		}
		if !identifierPattern.MatchString(input.PhysicalTable) {
			return fmt.Errorf("resolved ClickHouse input %d has an unsafe physical table name", index)
		}
		if input.Unrestricted && len(input.AuthResourcePaths) != 0 {
			return fmt.Errorf("resolved ClickHouse input %d has an unrestricted scope with path filters", index)
		}
		if !input.Unrestricted && len(input.AuthResourcePaths) == 0 {
			return fmt.Errorf("resolved ClickHouse input %d has an empty restricted authorization scope", index)
		}
		if firstScope == nil {
			copy := input
			firstScope = &copy
		} else if input.Unrestricted != firstScope.Unrestricted || !sameStrings(input.AuthResourcePaths, firstScope.AuthResourcePaths) {
			return fmt.Errorf("ClickHouse combine inputs must resolve to the same authorization scope")
		}
	}
	return nil
}

func validateResolvedSchema(plan ir.PhysicalClickHouseCombine, inputs []ir.ResolvedClickHouseTable) error {
	columns := make([]map[string]ir.ResolvedClickHouseColumn, len(inputs))
	for inputIndex, input := range inputs {
		columns[inputIndex] = make(map[string]ir.ResolvedClickHouseColumn, len(input.Columns))
		seenNames := map[string]bool{}
		for _, column := range input.Columns {
			reservedWithoutStableID := column.ID == "" && (column.Name == "__loom_row_id" || column.Name == "auth_resource_path" || column.Name == "project_id")
			if (!reservedWithoutStableID && column.ID == "") || !identifierPattern.MatchString(column.Name) || column.ClickHouseType == "" || (column.ID != "" && columns[inputIndex][column.ID].ID != "") || seenNames[column.Name] {
				return fmt.Errorf("resolved ClickHouse input %d has an unsafe or ambiguous schema", inputIndex)
			}
			if column.ID != "" {
				columns[inputIndex][column.ID] = column
			}
			seenNames[column.Name] = true
		}
		if _, ok := inputColumnByName(input, "__loom_row_id"); !ok {
			return fmt.Errorf("resolved ClickHouse input %d is missing its stable row identity", inputIndex)
		}
		if !input.Unrestricted {
			if _, ok := inputColumnByName(input, "auth_resource_path"); !ok {
				return fmt.Errorf("resolved restricted ClickHouse input %d is missing auth_resource_path", inputIndex)
			}
		}
	}
	for _, key := range plan.Keys {
		left, leftOK := columns[0][key.LeftColumnID]
		right, rightOK := columns[1][key.RightColumnID]
		if !leftOK || !rightOK {
			return fmt.Errorf("ClickHouse combine key references a missing exact input column")
		}
		if left.ClickHouseType != right.ClickHouseType || !joinableType(left.ClickHouseType) {
			return fmt.Errorf("ClickHouse combine key types must match and be non-null scalar types")
		}
	}
	for _, projection := range plan.Projections {
		inputColumn, ok := columns[projection.InputIndex][projection.InputColumnID]
		if !ok {
			return fmt.Errorf("ClickHouse combine projection references a missing exact input column")
		}
		output := outputColumn(plan, projection.OutputColumnID)
		wantType := inputColumn.ClickHouseType
		if plan.Kind == ir.PhysicalCombineKeyJoin && plan.JoinType == "LEFT" && projection.InputIndex == 1 {
			if strings.HasPrefix(wantType, "Array(") || strings.HasPrefix(wantType, "Nullable(Array(") {
				return fmt.Errorf("LEFT key join cannot null-extend repeated output %q", output.Name)
			}
			if !strings.HasPrefix(wantType, "Nullable(") {
				wantType = "Nullable(" + wantType + ")"
			}
		}
		if inputColumn.LogicalType != output.LogicalType {
			return fmt.Errorf("ClickHouse combine projection for %q has source logical type %s, output logical type %s", output.Name, inputColumn.LogicalType, output.LogicalType)
		}
		if inputColumn.ClickHouseType != output.ClickHouseType && wantType != output.ClickHouseType {
			return fmt.Errorf("ClickHouse combine projection for %q has source type %s, output type %s", output.Name, inputColumn.ClickHouseType, output.ClickHouseType)
		}
	}
	if plan.Kind == ir.PhysicalCombineKeyJoin && plan.JoinType == "LEFT" {
		for _, projection := range plan.Projections {
			if projection.InputIndex != 1 {
				continue
			}
			if !outputColumn(plan, projection.OutputColumnID).Nullable {
				return fmt.Errorf("LEFT key join right-side output %q must be nullable", outputColumn(plan, projection.OutputColumnID).Name)
			}
		}
	}
	return nil
}

func renderKeyJoin(plan ir.PhysicalClickHouseCombine, inputs []ir.ResolvedClickHouseTable) (string, []any) {
	left, right := inputs[0], inputs[1]
	leftAlias, rightAlias := "__loom_left", "__loom_right"
	selects := []string{keyJoinIdentity(leftAlias, rightAlias, plan.JoinType)}
	for _, projection := range plan.Projections {
		alias := leftAlias
		if projection.InputIndex == 1 {
			alias = rightAlias
		}
		column := resolvedColumnByID(inputs[projection.InputIndex], projection.InputColumnID)
		selects = append(selects, fmt.Sprintf("%s.`%s` AS `%s`", alias, column.Name, outputColumn(plan, projection.OutputColumnID).Name))
	}
	selects = append(selects, authorizationPathExpression(left, leftAlias)+" AS `auth_resource_path`")
	joinType := "INNER"
	if plan.JoinType == "LEFT" {
		joinType = "LEFT"
	}
	joinType = "ALL " + joinType
	conditions := make([]string, 0, len(plan.Keys)+2)
	for _, key := range plan.Keys {
		leftColumn := resolvedColumnByID(left, key.LeftColumnID)
		rightColumn := resolvedColumnByID(right, key.RightColumnID)
		conditions = append(conditions, fmt.Sprintf("%s.`%s` = %s.`%s`", leftAlias, leftColumn.Name, rightAlias, rightColumn.Name))
	}
	if !left.Unrestricted {
		conditions = append(conditions, leftAlias+".`auth_resource_path` = "+rightAlias+".`auth_resource_path`")
	}
	rightWhere, rightArgs := inputScopePredicate(right, rightAlias)
	conditions = append(conditions, rightWhere...)
	query := "SELECT " + strings.Join(selects, ", ") +
		" FROM " + quoteIdentifier(left.PhysicalTable) + " AS " + leftAlias +
		" " + joinType + " JOIN " + quoteIdentifier(right.PhysicalTable) + " AS " + rightAlias +
		" ON " + strings.Join(conditions, " AND ")
	where, leftArgs := inputScopePredicate(left, leftAlias)
	args := append(rightArgs, leftArgs...)
	if len(where) != 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY `__loom_row_id` ASC SETTINGS join_use_nulls = 1"
	return query, args
}

func renderAppend(plan ir.PhysicalClickHouseCombine, inputs []ir.ResolvedClickHouseTable) (string, []any) {
	queries := make([]string, 0, len(inputs))
	var args []any
	for inputIndex, input := range inputs {
		alias := fmt.Sprintf("__loom_input_%d", inputIndex)
		selects := []string{appendIdentity(alias, inputIndex)}
		for _, output := range plan.Outputs {
			projection := projectionFor(plan, inputIndex, output.ID)
			column := resolvedColumnByID(input, projection.InputColumnID)
			selects = append(selects, fmt.Sprintf("%s.`%s` AS `%s`", alias, column.Name, output.Name))
		}
		selects = append(selects, authorizationPathExpression(input, alias)+" AS `auth_resource_path`")
		query := "SELECT " + strings.Join(selects, ", ") + " FROM " + quoteIdentifier(input.PhysicalTable) + " AS " + alias
		if where, scopeArgs := inputScopePredicate(input, alias); len(where) != 0 {
			query += " WHERE " + strings.Join(where, " AND ")
			args = append(args, scopeArgs...)
		}
		queries = append(queries, query)
	}
	return strings.Join(queries, " UNION ALL ") + " ORDER BY `__loom_row_id` ASC", args
}

func renderMembership(plan ir.PhysicalClickHouseCombine, inputs []ir.ResolvedClickHouseTable) (string, []any) {
	left, right := inputs[0], inputs[1]
	leftAlias, rightAlias, rightSourceAlias := "__loom_left", "__loom_members", "__loom_member_source"
	selects := []string{leftAlias + ".`__loom_row_id` AS `__loom_row_id`"}
	for _, output := range plan.Outputs {
		projection := projectionFor(plan, 0, output.ID)
		column := resolvedColumnByID(left, projection.InputColumnID)
		selects = append(selects, fmt.Sprintf("%s.`%s` AS `%s`", leftAlias, column.Name, output.Name))
	}
	selects = append(selects, authorizationPathExpression(left, leftAlias)+" AS `auth_resource_path`")
	rightKeys := make([]string, 0, len(plan.Keys)+1)
	conditions := make([]string, 0, len(plan.Keys)+1)
	for index, key := range plan.Keys {
		leftColumn := resolvedColumnByID(left, key.LeftColumnID)
		rightColumn := resolvedColumnByID(right, key.RightColumnID)
		rightKeys = append(rightKeys, fmt.Sprintf("%s.`%s` AS `__loom_key_%d`", rightSourceAlias, rightColumn.Name, index))
		conditions = append(conditions, fmt.Sprintf("%s.`%s` = %s.`__loom_key_%d`", leftAlias, leftColumn.Name, rightAlias, index))
	}
	rightKeys = append(rightKeys, "1 AS `__loom_match`")
	if !left.Unrestricted {
		rightKeys = append(rightKeys, rightSourceAlias+".`auth_resource_path` AS `auth_resource_path`")
		conditions = append(conditions, leftAlias+".`auth_resource_path` = "+rightAlias+".`auth_resource_path`")
	}
	mode := "IS NOT NULL"
	if plan.MembershipMode == "EXCLUDE" {
		mode = "IS NULL"
	}
	rightQuery := "SELECT DISTINCT " + strings.Join(rightKeys, ", ") + " FROM " + quoteIdentifier(right.PhysicalTable) + " AS " + rightSourceAlias
	rightWhere, args := inputScopePredicate(right, rightSourceAlias)
	if len(rightWhere) != 0 {
		rightQuery += " WHERE " + strings.Join(rightWhere, " AND ")
	}
	query := "SELECT " + strings.Join(selects, ", ") +
		" FROM " + quoteIdentifier(left.PhysicalTable) + " AS " + leftAlias +
		" LEFT ANY JOIN (" + rightQuery + ") AS " + rightAlias + " ON " + strings.Join(conditions, " AND ")
	leftWhere, leftArgs := inputScopePredicate(left, leftAlias)
	where := append(leftWhere, rightAlias+".`__loom_match` "+mode)
	query += " WHERE " + strings.Join(where, " AND ")
	args = append(args, leftArgs...)
	query += " ORDER BY `__loom_row_id` ASC SETTINGS join_use_nulls = 1"
	return query, args
}

func inputScopePredicate(input ir.ResolvedClickHouseTable, alias string) ([]string, []any) {
	if input.Unrestricted {
		return nil, nil
	}
	return []string{alias + ".`auth_resource_path` IN ?"}, []any{append([]string(nil), input.AuthResourcePaths...)}
}

func keyJoinIdentity(leftAlias, rightAlias, joinType string) string {
	leftID := "toString(" + leftAlias + ".`__loom_row_id`)"
	rightID := "toString(" + rightAlias + ".`__loom_row_id`)"
	if joinType == "LEFT" {
		rightID = "ifNull(" + rightID + ", '')"
	}
	return "concat(toString(lengthUTF8(" + leftID + ")), ':', " + leftID + ", ':', toString(lengthUTF8(" + rightID + ")), ':', " + rightID + ") AS `__loom_row_id`"
}

func appendIdentity(alias string, inputIndex int) string {
	rowID := "toString(" + alias + ".`__loom_row_id`)"
	return fmt.Sprintf("concat('%d:', toString(lengthUTF8(%s)), ':', %s) AS `__loom_row_id`", inputIndex, rowID, rowID)
}

func authorizationPathExpression(input ir.ResolvedClickHouseTable, alias string) string {
	if !input.Unrestricted {
		return "ifNull(" + alias + ".`auth_resource_path`, '')"
	}
	return "''"
}

func combineQueryColumns(plan ir.PhysicalClickHouseCombine) []string {
	columns := make([]string, 0, len(plan.Outputs)+2)
	columns = append(columns, "__loom_row_id")
	for _, output := range plan.Outputs {
		columns = append(columns, output.Name)
	}
	return append(columns, "auth_resource_path")
}

func outputColumn(plan ir.PhysicalClickHouseCombine, id string) ir.PhysicalCombineOutputColumn {
	for _, output := range plan.Outputs {
		if output.ID == id {
			return output
		}
	}
	return ir.PhysicalCombineOutputColumn{}
}

func projectionFor(plan ir.PhysicalClickHouseCombine, inputIndex int, outputID string) ir.PhysicalCombineProjection {
	for _, projection := range plan.Projections {
		if projection.InputIndex == inputIndex && projection.OutputColumnID == outputID {
			return projection
		}
	}
	return ir.PhysicalCombineProjection{}
}

func resolvedColumnByID(input ir.ResolvedClickHouseTable, id string) ir.ResolvedClickHouseColumn {
	for _, column := range input.Columns {
		if column.ID == id {
			return column
		}
	}
	return ir.ResolvedClickHouseColumn{}
}

func inputColumnByName(input ir.ResolvedClickHouseTable, name string) (ir.ResolvedClickHouseColumn, bool) {
	for _, column := range input.Columns {
		if column.Name == name {
			return column, true
		}
	}
	return ir.ResolvedClickHouseColumn{}, false
}

func joinableType(value string) bool {
	if strings.HasPrefix(value, "Nullable(") || strings.HasPrefix(value, "Array(") {
		return false
	}
	return value == "String" || value == "Bool" || strings.HasPrefix(value, "Int") || strings.HasPrefix(value, "UInt") || strings.HasPrefix(value, "Decimal") || strings.HasPrefix(value, "Date")
}

func quoteIdentifier(value string) string { return "`" + value + "`" }

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy, rightCopy := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}
