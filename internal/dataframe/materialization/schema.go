package materialization

import (
	"fmt"
	"regexp"
	"strings"
)

var schemaIdentifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var supportedSchemaScalarRE = regexp.MustCompile(`^(String|UUID|Bool|Int8|Int16|Int32|Int64|Int128|Int256|UInt8|UInt16|UInt32|UInt64|UInt128|UInt256|Float32|Float64|Date|Date32|DateTime|DateTime64(\([^)]*\))?)$`)

func validateBundleColumn(column Column) error {
	if column.Name == "" || !schemaIdentifierRE.MatchString(column.Name) || column.Name == "__loom_row_id" {
		return fmt.Errorf("invalid dataframe schema column %q", column.Name)
	}
	if column.ClickHouse == "" || !validSchemaType(column.ClickHouse) {
		return fmt.Errorf("unsupported ClickHouse type %q for schema column %q", column.ClickHouse, column.Name)
	}
	return nil
}

func validSchemaType(typ string) bool {
	if strings.HasPrefix(typ, "Nullable(") && strings.HasSuffix(typ, ")") {
		typ = strings.TrimSuffix(strings.TrimPrefix(typ, "Nullable("), ")")
	}
	if strings.HasPrefix(typ, "Array(") && strings.HasSuffix(typ, ")") {
		typ = strings.TrimSuffix(strings.TrimPrefix(typ, "Array("), ")")
	}
	return supportedSchemaScalarRE.MatchString(typ)
}

func findColumn(columns []Column, name string) (Column, bool) {
	for _, column := range columns {
		if column.Name == name {
			return column, true
		}
	}
	return Column{}, false
}
