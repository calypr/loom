package rowsource

import (
	"fmt"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"
)

const maxDeriveSourceSelections = 2

func ValidateDeriveSourceSelections(selections []ConstructionDeriveSourceSelection) error {
	if len(selections) > maxDeriveSourceSelections {
		return fmt.Errorf("deriveSources accepts at most %d operand selections", maxDeriveSourceSelections)
	}
	seenChoices := make(map[string]bool, len(selections))
	seenColumns := make(map[string]bool, len(selections))
	for index, selection := range selections {
		if err := support.RequireExactIdentity(selection.ChoiceID, fmt.Sprintf("deriveSources[%d].choiceId", index)); err != nil {
			return err
		}
		if err := support.RequireExactIdentity(selection.ColumnID, fmt.Sprintf("deriveSources[%d].columnId", index)); err != nil {
			return err
		}
		if seenChoices[selection.ChoiceID] || seenColumns[selection.ColumnID] {
			return fmt.Errorf("deriveSources contains duplicate choiceId or columnId")
		}
		seenChoices[selection.ChoiceID], seenColumns[selection.ColumnID] = true, true
	}
	return nil
}

func ValidateExpandSourceSelections(selections []ConstructionExpandSourceSelection) error {
	seenChoices := make(map[string]bool, len(selections))
	seenColumns := make(map[string]bool, len(selections))
	for index, selection := range selections {
		if err := support.RequireExactIdentity(selection.ChoiceID, fmt.Sprintf("expandSources[%d].choiceId", index)); err != nil {
			return err
		}
		if err := support.RequireExactIdentity(selection.ColumnID, fmt.Sprintf("expandSources[%d].columnId", index)); err != nil {
			return err
		}
		if seenChoices[selection.ChoiceID] || seenColumns[selection.ColumnID] {
			return fmt.Errorf("expandSources contains duplicate choiceId or columnId")
		}
		seenChoices[selection.ChoiceID], seenColumns[selection.ColumnID] = true, true
	}
	return nil
}

func ValidateFilterSourceSelections(selections []ConstructionFilterSourceSelection) error {
	seenChoices := make(map[string]bool, len(selections))
	seenColumns := make(map[string]bool, len(selections))
	for index, selection := range selections {
		if err := support.RequireExactIdentity(selection.ChoiceID, fmt.Sprintf("filterSources[%d].choiceId", index)); err != nil {
			return err
		}
		if err := support.RequireExactIdentity(selection.ColumnID, fmt.Sprintf("filterSources[%d].columnId", index)); err != nil {
			return err
		}
		if seenChoices[selection.ChoiceID] || seenColumns[selection.ColumnID] {
			return fmt.Errorf("filterSources contains duplicate choiceId or columnId")
		}
		seenChoices[selection.ChoiceID], seenColumns[selection.ColumnID] = true, true
	}
	return nil
}

func ValidateUnpivotSourceSelections(selections []ConstructionUnpivotSourceSelection) error {
	seenChoices := make(map[string]bool, len(selections))
	seenColumns := make(map[string]bool, len(selections))
	for index, selection := range selections {
		if err := support.RequireExactIdentity(selection.ChoiceID, fmt.Sprintf("unpivotSources[%d].choiceId", index)); err != nil {
			return err
		}
		if err := support.RequireExactIdentity(selection.ColumnID, fmt.Sprintf("unpivotSources[%d].columnId", index)); err != nil {
			return err
		}
		if seenChoices[selection.ChoiceID] || seenColumns[selection.ColumnID] {
			return fmt.Errorf("unpivotSources contains duplicate choiceId or columnId")
		}
		seenChoices[selection.ChoiceID], seenColumns[selection.ColumnID] = true, true
	}
	return nil
}
