package objectvalue

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestEncodeDecodePreservesRecursiveMembership(t *testing.T) {
	value := []any{
		map[string]any{"unit": nil, "values": []any{111, nil}},
		map[string]any{"values": []any{}},
	}
	text, err := Encode(RepeatedObjects, value)
	if err != nil {
		t.Fatal(err)
	}
	if text != `[{"unit":null,"values":[111,null]},{"values":[]}]` {
		t.Fatalf("normalized JSON = %s", text)
	}
	decoded, err := Decode(RepeatedObjects, text)
	if err != nil {
		t.Fatal(err)
	}
	want := []any{
		map[string]any{"unit": nil, "values": []any{json.Number("111"), nil}},
		map[string]any{"values": []any{}},
	}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("decoded = %#v, want %#v", decoded, want)
	}
	first := decoded.([]any)[0].(map[string]any)
	if _, ok := first["unit"]; !ok {
		t.Fatal("explicit null member was lost")
	}
	if _, ok := decoded.([]any)[1].(map[string]any)["unit"]; ok {
		t.Fatal("absent member was fabricated")
	}
}

func TestEncodeDecodeSupportsUnsafeUnicodeKeysAndEmptyContainers(t *testing.T) {
	value := map[string]any{
		"":           map[string]any{},
		"a.b":        map[string]any{"null-only": nil},
		"\u2028":     []any{},
		"\U0001F600": "unicode",
	}
	text, err := Encode(ScalarObject, value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(ScalarObject, text)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, value) {
		t.Fatalf("decoded = %#v, want %#v", decoded, value)
	}
}

func TestDecodeRejectsMalformedTrailingAndWrongRoots(t *testing.T) {
	for _, test := range []struct {
		name  string
		shape Shape
		text  string
	}{
		{"malformed", ScalarObject, `{"value":`},
		{"trailing", ScalarObject, `{} {}`},
		{"scalar root", ScalarObject, `[]`},
		{"repeated scalar", RepeatedObjects, `[1]`},
		{"repeated nested array", RepeatedObjects, `[[{}]]`},
		{"unknown shape", Shape(99), `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Decode(test.shape, test.text); err == nil {
				t.Fatal("accepted invalid object document")
			}
		})
	}
}

func TestEncodeRejectsInvalidValues(t *testing.T) {
	type nestedObject struct {
		Value map[int]string `json:"value"`
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	for _, test := range []struct {
		name  string
		shape Shape
		value any
	}{
		{"non-string map key", ScalarObject, map[int]string{1: "one"}},
		{"nested non-string map key", ScalarObject, nestedObject{Value: map[int]string{1: "one"}}},
		{"non-finite", ScalarObject, map[string]any{"value": math.NaN()}},
		{"cycle", ScalarObject, cycle},
		{"unsupported function", ScalarObject, map[string]any{"value": func() {}}},
		{"wrong scalar root", ScalarObject, []any{}},
		{"wrong repeated root", RepeatedObjects, map[string]any{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Encode(test.shape, test.value); err == nil {
				t.Fatal("accepted invalid object value")
			}
		})
	}
}

func TestDecodeUseNumberPreservesLargeInteger(t *testing.T) {
	decoded, err := Decode(ScalarObject, `{"value":9007199254740993}`)
	if err != nil {
		t.Fatal(err)
	}
	number := decoded.(map[string]any)["value"]
	if number != json.Number("9007199254740993") {
		t.Fatalf("number = %#v (%T)", number, number)
	}
	if strings.Contains(number.(json.Number).String(), ".") {
		t.Fatal("large integer was converted to floating point")
	}
}
