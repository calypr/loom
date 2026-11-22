package recipe

import (
	"fmt"
	"strings"
)

type DynamicColumn struct {
	Name      string    `json:"name"`
	ValueMode ValueMode `json:"valueMode,omitempty"`

	ColumnPrefix *string     `json:"columnPrefix,omitempty"`
	Source       Expression  `json:"source"`
	Key          *Expression `json:"key,omitempty"`
	Value        *Expression `json:"value,omitempty"`
	Columns      []string    `json:"columns,omitzero"`
	MaxColumns   int         `json:"maxColumns,omitempty"`

	ColumnTypes map[string]string `json:"columnTypes,omitempty"`

	ColumnSourceKeys map[string]string `json:"columnSourceKeys,omitempty"`

	OutputOverrides []DynamicColumnOutputOverride `json:"outputOverrides,omitempty"`
	Discovered      bool                          `json:"-"`
}

type DynamicColumnOutputOverride struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

type ExtensionColumn struct {
	Name            string                          `json:"name"`
	Source          Expression                      `json:"source"`
	ColumnPrefix    *string                         `json:"columnPrefix,omitempty"`
	MaxColumns      int                             `json:"maxColumns"`
	Columns         []ExtensionColumnMapping        `json:"columns,omitzero"`
	OutputOverrides []ExtensionColumnOutputOverride `json:"outputOverrides,omitempty"`
	Discovered      bool                            `json:"-"`
}

type ExtensionColumnOutputOverride struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

type ExtensionColumnMapping struct {
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	URLPath    []string `json:"urlPath,omitempty"`
	SourcePath string   `json:"sourcePath,omitempty"`
	ValuePath  string   `json:"valuePath,omitempty"`
	ValueType  string   `json:"valueType"`
}

type CatalogProjection struct {
	Name         string   `json:"name"`
	IncludePaths []string `json:"includePaths,omitempty"`
	ExcludePaths []string `json:"excludePaths,omitempty"`

	OutputOverrides []CatalogProjectionOutputOverride `json:"outputOverrides,omitempty"`
	Kinds           []string                          `json:"kinds,omitempty"`
	Naming          ColumnNaming                      `json:"naming,omitempty"`
	ValueMode       ValueMode                         `json:"valueMode,omitempty"`
	MaxColumns      int                               `json:"maxColumns"`
}

type CatalogProjectionOutputOverride struct {
	FieldPath string `json:"fieldPath"`
	Enabled   bool   `json:"enabled"`
	Name      string `json:"name,omitempty"`
}

type ColumnNaming string

const (
	ColumnNamingPath       ColumnNaming = "PATH"
	ColumnNamingPathSuffix ColumnNaming = "PATH_SUFFIX"
)

func (p CatalogProjection) validateAt(path string) error {
	if err := validateRecipeName(p.Name, path+".name"); err != nil {
		return err
	}
	if len(p.IncludePaths) == 0 {
		return validationError("required", path+".includePaths", "at least one path pattern is required")
	}
	if p.MaxColumns <= 0 || p.MaxColumns > maxCatalogProjectionColumns {
		return validationError("invalid_max_columns", path+".maxColumns", fmt.Sprintf("must be 1..%d", maxCatalogProjectionColumns))
	}
	if p.Naming != "" && p.Naming != ColumnNamingPath && p.Naming != ColumnNamingPathSuffix {
		return validationError("invalid_naming", path+".naming", "must be PATH or PATH_SUFFIX")
	}
	if !p.ValueMode.Valid() {
		return validationError("invalid_value_mode", path+".valueMode", fmt.Sprintf("unsupported value mode %q", p.ValueMode))
	}
	if len(p.OutputOverrides) > maxCatalogProjectionColumns {
		return validationError("too_many_output_overrides", path+".outputOverrides", fmt.Sprintf("must contain at most %d entries", maxCatalogProjectionColumns))
	}
	overridePaths := map[string]struct{}{}
	for i, override := range p.OutputOverrides {
		overridePath := fmt.Sprintf("%s.outputOverrides[%d]", path, i)
		if override.FieldPath == "" || override.FieldPath != strings.TrimSpace(override.FieldPath) || len(override.FieldPath) > 512 {
			return validationError("invalid_field_path", overridePath+".fieldPath", "must be a nonempty trimmed path of at most 512 bytes")
		}
		if _, exists := overridePaths[override.FieldPath]; exists {
			return validationError("duplicate_field_path", overridePath+".fieldPath", "field path is repeated")
		}
		overridePaths[override.FieldPath] = struct{}{}
		if override.Name != "" {
			if err := validateRecipeName(override.Name, overridePath+".name"); err != nil {
				return err
			}
		}
	}
	seen := map[string]struct{}{}
	for i, kind := range p.Kinds {
		kind = strings.TrimSpace(kind)
		if kind == "" {
			return validationError("required", fmt.Sprintf("%s.kinds[%d]", path, i), "kind is required")
		}
		if _, ok := seen[kind]; ok {
			return validationError("duplicate_name", fmt.Sprintf("%s.kinds[%d]", path, i), "duplicate kind")
		}
		seen[kind] = struct{}{}
	}
	return nil
}

func (p CatalogProjection) Validate() error {
	return p.validateAt("catalogProjection")
}
