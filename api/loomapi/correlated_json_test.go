package loomapi

import (
	"encoding/json"
	"strings"
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

func TestApplyCommandsRequestPreservesBlankStringFallbackOnUpdateColumn(t *testing.T) {
	const raw = `{"commandId":"a847f075-fbd2-417e-a192-ed026a967599","semanticsVersion":10,"snapshotToken":"sha256:e49bb7e16de70d79a0d2796c7a4872ee59cec62b74c7dbf8a570122dd05a796c","expectedDraftVersion":3,"expectedDraftDigest":"sha256:51f8e85f6050bd11980623adc60ea065f4fb449f632984d69b08b1036d392b54","commands":[{"type":"UPDATE_COLUMN","outputId":"out_585e1e879bca39f69bc6beef","column":"col_e594ba9d5ea950fa45163f44","columnValue":{"column":"col_e594ba9d5ea950fa45163f44","label":"Medication semantic value edited","logicalType":"string","occurrenceId":"occ_d027a60310148fd349848dd4","source":{"kind":"codedValue","lookup":{"binding":{"keyPath":"code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueCodeableConcept.text","valueFallback":["valueCodeableConcept.coding[].display"],"fallbackOnBlankString":true,"choiceArms":["valueCodeableConcept"],"logicalType":"string"},"key":{"system":"http://snomed.info/sct","code":"1222590007"},"projectionMode":"VALUE"}},"table":{"visible":true,"order":1}}}]}`
	var request ApplyCommandsRequest
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		t.Fatalf("decode native UPDATE_COLUMN request: %v", err)
	}
	if len(request.Commands) != 1 || request.Commands[0].ColumnValue == nil ||
		request.Commands[0].ColumnValue.Source.Lookup == nil ||
		request.Commands[0].ColumnValue.Source.Lookup.Binding == nil ||
		request.Commands[0].ColumnValue.Source.Lookup.Binding.FallbackOnBlankString == nil ||
		!*request.Commands[0].ColumnValue.Source.Lookup.Binding.FallbackOnBlankString {
		t.Fatal("decoded UPDATE_COLUMN request lost fallbackOnBlankString")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("encode generated UPDATE_COLUMN request: %v", err)
	}
	if !strings.Contains(string(encoded), `"fallbackOnBlankString":true`) {
		t.Fatalf("UPDATE_COLUMN round trip omitted fallbackOnBlankString: %s", encoded)
	}
	invalid := strings.Replace(raw, `"fallbackOnBlankString":true`, `"fallbackOnBlankString":true,"untrusted":true`, 1)
	var invalidRequest ApplyCommandsRequest
	if err := json.Unmarshal([]byte(invalid), &invalidRequest); err == nil {
		t.Fatal("UPDATE_COLUMN request accepted an unknown correlated binding property")
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
