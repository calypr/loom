package receipt

import (
	"fmt"
	"sort"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

func orderReconciledOutputColumns(contract *receiptmodel.PublicOutputContract, presentation []explorercompilation.PresentationColumn, emitted []viewer.EmittedColumn) {
	tableOrder := make(map[string]int, len(presentation))
	physicalOrder := make(map[string]int, len(presentation))
	for _, column := range presentation {
		tableOrder[column.PublicColumn] = column.Order
		physicalOrder[column.PublicColumn] = column.PhysicalOrder
	}
	less := func(left, right string) bool {
		if tableOrder[left] != tableOrder[right] {
			return tableOrder[left] < tableOrder[right]
		}
		if physicalOrder[left] != physicalOrder[right] {
			return physicalOrder[left] < physicalOrder[right]
		}
		return left < right
	}
	sort.SliceStable(contract.Columns, func(i, j int) bool {
		return less(contract.Columns[i].Column, contract.Columns[j].Column)
	})
	sort.SliceStable(emitted, func(i, j int) bool {
		return less(emitted[i].PublicColumn, emitted[j].PublicColumn)
	})
	contract.Lossless = true
	contract.MLReady = true
	contract.StructuralSuitability = ""
	contract.LossReasons = nil
	for _, column := range emitted {
		mergeReconciledContractQuality(contract, column)
	}
}

func explicitGroupOutputEmission(document ws.Document, outputID string, column facade.CompiledOutputColumn) (viewer.EmittedColumn, bool, error) {
	if document.Rows.Kind != ws.RowDefinitionGroups || document.Rows.Groups == nil ||
		document.Rows.Groups.Source.Kind != ws.GroupSourceExplicit || document.Rows.Groups.Source.Explicit == nil {
		return viewer.EmittedColumn{}, false, nil
	}
	explicit := document.Rows.Groups.Source.Explicit
	if strings.TrimSpace(explicit.RevisionID) == "" {
		return viewer.EmittedColumn{}, false, fmt.Errorf("explicit group revision identity is required")
	}
	shape, label, filterable, chartable := "", "", false, false
	switch column.Name {
	case "group_label":
		shape, label, filterable, chartable = "scalar", "Group label", true, true
		if column.Kind != "string" || column.Cardinality != "required_one" || column.Nullable {
			return viewer.EmittedColumn{}, false, fmt.Errorf("compiled group label schema has unexpected type or cardinality")
		}
	case "group_ordinal":
		shape, label, filterable, chartable = "scalar", "Group ordinal", true, true
		if column.Kind != "integer" || column.Cardinality != "required_one" || column.Nullable {
			return viewer.EmittedColumn{}, false, fmt.Errorf("compiled group ordinal schema has unexpected type or cardinality")
		}
	case "members":
		shape, label = "record_list", "Members"
		if column.Kind != "object" || column.Cardinality != "many" || column.Nullable {
			return viewer.EmittedColumn{}, false, fmt.Errorf("compiled group members schema has unexpected type or cardinality")
		}
	default:
		return viewer.EmittedColumn{}, false, nil
	}
	emitted := viewer.EmittedColumn{
		EmissionID:            explicitGroupEmissionID(explicit.RevisionID, column.Name),
		OutputID:              outputID,
		PublicColumn:          column.Name,
		Label:                 label,
		Shape:                 shape,
		Lossless:              false,
		MLReady:               false,
		StructuralSuitability: "requires-review",
		LossReasons:           []string{"TABLE_SHAPE_GROUP_CHANGES_ROW_GRAIN", TableShapeMLReadinessUnassessed},
		Filterable:            filterable,
		Chartable:             chartable,
	}
	if err := applyCompiledColumnMetadata(&emitted, column); err != nil {
		return viewer.EmittedColumn{}, false, err
	}
	return emitted, true, nil
}

func explicitGroupEmissionID(revisionID, column string) string {
	return "explicit-group:" + revisionID + ":" + column
}

func compiledFilterSourceSlotMatches(compiledOutput facade.CompiledRecipeOutput, projection ws.ConstructionSourceProjection) bool {
	if len(compiledOutput.Stages) == 0 || compiledOutput.Stages[0].ID != recipe.ConstructionSourceProjectionID {
		return false
	}
	for _, column := range compiledOutput.Stages[0].Columns {
		if column.ID != projection.ColumnID {
			continue
		}
		return !column.Internal && !column.Identity &&
			column.Name == ws.ConstructionSourceProjectionName(projection.ColumnID) &&
			strings.TrimSpace(column.SemanticPath) == strings.TrimSpace(projection.FieldPath) &&
			column.Kind == projection.LogicalType &&
			strings.TrimSpace(column.Label) == strings.TrimSpace(projection.Label)
	}
	return false
}

func compiledDeriveSourceSlotMatches(compiledOutput facade.CompiledRecipeOutput, projection ws.ConstructionSourceProjection) bool {
	return compiledFilterSourceSlotMatches(compiledOutput, projection)
}

func compiledUnpivotSourceSlotMatches(compiledOutput facade.CompiledRecipeOutput, projection ws.ConstructionSourceProjection) bool {
	return compiledFilterSourceSlotMatches(compiledOutput, projection)
}

func AuthoredOutputColumnsWithCombineInputs(
	workspace ws.Workspace,
	compiledOutputs map[string]facade.CompiledRecipeOutput,
	combineInputs map[string]map[int]facade.ResolvedClickHouseTable,
	rowSourceSnapshot capability.Snapshot,
) (map[string]ws.Document, map[string]map[string]AuthoredOutputColumn, error) {
	documents := make(map[string]ws.Document, len(workspace.Documents))
	columns := make(map[string]map[string]AuthoredOutputColumn, len(workspace.Documents))
	for _, document := range workspace.Documents {
		outputID := document.Output.ID
		if strings.TrimSpace(outputID) == "" {
			return nil, nil, fmt.Errorf("workspace document output identity is required")
		}
		if _, duplicate := documents[outputID]; duplicate {
			return nil, nil, fmt.Errorf("duplicate workspace document output identity %q", outputID)
		}
		documents[outputID] = document
		columns[outputID] = make(map[string]AuthoredOutputColumn)
		if document.Construction == nil {
			continue
		}
		if document.Construction != nil {
			compiledOutput, exists := compiledOutputs[outputID]
			needsCompiledCombine := false
			for _, step := range document.Construction.Steps {
				if step.Operation.Kind == ws.ConstructionOperationCombine {
					needsCompiledCombine = true
					break
				}
			}
			if needsCompiledCombine && !exists {
				return nil, nil, fmt.Errorf("compiled output %q is missing for Combine construction metadata", outputID)
			}
			if err := AuthoredConstructionOutputsWithCombineInputs(document, columns[outputID], compiledOutput, combineInputs[outputID], rowSourceSnapshot); err != nil {
				return nil, nil, fmt.Errorf("construction outputs for document %q: %w", outputID, err)
			}
		}
	}
	return documents, columns, nil
}

func AuthoredConstructionOutputsWithCombineInputs(
	document ws.Document,
	authored map[string]AuthoredOutputColumn,
	compiledOutput facade.CompiledRecipeOutput,
	resolvedInputs map[int]facade.ResolvedClickHouseTable,
	rowSourceSnapshot capability.Snapshot,
) error {
	if document.Construction == nil {
		return nil
	}
	sourceOnlyIDs := make(map[string]struct{}, len(document.Construction.SourceProjections))
	for _, projection := range document.Construction.SourceProjections {
		sourceOnlyIDs[projection.ColumnID] = struct{}{}
	}
	prior := make(map[string]string, len(document.Columns))
	priorIDsByName := make(map[string]string, len(document.Columns))
	for _, column := range document.Columns {
		if !requiredConstructionID(column.ColumnID) {
			return fmt.Errorf("source column %q has no stable column ID", column.Column)
		}
		if strings.TrimSpace(column.Column) == "" {
			return fmt.Errorf("source column %q has no public name", column.ColumnID)
		}
		if _, duplicate := prior[column.ColumnID]; duplicate {
			return fmt.Errorf("source schema duplicates column ID %q", column.ColumnID)
		}
		if existingID, duplicate := priorIDsByName[column.Column]; duplicate {
			return fmt.Errorf("source schema maps public name %q to column IDs %q and %q", column.Column, existingID, column.ColumnID)
		}
		prior[column.ColumnID] = column.Column
		priorIDsByName[column.Column] = column.ColumnID
	}
	if len(compiledOutput.Stages) > 0 && compiledOutput.Stages[0].ID == recipe.ConstructionSourceProjectionID {
		seenSourceIDs := make(map[string]struct{}, len(compiledOutput.Stages[0].Columns))
		seenSourceNames := make(map[string]string, len(compiledOutput.Stages[0].Columns))
		for _, column := range compiledOutput.Stages[0].Columns {
			if column.Internal || column.Identity {
				continue
			}
			if !requiredConstructionID(column.ID) || strings.TrimSpace(column.Name) == "" {
				return fmt.Errorf("compiled source stage contains a column with a missing stable ID or public name")
			}
			if _, duplicate := seenSourceIDs[column.ID]; duplicate {
				return fmt.Errorf("compiled source stage duplicates column ID %q", column.ID)
			}
			seenSourceIDs[column.ID] = struct{}{}
			if existingID, duplicate := seenSourceNames[column.Name]; duplicate && existingID != column.ID {
				return fmt.Errorf("compiled source stage maps public name %q to column IDs %q and %q", column.Name, existingID, column.ID)
			}
			seenSourceNames[column.Name] = column.ID
			if existingName, exists := prior[column.ID]; exists && existingName != column.Name {
				return fmt.Errorf("compiled source column ID %q conflicts with authored public name %q", column.ID, existingName)
			}
			if existingID, exists := priorIDsByName[column.Name]; exists && existingID != column.ID {
				return fmt.Errorf("compiled source public name %q conflicts with authored column ID %q", column.Name, existingID)
			}
			prior[column.ID] = column.Name
			priorIDsByName[column.Name] = column.ID
		}
	}
	for stepIndex, step := range document.Construction.Steps {
		nextAuthored := make(map[string]AuthoredOutputColumn, len(step.Outputs))
		for _, output := range step.Outputs {
			if prior[output.ID] == output.Name {
				if metadata, exists := authored[output.Name]; exists {
					nextAuthored[output.Name] = metadata
				}
			}
		}
		stage := constructionOutputStep{
			document: document, step: step, stepIndex: stepIndex, compiledOutput: compiledOutput,
			resolvedInputs: resolvedInputs, rowSourceSnapshot: rowSourceSnapshot,
			authored: authored, nextAuthored: nextAuthored, prior: prior, sourceOnlyIDs: sourceOnlyIDs,
		}
		if err := stage.reconcile(); err != nil {
			return err
		}
		next := make(map[string]string, len(step.Outputs))
		for _, output := range step.Outputs {
			if !requiredConstructionID(output.ID) || strings.TrimSpace(output.Name) == "" {
				return fmt.Errorf("step %q output schema contains a missing stable ID or public name", step.ID)
			}
			if _, duplicate := next[output.ID]; duplicate {
				return fmt.Errorf("step %q output schema duplicates column ID %q", step.ID, output.ID)
			}
			next[output.ID] = output.Name
		}
		clear(authored)
		for name, metadata := range nextAuthored {
			authored[name] = metadata
		}
		prior = next
	}
	return nil
}

func combineProjectionInputName(
	compiledOutput facade.CompiledRecipeOutput,
	resolvedInputs map[int]facade.ResolvedClickHouseTable,
	inputIndex int,
	columnID string,
) (string, error) {
	if compiledOutput.Plan.ClickHouseCombine == nil || inputIndex < 0 || inputIndex >= len(compiledOutput.Plan.ClickHouseCombine.Inputs) {
		return "", fmt.Errorf("input %d is not present in the compiled Combine plan", inputIndex)
	}
	input := compiledOutput.Plan.ClickHouseCombine.Inputs[inputIndex]
	if input.WorkspaceOutputID != "" {
		for _, source := range compiledOutput.WorkspaceOutputSources {
			if source.InputIndex != inputIndex || source.OutputID != input.WorkspaceOutputID {
				continue
			}
			for _, column := range source.Schema {
				if column.ID == columnID && column.Name != "" && !column.Internal && !column.Identity {
					return column.Name, nil
				}
			}
			return "", fmt.Errorf("workspace input %d output %q has no compiler-resolved public column ID %q", inputIndex, input.WorkspaceOutputID, columnID)
		}
		return "", fmt.Errorf("workspace input %d output %q has no compiler-resolved sibling schema", inputIndex, input.WorkspaceOutputID)
	}
	resolved, ok := resolvedInputs[inputIndex]
	if !ok || resolved.TableID != input.TableID || resolved.RevisionID != input.RevisionID || resolved.OutputID != input.OutputID {
		return "", fmt.Errorf("published input %d does not match the exact compiled table revision", inputIndex)
	}
	for _, column := range resolved.Columns {
		if column.ID == columnID && strings.TrimSpace(column.Name) != "" {
			return column.Name, nil
		}
	}
	return "", fmt.Errorf("published input %d exact schema has no column ID %q", inputIndex, columnID)
}

type ConstructionInputMissingError struct {
	ColumnID   string
	StepID     string
	OutputID   string
	OutputName string
}

func (e *ConstructionInputMissingError) Error() string {
	if e.StepID != "" || e.OutputName != "" {
		return fmt.Sprintf("construction step %q output %q references input column %q, which is absent from the prior stage", e.StepID, e.OutputName, e.ColumnID)
	}
	return fmt.Sprintf("input column ID %q is absent from the prior stage", e.ColumnID)
}

func ConstructionInputNames(schema map[string]string, ids []string) ([]string, error) {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		name, exists := schema[id]
		if !exists {
			return nil, &ConstructionInputMissingError{ColumnID: id}
		}
		names = append(names, name)
	}
	return uniqueColumnsInOrder(names)
}

func requiredConstructionID(value string) bool {
	return strings.TrimSpace(value) != "" && value == strings.TrimSpace(value)
}
