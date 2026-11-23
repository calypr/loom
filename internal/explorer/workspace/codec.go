package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type persistedWorkspaceWire struct {
	APIVersion         string                           `json:"apiVersion"`
	Kind               string                           `json:"kind"`
	SemanticsVersion   int                              `json:"semanticsVersion,omitempty"`
	MigrationDecisions []string                         `json:"migrationDecisions,omitempty"`
	Explorer           ExplorerMetadata                 `json:"explorer"`
	Documents          []persistedDocumentWire          `json:"documents"`
	Tabs               []Tab                            `json:"tabs"`
	SharedFilters      map[string][]SharedFilterBinding `json:"sharedFilters,omitempty"`
	FileActions        *FileActions                     `json:"fileActions,omitempty"`
}

type persistedDocumentWire struct {
	Kind             string                `json:"kind"`
	Output           Output                `json:"output"`
	RootResourceType string                `json:"rootResourceType,omitempty"`
	Route            RouteNode             `json:"route,omitempty"`
	Rows             RowDefinition         `json:"rows"`
	Columns          []persistedColumnWire `json:"columns"`
	FixedFilters     []FixedFilter         `json:"fixedFilters,omitempty"`
	Actions          []Action              `json:"actions,omitempty"`
}

type persistedColumnWire struct {
	Column         string                 `json:"column"`
	Label          string                 `json:"label"`
	LogicalType    string                 `json:"logicalType,omitempty"`
	OccurrenceID   string                 `json:"occurrenceId"`
	Source         json.RawMessage        `json:"source"`
	Contributor    *ContributorPredicate  `json:"contributor,omitempty"`
	Interpretation *FeatureInterpretation `json:"interpretation,omitempty"`
	Table          *TablePresentation     `json:"table,omitempty"`
	Filter         *FilterPresentation    `json:"filter,omitempty"`
	Chart          *ChartPresentation     `json:"chart,omitempty"`
}

type legacyColumnSource struct {
	Kind           string   `json:"kind"`
	FieldPath      string   `json:"fieldPath,omitempty"`
	Match          string   `json:"match,omitempty"`
	ProjectionMode string   `json:"projectionMode,omitempty"`
	Operation      string   `json:"operation,omitempty"`
	WherePath      string   `json:"wherePath,omitempty"`
	WhereEquals    string   `json:"whereEquals,omitempty"`
	RequiredValues []string `json:"requiredValues,omitempty"`
}

func decodePersistedLegacyWorkspace(raw []byte) (Workspace, error) {
	var wire persistedWorkspaceWire
	if err := strictDecode(raw, &wire); err != nil {
		return Workspace{}, err
	}
	out := Workspace{
		APIVersion: wire.APIVersion, Kind: wire.Kind, SemanticsVersion: wire.SemanticsVersion,
		MigrationDecisions: append([]string(nil), wire.MigrationDecisions...), Explorer: wire.Explorer,
		Tabs: append([]Tab(nil), wire.Tabs...), SharedFilters: wire.SharedFilters, FileActions: wire.FileActions,
		Documents: make([]Document, 0, len(wire.Documents)),
	}
	for documentIndex, document := range wire.Documents {
		converted := Document{Kind: document.Kind, Output: document.Output, RootResourceType: document.RootResourceType, Route: document.Route, Rows: document.Rows, FixedFilters: document.FixedFilters, Actions: document.Actions, Columns: make([]Column, 0, len(document.Columns))}
		for columnIndex, column := range document.Columns {
			source, err := decodePersistedSource(column.Source, wire.SemanticsVersion)
			if err != nil {
				return Workspace{}, fmt.Errorf("documents[%d].columns[%d].source: %w", documentIndex, columnIndex, err)
			}
			converted.Columns = append(converted.Columns, Column{Column: column.Column, Label: column.Label, LogicalType: column.LogicalType, OccurrenceID: column.OccurrenceID, Source: source, Contributor: column.Contributor, Interpretation: column.Interpretation, Table: column.Table, Filter: column.Filter, Chart: column.Chart})
		}
		out.Documents = append(out.Documents, converted)
	}
	return out, nil
}

func decodePersistedSource(raw json.RawMessage, semanticsVersion int) (ColumnSource, error) {
	var current ColumnSource
	if err := strictDecode(raw, &current); err == nil {
		return current, nil
	}
	var typedLegacy struct {
		Kind   string        `json:"kind"`
		Lookup *LookupSource `json:"lookup,omitempty"`
	}
	if err := strictDecode(raw, &typedLegacy); err == nil && typedLegacy.Lookup != nil && typedLegacy.Lookup.Binding != nil && typedLegacy.Lookup.Key != nil {
		switch typedLegacy.Kind {
		case "codingBySystem", "observationComponentByCode":
			return ColumnSource{Kind: SourceCodedValue, Lookup: typedLegacy.Lookup}, nil
		}
	}
	var legacy legacyColumnSource
	if err := strictDecode(raw, &legacy); err == nil {
		return decodeLegacyFlatSource(legacy)
	}

	var nested struct {
		Kind      string       `json:"kind"`
		Field     *FieldSource `json:"field,omitempty"`
		Aggregate *struct {
			Operation         string                            `json:"operation"`
			Path              string                            `json:"path,omitempty"`
			Where             *SourceWhere                      `json:"where,omitempty"`
			RequiredValues    []string                          `json:"requiredValues,omitempty"`
			UnitNormalization *UnitNormalizationPolicy          `json:"unitNormalization,omitempty"`
			Temporal          *persistedTemporalReductionSource `json:"temporal,omitempty"`
		} `json:"aggregate,omitempty"`
		Lookup *LookupSource `json:"lookup,omitempty"`
	}
	if nestedErr := strictDecode(raw, &nested); nestedErr != nil {
		return ColumnSource{}, nestedErr
	}
	if nested.Aggregate != nil && nested.Aggregate.Temporal != nil && semanticsVersion != aggregateTemporalPolicySemanticsVersion-1 {
		return ColumnSource{}, fmt.Errorf("persisted aggregate temporal policy is only supported at semantics version %d", aggregateTemporalPolicySemanticsVersion-1)
	}
	if nested.Aggregate == nil || (nested.Aggregate.Where == nil && nested.Aggregate.Temporal == nil) {
		return ColumnSource{}, fmt.Errorf("source is not a supported legacy wire")
	}
	aggregate := &AggregateSource{
		Operation: strings.ToUpper(strings.TrimSpace(nested.Aggregate.Operation)), Path: nested.Aggregate.Path,
		Where: nested.Aggregate.Where, RequiredValues: append([]string(nil), nested.Aggregate.RequiredValues...),
		UnitNormalization: cloneUnitNormalization(nested.Aggregate.UnitNormalization),
	}
	if temporal := nested.Aggregate.Temporal; temporal != nil {
		aggregate.ContributorWindow = &ContributorWindowSource{
			TimestampPath: temporal.TimestampPath, AnchorPath: temporal.AnchorPath,
			LowerOffset: temporal.LowerOffset, UpperOffset: temporal.UpperOffset,
			LowerInclusive: temporal.LowerInclusive, UpperInclusive: temporal.UpperInclusive,
			Precision: temporal.Precision,
		}
		aggregate.Ordering = &TemporalOrderingSource{
			TimestampPath: temporal.TimestampPath, Direction: temporal.Direction, TiePolicy: temporal.TiePolicy,
		}
	}
	return ColumnSource{Kind: SourceAggregate, Aggregate: aggregate}, nil
}

type persistedTemporalReductionSource struct {
	TimestampPath  string `json:"timestampPath"`
	AnchorPath     string `json:"anchorPath"`
	LowerOffset    int64  `json:"lowerOffsetSeconds"`
	UpperOffset    int64  `json:"upperOffsetSeconds"`
	LowerInclusive bool   `json:"lowerInclusive"`
	UpperInclusive bool   `json:"upperInclusive"`
	Direction      string `json:"direction"`
	Precision      string `json:"precision"`
	TiePolicy      string `json:"tiePolicy"`
}

func decodeLegacyFlatSource(legacy legacyColumnSource) (ColumnSource, error) {
	mode := strings.ToUpper(strings.TrimSpace(legacy.ProjectionMode))
	if mode == "" {
		mode = "FIRST"
	}
	switch legacy.Kind {
	case SourceField:
		return ColumnSource{Kind: SourceField, Field: &FieldSource{Path: legacy.FieldPath, ProjectionMode: mode}}, nil
	case SourceIdentifierBySystem, SourceExtensionByURL:
		return ColumnSource{Kind: legacy.Kind, Lookup: &LookupSource{Match: legacy.Match, Path: legacy.FieldPath, ProjectionMode: mode}}, nil
	case SourceAggregate:
		aggregate := &AggregateSource{Operation: strings.ToUpper(strings.TrimSpace(legacy.Operation)), Path: legacy.FieldPath, RequiredValues: append([]string(nil), legacy.RequiredValues...)}
		if strings.TrimSpace(legacy.WherePath) != "" || strings.TrimSpace(legacy.WhereEquals) != "" {
			aggregate.Where = &SourceWhere{Path: legacy.WherePath, Equals: legacy.WhereEquals}
		}
		return ColumnSource{Kind: SourceAggregate, Aggregate: aggregate}, nil
	case SourceProjectID:
		return ColumnSource{Kind: SourceProjectID}, nil
	default:
		return ColumnSource{}, fmt.Errorf("unsupported source kind %q", legacy.Kind)
	}
}

func legacyJSONFieldSpan(raw []byte, field string) (int, int, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return 0, 0, fmt.Errorf("expected canonical object for %s", field)
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return 0, 0, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return 0, 0, err
		}
		if key == field {
			end := int(decoder.InputOffset())
			return end - len(value), end, nil
		}
	}
	return 0, 0, fmt.Errorf("canonical object omitted %s", field)
}
