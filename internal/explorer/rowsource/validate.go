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
	return ValidateUniqueSourceSelectionPairs(selections, "deriveSources", func(selection ConstructionDeriveSourceSelection) string { return selection.ChoiceID }, func(selection ConstructionDeriveSourceSelection) string { return selection.ColumnID }, "deriveSources contains duplicate choiceId or columnId")
}

func ValidateExpandSourceSelections(selections []ConstructionExpandSourceSelection) error {
	return ValidateUniqueSourceSelectionPairs(selections, "expandSources", func(selection ConstructionExpandSourceSelection) string { return selection.ChoiceID }, func(selection ConstructionExpandSourceSelection) string { return selection.ColumnID }, "expandSources contains duplicate choiceId or columnId")
}

func ValidateFilterSourceSelections(selections []ConstructionFilterSourceSelection) error {
	return ValidateUniqueSourceSelectionPairs(selections, "filterSources", func(selection ConstructionFilterSourceSelection) string { return selection.ChoiceID }, func(selection ConstructionFilterSourceSelection) string { return selection.ColumnID }, "filterSources contains duplicate choiceId or columnId")
}

func ValidateUnpivotSourceSelections(selections []ConstructionUnpivotSourceSelection) error {
	return ValidateUniqueSourceSelectionPairs(selections, "unpivotSources", func(selection ConstructionUnpivotSourceSelection) string { return selection.ChoiceID }, func(selection ConstructionUnpivotSourceSelection) string { return selection.ColumnID }, "unpivotSources contains duplicate choiceId or columnId")
}

func ValidateUniqueSourceSelectionPairs[T any](selections []T, field string, choiceID func(T) string, columnID func(T) string, duplicateError string) error {
	seenChoices := make(map[string]bool, len(selections))
	seenColumns := make(map[string]bool, len(selections))
	for index, selection := range selections {
		choice := choiceID(selection)
		column := columnID(selection)
		if err := support.RequireExactIdentity(choice, fmt.Sprintf("%s[%d].choiceId", field, index)); err != nil {
			return err
		}
		if err := support.RequireExactIdentity(column, fmt.Sprintf("%s[%d].columnId", field, index)); err != nil {
			return err
		}
		if seenChoices[choice] || seenColumns[column] {
			return fmt.Errorf("%s", duplicateError)
		}
		seenChoices[choice], seenColumns[column] = true, true
	}
	return nil
}
