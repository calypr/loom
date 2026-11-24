package receipt

import (
	"fmt"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

type AuthoredOutputColumn struct {
	ConstructionID          string
	Label                   string
	InputColumns            []string
	NodeID                  string
	CandidateID             string
	OccurrenceID            string
	SourceResourceType      string
	SourcePath              string
	TypedStageOutput        bool
	PriorStageSelfInput     bool
	PreservesSourceIdentity bool
	ExternalInputLineage    bool
	Quality                 ConstructedOutputQuality
}

type groupFieldSourceOutputIdentity struct {
	CandidateID        string
	OccurrenceID       string
	SourceResourceType string
	SourcePath         string
}

func groupFieldSourceOutputIdentityForReconcile(
	binding *ws.ConstructionGroupFieldSource,
) (groupFieldSourceOutputIdentity, bool) {
	if binding == nil || binding.Kind != "BINDING" || binding.SourceStageID == "" || binding.SourceShapeID == "" {
		return groupFieldSourceOutputIdentity{}, false
	}
	if err := binding.Validate(); err != nil {
		return groupFieldSourceOutputIdentity{}, false
	}
	identity := groupFieldSourceOutputIdentity{}
	switch {
	case binding.Input != nil && binding.Input.Kind == "CURRENT_STAGE" && binding.Input.CurrentStage != nil:
		current := binding.Input.CurrentStage
		identity.CandidateID = current.CandidateID
		identity.OccurrenceID = current.OccurrenceID
		identity.SourceResourceType = current.ResourceType
		identity.SourcePath = current.CanonicalPath
		if binding.ValueKind == "CODING_IDENTITY" {
			identity.CandidateID = ""
			identity.SourceResourceType = current.OwnerResourceType
		}
	case binding.Input != nil && binding.Input.Kind == "RELATED_OCCURRENCE" && binding.Input.RelatedOccurrence != nil:
		related := binding.Input.RelatedOccurrence
		identity.CandidateID = related.CandidateID
		identity.OccurrenceID = related.TargetOccurrenceID
		identity.SourceResourceType = related.TargetResourceType
		identity.SourcePath = related.CanonicalPath
		if binding.ValueKind == "CODING_IDENTITY" {
			identity.CandidateID = ""
			identity.SourcePath = related.CodingSelector
		}
	case binding.Input == nil:

		identity.CandidateID = binding.CandidateID
		identity.OccurrenceID = binding.OccurrenceID
		identity.SourceResourceType = binding.ResourceType
		identity.SourcePath = binding.CanonicalPath
		if binding.ValueKind == "CODING_IDENTITY" {
			identity.CandidateID = ""
			identity.SourceResourceType = binding.OwnerResourceType
			if binding.OwnerOccurrenceID != "" {
				identity.OccurrenceID = binding.OwnerOccurrenceID
			}
		}
	default:
		return groupFieldSourceOutputIdentity{}, false
	}
	if identity.OccurrenceID == "" || identity.SourceResourceType == "" || identity.SourcePath == "" ||
		(binding.ValueKind == "SCALAR" && identity.CandidateID == "") ||
		(binding.ValueKind != "SCALAR" && binding.ValueKind != "CODING_IDENTITY") {
		return groupFieldSourceOutputIdentity{}, false
	}
	return identity, true
}

type ConstructedOutputQuality struct {
	Lossless              bool
	Shape                 string
	StructuralSuitability string
	LossReasons           []string
}

const TableShapeMLReadinessUnassessed = "TABLE_SHAPE_ML_READINESS_UNASSESSED"

func ReconcileFinalOutputMetadataWithCombineInputs(
	translated explorercompilation.WorkspaceResult,
	resolved dataframeexecution.Resolved,
	combineInputs map[string]map[int]facade.ResolvedClickHouseTable,
	rowSourceSnapshot capability.Snapshot,
) (explorercompilation.WorkspaceResult, error) {
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
	if err := (receiptmodel.PublicOutputContracts{Outputs: translated.OutputContracts}).ValidateAgainst(translated.Bundle, translated.EmittedColumns); err != nil {
		return explorercompilation.WorkspaceResult{}, fmt.Errorf("translated output contract is inconsistent: %w", err)
	}

	compiledByOutput := make(map[string]facade.CompiledRecipeOutput, len(resolved.Compiled.Outputs))
	for _, output := range resolved.Compiled.Outputs {
		if strings.TrimSpace(output.Name) == "" {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("compiled output identity is required")
		}
		if _, duplicate := compiledByOutput[output.Name]; duplicate {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("duplicate compiled output identity %q", output.Name)
		}
		compiledByOutput[output.Name] = output
	}
	documents, authoredColumns, err := AuthoredOutputColumnsWithCombineInputs(translated.Workspace, compiledByOutput, combineInputs, rowSourceSnapshot)
	if err != nil {
		return explorercompilation.WorkspaceResult{}, err
	}
	if len(documents) != len(resolved.Bundle.Outputs) {
		return explorercompilation.WorkspaceResult{}, fmt.Errorf("workspace document set does not match compiled output set")
	}

	emittedByOutput := make(map[string]map[string]viewer.EmittedColumn, len(translated.Bundle.Outputs))
	emittedIDs := make(map[string]map[string]struct{}, len(translated.Bundle.Outputs))
	for _, output := range resolved.Bundle.Outputs {
		emittedByOutput[output.Name] = make(map[string]viewer.EmittedColumn)
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
	contracts := make(map[string]receiptmodel.PublicOutputContract, len(translated.OutputContracts))
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
	reconciled.EmittedColumns = make([]viewer.EmittedColumn, 0, len(translated.EmittedColumns))
	reconciled.OutputContracts = make([]receiptmodel.PublicOutputContract, 0, len(translated.OutputContracts))
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
		explicitGroupOutputs := make(map[string]viewer.EmittedColumn)
		inheritedExplicitGroupNames := make(map[string]struct{})
		for _, schemaColumn := range compiledOutput.OutputSchema {
			if schemaColumn.Internal || schemaColumn.Identity {
				continue
			}
			groupEmission, isExplicitGroupOutput, err := explicitGroupOutputEmission(documents[recipeOutput.Name], recipeOutput.Name, schemaColumn)
			if err != nil {
				return explorercompilation.WorkspaceResult{}, fmt.Errorf("reconcile explicit group output %q column %q: %w", recipeOutput.Name, schemaColumn.Name, err)
			}
			if isExplicitGroupOutput {
				explicitGroupOutputs[schemaColumn.Name] = groupEmission
				inheritedExplicitGroupNames[schemaColumn.Name] = struct{}{}
			}
		}
		lineage, err := resolveAuthoredOutputLineage(constructed, emittedByOutput[recipeOutput.Name], inheritedExplicitGroupNames)
		if err != nil {
			return explorercompilation.WorkspaceResult{}, fmt.Errorf("resolve authored lineage for output %q: %w", recipeOutput.Name, err)
		}
		finalNames := make(map[string]struct{}, len(compiledOutput.OutputSchema))
		contract := translatedContract
		contract.Columns = make([]receiptmodel.PublicOutputColumn, 0, len(compiledOutput.OutputSchema))
		contract.Lossless = true
		contract.MLReady = true
		contract.StructuralSuitability = ""
		contract.LossReasons = nil
		finalPresentation := explorercompilation.PresentationConfig{OutputID: recipeOutput.Name, Title: presentation.Title, Columns: make([]explorercompilation.PresentationColumn, 0, len(compiledOutput.OutputSchema))}
		emittedStart := len(reconciled.EmittedColumns)
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
			groupEmission, explicitGroupOutput := explicitGroupOutputs[schemaColumn.Name]
			stageShadow := authored && translatedEmission && metadata.TypedStageOutput
			if explicitGroupOutput {
				preservesInheritedGroupIdentity := authored && metadata.PreservesSourceIdentity && metadata.PriorStageSelfInput &&
					len(metadata.InputColumns) == 1 && metadata.InputColumns[0] == schemaColumn.Name
				if (authored && !preservesInheritedGroupIdentity) || translatedEmission {
					return explorercompilation.WorkspaceResult{}, fmt.Errorf("explicit group output %q column %q conflicts with a source or non-identity construction output", recipeOutput.Name, schemaColumn.Name)
				}
				emitted = groupEmission
				if _, collision := emittedIDs[recipeOutput.Name][emitted.EmissionID]; collision {
					return explorercompilation.WorkspaceResult{}, fmt.Errorf("explicit group output %q column %q duplicates translated emission identity %q", recipeOutput.Name, schemaColumn.Name, emitted.EmissionID)
				}
			} else if authored && translatedEmission && !stageShadow {
				return explorercompilation.WorkspaceResult{}, fmt.Errorf("output %q column %q is both a translated emission and a table-shape construction", recipeOutput.Name, schemaColumn.Name)
			} else if translatedEmission && !stageShadow {
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
				sourceEmission := cloneEmittedColumn(emitted)
				emitted = viewer.EmittedColumn{
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
				if stageShadow && metadata.PreservesSourceIdentity {
					emitted.NodeID = sourceEmission.NodeID
					emitted.CandidateID = sourceEmission.CandidateID
					emitted.OccurrenceID = sourceEmission.OccurrenceID
					emitted.SourceResourceType = sourceEmission.SourceResourceType
					emitted.SourcePath = sourceEmission.SourcePath
					emitted.ChoiceArm = sourceEmission.ChoiceArm
					emitted.Coordinates = sourceEmission.Coordinates
					emitted.ProjectionMode = sourceEmission.ProjectionMode
					emitted.UnitNormalization = sourceEmission.UnitNormalization
				}
				if metadata.NodeID != "" {
					emitted.NodeID = metadata.NodeID
				}
				if metadata.CandidateID != "" {
					emitted.CandidateID = metadata.CandidateID
				}
				if metadata.OccurrenceID != "" {
					emitted.OccurrenceID = metadata.OccurrenceID
				}
				if metadata.SourceResourceType != "" {
					emitted.SourceResourceType = metadata.SourceResourceType
				}
				if metadata.SourcePath != "" {
					emitted.SourcePath = metadata.SourcePath
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
				order := maxOrder
				if explicitGroupOutput {
					order = publicOrder
				}
				columnPresentation = explorercompilation.PresentationColumn{
					EmissionID: emitted.EmissionID, PublicColumn: schemaColumn.Name,
					Label: emitted.Label, Visible: true, Order: order,
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
		orderReconciledOutputColumns(&contract, finalPresentation.Columns, reconciled.EmittedColumns[emittedStart:])
		reconciled.OutputContracts = append(reconciled.OutputContracts, contract)
		reconciled.Presentations = append(reconciled.Presentations, finalPresentation)
	}

	reconciled.IdentityMappings, err = reconcileIdentityMappings(translated.IdentityMappings, publicEmissionIDs)
	if err != nil {
		return explorercompilation.WorkspaceResult{}, err
	}
	if err := (receiptmodel.PublicOutputContracts{Outputs: reconciled.OutputContracts}).ValidateAgainst(resolved.Bundle, reconciled.EmittedColumns); err != nil {
		return explorercompilation.WorkspaceResult{}, fmt.Errorf("reconciled output contract is inconsistent: %w", err)
	}
	return reconciled, nil
}
