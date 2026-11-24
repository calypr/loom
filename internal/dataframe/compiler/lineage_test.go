package compiler

import (
	"encoding/json"
	"testing"
)

func relatedExpandRowID(t *testing.T, parentKey, constructionID, terminalID string) string {
	t.Helper()
	encoded, err := json.Marshal([][]string{{"input", parentKey}, {"construction", constructionID}, {"related", terminalID}})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
