package recipe

import (
	"fmt"
	"math"
	"strings"

	"github.com/calypr/loom/internal/dataframe/lineage"
)

// ConstructionSourceProjectionID names the implicit source projection stage
// that precedes every authored construction step.
const ConstructionSourceProjectionID = "source_projection"

const maxConstructionSteps = 128

type Construction struct {
	Version int `json:"version"`
	// SourceColumns is populated by the resolved compiler frontend. It binds
	// persisted projection-slot IDs to the exact public names emitted by the
	// source compiler; authoring documents keep their own single source of truth.
	SourceColumns []StageColumn      `json:"sourceColumns,omitempty"`
	Steps         []ConstructionStep `json:"steps"`
}

// ConstructionStep applies one typed operation to the preceding stage and
// declares that stage's complete output schema. Column IDs are stable across
// display-name edits; Names are physical output names.
type ConstructionStep struct {
	ID        string                 `json:"id"`
	Inputs    []ConstructionInputRef `json:"inputs"`
	Operation ConstructionOperation  `json:"operation"`
	Outputs   []StageColumn          `json:"outputs"`
}

type ConstructionInputKind string

const (
	ConstructionSourceProjectionInput ConstructionInputKind = "SOURCE_PROJECTION"
	ConstructionStepOutputInput       ConstructionInputKind = "STEP_OUTPUT"
	ConstructionTableRevisionInput    ConstructionInputKind = "TABLE_REVISION"
)

// ConstructionInputRef is a closed reference to an input row set. Table
// revision references are legal only for a standalone terminal Combine step;
// they always identify an exact immutable publication.
type ConstructionInputRef struct {
	Kind       ConstructionInputKind `json:"kind"`
	StepID     string                `json:"stepId,omitempty"`
	TableID    string                `json:"tableId,omitempty"`
	RevisionID string                `json:"revisionId,omitempty"`
	OutputID   string                `json:"outputId,omitempty"`
}

// StageColumn is the logical schema contract for one stage output. Type is an
// optional authoring hint (or "INFER"); the compiler derives the authoritative
// type from source and operation semantics.
type StageColumn struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	Type     string `json:"type,omitempty"`
	Nullable bool   `json:"nullable,omitempty"`
	// SourceChild keeps the stable structural provenance for a compiler-
	// generated child of an INDEXED source slot. It is populated only on the
	// resolved compiler contract, never persisted as a second authored schema.
	SourceChild *lineage.SourceChild `json:"sourceChild,omitempty"`
}

type ConstructionOperationKind string

const (
	ConstructionPivotOp         ConstructionOperationKind = "PIVOT"
	ConstructionDeriveOp        ConstructionOperationKind = "DERIVE"
	ConstructionFilterOp        ConstructionOperationKind = "FILTER"
	ConstructionUnpivotOp       ConstructionOperationKind = "UNPIVOT"
	ConstructionGroupOp         ConstructionOperationKind = "GROUP"
	ConstructionExpandOp        ConstructionOperationKind = "EXPAND"
	ConstructionCombineOp       ConstructionOperationKind = "COMBINE"
	ConstructionRelatedSourceOp ConstructionOperationKind = "RELATED_SOURCE"
)

// ConstructionOperation is a closed tagged union. Its operands refer to
// stable stage column IDs, never mutable display or physical names.
type ConstructionOperation struct {
	Kind          ConstructionOperationKind  `json:"kind"`
	Pivot         *ConstructionPivot         `json:"pivot,omitempty"`
	Derive        *ConstructionDerive        `json:"derive,omitempty"`
	Filter        *ConstructionFilter        `json:"filter,omitempty"`
	Unpivot       *ConstructionUnpivot       `json:"unpivot,omitempty"`
	Group         *ConstructionGroup         `json:"group,omitempty"`
	Expand        *ConstructionExpand        `json:"expand,omitempty"`
	Combine       *ConstructionCombine       `json:"combine,omitempty"`
	RelatedSource *ConstructionRelatedSource `json:"relatedSource,omitempty"`
}

type ConstructionRelatedSource struct {
	AnchorColumnID     string                         `json:"anchorColumnId"`
	ChoiceID           string                         `json:"choiceId"`
	SourceOccurrenceID string                         `json:"sourceOccurrenceId"`
	Source             ConstructionRelatedFieldSource `json:"source"`
	Route              []ConstructionRelatedRouteStep `json:"route"`
	ContributorPolicy  string                         `json:"contributorPolicy"`
	Form               string                         `json:"form"`
	OutputColumnID     string                         `json:"outputColumnId"`
}

type ConstructionRelatedFieldSource struct {
	CandidateID  string `json:"candidateId"`
	NodeID       string `json:"nodeId"`
	ResourceType string `json:"resourceType"`
	Path         string `json:"path"`
	Cardinality  string `json:"cardinality"`
	LogicalType  string `json:"logicalType"`
}

type ConstructionRelatedRouteStep struct {
	EdgeID           string `json:"edgeId"`
	FromNodeID       string `json:"fromNodeId"`
	ToNodeID         string `json:"toNodeId"`
	FromResourceType string `json:"fromResourceType"`
	ToResourceType   string `json:"toResourceType"`
	Relationship     string `json:"relationship"`
	StorageDirection string `json:"storageDirection"`
	MatchMode        string `json:"matchMode"`
}

type ConstructionPivot struct {
	ConstructionID         string                      `json:"constructionId"`
	GroupKeyIDs            []string                    `json:"groupKeyIds"`
	CategoryColumnID       string                      `json:"categoryColumnId"`
	ValueColumnID          string                      `json:"valueColumnId"`
	Categories             []ConstructionPivotCategory `json:"categories"`
	DuplicatePolicy        PivotDuplicatePolicy        `json:"duplicatePolicy"`
	MissingCellPolicy      PivotMissingCellPolicy      `json:"missingCellPolicy"`
	UnlistedCategoryPolicy PivotUnlistedCategoryPolicy `json:"unlistedCategoryPolicy"`
}

type ConstructionPivotCategory struct {
	Key            TableScalar `json:"key"`
	OutputColumnID string      `json:"outputColumnId"`
}

type ConstructionDerive struct {
	ConstructionID       string               `json:"constructionId"`
	OutputColumnID       string               `json:"outputColumnId"`
	Operation            DerivedOperation     `json:"operation"`
	Left                 ConstructionOperand  `json:"left"`
	Right                ConstructionOperand  `json:"right"`
	MissingInputPolicy   MissingInputPolicy   `json:"missingInputPolicy"`
	DivisionByZeroPolicy DivisionByZeroPolicy `json:"divisionByZeroPolicy,omitempty"`
}

type ConstructionOperand struct {
	Kind     DerivedOperandKind `json:"kind"`
	ColumnID string             `json:"columnId,omitempty"`
	Literal  *DerivedLiteral    `json:"literal,omitempty"`
}

type ConstructionFilter struct {
	ColumnID string         `json:"columnId"`
	Operator FilterOperator `json:"operator"`
	Values   []FilterValue  `json:"values,omitempty"`
}

type ConstructionUnpivot struct {
	ConstructionID      string                     `json:"constructionId"`
	Inputs              []ConstructionUnpivotInput `json:"inputs"`
	KeyOutputColumnID   string                     `json:"keyOutputColumnId"`
	ValueOutputColumnID string                     `json:"valueOutputColumnId"`
	NullRowPolicy       UnpivotNullRowPolicy       `json:"nullRowPolicy"`
}

type ConstructionUnpivotInput struct {
	ColumnID string      `json:"columnId"`
	Key      TableScalar `json:"key"`
}

// ConstructionGroup reduces the preceding row set by zero or more typed
// columns. An empty GroupKeys slice summarizes the whole table.
type ConstructionGroup struct {
	ConstructionID string                       `json:"constructionId"`
	Keys           []ConstructionGroupKey       `json:"keys,omitempty"`
	Aggregates     []ConstructionGroupAggregate `json:"aggregates,omitempty"`
}

type ConstructionGroupKey struct {
	InputColumnID  string `json:"inputColumnId"`
	OutputColumnID string `json:"outputColumnId"`
}

type ConstructionGroupAggregate struct {
	Operation      ConstructionGroupAggregateOp `json:"operation"`
	InputColumnID  string                       `json:"inputColumnId,omitempty"`
	OutputColumnID string                       `json:"outputColumnId"`
}

type ConstructionGroupAggregateOp string

const (
	ConstructionGroupCountRows     ConstructionGroupAggregateOp = "COUNT_ROWS"
	ConstructionGroupCountNonNull  ConstructionGroupAggregateOp = "COUNT_NON_NULL"
	ConstructionGroupCountDistinct ConstructionGroupAggregateOp = "COUNT_DISTINCT"
	ConstructionGroupSum           ConstructionGroupAggregateOp = "SUM"
	ConstructionGroupMin           ConstructionGroupAggregateOp = "MIN"
	ConstructionGroupMax           ConstructionGroupAggregateOp = "MAX"
	ConstructionGroupMean          ConstructionGroupAggregateOp = "MEAN"
)

// ConstructionExpand replaces one array-valued stage column with one row per
// item. OrdinalColumnID is optional; row identity always includes the zero-
// based item ordinal, including when the ordinal is not exposed publicly.
type ConstructionExpand struct {
	ConstructionID  string               `json:"constructionId"`
	InputColumnID   string               `json:"inputColumnId"`
	OutputColumnID  string               `json:"outputColumnId"`
	OrdinalColumnID string               `json:"ordinalColumnId,omitempty"`
	EmptyPolicy     ExpansionEmptyPolicy `json:"emptyPolicy,omitempty"`
}

func (construction Construction) Validate(sourceFields []Field) error {
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
		// Recipe-only callers can validate simple field projections without a
		// resolved source schema. Explorer compilation supplies SourceColumns so
		// aggregate, lookup, and other projection variants retain slot identity.
		sourceColumns = make([]StageColumn, 0, len(sourceFields))
		for _, field := range sourceFields {
			if strings.TrimSpace(field.ColumnID) == "" {
				return fmt.Errorf("source field %q requires a stable columnId", field.Name)
			}
			sourceColumns = append(sourceColumns, StageColumn{ID: field.ColumnID, Name: field.Name, Label: field.Label})
		}
	}
	if err := validateStageColumns(sourceColumns, "source projection"); err != nil {
		return err
	}
	if err := validateSourceChildLineage(sourceColumns); err != nil {
		return err
	}
	if len(construction.Steps) == 0 {
		return nil
	}
	priorStepID := ""
	priorColumns := sourceColumns
	stepIDs := make(map[string]bool, len(construction.Steps))
	constructionIDs := make(map[string]bool, len(construction.Steps))
	for index, step := range construction.Steps {
		path := fmt.Sprintf("steps[%d]", index)
		if err := validateOpaqueIdentity(step.ID, path+".id"); err != nil {
			return err
		}
		if step.ID == ConstructionSourceProjectionID || stepIDs[step.ID] {
			return fmt.Errorf("%s.id is reserved or duplicated", path)
		}
		stepIDs[step.ID] = true
		if len(step.Inputs) != 1 {
			return fmt.Errorf("%s.inputs must contain exactly one stage reference", path)
		}
		input := step.Inputs[0]
		switch input.Kind {
		case ConstructionSourceProjectionInput:
			if index != 0 || input.StepID != "" || input.TableID != "" || input.RevisionID != "" || input.OutputID != "" {
				return fmt.Errorf("%s.inputs[0] must reference the source projection only on the first step", path)
			}
		case ConstructionStepOutputInput:
			if index == 0 || input.StepID != priorStepID || input.TableID != "" || input.RevisionID != "" || input.OutputID != "" {
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
		if input.Kind == ConstructionStepOutputInput && !stepIDs[input.StepID] {
			return fmt.Errorf("%s.inputs[0] references unknown prior step %q", path, input.StepID)
		}
		if err := validateConstructionOperation(step.Operation, priorColumns, step.Outputs, path+".operation", constructionIDs); err != nil {
			return err
		}
		if err := validateStageColumns(step.Outputs, path+".outputs"); err != nil {
			return err
		}
		priorStepID = step.ID
		priorColumns = step.Outputs
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

// TerminalCombineStep reports whether this construction is the standalone
// terminal Combine form. It deliberately excludes source and intermediate
// stages; those require a materialized engine boundary before ClickHouse can
// consume their rows.
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
		return fmt.Errorf("steps[0].inputs must contain at least two exact table revision references")
	}
	seenRefs := make(map[string]bool, len(step.Inputs))
	for index, input := range step.Inputs {
		path := fmt.Sprintf("steps[0].inputs[%d]", index)
		if input.Kind != ConstructionTableRevisionInput || input.StepID != "" {
			return fmt.Errorf("%s must be an exact TABLE_REVISION reference", path)
		}
		if strings.TrimSpace(input.TableID) == "" || input.TableID != strings.TrimSpace(input.TableID) ||
			strings.TrimSpace(input.RevisionID) == "" || input.RevisionID != strings.TrimSpace(input.RevisionID) ||
			strings.TrimSpace(input.OutputID) == "" || input.OutputID != strings.TrimSpace(input.OutputID) {
			return fmt.Errorf("%s requires trimmed tableId, revisionId, and outputId", path)
		}
		key := input.TableID + "\x00" + input.RevisionID + "\x00" + input.OutputID
		if seenRefs[key] {
			return fmt.Errorf("%s duplicates an exact table revision reference", path)
		}
		seenRefs[key] = true
	}
	if step.Operation.Combine == nil || step.Operation.Pivot != nil || step.Operation.Derive != nil || step.Operation.Filter != nil ||
		step.Operation.Unpivot != nil || step.Operation.Group != nil || step.Operation.Expand != nil {
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

func validateConstructionOperation(operation ConstructionOperation, input, output []StageColumn, path string, constructionIDs map[string]bool) error {
	payloads := 0
	for _, present := range []bool{operation.Pivot != nil, operation.Derive != nil, operation.Filter != nil, operation.Unpivot != nil, operation.Group != nil, operation.Expand != nil, operation.RelatedSource != nil} {
		if present {
			payloads++
		}
	}
	if operation.Combine != nil {
		payloads++
	}
	if payloads != 1 {
		return fmt.Errorf("%s must contain exactly one operation payload", path)
	}
	inputByID := stageColumnMap(input)
	outputByID := stageColumnMap(output)
	switch operation.Kind {
	case ConstructionPivotOp:
		if operation.Pivot == nil || operation.Derive != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Group != nil || operation.Expand != nil || operation.Combine != nil {
			return fmt.Errorf("%s pivot operation requires only pivot payload", path)
		}
		pivot := operation.Pivot
		if err := validateConstructionID(pivot.ConstructionID, path+".pivot.constructionId", constructionIDs); err != nil {
			return err
		}
		if len(pivot.GroupKeyIDs) == 0 || len(pivot.Categories) == 0 || len(pivot.Categories) > maxPivotColumns {
			return fmt.Errorf("%s pivot requires group keys and 1..%d categories", path, maxPivotColumns)
		}
		if pivot.CategoryColumnID == "" || pivot.ValueColumnID == "" || pivot.CategoryColumnID == pivot.ValueColumnID {
			return fmt.Errorf("%s pivot category and value column IDs must be distinct and non-empty", path)
		}
		used := map[string]bool{pivot.CategoryColumnID: true, pivot.ValueColumnID: true}
		for _, id := range pivot.GroupKeyIDs {
			if id == "" || used[id] || inputByID[id].ID == "" {
				return fmt.Errorf("%s pivot group key %q is missing or duplicated", path, id)
			}
			used[id] = true
		}
		if inputByID[pivot.CategoryColumnID].ID == "" || inputByID[pivot.ValueColumnID].ID == "" {
			return fmt.Errorf("%s pivot category or value column is not in the input schema", path)
		}
		if !validPivotDuplicatePolicy(pivot.DuplicatePolicy) || !validPivotMissingPolicy(pivot.MissingCellPolicy) || !validPivotUnlistedPolicy(pivot.UnlistedCategoryPolicy) {
			return fmt.Errorf("%s pivot policies are incomplete or unsupported", path)
		}
		expected := make(map[string]bool, len(pivot.GroupKeyIDs)+len(pivot.Categories))
		for _, id := range pivot.GroupKeyIDs {
			expected[id] = true
		}
		for index, category := range pivot.Categories {
			if err := category.Key.ValidatePivotCategoryKey(); err != nil {
				return fmt.Errorf("%s pivot categories[%d].key: %w", path, index, err)
			}
			if category.OutputColumnID == "" || expected[category.OutputColumnID] || inputByID[category.OutputColumnID].ID != "" {
				return fmt.Errorf("%s pivot category output ID %q is empty, duplicated, or collides with input", path, category.OutputColumnID)
			}
			expected[category.OutputColumnID] = true
		}
		return requireExactStageOutputIDs(expected, outputByID, path)
	case ConstructionDeriveOp:
		if operation.Derive == nil || operation.Pivot != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Group != nil || operation.Expand != nil || operation.Combine != nil {
			return fmt.Errorf("%s derive operation requires only derive payload", path)
		}
		derive := operation.Derive
		if err := validateConstructionID(derive.ConstructionID, path+".derive.constructionId", constructionIDs); err != nil {
			return err
		}
		if derive.OutputColumnID == "" || inputByID[derive.OutputColumnID].ID != "" {
			return fmt.Errorf("%s derive outputColumnId is empty or already exists", path)
		}
		if err := validateConstructionDerivedPolicies(*derive, path+".derive"); err != nil {
			return err
		}
		if err := validateConstructionOperand(derive.Left, inputByID, path+".derive.left"); err != nil {
			return err
		}
		if err := validateConstructionOperand(derive.Right, inputByID, path+".derive.right"); err != nil {
			return err
		}
		expected := make(map[string]bool, len(inputByID)+1)
		for id := range inputByID {
			expected[id] = true
		}
		expected[derive.OutputColumnID] = true
		return requireExactStageOutputIDs(expected, outputByID, path)
	case ConstructionFilterOp:
		if operation.Filter == nil || operation.Pivot != nil || operation.Derive != nil || operation.Unpivot != nil || operation.Group != nil || operation.Expand != nil || operation.Combine != nil {
			return fmt.Errorf("%s filter operation requires only filter payload", path)
		}
		filter := operation.Filter
		if filter.ColumnID == "" || inputByID[filter.ColumnID].ID == "" {
			return fmt.Errorf("%s filter columnId is missing from input schema", path)
		}
		if err := validateConstructionFilter(*filter, path+".filter"); err != nil {
			return err
		}
		expected := make(map[string]bool, len(inputByID))
		for id := range inputByID {
			expected[id] = true
		}
		return requireExactStageOutputIDs(expected, outputByID, path)
	case ConstructionUnpivotOp:
		if operation.Unpivot == nil || operation.Pivot != nil || operation.Derive != nil || operation.Filter != nil || operation.Group != nil || operation.Expand != nil || operation.Combine != nil {
			return fmt.Errorf("%s unpivot operation requires only unpivot payload", path)
		}
		unpivot := operation.Unpivot
		if err := validateConstructionID(unpivot.ConstructionID, path+".unpivot.constructionId", constructionIDs); err != nil {
			return err
		}
		if len(unpivot.Inputs) == 0 || unpivot.KeyOutputColumnID == "" || unpivot.ValueOutputColumnID == "" || unpivot.KeyOutputColumnID == unpivot.ValueOutputColumnID {
			return fmt.Errorf("%s unpivot requires inputs and distinct key/value output IDs", path)
		}
		selected := make(map[string]bool, len(unpivot.Inputs))
		keys := make(map[string]bool, len(unpivot.Inputs))
		expected := make(map[string]bool, len(inputByID)+2)
		for id := range inputByID {
			expected[id] = true
		}
		for index, item := range unpivot.Inputs {
			if item.ColumnID == "" || inputByID[item.ColumnID].ID == "" || selected[item.ColumnID] {
				return fmt.Errorf("%s unpivot inputs[%d].columnId is missing or duplicated", path, index)
			}
			if err := item.Key.ValidateConcreteValue(); err != nil {
				return fmt.Errorf("%s unpivot inputs[%d].key: %w", path, index, err)
			}
			identity := item.Key.identity()
			if keys[identity] {
				return fmt.Errorf("%s unpivot input keys must be unique", path)
			}
			keys[identity] = true
			selected[item.ColumnID] = true
			delete(expected, item.ColumnID)
		}
		if unpivot.NullRowPolicy != UnpivotNullDrop && unpivot.NullRowPolicy != UnpivotNullPreserve {
			return fmt.Errorf("%s unpivot nullRowPolicy is unsupported", path)
		}
		if inputByID[unpivot.KeyOutputColumnID].ID != "" || inputByID[unpivot.ValueOutputColumnID].ID != "" {
			return fmt.Errorf("%s unpivot output IDs collide with input schema", path)
		}
		expected[unpivot.KeyOutputColumnID] = true
		expected[unpivot.ValueOutputColumnID] = true
		return requireExactStageOutputIDs(expected, outputByID, path)
	case ConstructionGroupOp:
		if operation.Group == nil || operation.Pivot != nil || operation.Derive != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Expand != nil || operation.Combine != nil {
			return fmt.Errorf("%s group operation requires only group payload", path)
		}
		return validateConstructionGroup(*operation.Group, inputByID, outputByID, path, constructionIDs)
	case ConstructionExpandOp:
		if operation.Expand == nil || operation.Pivot != nil || operation.Derive != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Group != nil || operation.Combine != nil {
			return fmt.Errorf("%s expand operation requires only expand payload", path)
		}
		return validateConstructionExpand(*operation.Expand, inputByID, outputByID, path, constructionIDs)
	case ConstructionRelatedSourceOp:
		if operation.RelatedSource == nil || operation.Pivot != nil || operation.Derive != nil || operation.Filter != nil || operation.Unpivot != nil || operation.Group != nil || operation.Expand != nil || operation.Combine != nil {
			return fmt.Errorf("%s related source operation requires only relatedSource payload", path)
		}
		return validateConstructionRelatedSource(*operation.RelatedSource, inputByID, outputByID, path)
	case ConstructionCombineOp:
		return fmt.Errorf("%s combine must be the sole terminal operation over exact table revision inputs", path)
	default:
		return fmt.Errorf("%s has unsupported operation kind %q", path, operation.Kind)
	}
}

func validateConstructionRelatedSource(related ConstructionRelatedSource, input, output map[string]StageColumn, path string) error {
	if !validConstructionColumnID(related.AnchorColumnID) || input[related.AnchorColumnID].ID == "" {
		return fmt.Errorf("%s.relatedSource.anchorColumnId is not in the input schema", path)
	}
	if !validConstructionColumnID(related.ChoiceID) || !validConstructionColumnID(related.SourceOccurrenceID) ||
		related.SourceOccurrenceID != related.Source.NodeID {
		return fmt.Errorf("%s.relatedSource requires a durable choice and terminal source occurrence", path)
	}
	source := related.Source
	if !validConstructionColumnID(source.CandidateID) || !validConstructionColumnID(source.NodeID) ||
		!validConstructionColumnID(source.ResourceType) || !validConstructionColumnID(source.Path) ||
		!validConstructionColumnID(source.LogicalType) ||
		(source.Cardinality != "optional_one" && source.Cardinality != "required_one") {
		return fmt.Errorf("%s.relatedSource.source must identify one scalar field candidate", path)
	}
	if related.Form != "ALL" || related.ContributorPolicy != "ALL_MATCHES" {
		return fmt.Errorf("%s.relatedSource only supports ALL form with ALL_MATCHES contributor policy", path)
	}
	if len(related.Route) == 0 {
		return fmt.Errorf("%s.relatedSource.route must contain at least one hop", path)
	}
	priorNode, priorResource := related.Route[0].FromNodeID, related.Route[0].FromResourceType
	for index, hop := range related.Route {
		if !validConstructionColumnID(hop.EdgeID) || !validConstructionColumnID(hop.Relationship) ||
			!validConstructionColumnID(hop.ToNodeID) || !validConstructionColumnID(hop.ToResourceType) ||
			hop.FromNodeID != priorNode || hop.FromResourceType != priorResource ||
			(hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
			(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") {
			return fmt.Errorf("%s.relatedSource.route[%d] is not a contiguous authorized hop", path, index)
		}
		priorNode, priorResource = hop.ToNodeID, hop.ToResourceType
	}
	if priorNode != source.NodeID || priorResource != source.ResourceType {
		return fmt.Errorf("%s.relatedSource route terminal does not match source identity", path)
	}
	if !validConstructionColumnID(related.OutputColumnID) || input[related.OutputColumnID].ID != "" {
		return fmt.Errorf("%s.relatedSource.outputColumnId is empty or already exists", path)
	}
	want := make(map[string]bool, len(input)+1)
	for id := range input {
		want[id] = true
	}
	want[related.OutputColumnID] = true
	return requireExactStageOutputIDs(want, output, path)
}

func validateConstructionFilter(filter ConstructionFilter, path string) error {
	if !filter.Operator.Valid() {
		return fmt.Errorf("%s operator %q is unsupported", path, filter.Operator)
	}
	if filter.Operator == FilterExists || filter.Operator == FilterMissing {
		if len(filter.Values) != 0 {
			return fmt.Errorf("%s operator %s does not accept values", path, filter.Operator)
		}
		return nil
	}
	if filter.Operator == FilterIn {
		if len(filter.Values) == 0 {
			return fmt.Errorf("%s IN requires values", path)
		}
	} else if len(filter.Values) != 1 {
		return fmt.Errorf("%s operator %s requires exactly one value", path, filter.Operator)
	}
	for index, value := range filter.Values {
		if err := value.Validate(); err != nil {
			return fmt.Errorf("%s values[%d]: %w", path, index, err)
		}
		if !filterOperatorSupportsKind(filter.Operator, value.Kind) {
			return fmt.Errorf("%s operator %s is incompatible with %s", path, filter.Operator, value.Kind)
		}
	}
	return nil
}

func validateConstructionOperand(operand ConstructionOperand, columns map[string]StageColumn, path string) error {
	switch operand.Kind {
	case DerivedColumnOperand:
		if operand.ColumnID == "" || operand.Literal != nil || columns[operand.ColumnID].ID == "" {
			return fmt.Errorf("%s COLUMN requires a known columnId only", path)
		}
	case DerivedLiteralOperand:
		if operand.ColumnID != "" || operand.Literal == nil {
			return fmt.Errorf("%s LITERAL requires a literal only", path)
		}
		if err := validateDerivedLiteral(*operand.Literal, path+".literal"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%s kind must be COLUMN or LITERAL", path)
	}
	return nil
}

func validateDerivedLiteral(literal DerivedLiteral, path string) error {
	switch literal.Kind {
	case NumericInteger:
		if literal.Integer == nil || literal.Decimal != nil {
			return fmt.Errorf("%s INTEGER requires only an integer value", path)
		}
	case NumericDecimal:
		if literal.Decimal == nil || literal.Integer != nil || math.IsNaN(*literal.Decimal) || math.IsInf(*literal.Decimal, 0) {
			return fmt.Errorf("%s DECIMAL requires one finite decimal value", path)
		}
	default:
		return fmt.Errorf("%s kind must be INTEGER or DECIMAL", path)
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
		if column.Type != "" && !strings.EqualFold(column.Type, "INFER") && !validConstructionLogicalType(column.Type) {
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

func validateConstructionDerivedPolicies(derive ConstructionDerive, path string) error {
	switch derive.Operation {
	case DerivedAdd, DerivedSubtract, DerivedMultiply:
		if derive.DivisionByZeroPolicy != "" {
			return fmt.Errorf("%s divisionByZeroPolicy is only valid for DIVIDE", path)
		}
	case DerivedDivide:
		if derive.DivisionByZeroPolicy != DivisionByZeroNull && derive.DivisionByZeroPolicy != DivisionByZeroError {
			return fmt.Errorf("%s DIVIDE requires NULL or ERROR divisionByZeroPolicy", path)
		}
	default:
		return fmt.Errorf("%s operation must be ADD, SUBTRACT, MULTIPLY, or DIVIDE", path)
	}
	if derive.MissingInputPolicy != MissingInputPropagateNull && derive.MissingInputPolicy != MissingInputError {
		return fmt.Errorf("%s missingInputPolicy must be PROPAGATE_NULL or ERROR", path)
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

func validPivotDuplicatePolicy(policy PivotDuplicatePolicy) bool {
	return policy == PivotDuplicateError || policy == PivotDuplicateSum || policy == PivotDuplicateMin || policy == PivotDuplicateMax
}

func validPivotMissingPolicy(policy PivotMissingCellPolicy) bool {
	return policy == PivotMissingCellNull || policy == PivotMissingCellError
}

func validPivotUnlistedPolicy(policy PivotUnlistedCategoryPolicy) bool {
	return policy == PivotUnlistedCategoryError || policy == PivotUnlistedCategoryExcludeWithEvidence
}
