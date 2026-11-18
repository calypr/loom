package fhir

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestFastValidatorsMatchOfficialFHIRPatterns(t *testing.T) {
	source, err := os.ReadFile("../r5/fhir.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Definitions map[string]struct {
			Pattern    string
			Properties map[string]struct{ Pattern string }
		} `json:"definitions"`
	}
	if err := json.Unmarshal(source, &schema); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		primitive string
		check     func(string) bool
		inputs    []string
	}{
		{"id", validFHIRID, []string{"", "a-B.9", "a_b", "é", strings.Repeat("a", 64), strings.Repeat("a", 65)}},
		{"code", validFHIRCode, []string{"", "a", "two words", " two", "two ", "two  words", "a\tb", "a\nb"}},
		{"uri", func(s string) bool { return !hasFHIRSchemaSpace(s) }, []string{"", "LNC", "http://loinc.org", "urn:oid:1.2", "a b", "\ta"}},
		{"date", validFHIRDate, []string{"", "0000", "0001", "1900", "2024-02", "2024-02-29", "2024-02-31", "2024-00-01", "2024-13", "2024-02-32", "2024Z"}},
		{"dateTime", func(s string) bool { return validFHIRDateTime(s, false) }, []string{
			"", "0000-01-01", "2024", "2024-02", "2024-02Z", "2024-02-29", "2024-02-29T00:00:00",
			"2024-02-29T00:00:60Z", "2024-02-29T00:00:00.123456789+14:00", "2024-02-29T00:00:00.1234567890Z",
			"2024-02-29T00:00:00+14:01", "2024-02-29T00:00:00-", "2024-02-29T24:00:00Z",
		}},
		{"instant", func(s string) bool { return validFHIRDateTime(s, true) }, []string{
			"2024", "2024-02-29", "2024-02-29Z", "2024-02-29T00:00:00", "2024-02-29T00:00:00Z",
			"2024-02-29T00:00:60+14:00", "2024-02-29T00:00:00+14:01", "2024-02-29T00:00:00.1-02:30",
		}},
		{"time", validFHIRTime, []string{"00:00:00", "23:59:60", "23:59:00.", "23:59:00.999999999", "23:59:00.9999999999", "24:00:00", "00:00:00Z"}},
		{"oid", validFHIROID, []string{"urn:oid:2.4", "urn:oid:1.0.11", "urn:oid:1.02", "urn:oid:3.20", "urn:oid:1.", "urn:oid:1.1."}},
		{"uuid", validFHIRUUID, []string{"urn:uuid:00000000-0000-0000-0000-aabbccddeeff", "urn:uuid:00000000-0000-0000-0000-AABBCCDDEEFF", "urn:uuid:0000", "00000000-0000-0000-0000-aabbccddeeff"}},
		{"base64Binary", validFHIRBase64, []string{"", "AAAA", "AAAAAA==", "AAA=", "AA==", "A===", "AABBCCDD", "AAB", "AA=-", "AA==AA==", "A A="}},
	}
	for _, field := range cases {
		t.Run(field.primitive, func(t *testing.T) {
			pattern := schema.Definitions[field.primitive].Pattern
			if field.primitive == "base64Binary" {
				pattern = schema.Definitions["Extension"].Properties["valueBase64Binary"].Pattern
			}
			if pattern == "" {
				t.Fatal("primitive has no official schema pattern")
			}
			validator := regexp.MustCompile(pattern)
			for _, input := range field.inputs {
				if got, want := field.check(input), validator.MatchString(input); got != want {
					t.Errorf("%q accepted = %t, official schema = %t", input, got, want)
				}
			}
		})
	}
}

func TestFHIRWhitespaceUsesJSONSchemaWhitespace(t *testing.T) {
	for _, space := range []string{"\v", "\u00a0", "\u2003", "\u2028", "\ufeff"} {
		if !hasFHIRSchemaSpace("a"+space+"b") || validFHIRCode("a"+space+"b") {
			t.Errorf("expected the JSON Schema whitespace rule to reject %q in a URI or code", space)
		}
	}
}
