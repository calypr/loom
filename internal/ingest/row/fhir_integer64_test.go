package row

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGeneratedLoadNormalizesNumericFHIRInteger64(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{"numeric", "411314", "411314"},
		{"exact", "9007199254740993", "9007199254740993"},
		{"max", "9223372036854775807", "9223372036854775807"},
		{"min", "-9223372036854775808", "-9223372036854775808"},
		{"quoted", `"411314"`, "411314"},
		{"quoted exact", `"9007199254740993"`, "9007199254740993"},
		{"quoted max", `"9223372036854775807"`, "9223372036854775807"},
		{"quoted min", `"-9223372036854775808"`, "-9223372036854775808"},
		{"malformed quoted", `"abc1"`, ""},
		{"trailing junk", `"0garbage"`, ""},
		{"quoted overflow", `"9223372036854775808"`, ""},
		{"quoted underflow", `"-9223372036854775809"`, ""},
		{"exponent", "1e1", ""},
		{"fraction", "1.5", ""},
		{"overflow", "9223372036854775808", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte(`{"resourceType":"DocumentReference","id":"doc","status":"current","content":[{"attachment":{"size":` + test.value + `,"pages":2}},{"attachment":{"size":"7"}}],"unknownNumber":12}`)
			vertex, _, _, _, err := loadRowGeneratedWithPayload("DocumentReference", raw, "test", map[string]float64{})
			if test.want == "" {
				if err == nil {
					t.Fatal("expected invalid integer64 to be rejected")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var stored struct {
				Content []struct {
					Attachment struct {
						Size  string
						Pages int
					}
				}
				UnknownNumber int
				ProjectID     string `json:"project_id"`
			}
			if err := json.Unmarshal(vertex.Payload.(json.RawMessage), &stored); err != nil {
				t.Fatal(err)
			}
			if len(stored.Content) != 2 || stored.Content[0].Attachment.Size != test.want ||
				stored.Content[0].Attachment.Pages != 2 || stored.Content[1].Attachment.Size != "7" || stored.UnknownNumber != 12 || stored.ProjectID != "test" {
				t.Fatalf("unexpected stored payload: %+v", stored)
			}
		})
	}
}

func TestGeneratedLoadRejectsCodingSystemWhitespace(t *testing.T) {
	raw := []byte(`{"resourceType":"ImagingStudy","id":"study","status":"available","subject":{"reference":"Patient/p"},"series":[{"uid":"1.2.3","modality":{"coding":[{"system":" http://dicom.nema.org/resources/ontology/DCM","code":"CT"}]}}]}`)
	_, _, _, kind, err := loadRowGeneratedWithPayload("ImagingStudy", raw, "test", map[string]float64{})
	if kind != RowErrorValidation || err == nil || !strings.Contains(err.Error(), "field 'system' does not match its schema pattern") {
		t.Fatalf("expected Coding.system validation error, got %v (%v)", err, kind)
	}
}
