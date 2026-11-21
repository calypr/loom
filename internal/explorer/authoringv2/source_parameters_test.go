package authoringv2

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSourceParametersValidateStructureBeforePinResolution(t *testing.T) {
	valid := `{"stepId":"identity","parameters":{"identity":{"name":"id","expr":{"call":"uuid5","args":[{"literal":"namespace"},{"select":"root.id"}]}}}}`
	var c ImportedSourceStepChange
	if err := json.Unmarshal([]byte(valid), &c); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{
		"unknown setting": strings.Replace(valid, `"name":"id"`, `"name":"id","query":"RETURN 1"`, 1),
		"multiple arms":   strings.Replace(valid, `"identity":`, `"output":{"rowGrain":"resource"},"identity":`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(input), &c); err == nil {
				t.Fatal("unsafe source edit admitted")
			}
		})
	}
}
