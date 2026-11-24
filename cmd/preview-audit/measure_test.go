package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDigestRowsPreservesMultiplicityAndOptionalOrder(t *testing.T) {
	digest := func(ordered bool, rows ...string) string {
		t.Helper()
		input := make([]json.RawMessage, len(rows))
		for i, row := range rows {
			input[i] = json.RawMessage(row)
		}
		value, err := digestRows(input, ordered)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first := `{"a":1,"b":2}`
	reorderedKeys := `{"b":2,"a":1}`
	other := `{"a":3,"b":4}`
	if digest(false, first, other, first) != digest(false, other, reorderedKeys, first) {
		t.Fatal("unordered comparison changed with row or object key order")
	}
	if digest(false, first, other, first) == digest(false, other, first) {
		t.Fatal("unordered comparison lost row multiplicity")
	}
	if digest(true, first, other) == digest(true, other, reorderedKeys) {
		t.Fatal("ordered comparison lost row order")
	}
}

func TestCursorRequestOmitsNilBindVars(t *testing.T) {
	data, err := json.Marshal(cursorRequest{query: query{Query: "RETURN 1"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "bindVars") {
		t.Fatalf("Arango rejects null bind variables: %s", data)
	}
}
