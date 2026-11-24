package clickhouse

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/store/clickhouse"
	"regexp"
	"strings"
)

func withRowIdentityColumn(columns []clickhouse.Column) []clickhouse.Column {
	for _, column := range columns {
		if column.Name == "__loom_row_id" {
			return append([]clickhouse.Column(nil), columns...)
		}
	}
	result := make([]clickhouse.Column, 0, len(columns)+1)
	result = append(result, clickhouse.Column{Name: "__loom_row_id", Type: "String"})
	return append(result, columns...)
}

func addGeneratedRowIdentities(rows []map[string]any, start int64) []map[string]any {
	result := make([]map[string]any, len(rows))
	for index, row := range rows {
		copy := cloneBundleRow(row)
		if _, ok := copy["__loom_row_id"]; !ok {
			copy["__loom_row_id"] = fmt.Sprintf("%d", start+int64(index))
		}
		result[index] = copy
	}
	return result
}

func cloneBundleRow(row map[string]any) map[string]any {
	copy := make(map[string]any, len(row)+1)
	for key, value := range row {
		copy[key] = value
	}
	return copy
}

var bundleOutputRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var schemaIdentifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var supportedSchemaScalarRE = regexp.MustCompile(`^(String|JSON|UUID|Bool|Int8|Int16|Int32|Int64|Int128|Int256|UInt8|UInt16|UInt32|UInt64|UInt128|UInt256|Float32|Float64|Date|Date32|DateTime|DateTime64(\([^)]*\))?)$`)

func validateBundleColumn(column publication.PhysicalColumn) error {
	if column.Name == "" || !schemaIdentifierRE.MatchString(column.Name) || column.Name == "__loom_row_id" {
		return fmt.Errorf("invalid dataframe schema column %q", column.Name)
	}
	typ := column.ClickHouse
	for strings.HasPrefix(typ, "Nullable(") || strings.HasPrefix(typ, "Array(") {
		typ = strings.TrimSuffix(strings.TrimPrefix(typ, strings.SplitN(typ, "(", 2)[0]+"("), ")")
	}
	if typ == "" || !supportedSchemaScalarRE.MatchString(typ) {
		return fmt.Errorf("unsupported ClickHouse type %q for schema column %q", column.ClickHouse, column.Name)
	}
	return nil
}

func validBundleOutput(value string) bool { return bundleOutputRE.MatchString(value) }

func toColumns(columns []publication.LogicalColumn) []clickhouse.Column {
	result := make([]clickhouse.Column, 0, len(columns))
	for _, column := range columns {
		kind := strings.ToLower(strings.TrimSpace(column.Kind))
		columnType := "String"
		logicalType := ""
		switch kind {
		case "boolean":
			columnType = "Bool"
		case "integer":
			columnType = "Int64"
		case "decimal":
			columnType = "Float64"
		case "date":
			columnType = "Date"
		case "date-time":
			columnType = "DateTime64(3)"
		case "uuid":
			columnType = "UUID"
		case "code":
			columnType = "String"
		case "object":

			columnType = "String"
			logicalType = "object"
		}
		if column.Repeated {
			if logicalType == "object" {
				columnType = "Array(Nullable(String))"
			} else {
				columnType = "Array(" + columnType + ")"
			}
		} else if column.Nullable {
			columnType = "Nullable(" + columnType + ")"
		}
		result = append(result, clickhouse.Column{Name: column.Name, Type: columnType, LogicalType: logicalType})
	}
	return result
}

func allOutputsQueryable(outputs []publication.BundleOutputRecord) bool {
	if len(outputs) == 0 {
		return false
	}
	for _, output := range outputs {
		if !output.Queryable() {
			return false
		}
	}
	return true
}
