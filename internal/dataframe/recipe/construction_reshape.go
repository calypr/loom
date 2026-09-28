package recipe

import (
	"fmt"
	"strings"
)

func validateConstructionGroup(group ConstructionGroup, input, output map[string]StageColumn, path string, constructionIDs map[string]bool) error {
	if err := validateConstructionID(group.ConstructionID, path+".group.constructionId", constructionIDs); err != nil {
		return err
	}
	if !group.MissingKeyPolicy.Valid() {
		return validationError("invalid_group_missing_key_policy", path+".group.missingKeyPolicy", "missing-key policy is unsupported")
	}
	if len(group.Keys) == 0 && len(group.Aggregates) == 0 {
		return fmt.Errorf("%s group requires at least one key or aggregate", path)
	}
	expected := make(map[string]bool, len(group.Keys)+len(group.Aggregates))
	keyInputs := make(map[string]bool, len(group.Keys))
	for index, key := range group.Keys {
		if key.InputColumnID == "" || input[key.InputColumnID].ID == "" || keyInputs[key.InputColumnID] {
			return fmt.Errorf("%s group keys[%d].inputColumnId is missing or duplicated", path, index)
		}
		if key.OutputColumnID == "" || expected[key.OutputColumnID] {
			return fmt.Errorf("%s group keys[%d].outputColumnId is empty or duplicated", path, index)
		}
		keyInputs[key.InputColumnID] = true
		expected[key.OutputColumnID] = true
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
