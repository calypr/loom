package materialization

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

var schemaIdentifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var supportedSchemaScalarRE = regexp.MustCompile(`^(String|UUID|Bool|Int8|Int16|Int32|Int64|Int128|Int256|UInt8|UInt16|UInt32|UInt64|UInt128|UInt256|Float32|Float64|Date|Date32|DateTime|DateTime64(\([^)]*\))?)$`)

func validateSchemaColumn(column Column) error {
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

// ValidateValue checks the coarse Go-to-ClickHouse compatibility of a row
// value against an explicit schema column. It intentionally permits nil for
// every type; callers should use Nullable columns when missing values are
// expected in ClickHouse.
func ValidateValue(column Column, value any) error {
	if value == nil {
		return nil
	}
	t := column.ClickHouse
	if strings.HasPrefix(t, "Nullable(") && strings.HasSuffix(t, ")") {
		t = strings.TrimSuffix(strings.TrimPrefix(t, "Nullable("), ")")
	}
	if strings.HasPrefix(t, "Array(") && strings.HasSuffix(t, ")") {
		valueType := strings.TrimSuffix(strings.TrimPrefix(t, "Array("), ")")
		v := reflect.ValueOf(value)
		if v.Kind() != reflect.Array && v.Kind() != reflect.Slice {
			return fmt.Errorf("column %q expects %s but received %T", column.Name, column.ClickHouse, value)
		}
		for i := 0; i < v.Len(); i++ {
			if err := validateScalar(column.Name, valueType, v.Index(i).Interface()); err != nil {
				return err
			}
		}
		return nil
	}
	return validateScalar(column.Name, t, value)
}

func validateScalar(name, typ string, value any) error {
	switch {
	case typ == "String":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("column %q expects String but received %T", name, value)
		}
	case typ == "UUID":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("column %q expects UUID text but received %T", name, value)
		}
	case typ == "Bool":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("column %q expects Bool but received %T", name, value)
		}
	case strings.HasPrefix(typ, "Int") || strings.HasPrefix(typ, "UInt"):
		kind := reflect.ValueOf(value).Kind()
		if kind < reflect.Int || kind > reflect.Uint64 {
			return fmt.Errorf("column %q expects integer but received %T", name, value)
		}
	case strings.HasPrefix(typ, "Float"):
		kind := reflect.ValueOf(value).Kind()
		if (kind != reflect.Float32 && kind != reflect.Float64) && (kind < reflect.Int || kind > reflect.Uint64) {
			return fmt.Errorf("column %q expects number but received %T", name, value)
		}
	case strings.HasPrefix(typ, "Date"):
		if _, ok := value.(string); !ok {
			return fmt.Errorf("column %q expects date text but received %T", name, value)
		}
	default:
		return fmt.Errorf("unsupported ClickHouse scalar type %q", typ)
	}
	return nil
}
