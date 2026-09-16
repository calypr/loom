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

func TestExtensionLookupPreservesAncestryAndRejectsMixedPayloads(t *testing.T) {
	const extension = `"extension":{"ownerPath":"extension[].extension[]","urlPath":["urn:parent:left","urn:leaf"],"valuePath":"valueString","logicalType":"string"}`
	var valid LookupSource
	if err := json.Unmarshal([]byte("{"+extension+`,"projectionMode":"ALL"}`), &valid); err != nil {
		t.Fatal(err)
	}
	if valid.Extension == nil || len(valid.Extension.UrlPath) != 2 || valid.Extension.UrlPath[0] != "urn:parent:left" || valid.Extension.UrlPath[1] != "urn:leaf" {
		t.Fatalf("extension ancestry = %#v", valid.Extension)
	}
	for _, raw := range []string{
		"{" + extension + `,"match":"urn:leaf"}`,
		"{" + extension + `,"path":"extension[]"}`,
		"{" + extension + `,"key":{"system":"urn:system","code":"leaf"}}`,
		`{"extension":{"ownerPath":"extension[]","urlPath":["urn:leaf"],"valuePath":"valueString","logicalType":"string","rawAql":"RETURN 1"}}`,
	} {
		var value LookupSource
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			t.Fatalf("accepted ambiguous extension lookup %s", raw)
		}
	}
}
