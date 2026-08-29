package recipe

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const validDocument = `{
  "recipeSchemaVersion": 1,
  "name": "example",
  "translationVersion": "1",
  "outputs": [{
    "name": "members",
    "rootResourceType": "Group",
    "rowGrain": "group_member",
    "expand": {"from": {"select": "member[]"}, "as": "member"},
    "identity": {"name": "id", "expr": {"call": "uuid5", "args": [
      {"literal": "example"},
      {"literal": "group"},
      {"select": "member.id"}
    ]}},
    "fields": [
      {"name": "group_id", "expr": {"select": "root.id"}},
      {"name": "member_id", "expr": {"call": "reference_id", "args": [{"select": "member.reference"}]}}
    ]
  }]
}`

func TestParseCanonicalDigestIgnoresFormatting(t *testing.T) {
	b, err := Parse([]byte(validDocument))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := b.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(canonical) || bytes.ContainsAny(canonical, " \n\t") {
		t.Fatalf("canonical JSON is not compact: %s", canonical)
	}
	other := strings.ReplaceAll(strings.ReplaceAll(validDocument, " ", ""), "\n", "")
	b2, err := Parse([]byte(other))
	if err != nil {
		t.Fatal(err)
	}
	d1, err := b.Digest()
	if err != nil {
		t.Fatal(err)
	}
	d2, err := b2.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatalf("formatting changed digest: %s != %s", d1, d2)
	}
}

func TestParseRejectsDuplicateUnknownAndStorageFields(t *testing.T) {
	tests := []struct {
		name string
		json string
		code string
	}{
		{"duplicate", `{"recipeSchemaVersion":1,"recipeSchemaVersion":1,"name":"x","translationVersion":"1","outputs":[]}`, "duplicate_field"},
		{"unknown", `{"recipeSchemaVersion":1,"name":"x","translationVersion":"1","outputs":[],"unexpected":true}`, "parse_error"},
		{"storage", `{"recipeSchemaVersion":1,"name":"x","translationVersion":"1","outputs":[],"sql":"select 1"}`, "forbidden_storage_binding"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.json))
			if err == nil || !strings.HasPrefix(err.Error(), tc.code+" ") {
				t.Fatalf("expected %s, got %v", tc.code, err)
			}
		})
	}
}

func TestParseAcceptsBuilderFieldMetadataWithoutPersistingIt(t *testing.T) {
	input := `{"recipeSchemaVersion":1,"name":"builder","translationVersion":"interactive","outputs":[{"name":"DocumentReference","rootResourceType":"DocumentReference","rowGrain":"resource","fields":[{"name":"status","fieldRef":"DocumentReference.status","expr":{"select":"root.status"},"logicalType":"scalar","repeated":false,"family":"field","selectionKey":"DocumentReference.status","valueSelector":"status","familyName":"Fields","familyKind":"FIELD"}]}]}`
	bundle, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := bundle.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(canonical, []byte("logicalType")) || bytes.Contains(canonical, []byte("selectionKey")) {
		t.Fatalf("Builder metadata leaked into executable recipe: %s", canonical)
	}
	if !bytes.Contains(canonical, []byte(`"fieldRef":"DocumentReference.status"`)) {
		t.Fatalf("semantic field provenance was lost: %s", canonical)
	}
}

func TestParseStillRejectsUnknownBuilderFieldMetadata(t *testing.T) {
	input := `{"recipeSchemaVersion":1,"name":"builder","translationVersion":"interactive","outputs":[{"name":"DocumentReference","rootResourceType":"DocumentReference","rowGrain":"resource","fields":[{"name":"status","expr":{"select":"root.status"},"logicalTypo":"scalar"}]}]}`
	if _, err := Parse([]byte(input)); err == nil || !strings.HasPrefix(err.Error(), "parse_error ") {
		t.Fatalf("expected strict parse error, got %v", err)
	}
}

func TestValidationRejectsVersionNamesAndArity(t *testing.T) {
	bad := []string{
		`{"recipeSchemaVersion":2,"name":"x","translationVersion":"1","outputs":[]}`,
		`{"recipeSchemaVersion":1,"name":"x","translationVersion":"1","outputs":[{"name":"x","rootResourceType":"R","rowGrain":"r","fields":[{"name":"a","expr":{"call":"join","args":[{"literal":"one"}]}}]}]}`,
		`{"recipeSchemaVersion":1,"name":"x","translationVersion":"1","outputs":[{"name":"x","rootResourceType":"R","rowGrain":"r","fields":[{"name":"a","expr":{"call":"does_not_exist"}}]}]}`,
	}
	for _, input := range bad {
		if _, err := Parse([]byte(input)); err == nil {
			t.Fatalf("expected validation failure for %s", input)
		}
	}
}

func TestExplainContainsNoPhysicalDetails(t *testing.T) {
	b, err := Parse([]byte(validDocument))
	if err != nil {
		t.Fatal(err)
	}
	explanation, err := b.Explain()
	if err != nil {
		t.Fatal(err)
	}
	if explanation.Digest == "" || len(explanation.Outputs) != 1 || !explanation.Outputs[0].Expanded {
		t.Fatalf("unexpected explanation: %#v", explanation)
	}
	raw, _ := json.Marshal(explanation)
	if bytes.Contains(raw, []byte("collection")) || bytes.Contains(raw, []byte("table")) || bytes.Contains(raw, []byte("aql")) {
		t.Fatalf("explanation contains physical details: %s", raw)
	}
}

func TestRuntimeBindingsAreNotDigestContent(t *testing.T) {
	var b RuntimeBindings
	if b.Project != "" || b.DatasetGeneration != "" {
		t.Fatal("zero runtime bindings should be empty")
	}
}

func TestDocumentExpressionRoundTrips(t *testing.T) {
	input := Expression{Document: &DocumentRef{Context: "root"}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"document":{"context":"root"}}` {
		t.Fatalf("document wire form = %s", raw)
	}
	var decoded Expression
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Document == nil || decoded.Document.Context != "root" {
		t.Fatalf("decoded document = %#v", decoded.Document)
	}
}

func TestParseAcceptsDocumentExpression(t *testing.T) {
	input := `{"recipeSchemaVersion":1,"name":"document","translationVersion":"1","outputs":[{"name":"patients","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"resource","expr":{"document":{"context":"root"}}}]}]}`
	bundle, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Outputs[0].Fields[0].Expr.Document == nil {
		t.Fatalf("document expression was not parsed: %#v", bundle.Outputs[0].Fields[0].Expr)
	}
}
