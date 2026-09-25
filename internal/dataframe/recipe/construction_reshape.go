package recipe

import "fmt"

func validateConstructionGroup(group ConstructionGroup, input, output map[string]StageColumn, path string, constructionIDs map[string]bool) error {
	if err := validateConstructionID(group.ConstructionID, path+".group.constructionId", constructionIDs); err != nil {
		return err
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
