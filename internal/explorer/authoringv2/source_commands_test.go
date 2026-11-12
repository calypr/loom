package authoringv2

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestImportedSourceCommandsRejectUnresolvedOrInjectedState(t *testing.T) {
	for _, raw := range []string{
		`{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":{"stepId":"fields"}}`,
		`{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":{"stepId":"fields","enabled":false,"expr":{"select":"root.id"}}}`,
		`{"type":"REFRESH_IMPORTED_SOURCE","outputId":"patients","sourceStep":{"stepId":"fields","enabled":false}}`,
		`{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":{"stepId":"fields","enabled":false},"resolvedImportedDocument":{}}`,
	} {
		var command Command
		if err := json.Unmarshal([]byte(raw), &command); err == nil {
			t.Fatalf("accepted injected or incomplete source change: %s", raw)
		}
	}
	command := Command{Type: CommandRefreshImportedSource, OutputID: "patients"}
	workspace := constructionWorkspace(legacyPivotDocument())
	if _, _, err := ApplyCommands(workspace, commandCatalog(), "refresh", []Command{command}); err == nil || !strings.Contains(err.Error(), "lifecycle-resolved") {
		t.Fatalf("unresolved source command error = %v", err)
	}
	request := ApplyCommandsRequest{CommandID: "refresh", SemanticsVersion: CurrentSemanticsVersion, SnapshotToken: "snapshot", Commands: []Command{command}}
	if err := request.Validate(); err == nil {
		t.Fatal("source mutation accepted without exact draft identity")
	}
	request.ExpectedDraftVersion, request.ExpectedDraftDigest = 1, "digest"
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
}
