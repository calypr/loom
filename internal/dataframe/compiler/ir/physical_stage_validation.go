package ir

import (
	"fmt"
	"reflect"
	"strings"
)

func validatePhysicalStageSequence(sequence PhysicalStageSequence, sourceOperations []PhysicalOperation, bindVars map[string]any) error {
	if sequence.SourceStageID == "" || sequence.SourceRowIdentity == "" || sequence.FinalStageID == "" || sequence.FinalRowIdentity == "" {
		return fmt.Errorf("source/final stage IDs and row identity columns are required")
	}
	if len(sequence.Stages) == 0 {
		return fmt.Errorf("at least one construction stage is required")
	}
	if _, ok := bindVars[sequence.PreviewLimitBindKey]; sequence.PreviewLimitBindKey != "" && !ok {
		return fmt.Errorf("preview limit bind %q is missing", sequence.PreviewLimitBindKey)
	}
	if sequence.OutputAuthResourcePathBindKey != "" {
		if !physicalBindKeyPattern.MatchString(sequence.OutputAuthResourcePathBindKey) {
			return fmt.Errorf("private prefix authorization path bind %q is unsafe", sequence.OutputAuthResourcePathBindKey)
		}
		value, ok := bindVars[sequence.OutputAuthResourcePathBindKey].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("private prefix authorization path bind %q is missing or empty", sequence.OutputAuthResourcePathBindKey)
		}
	}
	projectionNames := map[string]bool{}
	for _, operation := range sourceOperations {
		if operation.Kind == PhysicalGroupedPivotOp || operation.Kind == PhysicalUnpivotOp {
			return fmt.Errorf("source projection cannot contain a terminal table reshape")
		}
		if operation.Kind == PhysicalReturnOp && operation.Return != nil {
			for _, projection := range operation.Return.Projections {
				projectionNames[projection.Name] = true
			}
		}
	}
	if err := validatePhysicalStageColumns(sequence.SourceColumns, sequence.SourceRowIdentity); err != nil {
		return fmt.Errorf("source projection schema: %w", err)
	}
	for _, column := range sequence.SourceColumns {
		if !projectionNames[column.Name] {
			return fmt.Errorf("source projection column %q is missing from the typed source RETURN", column.Name)
		}
	}
	priorStageID := sequence.SourceStageID
	priorColumns := sequence.SourceColumns
	priorRowIdentity := sequence.SourceRowIdentity
	seenIDs := map[string]bool{sequence.SourceStageID: true}
	for index, stage := range sequence.Stages {
		path := fmt.Sprintf("stage[%d]", index)
		if strings.TrimSpace(stage.ID) == "" || stage.ID != strings.TrimSpace(stage.ID) || seenIDs[stage.ID] {
			return fmt.Errorf("%s ID is empty, untrimmed, or duplicated", path)
		}
		seenIDs[stage.ID] = true
		if stage.InputStageID != priorStageID {
			return fmt.Errorf("%s input stage %q does not reference prior stage %q", path, stage.InputStageID, priorStageID)
		}
		if err := validatePhysicalStageColumns(stage.InputColumns, priorRowIdentity); err != nil {
			return fmt.Errorf("%s input schema: %w", path, err)
		}
		if !samePhysicalStageColumns(stage.InputColumns, priorColumns) {
			return fmt.Errorf("%s input schema differs from preceding stage output", path)
		}
		if err := validatePhysicalStageColumns(stage.OutputColumns, stage.RowIdentityColumn); err != nil {
			return fmt.Errorf("%s output schema: %w", path, err)
		}
		if err := validateStageVariables(stage); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if stage.Kind != PhysicalStageCodedGroupOp && stage.CodedGroup != nil {
			return fmt.Errorf("%s has a coded-group payload for operation %q", path, stage.Kind)
		}
		switch stage.Kind {
		case PhysicalStageDeriveOp:
			if stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedField != nil {
				return fmt.Errorf("%s DERIVE has mismatched operation payloads", path)
			}
			if len(stage.DerivedLets) == 0 {
				return fmt.Errorf("%s DERIVE requires at least one typed expression LET", path)
			}
			if err := validateStageRowOperations(stage, bindVars, false); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageFilterOp:
			if stage.Filter == nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedField != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s FILTER requires only a filter payload", path)
			}
			if err := validateStageRowOperations(stage, bindVars, true); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStagePivotOp:
			if stage.GroupedPivot == nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s PIVOT requires only a grouped-pivot payload", path)
			}
			if stage.OutputRowVariable != stage.GroupedPivot.OutputRowVariable {
				return fmt.Errorf("%s output row variable does not match grouped pivot", path)
			}
			defined := map[string]bool{stage.InputRowVariable: true}
			if stage.GroupedPivot.CodedCorrelation != nil {
				if index != 0 || stage.InputStageID != sequence.SourceStageID {
					return fmt.Errorf("%s coded Pivot must be the first stage over the direct source projection", path)
				}
				rootVariable, resourceType, rootErr := validateCodedPivotSourceRoot(sourceOperations, bindVars)
				if rootErr != nil {
					return fmt.Errorf("%s coded Pivot source: %w", path, rootErr)
				}
				if rootVariable != stage.GroupedPivot.CodedSourceVariable || resourceType != stage.GroupedPivot.CodedCorrelation.ResourceType {
					return fmt.Errorf("%s coded Pivot source does not match the exact root scan", path)
				}
				defined[rootVariable] = true
			}
			if err := validatePhysicalGroupedPivot(*stage.GroupedPivot, defined, bindVars); err != nil {
				return fmt.Errorf("%s grouped pivot: %w", path, err)
			}
			if err := validateShapeStageOutput(stage, groupedPivotOutputNames(*stage.GroupedPivot)); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageUnpivotOp:
			if stage.Unpivot == nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s UNPIVOT requires only an unpivot payload", path)
			}
			if stage.OutputRowVariable != stage.Unpivot.OutputRowVariable {
				return fmt.Errorf("%s output row variable does not match unpivot", path)
			}
			defined := map[string]bool{stage.InputRowVariable: true}
			if err := validatePhysicalUnpivot(*stage.Unpivot, defined, bindVars); err != nil {
				return fmt.Errorf("%s unpivot: %w", path, err)
			}
			if err := validateShapeStageOutput(stage, unpivotOutputNames(*stage.Unpivot)); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageGroupOp:
			if stage.Group == nil || stage.Filter != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s GROUP requires only a group payload", path)
			}
			if err := validatePhysicalStageGroup(stage, *stage.Group, bindVars); err != nil {
				return fmt.Errorf("%s group: %w", path, err)
			}
			expected := make([]string, 0, len(stage.Group.Keys)+len(stage.Group.Aggregates)+1)
			for _, key := range stage.Group.Keys {
				expected = append(expected, key.OutputColumn)
			}
			for _, aggregate := range stage.Group.Aggregates {
				expected = append(expected, aggregate.Output)
			}
			expected = append(expected, "__loom_row_id")
			if err := validateShapeStageOutput(stage, expected); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageCodedGroupOp:
			if stage.CodedGroup == nil || stage.Group != nil || stage.Filter != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedSource != nil || stage.RelatedExpand != nil || stage.RelatedField != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s CODED_GROUP requires only a coded-group payload", path)
			}
			if err := validatePhysicalStageCodedGroup(stage, *stage.CodedGroup, bindVars); err != nil {
				return fmt.Errorf("%s coded group: %w", path, err)
			}
			if err := validateShapeStageOutput(stage, []string{stage.CodedGroup.SystemOutputColumn, stage.CodedGroup.VersionOutputColumn, stage.CodedGroup.CodeOutputColumn, stage.CodedGroup.CountOutputColumn, "__loom_row_id"}); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageExpandOp:
			if stage.Expand == nil || stage.Filter != nil || stage.Group != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s EXPAND requires only an expand payload", path)
			}
			if err := validatePhysicalStageExpand(stage, *stage.Expand, bindVars); err != nil {
				return fmt.Errorf("%s expand: %w", path, err)
			}
			expected := make([]string, 0, len(stage.InputColumns)+1)
			for _, column := range stage.InputColumns {
				if column.Internal || column.Name == stage.Expand.InputColumn {
					continue
				}
				expected = append(expected, column.Name)
			}
			expected = append(expected, stage.Expand.OutputColumn)
			if stage.Expand.OrdinalColumn != "" {
				expected = append(expected, stage.Expand.OrdinalColumn)
			}
			expected = append(expected, "__loom_row_id")
			if err := validateShapeStageOutput(stage, expected); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		case PhysicalStageRelatedSourceOp:
			if stage.RelatedSource == nil || stage.RelatedExpand != nil || stage.RelatedField != nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s RELATED_SOURCE requires only a related-source payload", path)
			}
			if err := validatePhysicalStageRelatedSource(stage, *stage.RelatedSource, bindVars); err != nil {
				return fmt.Errorf("%s related source: %w", path, err)
			}
		case PhysicalStageRelatedExpandOp:
			if stage.RelatedExpand == nil || stage.RelatedSource != nil || stage.RelatedField != nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s RELATED_EXPAND requires only a related-expansion payload", path)
			}
			if err := validatePhysicalStageRelatedExpand(stage, *stage.RelatedExpand, bindVars); err != nil {
				return fmt.Errorf("%s related expansion: %w", path, err)
			}
		case PhysicalStageRelatedEligibilityOp:
			if stage.Filter == nil || stage.RelatedSource != nil || stage.RelatedExpand != nil || stage.RelatedField != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil {
				return fmt.Errorf("%s RELATED_ELIGIBILITY requires only a filter and optional typed count LET", path)
			}
			if err := validatePhysicalStageRelatedEligibility(stage, bindVars); err != nil {
				return fmt.Errorf("%s related eligibility: %w", path, err)
			}
			if err := validateStageRowOperations(stage, bindVars, true); err != nil {
				return err
			}
		case PhysicalStageRelatedFieldOp:
			if stage.RelatedField == nil || stage.RelatedExpand != nil || stage.RelatedSource != nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
				return fmt.Errorf("%s RELATED_FIELD requires only a related-field payload", path)
			}
			if err := validatePhysicalStageRelatedField(stage, *stage.RelatedField, bindVars); err != nil {
				return fmt.Errorf("%s related field: %w", path, err)
			}
		default:
			return fmt.Errorf("%s has unsupported operation kind %q", path, stage.Kind)
		}
		priorStageID, priorColumns, priorRowIdentity = stage.ID, stage.OutputColumns, stage.RowIdentityColumn
	}
	last := sequence.Stages[len(sequence.Stages)-1]
	if sequence.FinalStageID != last.ID || sequence.FinalRowIdentity != last.RowIdentityColumn {
		return fmt.Errorf("final stage or row identity does not match the final operation")
	}
	if sequence.PreviewTerminalPivotWindow {
		if sequence.PreviewLimitBindKey == "" || sequence.PreviewSourceWindowByRootID ||
			last.Kind != PhysicalStagePivotOp || last.GroupedPivot == nil ||
			last.GroupedPivot.OneInputRowPerGroup || len(last.GroupedPivot.GroupKeys) == 0 ||
			last.OutputRowVariable != last.GroupedPivot.OutputRowVariable {
			return fmt.Errorf("terminal Pivot preview window lacks a terminal nonunique Pivot proof")
		}
	}
	if !samePhysicalStageColumns(sequence.FinalColumns, last.OutputColumns) {
		return fmt.Errorf("final output schema differs from the final stage")
	}
	if sequence.CellTraceReturn != nil {
		if sequence.RowLineageReturn != nil {
			return fmt.Errorf("cell trace and row lineage cannot share a terminal")
		}
		if err := validatePhysicalStageCellTrace(sequence, *sequence.CellTraceReturn, bindVars); err != nil {
			return fmt.Errorf("cell trace: %w", err)
		}
	}
	if sequence.RowLineageReturn != nil {
		if err := validatePhysicalStageRowLineage(sequence, *sequence.RowLineageReturn, bindVars); err != nil {
			return fmt.Errorf("row lineage: %w", err)
		}
	}
	return nil
}

func validateCodedPivotSourceRoot(sourceOperations []PhysicalOperation, bindVars map[string]any) (string, string, error) {
	if len(sourceOperations) < 2 || sourceOperations[0].Kind != PhysicalRootScanOp || sourceOperations[0].RootScan == nil {
		return "", "", fmt.Errorf("a direct root scan is required")
	}
	root := sourceOperations[0].RootScan
	resourceType, ok := bindVars[root.CollectionBindKey].(string)
	if !ok || strings.TrimSpace(resourceType) == "" || root.Variable == "" {
		return "", "", fmt.Errorf("root scan collection and variable are required")
	}
	returns := 0
	for index, operation := range sourceOperations {
		switch operation.Kind {
		case PhysicalRootScanOp:
			if index != 0 || operation.RootScan == nil {
				return "", "", fmt.Errorf("exactly one direct root scan is required")
			}
		case PhysicalFilterOp, PhysicalDerivedLetOp, PhysicalExpressionLetOp,
			PhysicalSetOp, PhysicalSortOp, PhysicalLimitOp:
			// These source operations preserve root row grain.
		case PhysicalReturnOp:
			returns++
			if operation.Return == nil || index != len(sourceOperations)-1 {
				return "", "", fmt.Errorf("one terminal root projection is required")
			}
		default:
			return "", "", fmt.Errorf("source operation %q can multiply or replace root rows", operation.Kind)
		}
	}
	if returns != 1 {
		return "", "", fmt.Errorf("one terminal root projection is required")
	}
	return root.Variable, resourceType, nil
}

func validatePhysicalStageRowLineage(sequence PhysicalStageSequence, terminal PhysicalRowLineageReturn, bindVars map[string]any) error {
	if len(sequence.Stages) != 1 {
		return fmt.Errorf("row lineage requires exactly one construction stage")
	}
	stage := sequence.Stages[0]
	if stage.InputStageID != sequence.SourceStageID || stage.ID != sequence.FinalStageID || stage.RowIdentityColumn != sequence.FinalRowIdentity {
		return fmt.Errorf("row lineage requires one terminal construction stage over the direct source projection")
	}
	switch stage.Kind {
	case PhysicalStageGroupOp:
		if stage.Group == nil || terminal.ParentKeyBindKey != "" || terminal.RelatedTerminalIDBindKey != "" || terminal.RelatedRowKind != "" {
			return fmt.Errorf("row lineage Group payload or terminal bindings are invalid")
		}
	case PhysicalStageCodedGroupOp:
		coded := stage.CodedGroup
		if coded == nil || sequence.SourceRowIdentity != "_key" || coded.SourceIdentityColumn != "_key" ||
			terminal.ResourceType != coded.ResourceType || terminal.ParentKeyBindKey != "" ||
			terminal.RelatedTerminalIDBindKey != "" || terminal.RelatedRowKind != "" {
			return fmt.Errorf("row lineage CODED_GROUP requires direct root identity and no related-row bindings")
		}
	case PhysicalStageRelatedExpandOp:
		related := stage.RelatedExpand
		if related == nil || related.AnchorKind != "root" || related.AnchorColumnID != "_key" ||
			sequence.SourceRowIdentity != "_key" || related.ParentIdentityColumn != "_key" || len(related.Route) != 1 {
			return fmt.Errorf("row lineage RELATED_EXPAND requires one direct root-anchored hop")
		}
		if terminal.ParentKeyBindKey == "" || terminal.RelatedTerminalIDBindKey == "" ||
			(terminal.RelatedRowKind != "RELATED" && terminal.RelatedRowKind != "EMPTY") {
			return fmt.Errorf("row lineage RELATED_EXPAND requires exact parent and terminal identity bindings")
		}
		if related.EmptyPolicy != PhysicalUnnestPreserveParent && terminal.RelatedRowKind == "EMPTY" {
			return fmt.Errorf("row lineage empty row requires PRESERVE_PARENT")
		}
		if _, ok := bindVars[terminal.ParentKeyBindKey]; ok {
			if value := bindVars[terminal.ParentKeyBindKey]; value != nil {
				if _, stringOK := value.(string); !stringOK {
					return fmt.Errorf("row lineage parent key bind %q must be a string or null", terminal.ParentKeyBindKey)
				}
			}
		} else {
			return fmt.Errorf("row lineage parent key bind %q is required", terminal.ParentKeyBindKey)
		}
		terminalID, terminalIDOK := bindVars[terminal.RelatedTerminalIDBindKey].(string)
		if !terminalIDOK || terminal.RelatedRowKind == "RELATED" && bindVars[terminal.ParentKeyBindKey] != nil && terminalID == "" {
			return fmt.Errorf("row lineage related terminal ID bind %q must be a string and nonempty for a valid parent", terminal.RelatedTerminalIDBindKey)
		}
	default:
		return fmt.Errorf("row lineage requires a terminal Group or RELATED_EXPAND over the direct source projection")
	}
	if terminal.ResourceType == "" || terminal.ResourceIDColumn == "" || terminal.OccurrenceKeyColumn == "" {
		return fmt.Errorf("resource type, source resource ID, and occurrence key columns are required")
	}
	for _, key := range []string{terminal.RowIDBindKey, terminal.OffsetBindKey, terminal.LimitBindKey, terminal.FetchLimitBindKey} {
		if err := requireBind(bindVars, key); err != nil {
			return err
		}
	}
	if _, ok := bindVars[terminal.RowIDBindKey].(string); !ok {
		return fmt.Errorf("row ID bind %q must be a string", terminal.RowIDBindKey)
	}
	offset, offsetOK := bindVars[terminal.OffsetBindKey].(int)
	limit, limitOK := bindVars[terminal.LimitBindKey].(int)
	fetchLimit, fetchOK := bindVars[terminal.FetchLimitBindKey].(int)
	if !offsetOK || offset < 0 || !limitOK || limit < 1 || limit > 100 || !fetchOK || fetchLimit != limit+1 {
		return fmt.Errorf("row lineage requires a nonnegative offset and a page of 1 through 100 plus one row")
	}
	return nil
}

func validatePhysicalStageRelatedField(stage PhysicalConstructionStage, related PhysicalStageRelatedField, bindVars map[string]any) error {
	if related.ActiveRecordColumn == "" || related.CandidateID == "" || related.TargetNodeID == "" ||
		related.TargetResourceType == "" || related.OutputColumnID == "" || related.LogicalType == "" || len(related.Path) == 0 {
		return fmt.Errorf("active record, exact field identity, and output are required")
	}
	if project, ok := bindVars["project"].(string); !ok || project == "" {
		return fmt.Errorf("related field requires the exact project runtime binding")
	}
	if generation, ok := bindVars["dataset_generation"].(string); !ok || generation == "" {
		return fmt.Errorf("related field requires the exact dataset generation runtime binding")
	}
	if _, ok := bindVars["auth_resource_paths"].([]string); !ok {
		return fmt.Errorf("related field requires the effective authorization resource paths")
	}
	if _, ok := bindVars["auth_resource_paths_unrestricted"].(bool); !ok {
		return fmt.Errorf("related field requires the effective unrestricted authorization flag")
	}
	inputColumns := physicalStageColumnMap(stage.InputColumns)
	anchor, found := inputColumns[related.ActiveRecordColumn]
	if !found || !anchor.Internal || anchor.Identity || anchor.Kind != "string" ||
		(anchor.Cardinality != "required_one" && anchor.Cardinality != "optional_one") {
		return fmt.Errorf("active record does not identify a retained terminal document identity")
	}
	outputColumns := stageColumnsByID(stage.OutputColumns)
	output, found := outputColumns[related.OutputColumnID]
	wantNullable := true
	wantCardinality := "optional_one"
	if !found || output.Internal || output.Kind != related.LogicalType || output.Cardinality != wantCardinality {
		return fmt.Errorf("related field output schema differs from its exact source")
	}
	if output.Nullable != wantNullable {
		return fmt.Errorf("related field output nullability differs from its source and active record")
	}
	var fieldProjection *PhysicalProjection
	for index := range stage.OutputProjections {
		if stage.OutputProjections[index].Name == output.Name {
			fieldProjection = &stage.OutputProjections[index]
			break
		}
	}
	if fieldProjection == nil || fieldProjection.Expression == nil ||
		fieldProjection.Expression.Kind != PhysicalRelatedFieldExpression || fieldProjection.Expression.RelatedField == nil {
		return fmt.Errorf("related field projection does not use the typed exact-record expression")
	}
	value := fieldProjection.Expression.RelatedField
	if value.DocumentID.Variable != stage.InputRowVariable || value.ResourceType != related.TargetResourceType || len(value.DocumentID.Path) != 1 ||
		value.DocumentID.Path[0] != related.ActiveRecordColumn || !reflect.DeepEqual(value.Path, related.Path) {
		return fmt.Errorf("related field expression differs from its active record, resource, or selected path")
	}
	for index, segment := range related.Path {
		if !validPhysicalFieldPathSegment(segment) {
			return fmt.Errorf("related field path segment %d is invalid", index)
		}
	}
	return validateStageRowOperations(stage, bindVars, false)
}

func validatePhysicalStageRelatedExpand(stage PhysicalConstructionStage, related PhysicalStageRelatedExpand, bindVars map[string]any) error {
	if related.AnchorColumnID == "" || related.AnchorKind == "" || related.AnchorNodeID == "" || related.AnchorResourceType == "" || related.RelatedRecordColumnID == "" || related.TargetNodeID == "" ||
		related.TargetResourceType == "" || related.ParentIdentityColumn == "" || related.ParentIdentityColumnID == "" ||
		related.TerminalIdentityColumn == "" || related.ConstructionIDBindKey == "" || len(related.Route) == 0 {
		return fmt.Errorf("row resource anchor, target, route, identities, and expansion bind are required")
	}
	if err := requireNonEmptyStringBind(bindVars, related.ConstructionIDBindKey); err != nil {
		return fmt.Errorf("construction ID: %w", err)
	}
	if related.EmptyPolicy != PhysicalUnnestError && related.EmptyPolicy != PhysicalUnnestExclude && related.EmptyPolicy != PhysicalUnnestPreserveParent {
		return fmt.Errorf("empty policy %q is unsupported", related.EmptyPolicy)
	}
	variables := []string{related.RelatedRecordsVariable, related.IndexVariable, related.ItemVariable, related.IdentityVariable}
	seenVariables := make(map[string]bool, len(variables)+2)
	seenVariables[stage.InputRowVariable], seenVariables[stage.OutputRowVariable] = true, true
	for _, variable := range variables {
		if !physicalVariablePattern.MatchString(variable) || seenVariables[variable] {
			return fmt.Errorf("related expansion variable %q is unsafe or duplicated", variable)
		}
		seenVariables[variable] = true
	}

	inputColumns := stageColumnsByID(stage.InputColumns)
	anchor, found := inputColumns[related.AnchorColumnID]
	if !found || !anchor.Internal || anchor.Name != related.AnchorColumnID || anchor.Kind != "string" ||
		(anchor.Cardinality != "required_one" && anchor.Cardinality != "optional_one") {
		return fmt.Errorf("selected anchor does not identify a retained exact string resource identity")
	}
	switch related.AnchorKind {
	case "root":
		if related.AnchorColumnID != "_key" || anchor.Cardinality != "required_one" || related.AnchorNodeID != related.Route[0].FromNodeID {
			return fmt.Errorf("root anchor must select the retained _key and exact route root")
		}
	case "activeRelatedRecord":
		if related.AnchorColumnID == "_key" || anchor.RelatedRecordAnchor == nil ||
			anchor.RelatedRecordAnchor.NodeID != related.AnchorNodeID || anchor.RelatedRecordAnchor.ResourceType != related.AnchorResourceType {
			return fmt.Errorf("active anchor must select the exact retained related-record identity")
		}
	default:
		return fmt.Errorf("anchor kind %q is unsupported", related.AnchorKind)
	}
	if related.Route[0].FromNodeID != related.AnchorNodeID || related.Route[0].FromResourceType != related.AnchorResourceType {
		return fmt.Errorf("route starts at a different node or resource type from its exact anchor")
	}
	if len(related.RelatedRecords.Operations) == 0 {
		return fmt.Errorf("related route has no exact anchor lookup")
	}
	firstOperation := related.RelatedRecords.Operations[0]
	if related.AnchorKind == "root" && (firstOperation.Kind != PhysicalCollectionScanOp || firstOperation.CollectionScan == nil) {
		return fmt.Errorf("root anchor route must begin with the scoped root collection lookup")
	}
	if related.AnchorKind == "activeRelatedRecord" && (firstOperation.Kind != PhysicalDocumentLookupOp || firstOperation.DocumentLookup == nil ||
		firstOperation.DocumentLookup.Variable == "" || firstOperation.DocumentLookup.ExactID.Variable != stage.InputRowVariable ||
		len(firstOperation.DocumentLookup.ExactID.Path) != 1 || firstOperation.DocumentLookup.ExactID.Path[0] != related.AnchorColumnID) {
		return fmt.Errorf("active anchor route must begin with an exact DOCUMENT lookup of the selected hidden _id")
	}
	parentIdentity, found := physicalStageColumnMap(stage.InputColumns)[related.ParentIdentityColumn]
	if !found || !parentIdentity.Internal || !parentIdentity.Identity || parentIdentity.Kind != "string" || parentIdentity.Cardinality != "required_one" {
		return fmt.Errorf("parent identity does not identify the exact input row identity")
	}
	for _, column := range stage.InputColumns {
		if column.Identity && column.Name != related.ParentIdentityColumn {
			return fmt.Errorf("parent identity differs from the stage input row identity")
		}
	}
	if err := validatePhysicalSubplan(related.RelatedRecords, map[string]bool{stage.InputRowVariable: true}, bindVars); err != nil {
		return fmt.Errorf("related route subplan: %w", err)
	}
	if len(related.RelatedRecords.Captures) != 1 || related.RelatedRecords.Captures[0] != stage.InputRowVariable ||
		!related.RelatedRecords.Unique || related.RelatedRecords.Sort == nil {
		return fmt.Errorf("related route must be uniquely sorted and capture only its input row")
	}
	if related.RelatedRecords.Return.Kind != PhysicalObjectExpression || related.RelatedRecords.Return.Object == nil || len(related.RelatedRecords.Return.Object.Fields) != 2 {
		return fmt.Errorf("related route must return typed terminal _id and public FHIR id fields")
	}
	terminalVariable := ""
	traversals := make([]PhysicalOperation, 0, len(related.Route))
	for _, operation := range related.RelatedRecords.Operations {
		if operation.Kind == PhysicalTraversalOp {
			traversals = append(traversals, operation)
		}
	}
	if len(traversals) != len(related.Route) {
		return fmt.Errorf("related route metadata contains %d hops but subplan contains %d traversals", len(related.Route), len(traversals))
	}
	for index, hop := range related.Route {
		if hop.EdgeID == "" || hop.FromNodeID == "" || hop.ToNodeID == "" || hop.FromResourceType == "" || hop.ToResourceType == "" ||
			hop.Relationship == "" || (hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
			(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") || index > 0 &&
			(related.Route[index-1].ToNodeID != hop.FromNodeID || related.Route[index-1].ToResourceType != hop.FromResourceType) {
			return fmt.Errorf("route metadata hop %d is invalid or discontinuous", index)
		}
		operation := traversals[index]
		traversal := operation.Traversal
		wantDirection := PhysicalInbound
		if hop.StorageDirection == "OUTBOUND" {
			wantDirection = PhysicalOutbound
		}
		if operation.Source.ResourceType != hop.ToResourceType || operation.Source.SemanticNode != hop.ToNodeID ||
			operation.Source.Relationship != hop.Relationship || traversal.Direction != wantDirection ||
			traversal.TargetTypeBindKey == "" || bindVars[traversal.TargetTypeBindKey] != hop.ToResourceType {
			return fmt.Errorf("route metadata hop %d differs from its compiled traversal", index)
		}
		terminalVariable = traversal.TargetVariable
	}
	last := related.Route[len(related.Route)-1]
	if last.ToNodeID != related.TargetNodeID || last.ToResourceType != related.TargetResourceType {
		return fmt.Errorf("route metadata terminal differs from its exact target")
	}
	if terminalVariable == "" || related.RelatedRecords.Sort.Variable != terminalVariable || len(related.RelatedRecords.Sort.Path) != 1 || related.RelatedRecords.Sort.Path[0] != "_id" {
		return fmt.Errorf("related route sort must use the exact terminal document _id")
	}
	fields := map[string]PhysicalExpression{}
	for _, field := range related.RelatedRecords.Return.Object.Fields {
		fields[field.Name] = field.Expression
	}
	if !physicalValueMatches(fields["terminal_id"], terminalVariable, "_id") || !physicalValueMatches(fields["resource_id"], terminalVariable, "id") {
		return fmt.Errorf("related route return differs from terminal document identity")
	}

	outputColumns := stageColumnsByID(stage.OutputColumns)
	publicID, found := outputColumns[related.RelatedRecordColumnID]
	wantNullable := related.EmptyPolicy == PhysicalUnnestPreserveParent
	wantCardinality := "required_one"
	if wantNullable {
		wantCardinality = "optional_one"
	}
	if !found || publicID.Internal || publicID.Kind != "string" || publicID.Cardinality != wantCardinality || publicID.Nullable != wantNullable {
		return fmt.Errorf("public related FHIR ID does not match the route or empty policy")
	}
	parentID, found := outputColumns[related.ParentIdentityColumnID]
	if !found || !parentID.Internal || parentID.Identity || parentID.Kind != "string" || parentID.Cardinality != "required_one" || parentID.Nullable {
		return fmt.Errorf("parent row identity is not retained as a hidden required string")
	}
	terminalID, found := outputColumns[related.TerminalIdentityColumn]
	if !found || !terminalID.Internal || terminalID.Identity || terminalID.Kind != "string" || terminalID.Cardinality != wantCardinality || terminalID.Nullable != wantNullable {
		return fmt.Errorf("terminal _id is not retained as a typed hidden identity")
	}
	inputRootKey, inputHasRootKey := physicalStageColumnMap(stage.InputColumns)["_key"]
	outputRootKey, outputHasRootKey := physicalStageColumnMap(stage.OutputColumns)["_key"]
	if inputHasRootKey && (!outputHasRootKey || !inputRootKey.Internal || !outputRootKey.Internal || outputRootKey.Identity ||
		outputRootKey.Kind != "string" || outputRootKey.Cardinality != "required_one") {
		return fmt.Errorf("available root _key anchor must remain separate from the terminal identity")
	}
	if !inputHasRootKey && outputHasRootKey {
		return fmt.Errorf("related expansion cannot introduce a root _key that was absent from its input")
	}
	if err := validateStageProjectionNames(stage.OutputProjections, stage.OutputColumns); err != nil {
		return err
	}
	for _, projection := range stage.OutputProjections {
		switch projection.Name {
		case publicID.Name:
			if projection.Value.Variable != related.ItemVariable || len(projection.Value.Path) != 1 || projection.Value.Path[0] != "resource_id" {
				return fmt.Errorf("public related-record ID must project the terminal FHIR id")
			}
		case related.ParentIdentityColumnID:
			if projection.Value.Variable != stage.InputRowVariable || len(projection.Value.Path) != 1 || projection.Value.Path[0] != related.ParentIdentityColumn {
				return fmt.Errorf("parent identity must preserve the exact input row ID")
			}
		case related.TerminalIdentityColumn:
			if projection.Value.Variable != related.ItemVariable || len(projection.Value.Path) != 1 || projection.Value.Path[0] != "terminal_id" {
				return fmt.Errorf("terminal identity must preserve the exact terminal _id")
			}
		case stage.RowIdentityColumn:
			if projection.Value.Variable != related.IdentityVariable || len(projection.Value.Path) != 0 {
				return fmt.Errorf("row identity must use the parent, step, and terminal identity composite")
			}
		case "_key":
			if projection.Value.Variable != stage.InputRowVariable || len(projection.Value.Path) != 1 || projection.Value.Path[0] != "_key" {
				return fmt.Errorf("root identity must remain separate from the terminal identity")
			}
		}
		defined := map[string]bool{stage.InputRowVariable: true, related.ItemVariable: true, related.IdentityVariable: true}
		if err := validatePhysicalProjection(projection, defined, bindVars); err != nil {
			return fmt.Errorf("output projection %q: %w", projection.Name, err)
		}
	}
	if err := validateStageProjectionNames(stage.OutputProjections, stage.OutputColumns); err != nil {
		return err
	}
	return nil
}

func physicalValueMatches(expression PhysicalExpression, variable, path string) bool {
	return expression.Kind == PhysicalValueExpression && expression.Value != nil && expression.Value.Variable == variable &&
		len(expression.Value.Path) == 1 && expression.Value.Path[0] == path
}

func validatePhysicalStageRelatedEligibility(stage PhysicalConstructionStage, bindVars map[string]any) error {
	filter := stage.Filter
	if filter.Expression == nil {
		return fmt.Errorf("eligible-row filter must use a typed predicate expression")
	}
	switch filter.Expression.Kind {
	case PhysicalExistsPredicate:
		if filter.Expression.Exists == nil || len(stage.DerivedLets) != 0 {
			return fmt.Errorf("EXISTS requires one correlated subplan and no scalar LET")
		}
	case PhysicalNotPredicate:
		if len(filter.Expression.Children) != 1 || filter.Expression.Children[0].Kind != PhysicalExistsPredicate ||
			filter.Expression.Children[0].Exists == nil || len(stage.DerivedLets) != 0 {
			return fmt.Errorf("ABSENT requires NOT over one correlated EXISTS subplan and no scalar LET")
		}
	case PhysicalComparisonPredicate:
		comparison := filter.Expression.Comparison
		if comparison == nil || strings.ToUpper(strings.TrimSpace(comparison.Operator)) != "GTE" || comparison.LeftExpression != nil ||
			comparison.Left.Variable == "" || comparison.Right == nil || comparison.Right.BindKey == "" || len(stage.DerivedLets) != 1 {
			return fmt.Errorf("COUNT_AT_LEAST requires a scalar GTE comparison and one count LET")
		}
		let := stage.DerivedLets[0].ExpressionLet
		if stage.DerivedLets[0].Kind != PhysicalExpressionLetOp || let == nil || let.Variable != comparison.Left.Variable {
			return fmt.Errorf("count comparison must use the variable produced by its typed LET")
		}
		count := let.Expression
		if count.Kind != PhysicalCallExpression || count.Call == nil || strings.ToLower(strings.TrimSpace(count.Call.Name)) != "length" || len(count.Call.Args) != 1 {
			return fmt.Errorf("count LET must apply LENGTH to one related-resource subplan")
		}
		array := count.Call.Args[0]
		if array.Kind != PhysicalSubplanExpression || array.Subplan == nil || !array.Subplan.Unique || array.Subplan.Sort == nil ||
			array.Subplan.Return.Kind != PhysicalValueExpression || array.Subplan.Return.Value == nil ||
			!reflect.DeepEqual(*array.Subplan.Sort, *array.Subplan.Return.Value) || len(array.Subplan.Sort.Path) != 1 || array.Subplan.Sort.Path[0] != "_id" {
			return fmt.Errorf("COUNT_AT_LEAST must count a sorted, distinct terminal document _id subplan")
		}
		threshold, ok := bindVars[comparison.Right.BindKey].(int)
		if !ok || threshold <= 0 {
			return fmt.Errorf("COUNT_AT_LEAST threshold must be a positive integer bind")
		}
	default:
		return fmt.Errorf("related eligibility supports only EXISTS, ABSENT, or COUNT_AT_LEAST")
	}
	return nil
}

func validatePhysicalStageRelatedSource(stage PhysicalConstructionStage, related PhysicalStageRelatedSource, bindVars map[string]any) error {
	if related.AnchorColumnID == "" || related.OutputColumnID == "" || related.CandidateID == "" || related.SourceOccurrenceID == "" ||
		related.ResourceType == "" || related.Path == "" || related.LogicalType == "" {
		return fmt.Errorf("source, anchor, and output identities are required")
	}
	if (related.Form != "ALL" && related.Form != "COUNT" && related.Form != "PRESENCE") || related.ContributorPolicy != "ALL_MATCHES" {
		return fmt.Errorf("only ALL, COUNT, or PRESENCE with ALL_MATCHES contributor policy is supported")
	}
	var anchor, output *PhysicalStageColumn
	for index := range stage.InputColumns {
		if stage.InputColumns[index].ID == related.AnchorColumnID {
			anchor = &stage.InputColumns[index]
		}
	}
	for index := range stage.OutputColumns {
		if stage.OutputColumns[index].ID == related.OutputColumnID {
			output = &stage.OutputColumns[index]
		}
	}
	if anchor == nil || !anchor.Internal || !anchor.Identity || anchor.Name != "_key" || output == nil || output.Internal {
		return fmt.Errorf("anchor or related output does not match the typed stage schema")
	}
	outputKind, outputCardinality, outputNullable := related.LogicalType, "many", true
	switch related.Form {
	case "COUNT":
		outputKind, outputCardinality, outputNullable = "integer", "required_one", false
	case "PRESENCE":
		outputKind, outputCardinality, outputNullable = "boolean", "required_one", false
	}
	if output.Kind != outputKind || output.Cardinality != outputCardinality || output.Nullable != outputNullable {
		return fmt.Errorf("related output type does not match its selected form")
	}
	var relatedProjection *PhysicalProjection
	var identityProjection *PhysicalProjection
	for index := range stage.OutputProjections {
		projection := &stage.OutputProjections[index]
		if projection.Name == output.Name {
			relatedProjection = projection
		}
		if projection.Name == stage.RowIdentityColumn {
			identityProjection = projection
		}
	}
	if relatedProjection == nil || relatedProjection.Expression == nil || relatedSourceProjectionSubplan(*relatedProjection.Expression, related.Form) == nil {
		return fmt.Errorf("related output does not match its typed route and form")
	}
	if identityProjection == nil || identityProjection.Value.Variable != stage.InputRowVariable ||
		len(identityProjection.Value.Path) != 1 || identityProjection.Value.Path[0] != stage.RowIdentityColumn {
		return fmt.Errorf("related source must preserve the exact preceding row identity")
	}
	return validateStageRowOperations(stage, bindVars, false)
}

func relatedSourceProjectionSubplan(expression PhysicalExpression, form string) *PhysicalSubplan {
	if form == "ALL" {
		if expression.Kind == PhysicalSubplanExpression && expression.Subplan != nil &&
			expression.Cardinality == PhysicalArrayCardinality && expression.NullBehavior == PhysicalEmptyOnNull &&
			!expression.Subplan.Unique {
			return expression.Subplan
		}
		return nil
	}
	if expression.Kind != PhysicalCallExpression || expression.Call == nil || expression.Cardinality != PhysicalScalarCardinality {
		return nil
	}
	if form == "PRESENCE" {
		if expression.Call.Name != "gt" || len(expression.Call.Args) != 2 ||
			expression.Call.Args[1].Kind != PhysicalLiteralExpression {
			return nil
		}
		expression = expression.Call.Args[0]
	}
	if expression.Call == nil || expression.Call.Name != "length" || len(expression.Call.Args) != 1 {
		return nil
	}
	source := expression.Call.Args[0]
	if source.Kind != PhysicalSubplanExpression || source.Subplan == nil ||
		source.Cardinality != PhysicalArrayCardinality || !source.Subplan.Unique || source.Subplan.Sort == nil ||
		source.Subplan.Return.Value == nil || len(source.Subplan.Return.Value.Path) != 1 ||
		source.Subplan.Return.Value.Path[0] != "_id" {
		return nil
	}
	return source.Subplan
}

func validatePhysicalStageGroup(stage PhysicalConstructionStage, group PhysicalStageGroup, bindVars map[string]any) error {
	if len(group.Keys) == 0 && len(group.Aggregates) == 0 {
		return fmt.Errorf("requires at least one key or aggregate")
	}
	switch group.MissingKeyPolicy {
	case PhysicalStageGroupMissingKeyGroup, PhysicalStageGroupMissingKeyExclude, PhysicalStageGroupMissingKeyError:
	default:
		return fmt.Errorf("missing-key policy %q is unsupported", group.MissingKeyPolicy)
	}
	if err := requireNonEmptyStringBind(bindVars, group.ConstructionIDBindKey); err != nil {
		return fmt.Errorf("construction ID: %w", err)
	}
	if !physicalVariablePattern.MatchString(group.GroupRowsVariable) || !physicalVariablePattern.MatchString(group.IdentityVariable) {
		return fmt.Errorf("group row and identity variables must be safe")
	}
	if err := validateStageProjectionNames(stage.InputProjections, stage.InputColumns); err != nil {
		return fmt.Errorf("input projections: %w", err)
	}
	defined := map[string]bool{stage.InputRowVariable: true}
	for index, projection := range stage.InputProjections {
		if err := validatePhysicalProjection(projection, defined, bindVars); err != nil {
			return fmt.Errorf("input projection %d (%q): %w", index, projection.Name, err)
		}
	}
	if err := definePhysicalVariable(defined, group.GroupRowsVariable); err != nil {
		return err
	}
	inputByName, outputByName := physicalStageColumnMap(stage.InputColumns), physicalStageColumnMap(stage.OutputColumns)
	for index, key := range group.Keys {
		input, ok := inputByName[key.InputColumn]
		if !ok || input.Internal || input.Cardinality == "many" {
			return fmt.Errorf("key %d column %q is not a public scalar input", index, key.InputColumn)
		}
		kind, ok := physicalStageScalarKind(input.Kind)
		if !ok || kind != key.Kind {
			return fmt.Errorf("key %d column %q has unsupported or mismatched scalar kind %q", index, key.InputColumn, key.Kind)
		}
		output, ok := outputByName[key.OutputColumn]
		if !ok || output.Internal || output.Kind != input.Kind || output.Cardinality != input.Cardinality {
			return fmt.Errorf("key %d output column %q does not preserve its input type", index, key.OutputColumn)
		}
		if err := definePhysicalVariable(defined, key.Variable); err != nil {
			return fmt.Errorf("key %d: %w", index, err)
		}
	}
	for index, aggregate := range group.Aggregates {
		output, ok := outputByName[aggregate.Output]
		if !ok || output.Internal || output.Cardinality == "many" || output.Kind != aggregate.OutputKind {
			return fmt.Errorf("aggregate %d output column %q does not match its type", index, aggregate.Output)
		}
		needInput := aggregate.Operation != "COUNT_ROWS"
		if !needInput {
			if aggregate.InputColumn != "" || aggregate.InputKind != "" {
				return fmt.Errorf("aggregate %d COUNT_ROWS must not have an input column", index)
			}
		} else {
			input, ok := inputByName[aggregate.InputColumn]
			if !ok || input.Internal || input.Cardinality == "many" {
				return fmt.Errorf("aggregate %d input column %q is not a public scalar input", index, aggregate.InputColumn)
			}
			kind, ok := physicalStageScalarKind(input.Kind)
			if !ok || kind != aggregate.InputKind {
				return fmt.Errorf("aggregate %d input column %q has unsupported or mismatched scalar kind %q", index, aggregate.InputColumn, aggregate.InputKind)
			}
		}
		switch aggregate.Operation {
		case "COUNT_ROWS", "COUNT_NON_NULL", "COUNT_DISTINCT":
			if output.Kind != "integer" {
				return fmt.Errorf("aggregate %d count output must be integer", index)
			}
		case "MIN", "MAX":
			if output.Kind != inputByName[aggregate.InputColumn].Kind {
				return fmt.Errorf("aggregate %d %s output kind must match its input", index, aggregate.Operation)
			}
		case "SUM":
			if aggregate.InputKind != "INTEGER" && aggregate.InputKind != "DECIMAL" || output.Kind != inputByName[aggregate.InputColumn].Kind {
				return fmt.Errorf("aggregate %d SUM requires a matching numeric output", index)
			}
		case "MEAN":
			if aggregate.InputKind != "INTEGER" && aggregate.InputKind != "DECIMAL" || output.Kind != "decimal" {
				return fmt.Errorf("aggregate %d MEAN requires numeric input and decimal output", index)
			}
		default:
			return fmt.Errorf("aggregate %d has unsupported operation %q", index, aggregate.Operation)
		}
		if err := definePhysicalVariable(defined, aggregate.Variable); err != nil {
			return fmt.Errorf("aggregate %d: %w", index, err)
		}
	}
	if err := definePhysicalVariable(defined, group.IdentityVariable); err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	for index, projection := range stage.OutputProjections {
		if err := validatePhysicalProjection(projection, defined, bindVars); err != nil {
			return fmt.Errorf("output projection %d (%q): %w", index, projection.Name, err)
		}
	}
	if err := definePhysicalVariable(defined, stage.OutputRowVariable); err != nil {
		return err
	}
	if err := validateStageProjectionNames(stage.OutputProjections, stage.OutputColumns); err != nil {
		return fmt.Errorf("output projections: %w", err)
	}
	return nil
}

func validatePhysicalStageCodedGroup(stage PhysicalConstructionStage, coded PhysicalStageCodedGroup, bindVars map[string]any) error {
	for key, label := range map[string]string{
		coded.RootCollectionBindKey: "root collection",
		coded.ConstructionIDBindKey: "construction ID",
		coded.OccurrenceIDBindKey:   "source occurrence ID",
		coded.CodingPathBindKey:     "Coding path",
	} {
		if err := requireNonEmptyStringBind(bindVars, key); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
	}
	collection, _ := bindVars[coded.RootCollectionBindKey].(string)
	constructionID, _ := bindVars[coded.ConstructionIDBindKey].(string)
	occurrenceID, _ := bindVars[coded.OccurrenceIDBindKey].(string)
	codingPath, _ := bindVars[coded.CodingPathBindKey].(string)
	if collection != coded.ResourceType || strings.TrimSpace(coded.ResourceType) == "" {
		return fmt.Errorf("root collection does not match the exact root resource type")
	}
	if !coded.SourceRowsUnique {
		return fmt.Errorf("CODED_GROUP requires compiler proof that the direct source has at most one row per root _key")
	}
	if constructionID == "" || occurrenceID != "base" || codingPath != coded.CodingPath || coded.SourceIdentityColumn != "_key" {
		return fmt.Errorf("construction, root occurrence, Coding path, and root identity facts are inconsistent")
	}
	inputIdentity, exists := physicalStageColumnMap(stage.InputColumns)[coded.SourceIdentityColumn]
	if !exists || !inputIdentity.Internal || !inputIdentity.Identity || inputIdentity.Kind != "string" || inputIdentity.Cardinality != "required_one" {
		return fmt.Errorf("source identity column %q is not a required root key", coded.SourceIdentityColumn)
	}
	if len(coded.PathSegments) == 0 {
		return fmt.Errorf("Coding path segments are required")
	}
	var canonical strings.Builder
	for index, segment := range coded.PathSegments {
		if !physicalVariablePattern.MatchString(segment.Name) {
			return fmt.Errorf("Coding path segment %d has an unsafe field name", index)
		}
		if index > 0 {
			canonical.WriteByte('.')
		}
		canonical.WriteString(segment.Name)
		if segment.Repeated {
			canonical.WriteString("[]")
		}
	}
	last := coded.PathSegments[len(coded.PathSegments)-1]
	if !last.Repeated || canonical.String() != coded.CodingPath {
		return fmt.Errorf("Coding path must be the exact generated repeated Coding path")
	}
	switch coded.MissingKeyPolicy {
	case PhysicalStageGroupMissingKeyGroup, PhysicalStageGroupMissingKeyExclude, PhysicalStageGroupMissingKeyError:
	default:
		return fmt.Errorf("missing-key policy %q is unsupported", coded.MissingKeyPolicy)
	}
	variables := []string{coded.SystemVariable, coded.VersionVariable, coded.CodeVariable, coded.CountVariable, coded.IdentityVariable}
	seenVariables := map[string]bool{stage.InputRowVariable: true, stage.OutputRowVariable: true}
	for _, variable := range variables {
		if !physicalVariablePattern.MatchString(variable) || seenVariables[variable] {
			return fmt.Errorf("coded-group output variables must be safe and distinct")
		}
		seenVariables[variable] = true
	}
	outputs := physicalStageColumnMap(stage.OutputColumns)
	for _, columnName := range []string{coded.SystemOutputColumn, coded.VersionOutputColumn, coded.CodeOutputColumn} {
		column, ok := outputs[columnName]
		if !ok || column.Internal || column.Kind != "string" || column.Cardinality != "optional_one" || !column.Nullable {
			return fmt.Errorf("coded-group key output %q must be a nullable scalar string", columnName)
		}
	}
	count, ok := outputs[coded.CountOutputColumn]
	if !ok || count.Internal || count.Kind != "integer" || count.Cardinality != "required_one" || count.Nullable {
		return fmt.Errorf("coded-group source count must be a required integer")
	}
	identity, ok := outputs["__loom_row_id"]
	if !ok || !identity.Internal || !identity.Identity || identity.Kind != "string" || identity.Cardinality != "required_one" {
		return fmt.Errorf("coded-group output requires the hidden stable row identity")
	}
	projectionByName := make(map[string]PhysicalProjection, len(stage.OutputProjections))
	if err := validateStageProjectionNames(stage.OutputProjections, stage.OutputColumns); err != nil {
		return fmt.Errorf("output projections: %w", err)
	}
	for _, projection := range stage.OutputProjections {
		projectionByName[projection.Name] = projection
	}
	for name, variable := range map[string]string{
		coded.SystemOutputColumn:  coded.SystemVariable,
		coded.VersionOutputColumn: coded.VersionVariable,
		coded.CodeOutputColumn:    coded.CodeVariable,
		coded.CountOutputColumn:   coded.CountVariable,
		"__loom_row_id":           coded.IdentityVariable,
	} {
		projection, ok := projectionByName[name]
		if !ok || projection.Value.Variable != variable || len(projection.Value.Path) != 0 || projection.Expression != nil {
			return fmt.Errorf("coded-group output projection %q does not use its exact value variable", name)
		}
	}
	return nil
}

func validatePhysicalStageExpand(stage PhysicalConstructionStage, expand PhysicalStageExpand, bindVars map[string]any) error {
	if err := requireNonEmptyStringBind(bindVars, expand.ConstructionIDBindKey); err != nil {
		return fmt.Errorf("construction ID: %w", err)
	}
	inputByName, outputByName := physicalStageColumnMap(stage.InputColumns), physicalStageColumnMap(stage.OutputColumns)
	input, ok := inputByName[expand.InputColumn]
	inputKind, inputScalar := physicalStageScalarKind(input.Kind)
	if !ok || input.Internal || input.Cardinality != "many" || !inputScalar || inputKind != expand.InputKind {
		return fmt.Errorf("input column %q must be a public array-valued column", expand.InputColumn)
	}
	item, ok := outputByName[expand.OutputColumn]
	itemKind, itemScalar := physicalStageScalarKind(item.Kind)
	if !ok || item.Internal || !itemScalar || itemKind != expand.InputKind || item.Cardinality != "required_one" && item.Cardinality != "optional_one" {
		return fmt.Errorf("item output column %q does not match the array item type", expand.OutputColumn)
	}
	if expand.OrdinalColumn != "" {
		ordinal, ok := outputByName[expand.OrdinalColumn]
		if !ok || ordinal.Internal || ordinal.Kind != "integer" || ordinal.Cardinality != "required_one" && ordinal.Cardinality != "optional_one" {
			return fmt.Errorf("ordinal output column %q must be an integer scalar", expand.OrdinalColumn)
		}
	}
	switch expand.EmptyPolicy {
	case PhysicalUnnestError, PhysicalUnnestExclude, PhysicalUnnestPreserveParent:
	default:
		return fmt.Errorf("unsupported empty policy %q", expand.EmptyPolicy)
	}
	for _, variable := range []string{expand.ItemsVariable, expand.IndexVariable, expand.ItemVariable, expand.IdentityVariable} {
		if !physicalVariablePattern.MatchString(variable) {
			return fmt.Errorf("variable %q is unsafe", variable)
		}
	}
	defined := map[string]bool{stage.InputRowVariable: true}
	for _, variable := range []string{expand.ItemsVariable, expand.IndexVariable, expand.ItemVariable, expand.IdentityVariable} {
		if err := definePhysicalVariable(defined, variable); err != nil {
			return err
		}
	}
	for index, projection := range stage.OutputProjections {
		if err := validatePhysicalProjection(projection, defined, bindVars); err != nil {
			return fmt.Errorf("output projection %d (%q): %w", index, projection.Name, err)
		}
	}
	if err := definePhysicalVariable(defined, stage.OutputRowVariable); err != nil {
		return err
	}
	if err := validateStageProjectionNames(stage.OutputProjections, stage.OutputColumns); err != nil {
		return fmt.Errorf("output projections: %w", err)
	}
	return nil
}

func physicalStageColumnMap(columns []PhysicalStageColumn) map[string]PhysicalStageColumn {
	byName := make(map[string]PhysicalStageColumn, len(columns))
	for _, column := range columns {
		byName[column.Name] = column
	}
	return byName
}

func physicalStageScalarKind(kind string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "string", "date", "datetime", "code", "uuid":
		return "STRING", true
	case "integer":
		return "INTEGER", true
	case "decimal":
		return "DECIMAL", true
	case "boolean":
		return "BOOLEAN", true
	default:
		return "", false
	}
}

func requireNonEmptyStringBind(bindVars map[string]any, key string) error {
	if key == "" {
		return fmt.Errorf("bind key is required")
	}
	value, ok := bindVars[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return fmt.Errorf("bind %q must be a non-empty string", key)
	}
	return nil
}

func validatePhysicalStageCellTrace(sequence PhysicalStageSequence, terminal PhysicalCellTraceReturn, bindVars map[string]any) error {
	construction := terminal.Construction
	if construction == nil || terminal.Contribution != nil || terminal.Reshape != nil {
		return fmt.Errorf("construction lineage is required and must be the only contributor contract")
	}
	if construction.RelatedSource != nil && len(construction.Inputs) != 0 {
		return fmt.Errorf("related-source lineage cannot also contain construction input lineage")
	}
	if construction.Operation == string(PhysicalStageRelatedSourceOp) && construction.RelatedSource == nil && construction.OmissionCode == "" {
		return fmt.Errorf("complete RELATED_SOURCE lineage requires its typed route subplan")
	}
	for _, key := range []string{terminal.OffsetBindKey, terminal.LimitBindKey, terminal.FetchLimitBindKey} {
		if err := requireBind(bindVars, key); err != nil {
			return err
		}
	}
	if construction.FinalStageID != sequence.FinalStageID || construction.RowIdentityColumn != sequence.FinalRowIdentity {
		return fmt.Errorf("final stage or row identity does not match the construction sequence")
	}
	finalColumns := stageColumnsByID(sequence.FinalColumns)
	output, exists := finalColumns[construction.OutputColumnID]
	if !exists || output.Name != construction.OutputColumn || output.Internal {
		return fmt.Errorf("output column ID %q does not identify a public final column", construction.OutputColumnID)
	}
	if construction.OmissionCode == "" && len(construction.Inputs) == 0 && construction.RelatedSource == nil {
		return fmt.Errorf("complete construction lineage requires source columns or a related-source route")
	}
	producerInput := map[string]PhysicalStageColumn{}
	expectedInputStageID := sequence.SourceStageID
	producerFound := construction.ProducerStageID == sequence.SourceStageID && construction.Operation == "SOURCE_PROJECTION"
	if producerFound {
		producerInput = stageColumnsByID(sequence.SourceColumns)
	} else {
		for _, stage := range sequence.Stages {
			if stage.ID != construction.ProducerStageID {
				continue
			}
			producerFound = true
			if string(stage.Kind) != construction.Operation {
				return fmt.Errorf("producer operation %q does not match stage %q kind %q", construction.Operation, stage.ID, stage.Kind)
			}
			expectedInputStageID = stage.InputStageID
			producerInput = stageColumnsByID(stage.InputColumns)
			if _, ok := stageColumnsByID(stage.OutputColumns)[construction.OutputColumnID]; !ok {
				return fmt.Errorf("output column ID %q is absent from producer stage %q", construction.OutputColumnID, stage.ID)
			}
			if construction.RelatedSource != nil {
				if stage.Kind != PhysicalStageRelatedSourceOp || stage.RelatedSource == nil || construction.Operation != string(PhysicalStageRelatedSourceOp) {
					return fmt.Errorf("related-source lineage must be produced by a RELATED_SOURCE stage")
				}
				projection, found := findPhysicalStageProjection(stage.OutputProjections, construction.OutputColumn)
				if !found || projection.Expression == nil || relatedSourceProjectionSubplan(*projection.Expression, stage.RelatedSource.Form) == nil {
					return fmt.Errorf("related-source output has no typed route subplan")
				}
				subplan := relatedSourceProjectionSubplan(*projection.Expression, stage.RelatedSource.Form)
				related := construction.RelatedSource
				anchor, found := stageColumnsByID(stage.InputColumns)[stage.RelatedSource.AnchorColumnID]
				if !found || related.InputRowVariable != stage.InputRowVariable || related.AnchorColumn != anchor.Name ||
					related.ResourceType != stage.RelatedSource.ResourceType || !reflect.DeepEqual(related.Subplan, *subplan) {
					return fmt.Errorf("related-source trace must retain the producer's exact scoped route subplan")
				}
			}
			break
		}
	}
	if !producerFound {
		return fmt.Errorf("producer stage %q is absent from the construction sequence", construction.ProducerStageID)
	}
	seenInputs := map[string]bool{}
	for _, input := range construction.Inputs {
		stageColumn, ok := producerInput[input.ColumnID]
		if !ok || stageColumn.Name != input.Column || stageColumn.Internal || input.StageID != expectedInputStageID {
			return fmt.Errorf("input column ID %q is not a public producer input", input.ColumnID)
		}
		if seenInputs[input.ColumnID] {
			return fmt.Errorf("input column ID %q is duplicated", input.ColumnID)
		}
		seenInputs[input.ColumnID] = true
		finalColumn, ok := finalColumns[input.ColumnID]
		if !ok || finalColumn.Internal || finalColumn.Name != input.FinalValueColumn {
			return fmt.Errorf("input column ID %q does not survive in the final output", input.ColumnID)
		}
	}
	return nil
}

func findPhysicalStageProjection(projections []PhysicalProjection, name string) (PhysicalProjection, bool) {
	for _, projection := range projections {
		if projection.Name == name {
			return projection, true
		}
	}
	return PhysicalProjection{}, false
}

func stageColumnsByID(columns []PhysicalStageColumn) map[string]PhysicalStageColumn {
	result := make(map[string]PhysicalStageColumn, len(columns))
	for _, column := range columns {
		result[column.ID] = column
	}
	return result
}

func validatePhysicalStageColumns(columns []PhysicalStageColumn, identityName string) error {
	if len(columns) == 0 || strings.TrimSpace(identityName) == "" {
		return fmt.Errorf("columns and row identity are required")
	}
	ids, names := map[string]bool{}, map[string]bool{}
	identityFound := false
	for _, column := range columns {
		if strings.TrimSpace(column.ID) == "" || column.ID != strings.TrimSpace(column.ID) || ids[column.ID] {
			return fmt.Errorf("column ID %q is empty, untrimmed, or duplicated", column.ID)
		}
		if !physicalPathPartPattern.MatchString(column.Name) || names[column.Name] {
			return fmt.Errorf("column name %q is unsafe or duplicated", column.Name)
		}
		if strings.TrimSpace(column.Kind) == "" || strings.TrimSpace(column.Cardinality) == "" {
			return fmt.Errorf("column %q requires logical kind and cardinality", column.Name)
		}
		if column.RelatedRecordAnchor != nil && (!column.Internal || column.Identity || column.Kind != "string" ||
			(column.Cardinality != "required_one" && column.Cardinality != "optional_one") ||
			strings.TrimSpace(column.RelatedRecordAnchor.NodeID) == "" || strings.TrimSpace(column.RelatedRecordAnchor.ResourceType) == "") {
			return fmt.Errorf("related-record anchor column %q must be a hidden scalar terminal identity with exact node and type metadata", column.Name)
		}
		ids[column.ID], names[column.Name] = true, true
		if column.Name == identityName {
			if !column.Internal || !column.Identity {
				return fmt.Errorf("row identity column %q must be internal and identity-marked", identityName)
			}
			identityFound = true
		}
	}
	if !identityFound {
		return fmt.Errorf("row identity column %q is absent from schema", identityName)
	}
	return nil
}

func samePhysicalStageColumns(left, right []PhysicalStageColumn) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		a, b := left[index], right[index]
		if a.ID != b.ID || a.Name != b.Name || a.Label != b.Label || a.Kind != b.Kind || a.Cardinality != b.Cardinality || a.Nullable != b.Nullable || a.Internal != b.Internal || a.Identity != b.Identity || !reflect.DeepEqual(a.RelatedRecordAnchor, b.RelatedRecordAnchor) {
			return false
		}
	}
	return true
}

func validateStageVariables(stage PhysicalConstructionStage) error {
	if !physicalVariablePattern.MatchString(stage.InputRowVariable) || !physicalVariablePattern.MatchString(stage.OutputRowVariable) || stage.InputRowVariable == stage.OutputRowVariable {
		return fmt.Errorf("input and output row variables must be safe and distinct")
	}
	return nil
}

func validateStageRowOperations(stage PhysicalConstructionStage, bindVars map[string]any, expectFilter bool) error {
	defined := map[string]bool{stage.InputRowVariable: true}
	for index, operation := range stage.DerivedLets {
		if operation.Kind != PhysicalExpressionLetOp || operation.ExpressionLet == nil {
			return fmt.Errorf("derived operation %d must be a typed expression LET", index)
		}
		if err := validatePhysicalExpression(operation.ExpressionLet.Expression, defined, bindVars); err != nil {
			return fmt.Errorf("derived operation %d: %w", index, err)
		}
		if err := definePhysicalVariable(defined, operation.ExpressionLet.Variable); err != nil {
			return err
		}
	}
	if expectFilter {
		if err := validatePhysicalFilter(*stage.Filter, defined, bindVars); err != nil {
			return err
		}
	}
	for index, projection := range stage.OutputProjections {
		if err := validatePhysicalProjection(projection, defined, bindVars); err != nil {
			return fmt.Errorf("output projection %d (%q): %w", index, projection.Name, err)
		}
	}
	if err := definePhysicalVariable(defined, stage.OutputRowVariable); err != nil {
		return err
	}
	if err := validateStageProjectionNames(stage.OutputProjections, stage.OutputColumns); err != nil {
		return err
	}
	return nil
}

func validateStageProjectionNames(projections []PhysicalProjection, columns []PhysicalStageColumn) error {
	if len(projections) != len(columns) {
		return fmt.Errorf("output projection count %d does not match schema count %d", len(projections), len(columns))
	}
	for index := range projections {
		if projections[index].Name != columns[index].Name || projections[index].Hidden != columns[index].Internal {
			return fmt.Errorf("output projection %d does not match schema column %q", index, columns[index].Name)
		}
	}
	return nil
}

func groupedPivotOutputNames(pivot PhysicalGroupedPivot) []string {
	columns := make([]string, 0, len(pivot.GroupKeys)+len(pivot.Categories)+len(pivot.CodedCategories)+2)
	for _, key := range pivot.GroupKeys {
		name := key.Output
		if name == "" {
			name = key.Column
		}
		columns = append(columns, name)
	}
	for _, category := range pivot.Categories {
		columns = append(columns, category.Output)
	}
	for _, category := range pivot.CodedCategories {
		columns = append(columns, category.Output)
	}
	if pivot.UnlistedEvidenceColumn != "" {
		columns = append(columns, pivot.UnlistedEvidenceColumn)
	}
	columns = append(columns, "__loom_row_id")
	return columns
}

func unpivotOutputNames(unpivot PhysicalUnpivot) []string {
	selected := map[string]bool{}
	for _, input := range unpivot.Inputs {
		selected[input.Column] = true
	}
	preservedNames := map[string]string{}
	for _, output := range unpivot.PreservedOutputs {
		preservedNames[output.InputColumn] = output.OutputColumn
	}
	columns := make([]string, 0, len(unpivot.InputProjections)+2)
	for _, projection := range unpivot.InputProjections {
		if !selected[projection.Name] && projection.Name != "__loom_row_id" {
			name := projection.Name
			if renamed, ok := preservedNames[projection.Name]; ok {
				name = renamed
			}
			columns = append(columns, name)
		}
	}
	columns = append(columns, unpivot.KeyOutput, unpivot.ValueOutput, "__loom_row_id")
	return columns
}

func validateShapeStageOutput(stage PhysicalConstructionStage, expected []string) error {
	expectedNames := make(map[string]bool, len(expected))
	for _, name := range expected {
		expectedNames[name] = true
	}
	actual := make(map[string]PhysicalStageColumn, len(stage.OutputColumns))
	for _, column := range stage.OutputColumns {
		actual[column.Name] = column
	}
	for _, name := range expected {
		if _, ok := actual[name]; !ok {
			return fmt.Errorf("shape output schema is missing emitted column %q", name)
		}
	}
	for name, column := range actual {
		if !expectedNames[name] && !column.Internal {
			return fmt.Errorf("shape output schema contains unexpected public column %q", name)
		}
	}
	return nil
}
