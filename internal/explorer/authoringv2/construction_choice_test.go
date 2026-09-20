package authoringv2

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
)

func TestConstructionChoiceCommandRejectsClientSourceAndRouteFields(t *testing.T) {
	for _, raw := range []string{
		`{"type":"APPLY_CONSTRUCTION_CHOICE","outputId":"patients","constructionChoice":{"choiceId":"cc1.a.b","form":"VALUE"},"candidateId":"candidate"}`,
		`{"type":"APPLY_CONSTRUCTION_CHOICE","outputId":"patients","constructionChoice":{"choiceId":"cc1.a.b","form":"VALUE"},"projectionMode":"ALL"}`,
		`{"type":"APPLY_CONSTRUCTION_CHOICE","outputId":"patients","constructionChoice":{"choiceId":"cc1.a.b","form":"VALUE"},"occurrenceId":"child"}`,
		`{"type":"APPLY_CONSTRUCTION_CHOICE","outputId":"patients","constructionChoice":{"choiceId":"cc1.a.b","form":"VALUE"},"semanticSelections":[]}`,
		`{"type":"APPLY_CONSTRUCTION_CHOICE","outputId":"patients","constructionChoice":{"choiceId":"cc1.a.b","form":"VALUE","fieldPath":"id"}}`,
	} {
		var command Command
		if err := json.Unmarshal([]byte(raw), &command); err == nil {
			t.Fatalf("accepted source or route fields in %s", raw)
		}
	}
}

func TestConstructionChoiceRequestAcceptsOnlyBoundedChoiceBatches(t *testing.T) {
	selection := &ConstructionChoiceSelection{ChoiceID: "cc1.a.b", Form: capability.ConstructionChoiceValue}
	request := ApplyCommandsRequest{
		CommandID: "choice-batch", SemanticsVersion: CurrentSemanticsVersion, SnapshotToken: "snapshot", ExpectedDraftVersion: 1,
		Commands: []Command{
			{Type: CommandApplyConstructionChoice, OutputID: "patients", ConstructionChoice: selection},
			{Type: CommandApplyConstructionChoice, OutputID: "patients", ConstructionChoice: selection},
		},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid choice batch rejected: %v", err)
	}
	request.Commands = append(request.Commands, Command{Type: CommandCreateTable, Title: "Other", RootNodeID: "patient"})
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "entire request") {
		t.Fatalf("mixed command batch error = %v", err)
	}
	request.Commands = request.Commands[:2]
	request.Commands[0].ConstructionChoice.Form = "INDEXED"
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "form is unsupported") {
		t.Fatalf("unsupported form error = %v", err)
	}
	selection.Form = capability.ConstructionChoiceValue
	request.Commands = make([]Command, 101)
	for index := range request.Commands {
		request.Commands[index] = Command{Type: CommandApplyConstructionChoice, OutputID: "patients", ConstructionChoice: selection}
	}
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "at most 100") {
		t.Fatalf("oversized construction-choice batch error = %v", err)
	}
}
