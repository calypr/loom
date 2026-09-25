package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

type authoredOutputColumn struct {
	ConstructionID string
	Label          string
	InputColumns   []string
	Quality        constructedOutputQuality
}

type constructedOutputQuality struct {
	Lossless              bool
	StructuralSuitability string
	LossReasons           []string
}

const tableShapeMLReadinessUnassessed = "TABLE_SHAPE_ML_READINESS_UNASSESSED"

func reconcileFinalOutputMetadata(translated explorercompilation.WorkspaceResult, resolved dataframeexecution.Resolved) (explorercompilation.WorkspaceResult, error) {
	if len(translated.Bundle.Outputs) != len(resolved.Bundle.Outputs) || len(resolved.Bundle.Outputs) != len(resolved.Compiled.Outputs) {
		return explorercompilation.WorkspaceResult{}, fmt.Errorf("compiled output set does not match translated output set")
	}
	for index := range translated.Bundle.Outputs {
		if translated.Bundle.Outputs[index].Name != resolved.Bundle.Outputs[index].Name {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("compiled output %d identity %q does not match translated output %q", index, resolved.Bundle.Outputs[index].Name, translated.Bundle.Outputs[index].Name)
		}
		if resolved.Compiled.Outputs[index].Name != resolved.Bundle.Outputs[index].Name {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("final compiler output %d identity %q does not match recipe output %q", index, resolved.Compiled.Outputs[index].Name, resolved.Bundle.Outputs[index].Name)
		}
	}
	if err := (explorer.PublicOutputContracts{Outputs: translated.OutputContracts}).ValidateAgainst(translated.Bundle, translated.EmittedColumns); err != nil {
		return explorercompilation.WorkspaceResult{}, fmt.Errorf("translated output contract is inconsistent: %w", err)
	}

	documents, authoredColumns, err := authoredOutputColumns(translated.Workspace)
	if err != nil {
		return explorercompilation.WorkspaceResult{}, err
	}
	compiledByOutput := make(map[string]lower.CompiledRecipeOutput, len(resolved.Compiled.Outputs))
	for _, output := range resolved.Compiled.Outputs {
		if strings.TrimSpace(output.Name) == "" {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("compiled output identity is required")
		}
		if _, duplicate := compiledByOutput[output.Name]; duplicate {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("duplicate compiled output identity %q", output.Name)
		}
		compiledByOutput[output.Name] = output
	}
	if len(documents) != len(resolved.Bundle.Outputs) {
		return explorercompilation.WorkspaceResult{}, fmt.Errorf("workspace document set does not match compiled output set")
	}

	emittedByOutput := make(map[string]map[string]explorer.EmittedColumn, len(translated.Bundle.Outputs))
	emittedIDs := make(map[string]map[string]struct{}, len(translated.Bundle.Outputs))
	for _, output := range resolved.Bundle.Outputs {
		emittedByOutput[output.Name] = make(map[string]explorer.EmittedColumn)
		emittedIDs[output.Name] = make(map[string]struct{})
	}
	for _, emitted := range translated.EmittedColumns {
		columns, ok := emittedByOutput[emitted.OutputID]
		if !ok || strings.TrimSpace(emitted.PublicColumn) == "" || strings.TrimSpace(emitted.EmissionID) == "" {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("translated emission has missing or unknown output identity %q", emitted.OutputID)
		}
		if _, duplicate := columns[emitted.PublicColumn]; duplicate {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("duplicate translated public column %q for output %q", emitted.PublicColumn, emitted.OutputID)
		}
		if _, duplicate := emittedIDs[emitted.OutputID][emitted.EmissionID]; duplicate {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("duplicate translated emission identity %q for output %q", emitted.EmissionID, emitted.OutputID)
		}
		columns[emitted.PublicColumn] = emitted
		emittedIDs[emitted.OutputID][emitted.EmissionID] = struct{}{}
	}

	presentations, err := indexPresentations(translated.Presentations, compiledByOutput)
	if err != nil {
		return explorercompilation.WorkspaceResult{}, err
	}
	contracts := make(map[string]explorer.PublicOutputContract, len(translated.OutputContracts))
	for _, contract := range translated.OutputContracts {
		if _, duplicate := contracts[contract.OutputID]; duplicate {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("duplicate translated output contract identity %q", contract.OutputID)
		}
		contracts[contract.OutputID] = contract
	}
	if len(contracts) != len(resolved.Bundle.Outputs) || len(presentations) != len(resolved.Bundle.Outputs) {
		return explorercompilation.WorkspaceResult{}, fmt.Errorf("translated output contract or presentation set does not match compiled output set")
	}

	reconciled := translated
	reconciled.EmittedColumns = make([]explorer.EmittedColumn, 0, len(translated.EmittedColumns))
	reconciled.OutputContracts = make([]explorer.PublicOutputContract, 0, len(translated.OutputContracts))
	reconciled.Presentations = make([]explorercompilation.PresentationConfig, 0, len(translated.Presentations))
	publicEmissionIDs := make(map[string]map[string]struct{}, len(resolved.Bundle.Outputs))
	for _, output := range resolved.Bundle.Outputs {
		publicEmissionIDs[output.Name] = make(map[string]struct{})
	}

	for _, recipeOutput := range resolved.Bundle.Outputs {
		if _, ok := documents[recipeOutput.Name]; !ok {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("workspace document for output %q is missing", recipeOutput.Name)
		}
		compiledOutput, ok := compiledByOutput[recipeOutput.Name]
		if !ok {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("compiled output %q is missing", recipeOutput.Name)
		}
		translatedContract, ok := contracts[recipeOutput.Name]
		if !ok {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("translated output contract %q is missing", recipeOutput.Name)
		}
		presentation, ok := presentations[recipeOutput.Name]
		if !ok {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("translated presentation %q is missing", recipeOutput.Name)
		}
		constructed := authoredColumns[recipeOutput.Name]
		lineage, err := resolveAuthoredOutputLineage(constructed, emittedByOutput[recipeOutput.Name])
		if err != nil {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("resolve authored lineage for output %q: %w", recipeOutput.Name, err)
		}
		finalNames := make(map[string]struct{}, len(compiledOutput.OutputSchema))
		contract := translatedContract
		contract.Columns = make([]explorer.PublicOutputColumn, 0, len(compiledOutput.OutputSchema))
		contract.Lossless = true
		contract.MLReady = true
		contract.StructuralSuitability = ""
		contract.LossReasons = nil
		finalPresentation := explorercompilation.PresentationConfig{OutputID: recipeOutput.Name, Title: presentation.Title, Columns: make([]explorercompilation.PresentationColumn, 0, len(compiledOutput.OutputSchema))}
		maxOrder := -1
		for _, schemaColumn := range compiledOutput.OutputSchema {
			if schemaColumn.Internal || schemaColumn.Identity {
				continue
			}
			if column, ok := presentationColumnByName(presentation, schemaColumn.Name); ok && column.Order > maxOrder {
				maxOrder = column.Order
			}
		}
		publicOrder := 0
		for _, schemaColumn := range compiledOutput.OutputSchema {
			if schemaColumn.Internal || schemaColumn.Identity {
				continue
			}
			if strings.TrimSpace(schemaColumn.Name) == "" || strings.TrimSpace(schemaColumn.Kind) == "" || strings.TrimSpace(schemaColumn.Cardinality) == "" {
				return explorercompilation.WorkspaceResult{}, fmt.Errorf("compiled public output %q contains a column with missing identity or type metadata", recipeOutput.Name)
			}
			if _, duplicate := finalNames[schemaColumn.Name]; duplicate {
				return explorercompilation.WorkspaceResult{}, fmt.Errorf("compiled output %q has duplicate public column %q", recipeOutput.Name, schemaColumn.Name)
			}
			finalNames[schemaColumn.Name] = struct{}{}

			metadata, authored := constructed[schemaColumn.Name]
			emitted, translatedEmission := emittedByOutput[recipeOutput.Name][schemaColumn.Name]
			if authored && translatedEmission {
				return explorercompilation.WorkspaceResult{}, fmt.Errorf("output %q column %q is both a translated emission and a table-shape construction", recipeOutput.Name, schemaColumn.Name)
			}
			if translatedEmission {
				emitted = cloneEmittedColumn(emitted)
				if err := applyCompiledColumnMetadata(&emitted, schemaColumn); err != nil {
					return explorercompilation.WorkspaceResult{}, fmt.Errorf("reconcile output %q column %q: %w", recipeOutput.Name, schemaColumn.Name, err)
				}
			} else {
				if !authored {
					return explorercompilation.WorkspaceResult{}, fmt.Errorf("compiled output %q column %q has no translated emission or authored table-shape identity", recipeOutput.Name, schemaColumn.Name)
				}
				if strings.TrimSpace(metadata.ConstructionID) == "" || strings.TrimSpace(metadata.Label) == "" {
					return explorercompilation.WorkspaceResult{}, fmt.Errorf("authored table-shape output %q column %q is missing its construction identity or label", recipeOutput.Name, schemaColumn.Name)
				}
				profile, err := constructedOutputProfileFor(metadata.Quality, schemaColumn)
				if err != nil {
					return explorercompilation.WorkspaceResult{}, fmt.Errorf("reconcile constructed output %q column %q: %w", recipeOutput.Name, schemaColumn.Name, err)
				}
				emitted = explorer.EmittedColumn{
					EmissionID:            constructedEmissionID(metadata.ConstructionID, schemaColumn.Name),
					OutputID:              recipeOutput.Name,
					AuthoredColumns:       append([]string(nil), lineage[schemaColumn.Name]...),
					InputColumns:          append([]string(nil), metadata.InputColumns...),
					ConstructionID:        metadata.ConstructionID,
					PublicColumn:          schemaColumn.Name,
					Label:                 metadata.Label,
					Shape:                 profile.Shape,
					Lossless:              profile.Lossless,
					MLReady:               profile.MLReady,
					StructuralSuitability: profile.StructuralSuitability,
					LossReasons:           append([]string(nil), profile.LossReasons...),
					Filterable:            profile.Filterable,
					Chartable:             profile.Chartable,
				}
				if err := applyCompiledColumnMetadata(&emitted, schemaColumn); err != nil {
					return explorercompilation.WorkspaceResult{}, fmt.Errorf("reconcile constructed output %q column %q: %w", recipeOutput.Name, schemaColumn.Name, err)
				}
				if _, collision := emittedIDs[recipeOutput.Name][emitted.EmissionID]; collision {
					return explorercompilation.WorkspaceResult{}, fmt.Errorf("constructed output %q column %q duplicates translated emission identity %q", recipeOutput.Name, schemaColumn.Name, emitted.EmissionID)
				}
			}
			if strings.TrimSpace(emitted.EmissionID) == "" {
				return explorercompilation.WorkspaceResult{}, fmt.Errorf("final public output %q column %q has no emission identity", recipeOutput.Name, schemaColumn.Name)
			}
			if strings.TrimSpace(emitted.Label) == "" {
				return explorercompilation.WorkspaceResult{}, fmt.Errorf("final public output %q column %q has no label", recipeOutput.Name, schemaColumn.Name)
			}
			if _, duplicate := publicEmissionIDs[recipeOutput.Name][emitted.EmissionID]; duplicate {
				return explorercompilation.WorkspaceResult{}, fmt.Errorf("final public output %q has duplicate emission identity %q", recipeOutput.Name, emitted.EmissionID)
			}

			columnPresentation, presented := presentationColumnByName(presentation, schemaColumn.Name)
			if translatedEmission && !presented {
				return explorercompilation.WorkspaceResult{}, fmt.Errorf("translated presentation for output %q column %q is missing", recipeOutput.Name, schemaColumn.Name)
			}
			if !presented {
				maxOrder++
				columnPresentation = explorercompilation.PresentationColumn{
					EmissionID: emitted.EmissionID, PublicColumn: schemaColumn.Name,
					Label: emitted.Label, Visible: true, Order: maxOrder,
				}
			} else {
				columnPresentation.EmissionID = emitted.EmissionID
				columnPresentation.PublicColumn = schemaColumn.Name
				columnPresentation.Label = emitted.Label
			}
			columnPresentation.PhysicalOrder = publicOrder
			finalPresentation.Columns = append(finalPresentation.Columns, columnPresentation)
			reconciled.EmittedColumns = append(reconciled.EmittedColumns, emitted)
			contract.Columns = append(contract.Columns, publicOutputColumnFromEmission(emitted))
			publicEmissionIDs[recipeOutput.Name][emitted.EmissionID] = struct{}{}
			mergeReconciledContractQuality(&contract, emitted)
			publicOrder++
		}
		for name := range constructed {
			if _, ok := finalNames[name]; !ok {
				return explorercompilation.WorkspaceResult{}, fmt.Errorf("authored table-shape output %q column %q is absent from the final compiler schema", recipeOutput.Name, name)
			}
		}
		reconciled.OutputContracts = append(reconciled.OutputContracts, contract)
		reconciled.Presentations = append(reconciled.Presentations, finalPresentation)
	}

	reconciled.IdentityMappings, err = reconcileIdentityMappings(translated.IdentityMappings, publicEmissionIDs)
	if err != nil {
		return explorercompilation.WorkspaceResult{}, err
	}
	if err := (explorer.PublicOutputContracts{Outputs: reconciled.OutputContracts}).ValidateAgainst(resolved.Bundle, reconciled.EmittedColumns); err != nil {
		return explorercompilation.WorkspaceResult{}, fmt.Errorf("reconciled output contract is inconsistent: %w", err)
	}
	return reconciled, nil
}

func authoredOutputColumns(workspace authoringv2.Workspace) (map[string]authoringv2.Document, map[string]map[string]authoredOutputColumn, error) {
	documents := make(map[string]authoringv2.Document, len(workspace.Documents))
	columns := make(map[string]map[string]authoredOutputColumn, len(workspace.Documents))
	for _, document := range workspace.Documents {
		outputID := document.Output.ID
		if strings.TrimSpace(outputID) == "" {
			return nil, nil, fmt.Errorf("workspace document output identity is required")
		}
		if _, duplicate := documents[outputID]; duplicate {
			return nil, nil, fmt.Errorf("duplicate workspace document output identity %q", outputID)
		}
		documents[outputID] = document
		columns[outputID] = make(map[string]authoredOutputColumn)
		if document.TableShape == nil && document.Construction == nil {
			continue
		}
		if document.TableShape != nil {
			add := func(output authoringv2.ColumnOutput, constructionID authoringv2.ConstructionID, inputColumns []string, quality constructedOutputQuality) error {
				if strings.TrimSpace(output.Column) == "" || strings.TrimSpace(output.Label) == "" || strings.TrimSpace(string(constructionID)) == "" {
					return fmt.Errorf("table-shape output in document %q is missing its column, label, or construction identity", outputID)
				}
				if _, duplicate := columns[outputID][output.Column]; duplicate {
					return fmt.Errorf("duplicate table-shape output %q for document %q", output.Column, outputID)
				}
				inputs, err := uniqueColumnsInOrder(inputColumns)
				if err != nil {
					return fmt.Errorf("table-shape output %q in document %q: %w", output.Column, outputID, err)
				}
				columns[outputID][output.Column] = authoredOutputColumn{
					ConstructionID: string(constructionID),
					Label:          output.Label,
					InputColumns:   inputs,
					Quality:        quality,
				}
				return nil
			}
			shape := document.TableShape
			if shape.Reshape != nil {
				switch shape.Reshape.Kind {
				case "PIVOT":
					if shape.Reshape.Pivot == nil {
						return nil, nil, fmt.Errorf("pivot construction for document %q is missing", outputID)
					}
					pivot := shape.Reshape.Pivot
					inputs := append([]string(nil), pivot.GroupKeys...)
					inputs = append(inputs, pivot.CategoryColumn, pivot.ValueColumn)
					quality, err := pivotOutputQuality(pivot)
					if err != nil {
						return nil, nil, fmt.Errorf("pivot construction for document %q: %w", outputID, err)
					}
					for _, category := range pivot.Categories {
						if err := add(category.Output, pivot.ConstructionID, inputs, quality); err != nil {
							return nil, nil, err
						}
					}
				case "UNPIVOT":
					if shape.Reshape.Unpivot == nil {
						return nil, nil, fmt.Errorf("unpivot construction for document %q is missing", outputID)
					}
					unpivot := shape.Reshape.Unpivot
					dependencies := make([]string, 0, len(unpivot.Inputs))
					for _, input := range unpivot.Inputs {
						dependencies = append(dependencies, input.Column)
					}
					quality, err := unpivotOutputQuality(unpivot)
					if err != nil {
						return nil, nil, fmt.Errorf("unpivot construction for document %q: %w", outputID, err)
					}
					if err := add(unpivot.KeyOutput, unpivot.ConstructionID, dependencies, quality); err != nil {
						return nil, nil, err
					}
					if err := add(unpivot.ValueOutput, unpivot.ConstructionID, dependencies, quality); err != nil {
						return nil, nil, err
					}
				default:
					return nil, nil, fmt.Errorf("unsupported table reshape kind %q for document %q", shape.Reshape.Kind, outputID)
				}
			}
			for _, derived := range shape.Derived {
				dependencies := make([]string, 0, 2)
				for _, operand := range []authoringv2.ArithmeticOperand{derived.Left, derived.Right} {
					switch operand.Kind {
					case "COLUMN":
						if strings.TrimSpace(operand.Column) == "" {
							return nil, nil, fmt.Errorf("derived output %q in document %q has an empty column dependency", derived.Output.Column, outputID)
						}
						dependencies = append(dependencies, operand.Column)
					case "LITERAL":
					default:
						return nil, nil, fmt.Errorf("derived output %q in document %q has unsupported operand kind %q", derived.Output.Column, outputID, operand.Kind)
					}
				}
				quality, err := derivedOutputQuality(derived.Operation)
				if err != nil {
					return nil, nil, fmt.Errorf("derived output %q in document %q: %w", derived.Output.Column, outputID, err)
				}
				if err := add(derived.Output, derived.ConstructionID, dependencies, quality); err != nil {
					return nil, nil, err
				}
			}
		}
		if document.Construction != nil {
			if err := authoredConstructionOutputs(document, columns[outputID]); err != nil {
				return nil, nil, fmt.Errorf("construction outputs for document %q: %w", outputID, err)
			}
		}
	}
	return documents, columns, nil
}

func authoredConstructionOutputs(document authoringv2.Document, authored map[string]authoredOutputColumn) error {
	if document.Construction == nil {
		return nil
	}
	prior := make(map[string]string, len(document.Columns))
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
		prior[column.ColumnID] = column.Column
	}
	for stepIndex, step := range document.Construction.Steps {
		addOutput := func(outputColumnID, constructionID string, inputIDs []string, quality constructedOutputQuality) error {
			if strings.TrimSpace(constructionID) == "" {
				return fmt.Errorf("steps[%d] has no construction identity", stepIndex)
			}
			var output authoringv2.StageColumn
			found := false
			for _, candidate := range step.Outputs {
				if candidate.ID == outputColumnID {
					output, found = candidate, true
					break
				}
			}
			if !found || !requiredConstructionID(output.ID) || strings.TrimSpace(output.Name) == "" || strings.TrimSpace(output.Label) == "" {
				return fmt.Errorf("step %q generated output %q is missing a declared stable ID, name, or label", step.ID, outputColumnID)
			}
			if _, exists := prior[outputColumnID]; exists {
				return fmt.Errorf("step %q reuses existing output column ID %q", step.ID, outputColumnID)
			}
			if _, duplicate := authored[output.Name]; duplicate {
				return fmt.Errorf("construction output %q is duplicated for document %q", output.Name, document.Output.ID)
			}
			inputColumns, err := constructionInputNames(prior, inputIDs)
			if err != nil {
				return fmt.Errorf("step %q output %q: %w", step.ID, output.Name, err)
			}
			authored[output.Name] = authoredOutputColumn{
				ConstructionID: constructionID, Label: output.Label,
				InputColumns: inputColumns, Quality: quality,
			}
			return nil
		}
		switch step.Operation.Kind {
		case authoringv2.ConstructionOperationDerive:
			if step.Operation.Derive == nil {
				return fmt.Errorf("derive step %q has no operation payload", step.ID)
			}
			derive := step.Operation.Derive
			inputs := make([]string, 0, 2)
			for _, operand := range []authoringv2.ConstructionOperand{derive.Left, derive.Right} {
				switch operand.Kind {
				case authoringv2.ConstructionColumnOperand:
					inputs = append(inputs, operand.ColumnID)
				case authoringv2.ConstructionLiteralOperand:
				default:
					return fmt.Errorf("derive step %q has unsupported operand kind %q", step.ID, operand.Kind)
				}
			}
			quality, err := derivedOutputQuality(string(derive.Operation))
			if err != nil {
				return fmt.Errorf("derive step %q: %w", step.ID, err)
			}
			if err := addOutput(derive.OutputColumnID, derive.ConstructionID, inputs, quality); err != nil {
				return err
			}
		case authoringv2.ConstructionOperationPivot:
			if step.Operation.Pivot == nil {
				return fmt.Errorf("pivot step %q has no operation payload", step.ID)
			}
			pivot := step.Operation.Pivot
			dependencies := append([]string(nil), pivot.GroupKeyIDs...)
			dependencies = append(dependencies, pivot.CategoryColumnID, pivot.ValueColumnID)
			quality, err := pivotOutputQuality(&authoringv2.PivotConstruction{
				DuplicatePolicy: string(pivot.DuplicatePolicy), MissingCellPolicy: string(pivot.MissingCellPolicy),
				UnlistedCategoryPolicy: string(pivot.UnlistedCategoryPolicy),
			})
			if err != nil {
				return fmt.Errorf("pivot step %q: %w", step.ID, err)
			}
			for _, category := range pivot.Categories {
				if err := addOutput(category.OutputColumnID, pivot.ConstructionID, dependencies, quality); err != nil {
					return err
				}
			}
		case authoringv2.ConstructionOperationUnpivot:
			if step.Operation.Unpivot == nil {
				return fmt.Errorf("unpivot step %q has no operation payload", step.ID)
			}
			unpivot := step.Operation.Unpivot
			dependencies := make([]string, 0, len(unpivot.Inputs))
			for _, input := range unpivot.Inputs {
				dependencies = append(dependencies, input.ColumnID)
			}
			quality, err := unpivotOutputQuality(&authoringv2.UnpivotConstruction{NullRowPolicy: string(unpivot.NullRowPolicy)})
			if err != nil {
				return fmt.Errorf("unpivot step %q: %w", step.ID, err)
			}
			for _, columnID := range []string{unpivot.KeyOutputColumnID, unpivot.ValueOutputColumnID} {
				if err := addOutput(columnID, unpivot.ConstructionID, dependencies, quality); err != nil {
					return err
				}
			}
		case authoringv2.ConstructionOperationFilter:
			if step.Operation.Filter == nil {
				return fmt.Errorf("filter step %q has no operation payload", step.ID)
			}
		default:
			return fmt.Errorf("step %q has unsupported operation kind %q", step.ID, step.Operation.Kind)
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
		prior = next
	}
	return nil
}

func constructionInputNames(schema map[string]string, ids []string) ([]string, error) {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		name, exists := schema[id]
		if !exists {
			return nil, fmt.Errorf("input column ID %q is absent from the prior stage", id)
		}
		names = append(names, name)
	}
	return uniqueColumnsInOrder(names)
}

func requiredConstructionID(value string) bool {
	return strings.TrimSpace(value) != "" && value == strings.TrimSpace(value)
}

func derivedOutputQuality(operation string) (constructedOutputQuality, error) {
	operation = strings.ToUpper(strings.TrimSpace(operation))
	switch operation {
	case "ADD", "SUBTRACT", "MULTIPLY", "DIVIDE":
		return constructedOutputQuality{
			Lossless: false, StructuralSuitability: "requires-review",
			LossReasons: []string{"TABLE_SHAPE_DERIVED_" + operation + "_NON_LOSSLESS"},
		}, nil
	default:
		return constructedOutputQuality{}, fmt.Errorf("unsupported derived operation %q", operation)
	}
}

func pivotOutputQuality(pivot *authoringv2.PivotConstruction) (constructedOutputQuality, error) {
	quality := constructedOutputQuality{
		Lossless: false, StructuralSuitability: "requires-review",
		LossReasons: []string{"TABLE_SHAPE_PIVOT_BASE_COLUMNS_DROPPED"},
	}
	switch pivot.DuplicatePolicy {
	case "ERROR":
	case "SUM", "MIN", "MAX":
		quality.Lossless = false
		quality.LossReasons = append(quality.LossReasons, "TABLE_SHAPE_PIVOT_DUPLICATES_AGGREGATED")
	default:
		return constructedOutputQuality{}, fmt.Errorf("unsupported duplicate policy %q", pivot.DuplicatePolicy)
	}
	switch pivot.MissingCellPolicy {
	case "NULL", "ERROR":
	default:
		return constructedOutputQuality{}, fmt.Errorf("unsupported missing-cell policy %q", pivot.MissingCellPolicy)
	}
	switch pivot.UnlistedCategoryPolicy {
	case "ERROR":
	case "EXCLUDE_WITH_EVIDENCE":
		quality.Lossless = false
		quality.LossReasons = append(quality.LossReasons, "TABLE_SHAPE_PIVOT_UNLISTED_CATEGORIES_EXCLUDED")
	default:
		return constructedOutputQuality{}, fmt.Errorf("unsupported unlisted-category policy %q", pivot.UnlistedCategoryPolicy)
	}
	if !quality.Lossless {
		quality.StructuralSuitability = "requires-review"
	}
	return quality, nil
}

func unpivotOutputQuality(unpivot *authoringv2.UnpivotConstruction) (constructedOutputQuality, error) {
	quality := constructedOutputQuality{Lossless: true, StructuralSuitability: "scalar"}
	switch unpivot.NullRowPolicy {
	case "PRESERVE":
	case "DROP":
		quality.Lossless = false
		quality.StructuralSuitability = "requires-review"
		quality.LossReasons = append(quality.LossReasons, "TABLE_SHAPE_UNPIVOT_NULL_ROWS_DROPPED")
	default:
		return constructedOutputQuality{}, fmt.Errorf("unsupported null-row policy %q", unpivot.NullRowPolicy)
	}
	return quality, nil
}

type constructedColumnProfile struct {
	Shape                 string
	Lossless              bool
	MLReady               bool
	StructuralSuitability string
	LossReasons           []string
	Filterable            bool
	Chartable             bool
}

func constructedOutputProfileFor(quality constructedOutputQuality, column lower.CompiledOutputColumn) (constructedColumnProfile, error) {
	cardinality := expression.Cardinality(column.Cardinality)
	if cardinality != expression.RequiredOne && cardinality != expression.OptionalOne {
		return constructedColumnProfile{}, fmt.Errorf("constructed output cardinality %q is not scalar", column.Cardinality)
	}
	switch expression.ValueKind(column.Kind) {
	case expression.KindBoolean, expression.KindInteger, expression.KindDecimal, expression.KindString,
		expression.KindDate, expression.KindDateTime, expression.KindCode, expression.KindUUID:
	default:
		return constructedColumnProfile{}, fmt.Errorf("constructed output kind %q is not a supported scalar type", column.Kind)
	}
	if quality.StructuralSuitability == "" {
		return constructedColumnProfile{}, fmt.Errorf("constructed output quality policy is missing structural suitability")
	}
	reasons, err := uniqueColumnsInOrder(append(append([]string(nil), quality.LossReasons...), tableShapeMLReadinessUnassessed))
	if err != nil {
		return constructedColumnProfile{}, fmt.Errorf("constructed output quality policy has an invalid reason: %w", err)
	}
	return constructedColumnProfile{
		Shape: "scalar", Lossless: quality.Lossless, MLReady: false,
		StructuralSuitability: quality.StructuralSuitability, LossReasons: reasons,
		Filterable: true, Chartable: true,
	}, nil
}

func resolveAuthoredOutputLineage(constructed map[string]authoredOutputColumn, emitted map[string]explorer.EmittedColumn) (map[string][]string, error) {
	states := make(map[string]uint8, len(constructed))
	resolved := make(map[string][]string, len(constructed))
	var visit func(string) ([]string, error)
	visit = func(column string) ([]string, error) {
		if authored, ok := constructed[column]; ok {
			switch states[column] {
			case 1:
				return nil, fmt.Errorf("constructed dependency cycle includes %q", column)
			case 2:
				return append([]string(nil), resolved[column]...), nil
			}
			states[column] = 1
			roots := make([]string, 0)
			for _, input := range authored.InputColumns {
				lineage, err := visit(input)
				if err != nil {
					return nil, err
				}
				roots = append(roots, lineage...)
			}
			lineage, err := sortedUniqueColumns(roots)
			if err != nil {
				return nil, err
			}
			states[column] = 2
			resolved[column] = lineage
			return append([]string(nil), lineage...), nil
		}
		emission, ok := emitted[column]
		if !ok {
			return nil, fmt.Errorf("missing dependency %q", column)
		}
		roots, err := sortedUniqueColumns(emission.AuthoredColumns)
		if err != nil {
			return nil, fmt.Errorf("base dependency %q: %w", column, err)
		}
		if len(roots) == 0 {
			return nil, fmt.Errorf("base dependency %q has no authored lineage", column)
		}
		return roots, nil
	}

	columns := make([]string, 0, len(constructed))
	for column := range constructed {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	for _, column := range columns {
		if _, err := visit(column); err != nil {
			return nil, fmt.Errorf("constructed output %q: %w", column, err)
		}
	}
	return resolved, nil
}

func uniqueColumnsInOrder(columns []string) ([]string, error) {
	result := make([]string, 0, len(columns))
	seen := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		if strings.TrimSpace(column) == "" {
			return nil, fmt.Errorf("column dependency is empty")
		}
		if _, duplicate := seen[column]; duplicate {
			continue
		}
		seen[column] = struct{}{}
		result = append(result, column)
	}
	return result, nil
}

func sortedUniqueColumns(columns []string) ([]string, error) {
	result, err := uniqueColumnsInOrder(columns)
	if err != nil {
		return nil, err
	}
	sort.Strings(result)
	return result, nil
}

func indexPresentations(values []explorercompilation.PresentationConfig, outputs map[string]lower.CompiledRecipeOutput) (map[string]explorercompilation.PresentationConfig, error) {
	presentations := make(map[string]explorercompilation.PresentationConfig, len(values))
	for _, presentation := range values {
		if _, ok := outputs[presentation.OutputID]; !ok {
			return nil, fmt.Errorf("translated presentation has unknown output identity %q", presentation.OutputID)
		}
		if _, duplicate := presentations[presentation.OutputID]; duplicate {
			return nil, fmt.Errorf("duplicate translated presentation identity %q", presentation.OutputID)
		}
		seen := make(map[string]struct{}, len(presentation.Columns))
		for _, column := range presentation.Columns {
			if strings.TrimSpace(column.PublicColumn) == "" {
				return nil, fmt.Errorf("translated presentation for output %q has a column without identity", presentation.OutputID)
			}
			if _, duplicate := seen[column.PublicColumn]; duplicate {
				return nil, fmt.Errorf("duplicate translated presentation column %q for output %q", column.PublicColumn, presentation.OutputID)
			}
			seen[column.PublicColumn] = struct{}{}
		}
		presentations[presentation.OutputID] = presentation
	}
	return presentations, nil
}

func presentationColumnByName(presentation explorercompilation.PresentationConfig, name string) (explorercompilation.PresentationColumn, bool) {
	for _, column := range presentation.Columns {
		if column.PublicColumn == name {
			return column, true
		}
	}
	return explorercompilation.PresentationColumn{}, false
}

func applyCompiledColumnMetadata(emitted *explorer.EmittedColumn, column lower.CompiledOutputColumn) error {
	if emitted == nil || emitted.PublicColumn != column.Name || strings.TrimSpace(column.Kind) == "" || strings.TrimSpace(column.Cardinality) == "" {
		return fmt.Errorf("compiled public column identity, kind, or cardinality is missing")
	}
	emitted.LogicalType = column.Kind
	emitted.Cardinality = column.Cardinality
	emitted.Nullable = column.Nullable
	emitted.ResultUnit = cloneResultUnit(column.NormalizedUnit)
	return nil
}

func cloneEmittedColumn(value explorer.EmittedColumn) explorer.EmittedColumn {
	value.AuthoredColumns = append([]string(nil), value.AuthoredColumns...)
	value.InputColumns = append([]string(nil), value.InputColumns...)
	value.Coordinates = append([]capability.RepeatedCoordinate(nil), value.Coordinates...)
	value.LossReasons = append([]string(nil), value.LossReasons...)
	if value.UnitNormalization != nil {
		copy := *value.UnitNormalization
		copy.Rules = append([]explorer.PublicUnitRuleIdentity(nil), value.UnitNormalization.Rules...)
		value.UnitNormalization = &copy
	}
	value.ResultUnit = cloneResultUnit(value.ResultUnit)
	return value
}

func cloneResultUnit(value *unit.UnitIdentity) *unit.UnitIdentity {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func publicOutputColumnFromEmission(emitted explorer.EmittedColumn) explorer.PublicOutputColumn {
	return explorer.PublicOutputColumn{
		Column: emitted.PublicColumn, AuthoredColumns: append([]string(nil), emitted.AuthoredColumns...), InputColumns: append([]string(nil), emitted.InputColumns...),
		ConstructionID: emitted.ConstructionID, Label: emitted.Label,
		LogicalType: emitted.LogicalType, Cardinality: emitted.Cardinality, Nullable: emitted.Nullable,
		ResultUnit: cloneResultUnit(emitted.ResultUnit), Shape: emitted.Shape,
		SourceResourceType: emitted.SourceResourceType, SourcePath: emitted.SourcePath,
		ChoiceArm: emitted.ChoiceArm, Coordinates: append([]capability.RepeatedCoordinate(nil), emitted.Coordinates...),
		Lossless: emitted.Lossless, MLReady: emitted.MLReady, StructuralSuitability: emitted.StructuralSuitability,
		LossReasons: append([]string(nil), emitted.LossReasons...), Filterable: emitted.Filterable, Chartable: emitted.Chartable,
		UnitNormalization: cloneOutputUnitNormalization(emitted.UnitNormalization),
		EmissionID:        emitted.EmissionID, PublicColumn: emitted.PublicColumn, CandidateID: emitted.CandidateID,
		OccurrenceID: emitted.OccurrenceID, ProjectionMode: emitted.ProjectionMode,
	}
}

func mergeReconciledContractQuality(contract *explorer.PublicOutputContract, emitted explorer.EmittedColumn) {
	contract.Lossless = contract.Lossless && emitted.Lossless
	contract.MLReady = contract.MLReady && emitted.MLReady
	if emitted.StructuralSuitability == "requires-review" {
		contract.StructuralSuitability = "requires-review"
	} else if contract.StructuralSuitability == "" && emitted.StructuralSuitability != "" {
		contract.StructuralSuitability = emitted.StructuralSuitability
	} else if contract.StructuralSuitability == "scalar" && emitted.StructuralSuitability == "array" {
		contract.StructuralSuitability = "array"
	}
	for _, reason := range emitted.LossReasons {
		if !containsOutputContractString(contract.LossReasons, reason) {
			contract.LossReasons = append(contract.LossReasons, reason)
		}
	}
}

func containsOutputContractString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func cloneOutputUnitNormalization(value *explorer.PublicUnitNormalization) *explorer.PublicUnitNormalization {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Rules = append([]explorer.PublicUnitRuleIdentity(nil), value.Rules...)
	return &copy
}

func reconcileIdentityMappings(values []explorer.IdentityMapping, emittedIDs map[string]map[string]struct{}) ([]explorer.IdentityMapping, error) {
	result := make([]explorer.IdentityMapping, 0, len(values))
	for _, mapping := range values {
		ids, ok := emittedIDs[mapping.OutputID]
		if !ok {
			return nil, fmt.Errorf("identity mapping has missing or unknown output identity %q", mapping.OutputID)
		}
		kept := make([]string, 0, len(mapping.EmissionIDs))
		for _, emissionID := range mapping.EmissionIDs {
			if strings.TrimSpace(emissionID) == "" {
				return nil, fmt.Errorf("identity mapping for output %q contains an empty emission identity", mapping.OutputID)
			}
			if _, ok := ids[emissionID]; ok {
				kept = append(kept, emissionID)
			}
		}
		if len(kept) == 0 {
			continue
		}
		mapping.EmissionIDs = kept
		result = append(result, mapping)
	}
	return result, nil
}

func constructedEmissionID(constructionID, column string) string {
	return "construction:" + constructionID + ":" + column
}
