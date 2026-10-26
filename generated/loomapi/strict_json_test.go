package loomapi

import (
	"encoding/json"
	"testing"
)

func TestApplyCommandsRequestRejectsUnknownFieldsAtGeneratedBoundary(t *testing.T) {
	for name, raw := range map[string]string{
		"envelope":    `{"commandId":"cmd","semanticsVersion":4,"snapshotToken":"token","expectedDraftVersion":0,"commands":[],"unknown":true}`,
		"source":      `{"commandId":"cmd","semanticsVersion":4,"snapshotToken":"token","expectedDraftVersion":0,"commands":[{"type":"ADD_COLUMN_SOURCE","outputId":"out","occurrenceId":"base","source":{"kind":"field","field":{"path":"id","unknown":true}}}]}`,
		"contributor": `{"commandId":"cmd","semanticsVersion":4,"snapshotToken":"token","expectedDraftVersion":0,"commands":[{"type":"SET_COLUMN_CONTRIBUTOR","outputId":"out","column":"count","contributor":{"candidateId":"status","operator":"EQUALS","unknown":true,"value":{"kind":"STRING","string":"final"}}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var request ApplyCommandsRequest
			if err := json.Unmarshal([]byte(raw), &request); err == nil {
				t.Fatal("unknown field was accepted")
			}
		})
	}
}
