package lifecycle

import (
	"reflect"

	"github.com/calypr/loom/internal/explorer/capability"
)

func semanticChoiceKey(conceptID, bindingID string) string {
	return bindingID + "\x00" + conceptID
}

func semanticChoiceFieldOptionsMatch(semanticChoice, fieldChoice capability.ConstructionChoice) bool {
	fieldOptions := make([]capability.ConstructionChoiceOption, 0, len(semanticChoice.Options))
	for _, option := range semanticChoice.Options {
		if option.Form != capability.ConstructionChoiceOwnerRecords {
			fieldOptions = append(fieldOptions, option)
		}
	}
	return reflect.DeepEqual(fieldOptions, fieldChoice.Options)
}

func uniqueCapabilityCandidate(snapshot capability.Snapshot, candidateID string) (capability.Candidate, bool) {
	var match capability.Candidate
	found := false
	for _, candidate := range snapshot.Candidates {
		if candidate.ID != candidateID {
			continue
		}
		if found {
			return capability.Candidate{}, false
		}
		match, found = candidate, true
	}
	if found {
		return match, true
	}
	return resolveSchemaFieldCandidate(snapshot, candidateID)
}
