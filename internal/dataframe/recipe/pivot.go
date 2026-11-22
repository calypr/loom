package recipe

import (
	"encoding/json"
	"fmt"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
	"strings"
)

type Pivot struct {
	Name             string       `json:"name"`
	FieldRef         string       `json:"fieldRef,omitempty"`
	ColumnExpr       Expression   `json:"columnExpr"`
	ValueExpr        Expression   `json:"valueExpr"`
	ValueFallbacks   []Expression `json:"valueFallbacks,omitempty"`
	ItemSource       Expression   `json:"itemSource,omitempty"`
	ItemResourceType string       `json:"itemResourceType,omitempty"`
	Columns          []string     `json:"columns"`

	ColumnAliases map[string]string `json:"columnAliases,omitempty"`

	OutputOverrides []PivotOutputOverride `json:"outputOverrides,omitempty"`

	ProjectionMode string          `json:"projectionMode,omitempty"`
	Discovery      *PivotDiscovery `json:"discovery,omitempty"`

	Correlation        *fhirbinding.CorrelatedBinding `json:"correlation,omitempty"`
	CorrelationSystem  string                         `json:"correlationSystem,omitempty"`
	CorrelationCode    string                         `json:"correlationCode,omitempty"`
	CorrelationVersion string                         `json:"correlationVersion,omitempty"`

	ExtensionCorrelation *fhirbinding.ExtensionBinding `json:"extensionCorrelation,omitempty"`
	Discovered           bool                          `json:"-"`
}

type PivotOutputOverride struct {
	Key     string  `json:"key"`
	Enabled bool    `json:"enabled"`
	Name    *string `json:"name,omitempty"`
}

type OwnerRecordProjection struct {
	Name     string                        `json:"name"`
	FieldRef string                        `json:"fieldRef,omitempty"`
	Binding  fhirbinding.CorrelatedBinding `json:"binding"`
	Key      fhirbinding.CorrelatedKey     `json:"key"`
}

const (
	PivotProjectionValue    = "VALUE"
	PivotProjectionFirst    = "FIRST"
	PivotProjectionAll      = "ALL"
	PivotProjectionDistinct = "DISTINCT"
)

func ValidPivotProjectionMode(mode string) bool {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case "", PivotProjectionValue, PivotProjectionFirst, PivotProjectionAll, PivotProjectionDistinct:
		return true
	default:
		return false
	}
}

func NormalizedPivotProjectionMode(mode string) string {
	mode = strings.ToUpper(strings.TrimSpace(mode))
	if mode == "" {
		return PivotProjectionFirst
	}
	return mode
}

func (p Pivot) MarshalJSON() ([]byte, error) {
	type pivotJSON struct {
		Name                 string                         `json:"name"`
		FieldRef             string                         `json:"fieldRef,omitempty"`
		ColumnExpr           *Expression                    `json:"columnExpr,omitempty"`
		ValueExpr            *Expression                    `json:"valueExpr,omitempty"`
		ValueFallbacks       []Expression                   `json:"valueFallbacks,omitempty"`
		ItemSource           *Expression                    `json:"itemSource,omitempty"`
		ItemResourceType     string                         `json:"itemResourceType,omitempty"`
		Columns              []string                       `json:"columns"`
		ColumnAliases        map[string]string              `json:"columnAliases,omitempty"`
		OutputOverrides      []PivotOutputOverride          `json:"outputOverrides,omitempty"`
		ProjectionMode       string                         `json:"projectionMode,omitempty"`
		Discovery            *PivotDiscovery                `json:"discovery,omitempty"`
		Correlation          *fhirbinding.CorrelatedBinding `json:"correlation,omitempty"`
		CorrelationSystem    string                         `json:"correlationSystem,omitempty"`
		CorrelationCode      string                         `json:"correlationCode,omitempty"`
		CorrelationVersion   string                         `json:"correlationVersion,omitempty"`
		ExtensionCorrelation *fhirbinding.ExtensionBinding  `json:"extensionCorrelation,omitempty"`
	}
	wire := pivotJSON{Name: p.Name, FieldRef: p.FieldRef, ValueFallbacks: p.ValueFallbacks, ItemResourceType: p.ItemResourceType, Columns: p.Columns, ColumnAliases: p.ColumnAliases, OutputOverrides: p.OutputOverrides, ProjectionMode: p.ProjectionMode, Discovery: p.Discovery, Correlation: p.Correlation, CorrelationSystem: p.CorrelationSystem, CorrelationCode: p.CorrelationCode, CorrelationVersion: p.CorrelationVersion, ExtensionCorrelation: p.ExtensionCorrelation}
	if p.Correlation == nil && p.ExtensionCorrelation == nil && (p.Discovery == nil || !p.ColumnExpr.zero()) {
		value := p.ColumnExpr
		wire.ColumnExpr = &value
	}
	if p.Correlation == nil && p.ExtensionCorrelation == nil && (p.Discovery == nil || !p.ValueExpr.zero()) {
		value := p.ValueExpr
		wire.ValueExpr = &value
	}
	if !p.ItemSource.zero() {
		value := p.ItemSource
		wire.ItemSource = &value
	}
	return json.Marshal(wire)
}

func (e Expression) zero() bool {
	return e.Select == "" && e.Call == "" && e.Literal == nil && e.Document == nil && len(e.Args) == 0
}

type PivotDiscovery struct {
	Family     string `json:"family,omitempty"`
	Path       string `json:"path,omitempty"`
	MaxColumns int    `json:"maxColumns"`
}

func (p Pivot) validateAt(path string, budget *int) error {
	if err := validateRecipeName(p.Name, path+".name"); err != nil {
		return err
	}
	if p.Discovery != nil && len(p.Columns) != 0 {
		return validationError("ambiguous_columns", path, "pivot may use static columns or discovery, not both")
	}
	if p.Discovery == nil && (len(p.Columns) == 0 || len(p.Columns) > maxPivotColumns) {
		return validationError("invalid_columns", path+".columns", fmt.Sprintf("must contain 1..%d columns", maxPivotColumns))
	}
	if p.Discovery != nil {
		if strings.TrimSpace(p.Discovery.Family) == "" && strings.TrimSpace(p.Discovery.Path) == "" {
			return validationError("required", path+".discovery", "family or path is required")
		}
		if p.Discovery.MaxColumns <= 0 || p.Discovery.MaxColumns > maxPivotColumns {
			return validationError("invalid_max_columns", path+".discovery.maxColumns", fmt.Sprintf("must be 1..%d", maxPivotColumns))
		}
	}
	seen := map[string]bool{}
	for i, column := range p.Columns {
		if strings.TrimSpace(column) == "" {
			return validationError("required", fmt.Sprintf("%s.columns[%d]", path, i), "pivot column is required")
		}
		if seen[column] {
			return validationError("duplicate_name", fmt.Sprintf("%s.columns[%d]", path, i), "duplicate pivot column")
		}
		seen[column] = true
	}
	if !ValidPivotProjectionMode(p.ProjectionMode) {
		return validationError("invalid_projection_mode", path+".projectionMode", fmt.Sprintf("unsupported projection mode %q", p.ProjectionMode))
	}
	aliases := map[string]string{}
	for key, alias := range p.ColumnAliases {
		if !seen[key] {
			return validationError("unknown_column_alias", path+".columnAliases", fmt.Sprintf("alias key %q is not a pivot column", key))
		}
		if err := validateRecipeName(alias, path+".columnAliases."+key); err != nil {
			return err
		}
		if previous, exists := aliases[alias]; exists && previous != key {
			return validationError("duplicate_column_alias", path+".columnAliases", fmt.Sprintf("columns %q and %q share output alias %q", previous, key, alias))
		}
		aliases[alias] = key
	}
	if len(p.OutputOverrides) > maxPivotColumns {
		return validationError("too_many_output_overrides", path+".outputOverrides", fmt.Sprintf("must contain at most %d entries", maxPivotColumns))
	}
	overrideKeys := map[string]bool{}
	for i, override := range p.OutputOverrides {
		overridePath := fmt.Sprintf("%s.outputOverrides[%d]", path, i)
		if strings.TrimSpace(override.Key) == "" || len(override.Key) > 2048 {
			return validationError("invalid_output_key", overridePath+".key", "must be a nonempty key of at most 2048 bytes")
		}
		if overrideKeys[override.Key] {
			return validationError("duplicate_output_key", overridePath+".key", "pivot output key is repeated")
		}
		overrideKeys[override.Key] = true
		if p.Discovery == nil && !seen[override.Key] {
			return validationError("unknown_output_override", overridePath+".key", fmt.Sprintf("key %q is not a pinned pivot column", override.Key))
		}
		if override.Name != nil && *override.Name != "" {
			if err := validateRecipeName(*override.Name, overridePath+".name"); err != nil {
				return err
			}
		}
	}
	if p.Correlation != nil && p.ExtensionCorrelation != nil {
		return validationError("ambiguous_correlation", path, "pivot cannot carry both terminology and extension correlation")
	}
	if p.Correlation != nil {
		if p.Discovery != nil {
			return validationError("ambiguous_correlation", path, "correlated pivot cannot use discovery")
		}
		if err := fhirbinding.ValidateCorrelatedKey(p.Correlation.SelectionScope, fhirbinding.CorrelatedKey{System: p.CorrelationSystem, Code: p.CorrelationCode, Version: p.CorrelationVersion}); err != nil {
			return validationError("invalid_correlation", path+".correlation", err.Error())
		}
		if p.Correlation.SelectionScope == fhirbinding.CorrelatedSelectionScopeSystemVersion && len(p.Columns) != 1 {
			return validationError("invalid_correlation", path+".columns", "SYSTEM_VERSION selection requires exactly one output column")
		}
		if strings.TrimSpace(p.CorrelationVersion) != "" && strings.TrimSpace(p.Correlation.VersionPath) == "" {
			return validationError("invalid_correlation", path+".correlation", "selected version requires a versionPath")
		}
		if p.ItemResourceType == "" && p.ColumnExpr.zero() && p.ValueExpr.zero() && p.ItemSource.zero() && len(p.ValueFallbacks) == 0 {

			return nil
		}
		return validationError("ambiguous_correlation", path, "correlated pivot must not carry legacy selector expressions")
	}
	if p.ExtensionCorrelation != nil {
		if p.Discovery != nil {
			return validationError("ambiguous_correlation", path, "extension-correlated pivot cannot use discovery")
		}
		if p.Correlation != nil || strings.TrimSpace(p.CorrelationSystem) != "" || strings.TrimSpace(p.CorrelationCode) != "" || strings.TrimSpace(p.CorrelationVersion) != "" {
			return validationError("ambiguous_correlation", path, "extension-correlated pivot must not carry terminology correlation fields")
		}
		if p.ItemResourceType != "" || !p.ColumnExpr.zero() || !p.ValueExpr.zero() || !p.ItemSource.zero() || len(p.ValueFallbacks) != 0 {
			return validationError("ambiguous_correlation", path, "extension-correlated pivot must not carry legacy selector expressions")
		}
		return nil
	}

	if p.Discovery != nil && p.ColumnExpr.Select == "" && p.ValueExpr.Select == "" && p.ColumnExpr.Call == "" && p.ValueExpr.Call == "" {
		return nil
	}
	if err := validateSelectorExpression(p.ColumnExpr, path+".columnExpr"); err != nil {
		return err
	}
	if err := validateSelectorExpression(p.ValueExpr, path+".valueExpr"); err != nil {
		return err
	}
	if err := validateExpressionBudget(p.ColumnExpr, path+".columnExpr", budget); err != nil {
		return err
	}
	if err := validateExpressionBudget(p.ValueExpr, path+".valueExpr", budget); err != nil {
		return err
	}
	if !p.ItemSource.zero() {
		if err := validateExpressionBudget(p.ItemSource, path+".itemSource", budget); err != nil {
			return err
		}
	}
	for index, fallback := range p.ValueFallbacks {
		if err := validateExpressionBudget(fallback, fmt.Sprintf("%s.valueFallbacks[%d]", path, index), budget); err != nil {
			return err
		}
	}
	return nil
}
