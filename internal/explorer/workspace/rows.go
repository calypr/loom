package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

func constructionShapesRows(kind ConstructionOperationKind) bool {
	switch kind {
	case ConstructionOperationGroup, ConstructionOperationCodedGroup, ConstructionOperationPivot, ConstructionOperationCodedPivot:
		return true
	default:
		return false
	}
}

func CanPopulateConstructionRows(construction *Construction) bool {
	if construction == nil {
		return false
	}
	shaped := false
	for _, step := range construction.Steps {
		if constructionShapesRows(step.Operation.Kind) {
			shaped = true
			continue
		}
		if !ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			return false
		}
	}
	return shaped
}

func constructionStepWithoutRowValueOutputs(step ConstructionStep) ConstructionStep {
	if len(step.RowValues) == 0 {
		return step
	}
	attached := make(map[string]bool, len(step.RowValues))
	for _, value := range step.RowValues {
		attached[value.OutputColumnID] = true
	}
	outputs := make([]StageColumn, 0, len(step.Outputs))
	for _, output := range step.Outputs {
		if !attached[output.ID] {
			outputs = append(outputs, output)
		}
	}
	step.Outputs = outputs
	return step
}

func validateConstructionRowValues(step ConstructionStep, input map[string]StageColumn) error {
	if len(step.RowValues) == 0 {
		return nil
	}
	if !constructionShapesRows(step.Operation.Kind) {
		return fmt.Errorf("rowValues require a grouping or pivot step")
	}
	if step.Operation.Kind != ConstructionOperationGroup {
		for index, value := range step.RowValues {
			if value.FieldSource != nil {
				return fmt.Errorf("rowValues[%d].fieldSource is supported only by GROUP", index)
			}
		}
	}
	outputs := make(map[string]StageColumn, len(step.Outputs))
	for _, column := range step.Outputs {
		outputs[column.ID] = column
	}
	seen := make(map[string]bool, len(step.RowValues))
	for index, value := range step.RowValues {
		hasColumn := requiredID(value.InputColumnID)
		hasFieldSource := value.FieldSource != nil
		if hasColumn == hasFieldSource || !requiredID(value.OutputColumnID) || seen[value.OutputColumnID] {
			return fmt.Errorf("rowValues[%d] requires exactly one inputColumnId or fieldSource and a distinct outputColumnId", index)
		}
		seen[value.OutputColumnID] = true
		if value.FieldSource != nil {
			if err := validateConstructionGroupFieldSource(*value.FieldSource); err != nil {
				return fmt.Errorf("rowValues[%d].fieldSource: %w", index, err)
			}
			if value.FieldSource.ValueKind == "CODING_IDENTITY" {
				return fmt.Errorf("rowValues[%d].fieldSource supports SCALAR values only", index)
			}
		} else if _, found := input[value.InputColumnID]; !found {
			return missingConstructionColumn("rowValues.inputColumnId", value.InputColumnID)
		}
		if _, collision := input[value.OutputColumnID]; collision {
			return fmt.Errorf("rowValues output collides with an input column")
		}
		output, found := outputs[value.OutputColumnID]
		if !found {
			return fmt.Errorf("rowValues output %q is not declared", value.OutputColumnID)
		}
		switch value.Policy {
		case ConstructionRowValueAll:
			if output.Type != "array" {
				return fmt.Errorf("ALL row values require an array output")
			}
		case ConstructionRowValueOne:
			if output.Type == "array" {
				return fmt.Errorf("ONE row values require a scalar output")
			}
		default:
			return fmt.Errorf("rowValues policy must be ALL or ONE")
		}
	}
	return nil
}

func populateConstructionColumn(document *Document, column Column, policy ConstructionRowValuePolicy) error {
	if document.Construction == nil || len(document.Construction.Steps) == 0 {
		return nil
	}
	if policy == "" {
		policy = ConstructionRowValueAll
	}
	if policy != ConstructionRowValueAll && policy != ConstructionRowValueOne {
		return fmt.Errorf("row value policy must be ALL or ONE")
	}
	current := StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType, Nullable: true}
	for index := range document.Construction.Steps {
		step := &document.Construction.Steps[index]
		if constructionShapesRows(step.Operation.Kind) {
			digest := sha256.Sum256([]byte(step.ID + "\x00" + column.ColumnID))
			output := current
			output.ID = "row_value_" + hex.EncodeToString(digest[:12])
			output.Type = column.LogicalType
			if policy == ConstructionRowValueAll {
				output.Type = "array"
				output.Nullable = false
			}
			step.RowValues = append(step.RowValues, ConstructionRowValue{InputColumnID: current.ID, OutputColumnID: output.ID, Policy: policy})
			step.Outputs = append(step.Outputs, output)
			current = output
		} else if ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			step.Outputs = append(step.Outputs, current)
		} else {
			return fmt.Errorf("adding source values across %s is not supported", step.Operation.Kind)
		}
	}
	return nil
}

func populateExplicitGroupRowValue(document *Document, column Column, policy ConstructionRowValuePolicy) error {
	if document == nil || document.Rows.Kind != RowDefinitionGroups || document.Rows.Groups == nil ||
		document.Rows.Groups.Source.Kind != GroupSourceExplicit || document.Rows.Groups.Source.Explicit == nil {
		return fmt.Errorf("row values require an explicit group source")
	}
	if column.ColumnID == "" || column.OccurrenceID != RootOccurrenceID || column.Source.Kind != SourceField || column.Source.Field == nil || column.ValueTransformation != nil {
		return fmt.Errorf("explicit group row values support only an untransformed root FHIR field column with a stable columnId")
	}
	if policy == "" {
		policy = ConstructionRowValueAll
	}
	if policy != ConstructionRowValueAll && policy != ConstructionRowValueOne {
		return fmt.Errorf("row value policy must be ALL or ONE")
	}
	values := document.Rows.Groups.RowValues
	for index := range values {
		if values[index].ColumnID == column.ColumnID {
			values[index].Policy = policy
			document.Rows.Groups.RowValues = values
			return nil
		}
	}
	document.Rows.Groups.RowValues = append(values, ExplicitGroupRowValue{ColumnID: column.ColumnID, Policy: policy})
	return nil
}

func removeExplicitGroupRowValue(document *Document, columnID string) {
	if document == nil || document.Rows.Groups == nil || len(document.Rows.Groups.RowValues) == 0 {
		return
	}
	values := document.Rows.Groups.RowValues[:0]
	for _, value := range document.Rows.Groups.RowValues {
		if value.ColumnID != columnID {
			values = append(values, value)
		}
	}
	document.Rows.Groups.RowValues = values
}

func removeConstructionColumnValues(document *Document, columnID string) {
	if document.Construction == nil {
		return
	}
	removed := map[string]bool{columnID: true}
	for index := range document.Construction.Steps {
		step := &document.Construction.Steps[index]
		values := make([]ConstructionRowValue, 0, len(step.RowValues))
		for _, value := range step.RowValues {
			if removed[value.InputColumnID] {
				removed[value.OutputColumnID] = true
			} else {
				values = append(values, value)
			}
		}
		step.RowValues = values
		outputs := make([]StageColumn, 0, len(step.Outputs))
		for _, output := range step.Outputs {
			if !removed[output.ID] {
				outputs = append(outputs, output)
			}
		}
		step.Outputs = outputs
	}
}

const explicitRowsSemanticsVersion = 8

type RowDefinitionKind string

const (
	RowDefinitionRecords  RowDefinitionKind = "RECORDS"
	RowDefinitionGroups   RowDefinitionKind = "GROUPS"
	RowDefinitionExpanded RowDefinitionKind = "EXPANDED"
)

type RowDefinition struct {
	Kind     RowDefinitionKind `json:"kind"`
	Records  *RecordRows       `json:"records,omitempty"`
	Groups   *GroupedRows      `json:"groups,omitempty"`
	Expanded *ExpandedRows     `json:"expanded,omitempty"`
}

func (r RowDefinition) Validate() error {
	payloads := 0
	if r.Records != nil {
		payloads++
	}
	if r.Groups != nil {
		payloads++
	}
	if r.Expanded != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("rows must contain exactly one payload matching kind")
	}

	switch r.Kind {
	case RowDefinitionRecords:
		if r.Records == nil {
			return fmt.Errorf("rows.records is required for kind %q", r.Kind)
		}
	case RowDefinitionGroups:
		if r.Groups == nil {
			return fmt.Errorf("rows.groups is required for kind %q", r.Kind)
		}
		if err := r.Groups.validate(); err != nil {
			return err
		}
	case RowDefinitionExpanded:
		if r.Expanded == nil {
			return fmt.Errorf("rows.expanded is required for kind %q", r.Kind)
		}
		if err := r.Expanded.validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("rows.kind %q is unsupported", r.Kind)
	}
	return nil
}

type RecordRows struct{}

type GroupedRows struct {
	Source      GroupSource             `json:"source"`
	AfterStepID string                  `json:"afterStepId,omitempty"`
	RowValues   []ExplicitGroupRowValue `json:"rowValues,omitempty"`
}

func (g GroupedRows) validate() error {
	if err := g.Source.validate(); err != nil {
		return err
	}
	if g.AfterStepID != "" {
		if err := requireTrimmedIdentity(g.AfterStepID, "afterStepId"); err != nil {
			return err
		}
		if g.AfterStepID == "source_projection" {
			return fmt.Errorf("afterStepId must name an authored construction step")
		}
	}
	if len(g.RowValues) > 0 && g.Source.Kind != GroupSourceExplicit {
		return fmt.Errorf("group row values require an explicit group source")
	}
	seen := make(map[string]bool, len(g.RowValues))
	for index, value := range g.RowValues {
		if err := requireTrimmedIdentity(value.ColumnID, fmt.Sprintf("rowValues[%d].columnId", index)); err != nil {
			return err
		}
		if seen[value.ColumnID] {
			return fmt.Errorf("group row values contain duplicate columnId %q", value.ColumnID)
		}
		seen[value.ColumnID] = true
		if value.Policy != ConstructionRowValueAll && value.Policy != ConstructionRowValueOne {
			return fmt.Errorf("group row values policy must be ALL or ONE")
		}
	}
	return nil
}

type ExplicitGroupRowValue struct {
	ColumnID string                     `json:"columnId"`
	Policy   ConstructionRowValuePolicy `json:"policy"`
}

type GroupSourceKind string

const (
	GroupSourceField    GroupSourceKind = "FIELD"
	GroupSourceExplicit GroupSourceKind = "EXPLICIT"
)

type GroupSource struct {
	Kind     GroupSourceKind      `json:"kind"`
	Field    *FieldGroupSource    `json:"field,omitempty"`
	Explicit *ExplicitGroupSource `json:"explicit,omitempty"`
}

func (s GroupSource) validate() error {
	payloads := 0
	if s.Field != nil {
		payloads++
	}
	if s.Explicit != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("must contain exactly one payload matching kind")
	}
	switch s.Kind {
	case GroupSourceField:
		if s.Field == nil {
			return fmt.Errorf("field payload is required for kind %q", s.Kind)
		}
		return s.Field.validate()
	case GroupSourceExplicit:
		if s.Explicit == nil {
			return fmt.Errorf("explicit payload is required for kind %q", s.Kind)
		}
		return s.Explicit.validate()
	default:
		return fmt.Errorf("kind %q is unsupported", s.Kind)
	}
}

type FieldGroupSource struct {
	OccurrenceID     string           `json:"occurrenceId"`
	FieldPath        string           `json:"fieldPath"`
	MissingKeyPolicy MissingKeyPolicy `json:"missingKeyPolicy"`
}

func (f FieldGroupSource) validate() error {
	if err := requireTrimmedIdentity(f.OccurrenceID, "occurrenceId"); err != nil {
		return fmt.Errorf("field source: %w", err)
	}
	if err := requireTrimmedIdentity(f.FieldPath, "fieldPath"); err != nil {
		return fmt.Errorf("field source: %w", err)
	}
	switch f.MissingKeyPolicy {
	case MissingKeyError, MissingKeyExclude, MissingKeyGroupAsMissing:
		return nil
	default:
		return fmt.Errorf("field source missingKeyPolicy must be ERROR, EXCLUDE, or GROUP_AS_MISSING")
	}
}

type ExplicitGroupSource struct {
	RevisionID             string                 `json:"revisionId"`
	UnassignedMemberPolicy UnassignedMemberPolicy `json:"unassignedMemberPolicy"`
}

func (e ExplicitGroupSource) validate() error {
	if err := requireTrimmedIdentity(e.RevisionID, "revisionId"); err != nil {
		return fmt.Errorf("explicit source: %w", err)
	}
	switch e.UnassignedMemberPolicy {
	case UnassignedMemberError, UnassignedMemberExclude, UnassignedMemberGroupAsUnassigned:
		return nil
	default:
		return fmt.Errorf("explicit source unassignedMemberPolicy must be ERROR, EXCLUDE, or GROUP_AS_UNASSIGNED")
	}
}

type MissingKeyPolicy string

const (
	MissingKeyError          MissingKeyPolicy = "ERROR"
	MissingKeyExclude        MissingKeyPolicy = "EXCLUDE"
	MissingKeyGroupAsMissing MissingKeyPolicy = "GROUP_AS_MISSING"
)

type UnassignedMemberPolicy string

const (
	UnassignedMemberError             UnassignedMemberPolicy = "ERROR"
	UnassignedMemberExclude           UnassignedMemberPolicy = "EXCLUDE"
	UnassignedMemberGroupAsUnassigned UnassignedMemberPolicy = "GROUP_AS_UNASSIGNED"
)

type ExpandedRows struct {
	OccurrenceID          string                `json:"occurrenceId"`
	ScopePath             string                `json:"scopePath"`
	EmptyCollectionPolicy EmptyCollectionPolicy `json:"emptyCollectionPolicy"`
}

func (e ExpandedRows) validate() error {
	if err := requireTrimmedIdentity(e.OccurrenceID, "occurrenceId"); err != nil {
		return fmt.Errorf("rows.expanded: %w", err)
	}
	if err := requireTrimmedIdentity(e.ScopePath, "scopePath"); err != nil {
		return fmt.Errorf("rows.expanded: %w", err)
	}
	switch e.EmptyCollectionPolicy {
	case EmptyCollectionError, EmptyCollectionExclude, EmptyCollectionPreserveParent:
		return nil
	default:
		return fmt.Errorf("rows.expanded.emptyCollectionPolicy must be ERROR, EXCLUDE, or PRESERVE_PARENT")
	}
}

type EmptyCollectionPolicy string

const (
	EmptyCollectionError          EmptyCollectionPolicy = "ERROR"
	EmptyCollectionExclude        EmptyCollectionPolicy = "EXCLUDE"
	EmptyCollectionPreserveParent EmptyCollectionPolicy = "PRESERVE_PARENT"
)

func requireTrimmedIdentity(value, name string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s is required", name)
	}
	if value != trimmed {
		return fmt.Errorf("%s must equal its trimmed value", name)
	}
	return nil
}

func RecordsRowDefinition() RowDefinition {
	return RowDefinition{Kind: RowDefinitionRecords, Records: &RecordRows{}}
}

func cloneRowDefinition(rows RowDefinition) (RowDefinition, error) {
	raw, err := json.Marshal(rows)
	if err != nil {
		return RowDefinition{}, fmt.Errorf("clone row definition: %w", err)
	}
	var cloned RowDefinition
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return RowDefinition{}, fmt.Errorf("clone row definition: %w", err)
	}
	return cloned, nil
}

func missingRowDefinition(rows RowDefinition) bool {
	return rows.Kind == "" && rows.Records == nil && rows.Groups == nil && rows.Expanded == nil
}
