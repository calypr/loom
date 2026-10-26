package capability

import "testing"

func TestSchemaFieldDescriptorsExposesNullablePatientGenderMetadata(t *testing.T) {
	for _, field := range SchemaFieldDescriptors("Patient") {
		if field.Path != "gender" {
			continue
		}
		if field.ResourceType != "Patient" || field.PrimitiveType != "string" || field.Cardinality != "optional_one" || len(field.RepeatedPaths) != 0 {
			t.Fatalf("Patient.gender descriptor = %#v", field)
		}
		return
	}
	t.Fatal("generated Patient schema did not expose primitive gender")
}

func TestSchemaFieldDescriptorsRetainsNestedManyPathStructureWithoutWidths(t *testing.T) {
	for _, field := range SchemaFieldDescriptors("Observation") {
		if field.Path != "category[].coding[].code" {
			continue
		}
		if field.ResourceType != "Observation" || field.PrimitiveType != "string" || field.Cardinality != "many" {
			t.Fatalf("nested descriptor metadata = %#v", field)
		}
		want := []string{"category[]", "category[].coding[]"}
		if len(field.RepeatedPaths) != len(want) {
			t.Fatalf("repeated path identities = %#v, want %#v", field.RepeatedPaths, want)
		}
		for index := range want {
			if field.RepeatedPaths[index] != want[index] {
				t.Fatalf("repeated path identities = %#v, want %#v", field.RepeatedPaths, want)
			}
		}
		return
	}
	t.Fatal("generated Observation schema did not expose category[].coding[].code")
}

func TestSchemaFieldDescriptorsRejectsNonResourceDefinitions(t *testing.T) {
	if got := SchemaFieldDescriptors("HumanName"); len(got) != 0 {
		t.Fatalf("non-resource type produced schema fields: %#v", got)
	}
}
