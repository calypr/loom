package authoringv2

import (
	"fmt"
	"reflect"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func applyAddColumn(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	candidate, ok := catalogCandidate(catalog, command.CandidateID())
	if document < 0 || !ok {
		return result, fmt.Errorf("output or candidate was not found")
	}
	occurrence := findRoute(&workspace.Documents[document].Route, command.OccurrenceID())
	node, nodeOK := catalogNode(catalog, candidate.NodeID)
	if occurrence == nil || !nodeOK || occurrence.ResourceType != node.ResourceType {
		return result, fmt.Errorf("candidate %q does not belong to occurrence %q", candidate.ID, command.OccurrenceID())
	}
	mode := strings.ToUpper(strings.TrimSpace(command.ProjectionMode()))
	if mode == "" {
		mode = candidate.DefaultProjectionMode
	}
	if !contains(candidate.ProjectionModes, mode) {
		return result, fmt.Errorf("projection mode %q is not advertised", mode)
	}
	presentation := strings.ToUpper(strings.TrimSpace(command.InitialPresentation))
	if presentation == "" {
		presentation = InitialPresentationTable
	}
	if presentation == InitialPresentationFilter && !candidate.Filterable {
		return result, fmt.Errorf("candidate %q does not support filters", candidate.ID)
	}
	if presentation == InitialPresentationChart && !candidate.Chartable {
		return result, fmt.Errorf("candidate %q does not support charts", candidate.ID)
	}
	for i := range workspace.Documents[document].Columns {
		column := &workspace.Documents[document].Columns[i]
		if column.OccurrenceID == command.OccurrenceID() && column.Source.Kind == ws.SourceField && column.Source.Field != nil && strings.TrimPrefix(column.Source.Field.Path, "root.") == strings.TrimPrefix(candidate.FieldPath, "root.") && strings.EqualFold(column.Source.Field.ProjectionMode, mode) {
			applyInitialPresentation(column, presentation, nextTableOrder(workspace.Documents[document]))
			return CommandResult{Type: CommandResultColumnAdded, OutputID: command.OutputID, Column: column.Column}, nil
		}
	}
	columnID := commandGeneratedID("col_", command.OutputID, command.OccurrenceID(), candidate.ID, mode)
	if command.OccurrenceID() != ws.RootOccurrenceID {
		columnID = command.OccurrenceID() + "__" + columnID
	}
	label := strings.TrimSpace(command.Title)
	if label == "" {
		label = candidate.Label
	}
	source := editableSource(command.OccurrenceID(), ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: strings.TrimPrefix(candidate.FieldPath, "root."), ProjectionMode: mode}})
	column := ws.Column{Column: columnID, Label: label, LogicalType: candidate.LogicalType, OccurrenceID: command.OccurrenceID(), Source: source}
	column.ColumnID = stagedSourceColumnID(workspace.Documents[document].Output.ID, commandID, index, command.Type, column.Column)
	applyInitialPresentation(&column, presentation, nextTableOrder(workspace.Documents[document]))
	workspace.Documents[document].Columns = append(workspace.Documents[document].Columns, column)
	return CommandResult{Type: CommandResultColumnAdded, OutputID: command.OutputID, Column: columnID}, nil
}

func applyAddColumnSource(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	return applyColumnSource(workspace, catalog, commandID, index, command, *command.Source(), "", InitialPresentationTable)
}

func applyApplyConstructionChoice(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	resolved := command.ResolvedChoice
	if resolved == nil || strings.TrimSpace(resolved.CandidateID) == "" || strings.TrimSpace(resolved.LogicalType) == "" {
		return result, fmt.Errorf("construction choice has no server-resolved source")
	}
	presentation := strings.ToUpper(strings.TrimSpace(command.InitialPresentation))
	if presentation == "" {
		presentation = InitialPresentationTable
	}
	documentPos := documentIndex(workspace, command.OutputID)
	if documentPos < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	document := workspace.Documents[documentPos]
	if document.Route.OccurrenceID != ws.RootOccurrenceID || document.Route.ResourceType != document.RootResourceType {
		return result, fmt.Errorf("table root route is invalid")
	}
	targetResourceType := document.RootResourceType
	if len(resolved.Route) > 0 {
		targetResourceType = resolved.Route[len(resolved.Route)-1].ToResourceType
	}
	if presentation != InitialPresentationTable {
		candidate, ok := catalogCandidate(catalog, resolved.CandidateID)
		if !ok {
			return result, fmt.Errorf("construction choice candidate %q was not found", resolved.CandidateID)
		}
		node, ok := catalogNode(catalog, candidate.NodeID)
		if !ok {
			return result, fmt.Errorf("construction choice candidate %q has no catalog node", resolved.CandidateID)
		}
		if node.ResourceType != targetResourceType || len(resolved.Route) > 0 && node.ID != resolved.Route[len(resolved.Route)-1].ToNodeID {
			return result, fmt.Errorf("construction choice candidate %q does not match its exact terminal route", resolved.CandidateID)
		}
		if presentation == InitialPresentationFilter && !candidate.Filterable {
			return result, fmt.Errorf("construction choice candidate %q does not support filters", resolved.CandidateID)
		}
		if presentation == InitialPresentationChart && !candidate.Chartable {
			return result, fmt.Errorf("construction choice candidate %q does not support charts", resolved.CandidateID)
		}
	}
	occurrenceID, routeErr := ensureConstructionRoute(&workspace.Documents[documentPos], catalog, commandID, index, resolved.Route, resolved.CandidateID)
	if routeErr != nil {
		return result, routeErr
	}
	if err := initializeEmptyConstructionBeforeSourceAdd(workspace, command.OutputID); err != nil {
		return result, err
	}
	if payload, ok := command.Payload.(*ConstructionChoicePayload); ok {
		payload.OccurrenceID = occurrenceID
	}
	applied, err := applyColumnSource(workspace, catalog, commandID, index, command, resolved.Source, resolved.LogicalType, presentation)
	if err != nil {
		return result, err
	}
	if resolved.FrameID != "" {
		if presentation != InitialPresentationTable {
			return result, fmt.Errorf("frame categories can only be added as table columns")
		}
		columns := workspace.Documents[documentPos].Columns
		found := false
		for columnIndex := range columns {
			if columns[columnIndex].Column == applied.Column {
				columns[columnIndex].FrameID = resolved.FrameID
				found = true
				break
			}
		}
		if !found {
			return result, fmt.Errorf("applied frame category column %q was not found", applied.Column)
		}
	}
	return applied, nil
}

func applyAddSemanticSelections(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	if err := initializeEmptyConstructionBeforeSourceAdd(workspace, command.OutputID); err != nil {
		return result, err
	}
	return applySemanticSelections(workspace, catalog, commandID, index, command)
}

func applyUpdateColumn(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	for i := range workspace.Documents[document].Columns {
		current := &workspace.Documents[document].Columns[i]
		if current.Column != command.Column() {
			continue
		}
		value := command.ColumnValue()
		if strings.TrimSpace(value.Label) == "" {
			return result, fmt.Errorf("column label is required")
		}
		current.Label, current.Table, current.Filter, current.Chart = value.Label, value.Table, value.Filter, value.Chart
		if value.Contributor != nil {
			contributor := value.Contributor.Normalized()
			current.Contributor = &contributor
		}
		return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: current.Column}, nil
	}
	return result, fmt.Errorf("column %q was not found", command.Column())
}

func applyUpdateConstructionOutput(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	documentIndex := documentIndex(workspace, command.OutputID)
	if documentIndex < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	construction := workspace.Documents[documentIndex].Construction
	if construction == nil {
		return result, fmt.Errorf("output %q has no construction", command.OutputID)
	}
	presentation := command.ConstructionOutput()
	outputName := ""
	for _, step := range construction.Steps {
		if step.ID != presentation.StepID {
			continue
		}
		for _, output := range step.Outputs {
			if output.ID == presentation.ColumnID {
				outputName = output.Name
				break
			}
		}
		break
	}
	if outputName == "" {
		return result, fmt.Errorf("construction output %q was not found in step %q", presentation.ColumnID, presentation.StepID)
	}
	for stepIndex := range construction.Steps {
		for outputIndex := range construction.Steps[stepIndex].Outputs {
			current := &construction.Steps[stepIndex].Outputs[outputIndex]
			if current.ID != presentation.ColumnID {
				continue
			}
			current.Label = presentation.Label
			if presentation.Table == nil {
				current.Table = nil
			} else {
				table := *presentation.Table
				current.Table = &table
			}
		}
	}
	return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: outputName}, nil
}

func applyUpdateColumnTransformation(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	for i := range workspace.Documents[document].Columns {
		current := &workspace.Documents[document].Columns[i]
		if current.Column != command.Column() {
			continue
		}
		if command.TransformationChange().Kind == "REMOVE" {
			current.ValueTransformation = nil
		} else {
			if err := validateColumnValueTransformationForCatalog(
				workspace.Documents[document], catalog, *current, current.Source, *command.TransformationChange().Transformation,
			); err != nil {
				return result, err
			}
			transformation := command.TransformationChange().Transformation.Clone()
			current.ValueTransformation = &transformation
		}
		return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: current.Column}, nil
	}
	return result, fmt.Errorf("column %q was not found", command.Column())
}

func applySetColumnContributor(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	for i := range workspace.Documents[document].Columns {
		current := &workspace.Documents[document].Columns[i]
		if current.Column != command.Column() {
			continue
		}
		contributor := command.Contributor().Normalized()
		if err := ValidateContributorForCatalog(workspace.Documents[document], catalog, current.OccurrenceID, current.Source, contributor); err != nil {
			return result, err
		}
		current.Contributor = &contributor
		return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: current.Column}, nil
	}
	return result, fmt.Errorf("column %q was not found", command.Column())
}

func applyClearColumnContributor(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	for i := range workspace.Documents[document].Columns {
		current := &workspace.Documents[document].Columns[i]
		if current.Column != command.Column() {
			continue
		}
		current.Contributor = nil
		return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: current.Column}, nil
	}
	return result, fmt.Errorf("column %q was not found", command.Column())
}

func applyUpdateColumnSource(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	for i := range workspace.Documents[document].Columns {
		current := &workspace.Documents[document].Columns[i]
		if current.Column != command.Column() {
			continue
		}
		source := editableSource(current.OccurrenceID, *command.Source())
		if err := validateEditableSource(workspace.Documents[document], catalog, current.OccurrenceID, source); err != nil {
			return result, err
		}
		if current.ValueTransformation != nil {
			if err := validateColumnValueTransformationForCatalog(workspace.Documents[document], catalog, *current, source, *current.ValueTransformation); err != nil {
				return result, err
			}
		}
		current.Source = source
		current.LogicalType = inferredSourceLogicalType(workspace.Documents[document], catalog, current.OccurrenceID, source, current.LogicalType)
		return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: current.Column}, nil
	}
	return result, fmt.Errorf("column %q was not found", command.Column())
}

func applyUpdateColumnRowValuePolicy(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	if err := command.validate(); err != nil {
		return result, err
	}
	documentPos := documentIndex(workspace, command.OutputID)
	if documentPos < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	document := &workspace.Documents[documentPos]
	if document.Rows.Kind != ws.RowDefinitionGroups || document.Rows.Groups == nil ||
		document.Rows.Groups.Source.Kind != ws.GroupSourceExplicit || document.Rows.Groups.Source.Explicit == nil {
		return result, fmt.Errorf("UPDATE_COLUMN_ROW_VALUE_POLICY requires explicit grouped rows")
	}
	columnIndex := -1
	for index := range document.Columns {
		if document.Columns[index].Column != command.Column() {
			continue
		}
		if columnIndex >= 0 {
			return result, fmt.Errorf("column %q is ambiguous", command.Column())
		}
		columnIndex = index
	}
	if columnIndex < 0 {
		return result, fmt.Errorf("column %q was not found", command.Column())
	}
	column := document.Columns[columnIndex]
	if column.ColumnID == "" || !supportsExplicitGroupRowValueColumn(column) {
		return result, fmt.Errorf("column %q must be a supported root FHIR field with a stable columnId", command.Column())
	}
	matchingBinding := -1
	for index := range document.Rows.Groups.RowValues {
		if document.Rows.Groups.RowValues[index].ColumnID != column.ColumnID {
			continue
		}
		if matchingBinding >= 0 {
			return result, fmt.Errorf("row value binding for columnId %q is ambiguous", column.ColumnID)
		}
		matchingBinding = index
	}
	if matchingBinding < 0 {
		return result, fmt.Errorf("row value binding for columnId %q was not found", column.ColumnID)
	}
	document.Rows.Groups.RowValues[matchingBinding].Policy = command.RowValuePolicy()
	return CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID, Column: column.Column}, nil
}

func applyRemoveColumn(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	document := documentIndex(workspace, command.OutputID)
	if document < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	columns := workspace.Documents[document].Columns[:0]
	found := false
	for _, column := range workspace.Documents[document].Columns {
		if column.Column == command.Column() {
			found = true
			removeConstructionColumnValues(&workspace.Documents[document], column.ColumnID)
			removeExplicitGroupRowValue(&workspace.Documents[document], column.ColumnID)
			continue
		}
		columns = append(columns, column)
	}
	if !found {
		return result, fmt.Errorf("column %q was not found", command.Column())
	}
	workspace.Documents[document].Columns = columns
	cleanupDocumentReferences(&workspace.Documents[document])
	cleanupWorkspaceBindings(workspace)
	return result, nil
}

func applyRemoveProjectedField(workspace *ws.Workspace, catalog ws.CatalogSnapshot, commandID string, index int, command Command) (CommandResult, error) {
	result := CommandResult{Type: CommandResultTableChanged, OutputID: command.OutputID}
	documentPos := documentIndex(workspace, command.OutputID)
	if documentPos < 0 {
		return result, fmt.Errorf("output %q was not found", command.OutputID)
	}
	current := workspace.Documents[documentPos]
	targetIndex := -1
	for index := range current.Columns {
		if current.Columns[index].ColumnID != command.ColumnID() {
			continue
		}
		if targetIndex >= 0 {
			return result, fmt.Errorf("columnId %q is ambiguous", command.ColumnID())
		}
		targetIndex = index
	}
	if targetIndex < 0 {
		return result, fmt.Errorf("columnId %q was not found", command.ColumnID())
	}
	if current.Columns[targetIndex].Source.Kind != ws.SourceImported {
		if command.resolvedImportedDocument != nil {
			return result, fmt.Errorf("authored projected field removal cannot replace the document")
		}
		if err := removeDocumentColumnByID(workspace, documentPos, command.ColumnID()); err != nil {
			return result, err
		}
		return result, nil
	}
	if current.ImportedRecipe == nil {
		return result, fmt.Errorf("pinned projected field removal requires an imported recipe")
	}
	if command.resolvedImportedDocument == nil {
		return result, fmt.Errorf("pinned projected field removal requires a lifecycle-resolved source edit")
	}
	resolved := *command.resolvedImportedDocument
	if current.ImportedRecipe == nil || resolved.ImportedRecipe == nil || *current.ImportedRecipe != *resolved.ImportedRecipe || resolved.Output.ID != current.Output.ID {
		return result, fmt.Errorf("projected field removal does not match its pinned output")
	}
	if current.RootResourceType != resolved.RootResourceType || !reflect.DeepEqual(current.Output, resolved.Output) ||
		!reflect.DeepEqual(current.Route, resolved.Route) || !reflect.DeepEqual(current.Rows, resolved.Rows) ||
		!reflect.DeepEqual(current.Population, resolved.Population) || !reflect.DeepEqual(current.Frames, resolved.Frames) ||
		!reflect.DeepEqual(current.Construction, resolved.Construction) ||
		!reflect.DeepEqual(current.FixedFilters, resolved.FixedFilters) || !reflect.DeepEqual(current.Actions, resolved.Actions) {
		return result, fmt.Errorf("projected field removal must preserve row identity and all downstream authored work")
	}
	if !reflect.DeepEqual(authoredColumns(current.Columns), authoredColumns(resolved.Columns)) {
		return result, fmt.Errorf("projected field removal must preserve authored columns")
	}
	foundTarget := false
	for _, column := range current.Columns {
		if column.ColumnID == command.ColumnID() && column.Source.Kind == ws.SourceImported {
			if foundTarget {
				return result, fmt.Errorf("projected columnId %q is ambiguous", command.ColumnID())
			}
			foundTarget = true
		}
	}
	if !foundTarget {
		return result, fmt.Errorf("pinned projected field %q was not found", command.ColumnID())
	}
	for _, column := range resolved.Columns {
		if column.ColumnID == command.ColumnID() {
			return result, fmt.Errorf("pinned projected field %q remains in the resolved source edit", command.ColumnID())
		}
	}
	workspace.Documents[documentPos] = resolved
	cleanupWorkspaceBindings(workspace)
	return result, nil
}
