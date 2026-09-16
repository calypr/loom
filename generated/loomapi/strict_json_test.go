package loomapi

import (
	"encoding/json"
	"testing"
)

func TestApplyCommandsRequestRejectsUnknownFieldsAtGeneratedBoundary(t *testing.T) {
	for name, raw := range map[string]string{
		"envelope": `{"commandId":"cmd","semanticsVersion":3,"snapshotToken":"token","expectedDraftVersion":0,"commands":[],"unknown":true}`,
		"source":   `{"commandId":"cmd","semanticsVersion":3,"snapshotToken":"token","expectedDraftVersion":0,"commands":[{"type":"ADD_COLUMN_SOURCE","outputId":"out","occurrenceId":"base","source":{"kind":"field","field":{"path":"id","unknown":true}}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var request ApplyCommandsRequest
			if err := json.Unmarshal([]byte(raw), &request); err == nil {
				t.Fatal("unknown field was accepted")
			}
		})
	}
}
