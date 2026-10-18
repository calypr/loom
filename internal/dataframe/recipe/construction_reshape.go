package recipe

import (
	"fmt"
	"strings"
)

func validateConstructionGroup(group ConstructionGroup, input, output map[string]StageColumn, path string, constructionIDs map[string]bool, hasRowValues bool) error {
	if err := validateConstructionID(group.ConstructionID, path+".group.constructionId", constructionIDs); err != nil {
		return err
	}
	if !group.MissingKeyPolicy.Valid() {
		return validationError("invalid_group_missing_key_policy", path+".group.missingKeyPolicy", "missing-key policy is unsupported")
	}
	if len(group.Keys) == 0 && len(group.Aggregates) == 0 && !hasRowValues {
		return fmt.Errorf("%s group requires at least one key or aggregate", path)
	}
	expected := make(map[string]bool, len(group.Keys)+len(group.Aggregates))
	keyInputs := make(map[string]bool, len(group.Keys))
	fieldSources := make(map[string]bool, len(group.Keys))
	semanticKeys := 0
	codingIdentityKeys := 0
	for index, key := range group.Keys {
		if key.FieldSource != nil {
			if key.InputColumnID != "" || key.SemanticCategory != nil {
				return fmt.Errorf("%s group keys[%d] must choose exactly one source representation", path, index)
			}
			source := key.FieldSource
			if source.Kind != "BINDING" || strings.TrimSpace(source.SourceStageID) == "" || source.SourceStageID != strings.TrimSpace(source.SourceStageID) ||
				strings.TrimSpace(source.SourceShapeID) == "" || source.SourceShapeID != strings.TrimSpace(source.SourceShapeID) ||
				(source.SourceRole != "ROOT" && source.SourceRole != "OWNER" && source.SourceRole != "ANCESTOR" && source.SourceRole != "CURRENT_ITEM") ||
				strings.TrimSpace(source.OccurrenceID) == "" || source.OccurrenceID != strings.TrimSpace(source.OccurrenceID) ||
				strings.TrimSpace(source.OwnerResourceType) == "" || source.OwnerResourceType != strings.TrimSpace(source.OwnerResourceType) ||
				strings.TrimSpace(source.ResourceType) == "" || source.ResourceType != strings.TrimSpace(source.ResourceType) ||
				strings.TrimSpace(source.CanonicalPath) == "" || source.CanonicalPath != strings.TrimSpace(source.CanonicalPath) {
				return fmt.Errorf("%s group keys[%d].fieldSource is not a resolved BINDING", path, index)
			}
			switch source.ValueKind {
			case "SCALAR":
				if strings.TrimSpace(source.CandidateID) == "" || source.CandidateID != strings.TrimSpace(source.CandidateID) {
					return fmt.Errorf("%s group keys[%d].fieldSource SCALAR requires candidateId", path, index)
				}
				if source.SourceRole == "ANCESTOR" {
					if strings.TrimSpace(source.BoundarySelector) == "" || source.BoundarySelector != strings.TrimSpace(source.BoundarySelector) {
						return fmt.Errorf("%s group keys[%d].fieldSource ANCESTOR requires an exact boundarySelector", path, index)
					}
				} else if source.BoundarySelector != "" {
					return fmt.Errorf("%s group keys[%d].fieldSource boundarySelector is only valid for ANCESTOR", path, index)
				}
			case "CODING_IDENTITY":
				codingIdentityKeys++
				if source.CandidateID != "" || source.SourceRole != "CURRENT_ITEM" || source.ResourceType != "Coding" || source.BoundarySelector != "" {
					return fmt.Errorf("%s group keys[%d].fieldSource CODING_IDENTITY must bind the current Coding item without candidateId", path, index)
				}
			default:
				return fmt.Errorf("%s group keys[%d].fieldSource has unsupported valueKind %q", path, index, source.ValueKind)
			}
			sourceKey := strings.Join([]string{source.SourceStageID, source.SourceShapeID, source.SourceRole, source.OccurrenceID, source.OwnerResourceType, source.ResourceType, source.CandidateID, source.CanonicalPath, source.BoundarySelector}, "\x00")
			if fieldSources[sourceKey] {
				return fmt.Errorf("%s group keys[%d].fieldSource is duplicated", path, index)
			}
			fieldSources[sourceKey] = true
		} else if key.SemanticCategory == nil {
			if key.InputColumnID == "" || input[key.InputColumnID].ID == "" || keyInputs[key.InputColumnID] {
				return fmt.Errorf("%s group keys[%d].inputColumnId is missing or duplicated", path, index)
			}
			keyInputs[key.InputColumnID] = true
		} else {
			semanticKeys++
			if key.InputColumnID != "" || key.FieldSource != nil {
				return fmt.Errorf("%s group keys[%d] must choose exactly one source representation", path, index)
			}
			category := key.SemanticCategory
			if category.Kind != "BINDING" || strings.TrimSpace(category.BindingID) == "" ||
				strings.TrimSpace(category.RuleVersion) == "" || strings.TrimSpace(category.FamilyID) == "" ||
				(category.SourceKind != "file" && category.SourceKind != "retained_vertex") ||
				strings.TrimSpace(category.System) == "" || category.System != strings.TrimSpace(category.System) ||
				category.Version != strings.TrimSpace(category.Version) || strings.TrimSpace(category.ResourceType) == "" ||
				strings.TrimSpace(category.CodingPath) == "" || category.CodingPath != strings.TrimSpace(category.CodingPath) {
				return fmt.Errorf("%s group keys[%d].semanticCategory is not a resolved BINDING", path, index)
			}
			if key.OutputColumnID == "" || output[key.OutputColumnID].ID == "" {
				return fmt.Errorf("%s group keys[%d].outputColumnId is missing", path, index)
			}
			declared := output[key.OutputColumnID]
			if declared.Type != "" && declared.Type != "INFER" && !strings.EqualFold(declared.Type, "string") {
				return fmt.Errorf("%s group semantic category output %q must be a string", path, key.OutputColumnID)
			}
		}
		if key.OutputColumnID == "" || expected[key.OutputColumnID] {
			return fmt.Errorf("%s group keys[%d].outputColumnId is empty or duplicated", path, index)
		}
		expected[key.OutputColumnID] = true
	}
	if semanticKeys > 1 || codingIdentityKeys > 1 || semanticKeys > 0 && codingIdentityKeys > 0 {
		return fmt.Errorf("%s group supports at most one semantic category key", path)
	}
	for index, aggregate := range group.Aggregates {
		if aggregate.OutputColumnID == "" || expected[aggregate.OutputColumnID] {
			return fmt.Errorf("%s group aggregates[%d].outputColumnId is empty or duplicated", path, index)
		}
		switch aggregate.Operation {
		case ConstructionGroupCountRows:
			if aggregate.InputColumnID != "" {
				return fmt.Errorf("%s group aggregates[%d] COUNT_ROWS does not accept inputColumnId", path, index)
			}
		case ConstructionGroupCountNonNull, ConstructionGroupCountDistinct, ConstructionGroupSum, ConstructionGroupMin, ConstructionGroupMax, ConstructionGroupMean:
			if aggregate.InputColumnID == "" || input[aggregate.InputColumnID].ID == "" {
				return fmt.Errorf("%s group aggregates[%d] %s requires an input column", path, index, aggregate.Operation)
			}
		default:
			return fmt.Errorf("%s group aggregates[%d] has unsupported operation %q", path, index, aggregate.Operation)
		}
		expected[aggregate.OutputColumnID] = true
	}
	return requireExactStageOutputIDs(expected, output, path)
}

func validateConstructionCodedGroup(group ConstructionCodedGroup, input, output map[string]StageColumn, path string, constructionIDs map[string]bool) error {
	if err := validateConstructionID(group.ConstructionID, path+".codedGroup.constructionId", constructionIDs); err != nil {
		return err
	}
	if group.Source.OccurrenceID != "base" || strings.TrimSpace(group.Source.ResourceType) == "" ||
		strings.TrimSpace(group.Source.CodingPath) == "" || group.Source.FHIRType != "Coding" ||
		group.Source.Cardinality != "MANY" || group.Source.Shape != "ARRAY" || len(group.Source.Route) != 0 {
		return fmt.Errorf("%s codedGroup source must identify a root repeated Coding path with an empty route", path)
	}
	if !group.MissingKeyPolicy.Valid() {
		return validationError("invalid_coded_group_missing_key_policy", path+".codedGroup.missingKeyPolicy", "missing-key policy is unsupported")
	}
	ids := []string{group.SystemOutputColumnID, group.VersionOutputColumnID, group.CodeOutputColumnID, group.DistinctSourceCountOutputColumnID}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] || input[id].ID != "" {
			return fmt.Errorf("%s codedGroup requires four distinct output IDs that do not collide with the input schema", path)
		}
		seen[id] = true
	}
	if len(output) != 4 {
		return fmt.Errorf("%s codedGroup output schema must contain exactly system, version, code, and distinct source count", path)
	}
	for _, id := range ids[:3] {
		column := output[id]
		if column.ID == "" || !strings.EqualFold(column.Type, "string") || !column.Nullable {
			return fmt.Errorf("%s codedGroup key output %q must be a nullable string", path, id)
		}
	}
	count := output[group.DistinctSourceCountOutputColumnID]
	if count.ID == "" || !strings.EqualFold(count.Type, "integer") || count.Nullable {
		return fmt.Errorf("%s codedGroup distinct source count output must be a required integer", path)
	}
	expected := map[string]bool{
		group.SystemOutputColumnID:              true,
		group.VersionOutputColumnID:             true,
		group.CodeOutputColumnID:                true,
		group.DistinctSourceCountOutputColumnID: true,
	}
	return requireExactStageOutputIDs(expected, output, path)
}

func validateConstructionCodedPivot(pivot ConstructionCodedPivot, input, output map[string]StageColumn, path string, constructionIDs map[string]bool) error {
	if err := validateConstructionID(pivot.ConstructionID, path+".codedPivot.constructionId", constructionIDs); err != nil {
		return err
	}
	source := pivot.Source
	for name, value := range map[string]string{
		"bindingId": source.BindingID, "resourceType": source.ResourceType, "sourcePath": source.SourcePath,
		"keyPath": source.KeyPath, "valuePath": source.ValuePath, "logicalType": source.LogicalType,
		"ruleVersion": source.RuleVersion, "candidateId": source.CandidateID, "nodeId": source.NodeID, "fieldPath": source.FieldPath,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("%s codedPivot source %s is empty or untrimmed", path, name)
		}
	}
	if source.SchemaVersion <= 0 || len(source.Route) != 0 {
		return fmt.Errorf("%s codedPivot requires a positive schemaVersion and an empty direct-root route", path)
	}
	for index, fallback := range source.ValueFallbacks {
		if strings.TrimSpace(fallback) == "" || fallback != strings.TrimSpace(fallback) {
			return fmt.Errorf("%s codedPivot source valueFallbacks[%d] is empty or untrimmed", path, index)
		}
	}
	if source.FallbackOnBlankString && (source.LogicalType != "string" || len(source.ValueFallbacks) == 0) {
		return fmt.Errorf("%s codedPivot blank-string fallback requires string values and at least one fallback selector", path)
	}
	if len(pivot.Categories) == 0 || len(pivot.Categories) > 50 {
		return fmt.Errorf("%s codedPivot requires 1..50 categories", path)
	}
	if pivot.DuplicatePolicy != PivotDuplicateError && pivot.DuplicatePolicy != PivotDuplicateSum &&
		pivot.DuplicatePolicy != PivotDuplicateMin && pivot.DuplicatePolicy != PivotDuplicateMax {
		return fmt.Errorf("%s codedPivot duplicatePolicy is unsupported", path)
	}
	if pivot.MissingCellPolicy != PivotMissingCellNull && pivot.MissingCellPolicy != PivotMissingCellError {
		return fmt.Errorf("%s codedPivot missingCellPolicy is unsupported", path)
	}
	identities, expected := map[string]bool{}, map[string]bool{}
	for index, category := range pivot.Categories {
		if strings.TrimSpace(category.System) == "" || category.System != strings.TrimSpace(category.System) ||
			strings.TrimSpace(category.Code) == "" || category.Code != strings.TrimSpace(category.Code) {
			return fmt.Errorf("%s codedPivot categories[%d] requires exact system and code values", path, index)
		}
		identity := category.System + "\x00" + category.Code
		if identities[identity] {
			return fmt.Errorf("%s codedPivot categories[%d] duplicates a system/code key", path, index)
		}
		identities[identity] = true
		id := category.OutputColumnID
		if strings.TrimSpace(id) == "" || id != strings.TrimSpace(id) || input[id].ID != "" || expected[id] {
			return fmt.Errorf("%s codedPivot categories[%d].outputColumnId is empty, untrimmed, or collides", path, index)
		}
		expected[id] = true
	}
	return requireExactStageOutputIDs(expected, output, path)
}

func validateConstructionExpand(expand ConstructionExpand, input, output map[string]StageColumn, path string, constructionIDs map[string]bool) error {
	if err := validateConstructionID(expand.ConstructionID, path+".expand.constructionId", constructionIDs); err != nil {
		return err
	}
	if expand.InputColumnID == "" || input[expand.InputColumnID].ID == "" {
		return fmt.Errorf("%s expand inputColumnId is missing from input schema", path)
	}
	if expand.OutputColumnID == "" {
		return fmt.Errorf("%s expand outputColumnId is required", path)
	}
	if !expand.EmptyPolicy.Valid() {
		return fmt.Errorf("%s expand emptyPolicy is unsupported", path)
	}
	if expand.OrdinalColumnID != "" && expand.OrdinalColumnID == expand.OutputColumnID {
		return fmt.Errorf("%s expand ordinal and item output IDs must differ", path)
	}
	expected := make(map[string]bool, len(input)+2)
	for id := range input {
		if id != expand.InputColumnID {
			expected[id] = true
		}
	}
	if input[expand.OutputColumnID].ID != "" && expand.OutputColumnID != expand.InputColumnID {
		return fmt.Errorf("%s expand outputColumnId collides with a preserved input column", path)
	}
	expected[expand.OutputColumnID] = true
	if expand.OrdinalColumnID != "" {
		if input[expand.OrdinalColumnID].ID != "" {
			return fmt.Errorf("%s expand ordinalColumnId collides with an input column", path)
		}
		expected[expand.OrdinalColumnID] = true
	}
	return requireExactStageOutputIDs(expected, output, path)
}
