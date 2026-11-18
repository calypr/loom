package fhir

import "testing"

func TestValidateAndExtractFHIRPrimitiveValues(t *testing.T) {
	cases := []struct {
		name, resourceType, raw string
		valid                   bool
	}{
		{"partial date", "Patient", `{"resourceType":"Patient","id":"p","birthDate":"2025-02"}`, true},
		{"leap date", "Patient", `{"resourceType":"Patient","id":"p","birthDate":"2000-02-29"}`, true},
		{"bad leap date", "Patient", `{"resourceType":"Patient","id":"p","birthDate":"1900-02-29"}`, false},
		{"bad month day", "Patient", `{"resourceType":"Patient","id":"p","birthDate":"2025-04-31"}`, false},
		{"bad datetime day", "Observation", `{"resourceType":"Observation","id":"o","status":"final","code":{},"effectiveDateTime":"2025-02-31T12:00:00Z"}`, false},
		{"bad instant day", "Observation", `{"resourceType":"Observation","id":"o","status":"final","code":{},"issued":"2025-02-31T12:00:00Z"}`, false},
		{"leap second", "Observation", `{"resourceType":"Observation","id":"o","status":"final","code":{},"issued":"2016-12-31T23:59:60Z"}`, true},
		{"valid attachment", "DocumentReference", `{"resourceType":"DocumentReference","id":"d","status":"current","content":[{"attachment":{"data":"Zg==\n"}}]}`, true},
		{"invalid attachment", "DocumentReference", `{"resourceType":"DocumentReference","id":"d","status":"current","content":[{"attachment":{"data":"@not-base64@"}}]}`, false},
		{"invalid attachment padding", "DocumentReference", `{"resourceType":"DocumentReference","id":"d","status":"current","content":[{"attachment":{"data":"Zg=A"}}]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateAndExtract([]byte(tc.raw), tc.resourceType, "test")
			if (err == nil) != tc.valid {
				t.Fatalf("ValidateAndExtract error = %v, want valid = %t", err, tc.valid)
			}
		})
	}
}
