package recipeeval

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

// These vectors are generated with Python's uuid.uuid3/uuid.uuid5.  A string
// namespace is the legacy dataframer convention: it is first resolved as
// uuid.uuid3(uuid.NAMESPACE_DNS, namespace).  Name arguments are concatenated
// in order before applying the requested UUID version.
func TestNamedUUIDMatchesLegacyPythonVectors(t *testing.T) {
	tests := []struct {
		name      string
		call      string
		namespace string
		parts     []string
		want      string
	}{
		{
			name: "aced group member uuid5",
			call: "uuid5", namespace: "aced-idp.org",
			parts: []string{"group-1", ",member-1"},
			want:  "bffec5ae-0bb6-5f30-b09b-43a5ab3a0181",
		},
		{
			name: "aced uuid3",
			call: "uuid3", namespace: "aced-idp.org",
			parts: []string{"hello"},
			want:  "c2a32694-5b4e-3996-bb2b-5f6e69b14141",
		},
		{
			name: "direct DNS namespace uuid5",
			call: "uuid5", namespace: "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
			parts: []string{"name"},
			want:  "9b8edca0-90f2-5031-8e5d-3f708834696c",
		},
		{
			name: "empty named namespace",
			call: "uuid5", namespace: "",
			parts: []string{"a", "b"},
			want:  "846f6261-7a65-5bb1-ac39-042bf76eb982",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := []recipe.Expression{{Literal: jsonString(test.namespace)}}
			for _, part := range test.parts {
				args = append(args, recipe.Expression{Literal: jsonString(part)})
			}
			got, err := eval(recipe.Expression{Call: test.call, Args: args}, context{})
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("%s = %v, want %s", test.call, got, test.want)
			}
		})
	}
}

func TestNamedUUIDPropagatesMissingOptionalInputs(t *testing.T) {
	got, err := eval(recipe.Expression{Call: "uuid5", Args: []recipe.Expression{
		{Literal: jsonString("aced-idp.org")},
		{Select: "root.missing"},
		{Literal: jsonString("suffix")},
	}}, context{"root": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("missing name = %#v, want null", got)
	}
}

func jsonString(value string) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}
