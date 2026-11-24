package recipe

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/lineage"
)

const ConstructionSourceProjectionID = "source_projection"

const ConstructionCohortGroupStageID = "group_rows"

const maxConstructionSteps = 128

type Construction struct {
	Version int `json:"version"`

	SourceColumns []StageColumn      `json:"sourceColumns,omitempty"`
	Steps         []ConstructionStep `json:"steps"`
}

type ConstructionStep struct {
	ID        string                 `json:"id"`
	Inputs    []ConstructionInputRef `json:"inputs"`
	Operation ConstructionOperation  `json:"operation"`
	Outputs   []StageColumn          `json:"outputs"`
	RowValues []ConstructionRowValue `json:"rowValues,omitempty"`
}

type ConstructionRowValue struct {
	InputColumnID  string                        `json:"inputColumnId,omitempty"`
	FieldSource    *ConstructionGroupFieldSource `json:"fieldSource,omitempty"`
	OutputColumnID string                        `json:"outputColumnId"`
	Policy         ConstructionRowValuePolicy    `json:"policy"`
}

type ConstructionRowValuePolicy string

const (
	ConstructionRowValueAll ConstructionRowValuePolicy = "ALL"
	ConstructionRowValueOne ConstructionRowValuePolicy = "ONE"
)

type ConstructionInputKind string

const (
	ConstructionSourceProjectionInput ConstructionInputKind = "SOURCE_PROJECTION"
	ConstructionStepOutputInput       ConstructionInputKind = "STEP_OUTPUT"
	ConstructionTableRevisionInput    ConstructionInputKind = "TABLE_REVISION"
	ConstructionWorkspaceOutputInput  ConstructionInputKind = "WORKSPACE_OUTPUT"
)

type ConstructionInputRef struct {
	Kind       ConstructionInputKind `json:"kind"`
	StepID     string                `json:"stepId,omitempty"`
	TableID    string                `json:"tableId,omitempty"`
	RevisionID string                `json:"revisionId,omitempty"`
	OutputID   string                `json:"outputId,omitempty"`
}

type StageColumn struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	Type     string `json:"type,omitempty"`
	Nullable bool   `json:"nullable,omitempty"`

	SourceChild *lineage.SourceChild `json:"sourceChild,omitempty"`
}

type ConstructionOperationKind string

const (
	ConstructionPivotOp              ConstructionOperationKind = "PIVOT"
	ConstructionDeriveOp             ConstructionOperationKind = "DERIVE"
	ConstructionFilterOp             ConstructionOperationKind = "FILTER"
	ConstructionUnpivotOp            ConstructionOperationKind = "UNPIVOT"
	ConstructionGroupOp              ConstructionOperationKind = "GROUP"
	ConstructionCodedGroupOp         ConstructionOperationKind = "CODED_GROUP"
	ConstructionCodedPivotOp         ConstructionOperationKind = "CODED_PIVOT"
	ConstructionExpandOp             ConstructionOperationKind = "EXPAND"
	ConstructionCombineOp            ConstructionOperationKind = "COMBINE"
	ConstructionRelatedSourceOp      ConstructionOperationKind = "RELATED_SOURCE"
	ConstructionRelatedExpandOp      ConstructionOperationKind = "RELATED_EXPAND"
	ConstructionRelatedEligibilityOp ConstructionOperationKind = "RELATED_ELIGIBILITY"
	ConstructionRelatedFieldOp       ConstructionOperationKind = "RELATED_FIELD"
)

type ConstructionOperation struct {
	Kind               ConstructionOperationKind       `json:"kind"`
	Pivot              *ConstructionPivot              `json:"pivot,omitempty"`
	Derive             *ConstructionDerive             `json:"derive,omitempty"`
	Filter             *ConstructionFilter             `json:"filter,omitempty"`
	Unpivot            *ConstructionUnpivot            `json:"unpivot,omitempty"`
	Group              *ConstructionGroup              `json:"group,omitempty"`
	CodedGroup         *ConstructionCodedGroup         `json:"codedGroup,omitempty"`
	CodedPivot         *ConstructionCodedPivot         `json:"codedPivot,omitempty"`
	Expand             *ConstructionExpand             `json:"expand,omitempty"`
	Combine            *ConstructionCombine            `json:"combine,omitempty"`
	RelatedSource      *ConstructionRelatedSource      `json:"relatedSource,omitempty"`
	RelatedExpand      *ConstructionRelatedExpand      `json:"relatedExpand,omitempty"`
	RelatedEligibility *ConstructionRelatedEligibility `json:"relatedEligibility,omitempty"`
	RelatedField       *ConstructionRelatedField       `json:"relatedField,omitempty"`
}

func (construction Construction) Validate(sourceFields []Field) error {
	return construction.validate(sourceFields, nil)
}

func (construction Construction) ValidateWithGroupRows(sourceFields []Field, groupRows *GroupRows) error {
	if groupRows == nil {
		return construction.Validate(sourceFields)
	}
	return construction.validate(sourceFields, groupRows)
}

func (construction Construction) validate(sourceFields []Field, groupRows *GroupRows) error {
	if construction.Version != 1 {
		return fmt.Errorf("construction version must be 1")
	}
	if len(construction.Steps) > maxConstructionSteps {
		return fmt.Errorf("construction steps must contain at most %d entries", maxConstructionSteps)
	}
	if len(construction.Steps) == 1 && construction.Steps[0].Operation.Kind == ConstructionCombineOp {
		return construction.validateTerminalCombine(sourceFields)
	}
	sourceColumns := append([]StageColumn(nil), construction.SourceColumns...)
	if len(sourceColumns) == 0 {

		sourceColumns = make([]StageColumn, 0, len(sourceFields))
		for _, field := range sourceFields {
			if strings.TrimSpace(field.ColumnID) == "" {
				return fmt.Errorf("source field %q requires a stable columnId", field.Name)
			}
			sourceColumns = append(sourceColumns, StageColumn{ID: field.ColumnID, Name: field.Name, Label: field.Label})
		}
	}
	allowEmptyCodedPivotSource := len(sourceColumns) == 0 && len(sourceFields) == 0 && len(construction.Steps) > 0 &&
		construction.Steps[0].Operation.Kind == ConstructionCodedPivotOp

	allowEmptyRootRelatedExpandSource := len(construction.Steps) > 0 &&
		construction.Steps[0].Operation.Kind == ConstructionRelatedExpandOp &&
		construction.Steps[0].Operation.RelatedExpand != nil && construction.Steps[0].Operation.RelatedExpand.AnchorColumnID == "_key"
	allowEmptyBoundGroupSource := len(construction.Steps) > 0 && constructionFirstGroupHasResolvedFieldSource(construction.Steps[0])
	allowEmptyIdentitySource := len(sourceColumns) == 0 && len(sourceFields) == 0 &&
		(len(construction.Steps) == 0 || allowEmptyRootRelatedExpandSource || allowEmptyBoundGroupSource)
	if len(sourceColumns) == 0 && !allowEmptyCodedPivotSource && !allowEmptyIdentitySource {
		return fmt.Errorf("source projection must contain at least one column")
	}
	if len(sourceColumns) != 0 {
		if err := validateStageColumns(sourceColumns, "source projection"); err != nil {
			return err
		}
	}
	if err := validateSourceChildLineage(sourceColumns); err != nil {
		return err
	}
	if len(construction.Steps) == 0 {
		if groupRows != nil && groupRows.AfterStepID != "" {
			return fmt.Errorf("group rows afterStepId %q does not name a construction step", groupRows.AfterStepID)
		}
		return nil
	}
	groupAnchorIndex := -1
	if groupRows != nil && groupRows.AfterStepID != "" {
		groupAnchorIndex = -1
		for index, step := range construction.Steps {
			if step.ID == groupRows.AfterStepID {
				groupAnchorIndex = index
				break
			}
		}
		if groupAnchorIndex < 0 {
			return fmt.Errorf("group rows afterStepId %q does not name a construction step", groupRows.AfterStepID)
		}
	}
	priorStepID := ""
	priorColumns := sourceColumns
	stepIDs := make(map[string]bool, len(construction.Steps))
	constructionIDs := make(map[string]bool, len(construction.Steps))
	var err error
	for index, step := range construction.Steps {
		if groupRows != nil && index == groupAnchorIndex+1 {
			priorStepID = ConstructionCohortGroupStageID
			priorColumns, err = cohortGroupStageColumns(sourceFields, groupRows)
			if err != nil {
				return fmt.Errorf("group rows stage: %w", err)
			}
		}
		path := fmt.Sprintf("steps[%d]", index)
		if err := validateOpaqueIdentity(step.ID, path+".id"); err != nil {
			return err
		}
		if step.ID == ConstructionSourceProjectionID || groupRows != nil && step.ID == ConstructionCohortGroupStageID || stepIDs[step.ID] {
			return fmt.Errorf("%s.id is reserved or duplicated", path)
		}
		stepIDs[step.ID] = true
		if len(step.Inputs) != 1 {
			return fmt.Errorf("%s.inputs must contain exactly one stage reference", path)
		}
		input := step.Inputs[0]
		if (step.Operation.Kind == ConstructionCodedGroupOp || step.Operation.Kind == ConstructionCodedPivotOp) && (index != 0 || input.Kind != ConstructionSourceProjectionInput || priorStepID != "") {
			return fmt.Errorf("%s %s currently requires the direct source projection stage; prior construction stages are not supported", path, step.Operation.Kind)
		}
		switch input.Kind {
		case ConstructionSourceProjectionInput:
			if index != 0 || priorStepID != "" || input.StepID != "" || input.TableID != "" || input.RevisionID != "" || input.OutputID != "" {
				return fmt.Errorf("%s.inputs[0] must reference the source projection only on the first step", path)
			}
		case ConstructionStepOutputInput:
			if input.StepID != priorStepID || input.TableID != "" || input.RevisionID != "" || input.OutputID != "" || input.StepID == ConstructionCohortGroupStageID && groupRows == nil {
				return fmt.Errorf("%s.inputs[0] must reference the immediately preceding step", path)
			}
		case ConstructionTableRevisionInput:
			if strings.TrimSpace(input.TableID) == "" || strings.TrimSpace(input.RevisionID) == "" || strings.TrimSpace(input.OutputID) == "" || input.StepID != "" {
				return fmt.Errorf("%s.inputs[0] requires tableId, immutable revisionId, and outputId", path)
			}
			return fmt.Errorf("%s.inputs[0] table revision inputs are not supported by this compiler", path)
		default:
			return fmt.Errorf("%s.inputs[0] has unsupported kind %q", path, input.Kind)
		}
		if input.Kind == ConstructionStepOutputInput && !stepIDs[input.StepID] && !(groupRows != nil && input.StepID == ConstructionCohortGroupStageID && input.StepID == priorStepID) {
			return fmt.Errorf("%s.inputs[0] references unknown prior step %q", path, input.StepID)
		}
		if err := validateConstructionOperation(step.Operation, priorColumns, step.Outputs, step.RowValues, path+".operation", constructionIDs); err != nil {
			return err
		}
		if err := validateStageColumns(step.Outputs, path+".outputs"); err != nil {
			return err
		}
		priorStepID = step.ID
		priorColumns = step.Outputs
	}
	if groupRows != nil && groupAnchorIndex == len(construction.Steps)-1 {
		if _, err := cohortGroupStageColumns(sourceFields, groupRows); err != nil {
			return fmt.Errorf("group rows stage: %w", err)
		}
	}
	return nil
}

func validateSourceChildLineage(columns []StageColumn) error {
	for index, column := range columns {
		parsed, generated := lineage.ParseSourceChildID(column.ID)
		if lineage.IsSourceChildID(column.ID) && !generated {
			return fmt.Errorf("source projection[%d].id is a malformed generated child ID", index)
		}
		if generated && column.SourceChild == nil {
			return fmt.Errorf("source projection[%d] generated child ID has no typed source lineage", index)
		}
		if !generated && column.SourceChild != nil {
			return fmt.Errorf("source projection[%d] has lineage for a non-generated child ID", index)
		}
		if column.SourceChild == nil {
			continue
		}
		stableID, _, err := lineage.StableSourceChildID(*column.SourceChild)
		if err != nil {
			return fmt.Errorf("source projection[%d] has invalid source lineage: %w", index, err)
		}
		if stableID != column.ID || parsed.Kind != column.SourceChild.Kind {
			return fmt.Errorf("source projection[%d] source lineage does not match its stable column ID", index)
		}
	}
	return nil
}

func (construction Construction) TerminalCombineStep() (ConstructionStep, bool) {
	if len(construction.Steps) != 1 || construction.Steps[0].Operation.Kind != ConstructionCombineOp {
		return ConstructionStep{}, false
	}
	return construction.Steps[0], true
}

func (construction Construction) validateTerminalCombine(sourceFields []Field) error {
	if len(construction.SourceColumns) != 0 || len(sourceFields) != 0 {
		return fmt.Errorf("terminal combine cannot also declare a source projection")
	}
	step := construction.Steps[0]
	if err := validateOpaqueIdentity(step.ID, "steps[0].id"); err != nil {
		return err
	}
	if step.ID == ConstructionSourceProjectionID {
		return fmt.Errorf("steps[0].id is reserved")
	}
	if len(step.Inputs) < 2 {
		return fmt.Errorf("steps[0].inputs must contain at least two exact inputs")
	}
	seenRefs := make(map[string]bool, len(step.Inputs))
	for index, input := range step.Inputs {
		path := fmt.Sprintf("steps[0].inputs[%d]", index)
		var key string
		switch input.Kind {
		case ConstructionTableRevisionInput:
			if input.StepID != "" || strings.TrimSpace(input.TableID) == "" || input.TableID != strings.TrimSpace(input.TableID) ||
				strings.TrimSpace(input.RevisionID) == "" || input.RevisionID != strings.TrimSpace(input.RevisionID) ||
				strings.TrimSpace(input.OutputID) == "" || input.OutputID != strings.TrimSpace(input.OutputID) {
				return fmt.Errorf("%s requires trimmed tableId, revisionId, and outputId", path)
			}
			key = "table\x00" + input.TableID + "\x00" + input.RevisionID + "\x00" + input.OutputID
		case ConstructionWorkspaceOutputInput:
			if input.StepID != "" || input.TableID != "" || input.RevisionID != "" || strings.TrimSpace(input.OutputID) == "" || input.OutputID != strings.TrimSpace(input.OutputID) {
				return fmt.Errorf("%s requires only a trimmed outputId for WORKSPACE_OUTPUT", path)
			}
			key = "workspace\x00" + input.OutputID
		default:
			return fmt.Errorf("%s must be a TABLE_REVISION or WORKSPACE_OUTPUT reference", path)
		}
		if seenRefs[key] {
			return fmt.Errorf("%s duplicates an exact input reference", path)
		}
		seenRefs[key] = true
	}
	if step.Operation.Combine == nil || step.Operation.Pivot != nil || step.Operation.Derive != nil || step.Operation.Filter != nil ||
		step.Operation.Unpivot != nil || step.Operation.Group != nil || step.Operation.CodedGroup != nil || step.Operation.CodedPivot != nil || step.Operation.Expand != nil {
		return fmt.Errorf("steps[0].operation must contain only a combine payload")
	}
	if err := validateStageColumns(step.Outputs, "steps[0].outputs"); err != nil {
		return err
	}
	for index, output := range step.Outputs {
		if strings.TrimSpace(output.Type) == "" || strings.EqualFold(output.Type, "INFER") || strings.EqualFold(output.Type, "object") {
			return fmt.Errorf("steps[0].outputs[%d].type must be an explicit scalar type", index)
		}
	}
	if err := step.Operation.Combine.Validate(len(step.Inputs), step.Outputs); err != nil {
		return fmt.Errorf("steps[0].operation.combine: %w", err)
	}
	if step.Operation.Combine.Kind == ConstructionCombineKeyJoin && step.Operation.Combine.JoinType == ConstructionCombineLeftJoin {
		for _, projection := range step.Operation.Combine.Projections {
			if projection.InputIndex == 1 && !stageColumnMap(step.Outputs)[projection.OutputColumnID].Nullable {
				return fmt.Errorf("steps[0].outputs column %q must be nullable for a LEFT key join", projection.OutputColumnID)
			}
		}
	}
	return nil
}

func validateStageColumns(columns []StageColumn, path string) error {
	if len(columns) == 0 {
		return fmt.Errorf("%s must contain at least one column", path)
	}
	ids := make(map[string]bool, len(columns))
	names := make(map[string]bool, len(columns))
	for index, column := range columns {
		columnPath := fmt.Sprintf("%s[%d]", path, index)
		if strings.TrimSpace(column.ID) == "" || column.ID != strings.TrimSpace(column.ID) || ids[column.ID] {
			return fmt.Errorf("%s.id is empty, untrimmed, or duplicated", columnPath)
		}
		if err := validateRecipeName(column.Name, columnPath+".name"); err != nil {
			return err
		}
		if names[column.Name] {
			return fmt.Errorf("%s.name %q is duplicated", columnPath, column.Name)
		}
		if column.Type != "" && !strings.EqualFold(column.Type, "INFER") && !strings.EqualFold(column.Type, "array") && !validConstructionLogicalType(column.Type) {
			return fmt.Errorf("%s.type %q is unsupported", columnPath, column.Type)
		}
		ids[column.ID] = true
		names[column.Name] = true
	}
	return nil
}

func validConstructionLogicalType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "boolean", "code", "date", "date-time", "date_time", "datetime", "decimal", "integer", "number", "object", "string", "uuid":
		return true
	default:
		return false
	}
}

func IsValidConstructionLogicalType(value string) bool {
	return validConstructionLogicalType(value)
}

func stageColumnMap(columns []StageColumn) map[string]StageColumn {
	result := make(map[string]StageColumn, len(columns))
	for _, column := range columns {
		result[column.ID] = column
	}
	return result
}

func requireExactStageOutputIDs(expected map[string]bool, actual map[string]StageColumn, path string) error {
	if len(expected) != len(actual) {
		return fmt.Errorf("%s outputs do not match the operation's complete output schema", path)
	}
	for id := range expected {
		if actual[id].ID == "" {
			return fmt.Errorf("%s outputs are missing column ID %q", path, id)
		}
	}
	return nil
}

func validateConstructionID(value, path string, seen map[string]bool) error {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s is required and must be trimmed", path)
	}
	if seen[value] {
		return fmt.Errorf("%s duplicates another construction ID", path)
	}
	seen[value] = true
	return nil
}

func validateOpaqueIdentity(value, path string) error {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || len(value) > 256 {
		return fmt.Errorf("%s is required, trimmed, and at most 256 characters", path)
	}
	return nil
}

func validConstructionColumnID(value string) bool {
	return strings.TrimSpace(value) != "" && value == strings.TrimSpace(value)
}
