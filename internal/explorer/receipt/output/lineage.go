package output

import (
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

func resolveAuthoredOutputLineage(constructed map[string]AuthoredOutputColumn, emitted map[string]viewer.EmittedColumn, sourceFreeGenerated map[string]struct{}) (map[string][]string, error) {
	states := make(map[string]uint8, len(constructed))
	resolved := make(map[string][]string, len(constructed))
	baseLineage := func(column string) ([]string, error) {
		if _, generated := sourceFreeGenerated[column]; generated {
			return []string{}, nil
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
			if authored.ExternalInputLineage {
				states[column] = 2
				resolved[column] = []string{}
				return []string{}, nil
			}
			roots := make([]string, 0)
			for _, input := range authored.InputColumns {
				if input == column && authored.PriorStageSelfInput {
					lineage, err := baseLineage(input)
					if err != nil {
						return nil, err
					}
					roots = append(roots, lineage...)
					continue
				}
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
		return baseLineage(column)
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

func indexPresentations(values []explorercompilation.PresentationConfig, outputs map[string]facade.CompiledRecipeOutput) (map[string]explorercompilation.PresentationConfig, error) {
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

func applyCompiledColumnMetadata(emitted *viewer.EmittedColumn, column facade.CompiledOutputColumn) error {
	if emitted == nil || emitted.PublicColumn != column.Name || strings.TrimSpace(column.Kind) == "" || strings.TrimSpace(column.Cardinality) == "" {
		return fmt.Errorf("compiled public column identity, kind, or cardinality is missing")
	}
	emitted.LogicalType = column.Kind
	emitted.Cardinality = column.Cardinality
	emitted.Nullable = column.Nullable
	emitted.ResultUnit = cloneResultUnit(column.NormalizedUnit)
	return nil
}

func cloneEmittedColumn(value viewer.EmittedColumn) viewer.EmittedColumn {
	value.AuthoredColumns = append([]string(nil), value.AuthoredColumns...)
	value.InputColumns = append([]string(nil), value.InputColumns...)
	value.Coordinates = append([]capability.RepeatedCoordinate(nil), value.Coordinates...)
	value.LossReasons = append([]string(nil), value.LossReasons...)
	if value.UnitNormalization != nil {
		copy := *value.UnitNormalization
		copy.Rules = append([]viewer.PublicUnitRuleIdentity(nil), value.UnitNormalization.Rules...)
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

func publicOutputColumnFromEmission(emitted viewer.EmittedColumn) receiptmodel.PublicOutputColumn {
	return receiptmodel.PublicOutputColumn{
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

func mergeReconciledContractQuality(contract *receiptmodel.PublicOutputContract, emitted viewer.EmittedColumn) {
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
		if !ContainsOutputContractString(contract.LossReasons, reason) {
			contract.LossReasons = append(contract.LossReasons, reason)
		}
	}
}

func ContainsOutputContractString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func cloneOutputUnitNormalization(value *viewer.PublicUnitNormalization) *viewer.PublicUnitNormalization {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Rules = append([]viewer.PublicUnitRuleIdentity(nil), value.Rules...)
	return &copy
}

func reconcileIdentityMappings(values []receiptmodel.IdentityMapping, emittedIDs map[string]map[string]struct{}) ([]receiptmodel.IdentityMapping, error) {
	result := make([]receiptmodel.IdentityMapping, 0, len(values))
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
