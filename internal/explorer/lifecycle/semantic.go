package lifecycle

import (
	"reflect"

	"github.com/calypr/loom/internal/explorer/capability"
)

func semanticChoiceFieldOptionsMatch(semanticChoice, fieldChoice capability.ConstructionChoice) bool {
	fieldOptions := make([]capability.ConstructionChoiceOption, 0, len(semanticChoice.Options))
	for _, option := range semanticChoice.Options {
		if option.Form != capability.ConstructionChoiceOwnerRecords {
			fieldOptions = append(fieldOptions, option)
		}
	}
	return reflect.DeepEqual(fieldOptions, fieldChoice.Options)
}
