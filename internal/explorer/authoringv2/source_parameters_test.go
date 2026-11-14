package authoringv2

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSourceParametersStrictEditAdmission(t *testing.T) {
	valid := `{"stepId":"identity","enabled":true,"parameters":{"identity":{"name":"id","expr":{"call":"uuid5","args":[{"literal":"namespace"},{"select":"root.id"}]}}}}`
	var c ImportedSourceStepChange
	if err := json.Unmarshal([]byte(valid), &c); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{
		"unknown call":       strings.Replace(valid, "uuid5", "arbitrary_query", 1),
		"unknown setting":    strings.Replace(valid, `"name":"id"`, `"name":"id","query":"RETURN 1"`, 1),
		"multiple arms":      strings.Replace(valid, `"identity":`, `"output":{"rowGrain":"resource"},"identity":`, 1),
		"object literal":     strings.Replace(valid, `"namespace"`, `{"not":"primitive"}`, 1),
		"document reference": strings.Replace(valid, `{"select":"root.id"}`, `{"document":{"context":"root","output":"other"}}`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(input), &c); err == nil {
				t.Fatal("unsafe source edit admitted")
			}
		})
	}
}
