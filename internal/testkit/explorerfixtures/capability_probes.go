package explorerfixtures

import (
	"encoding/json"
	"testing"
)

type deriveSourceProbe struct {
	Supported  *bool                     `json:"supported"`
	StageID    *string                   `json:"stageId"`
	ReasonCode *string                   `json:"reasonCode"`
	Reason     *string                   `json:"reason"`
	Choices    []FilterSourceChoiceProbe `json:"choices"`
}

func DecodeDeriveSourceInput(t *testing.T, body string) (map[string]json.RawMessage, *deriveSourceProbe) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode capabilities envelope: %v body=%s", err, body)
	}
	raw, ok := envelope["deriveSourceInput"]
	if !ok {
		t.Fatalf("capabilities wire response dropped deriveSourceInput: %s", body)
	}
	var input deriveSourceProbe
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	return keys, &input
}

type FilterSourceChoiceProbe struct {
	ChoiceID    *string `json:"choiceId"`
	FieldPath   *string `json:"fieldPath"`
	Label       *string `json:"label"`
	LogicalType *string `json:"logicalType"`
	IsPopulated *bool   `json:"isPopulated"`
	ColumnID    *string `json:"columnId"`
}

type filterSourceProbe struct {
	Supported  *bool                     `json:"supported"`
	StageID    *string                   `json:"stageId"`
	ReasonCode *string                   `json:"reasonCode"`
	Reason     *string                   `json:"reason"`
	Choices    []FilterSourceChoiceProbe `json:"choices"`
}

func DecodeFilterSourceInput(t *testing.T, body string) (map[string]json.RawMessage, *filterSourceProbe) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode capabilities envelope: %v body=%s", err, body)
	}
	raw, ok := envelope["filterSourceInput"]
	if !ok {
		t.Fatalf("capabilities wire response dropped filterSourceInput: %s", body)
	}
	var input filterSourceProbe
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	return keys, &input
}
