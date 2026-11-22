package projectconfig

import (
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func projectConfigPublicColumnName(output recipe.Output, column lower.CompiledOutputColumn) (string, error) {
	name := strings.TrimSpace(column.Name)
	if name == "" {
		return "", errors.New("public column name is empty")
	}
	if output.RootColumnNaming.Normalized() == recipe.RootColumnNamingPrefixed {
		name = publication.FlatColumnName(output.RootResourceType, name)
	}
	if name == "" || name == "auth_resource_path" || name == "project_id" || name == "_key" || strings.HasPrefix(name, "__loom_") {
		return "", fmt.Errorf("compiler marked reserved column %q as public", name)
	}
	return name, nil
}

func validProjectConfigCardinality(value string) bool {
	switch value {
	case "required_one", "optional_one", "many":
		return true
	default:
		return false
	}
}

func validProjectConfigLogicalType(value string) bool {
	switch value {
	case "boolean", "integer", "decimal", "string", "code", "uuid", "date", "date_time", "date-time", "object":
		return true
	default:
		return false
	}
}

func projectConfigScalarCapabilities(column lower.CompiledOutputColumn) (filterable, chartable bool) {
	if column.Cardinality == "many" || column.Kind == "object" {
		return false, false
	}
	return true, true
}

func projectConfigColumnShape(column lower.CompiledOutputColumn) string {
	if column.Cardinality == "many" {
		if column.Kind == "object" {
			return "record_list"
		}
		return "array"
	}
	if column.Kind == "object" {
		return "record"
	}
	return "scalar"
}
