package loomapi

import (
	"encoding/json"
	"testing"
)

func TestCorrelatedLookupIsAClosedAlternative(t *testing.T) {
	const binding = `"binding":{"ownerPath":"component[]","keyPath":"component[].code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","logicalType":"decimal"}`
	const key = `"key":{"system":"urn:system:B","code":"shared"}`
	var valid LookupSource
	if err := json.Unmarshal([]byte("{"+binding+","+key+"}"), &valid); err != nil {
		t.Fatal(err)
	}
	if valid.Key.System != "urn:system:B" || valid.Key.Code != "shared" || valid.Binding.OwnerPath == nil || *valid.Binding.OwnerPath != "component[]" {
		t.Fatalf("binding changed: %+v", valid)
	}
	for _, raw := range []string{
		"{" + binding + "}",
		"{" + key + "}",
		"{" + binding + "," + key + `,"match":"shared"}`,
		"{" + binding + "," + key + `,"path":"valueString"}`,
		`{"binding":{"keyPath":"code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueString","logicalType":"string","untrusted":true},` + key + "}",
	} {
		var value LookupSource
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			t.Fatalf("accepted ambiguous lookup %s", raw)
		}
	}
}
