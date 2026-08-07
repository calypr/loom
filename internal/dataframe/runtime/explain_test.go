package runtime

import "testing"

func TestCompiledQueryFingerprintExcludesBindValues(t *testing.T) {
	left := CompiledQuery{Query: "FOR root IN Patient FILTER root.id == @id RETURN root", BindVars: map[string]any{"id": "one"}, Columns: []string{"id"}}
	right := left
	right.BindVars = map[string]any{"id": "two"}
	if got, want := CompiledQueryFingerprint(left), CompiledQueryFingerprint(right); got != want {
		t.Fatalf("fingerprint includes bind value: left=%q right=%q", got, want)
	}
	right.BindVars["other"] = "value"
	if CompiledQueryFingerprint(left) == CompiledQueryFingerprint(right) {
		t.Fatal("fingerprint did not include bind-key shape")
	}
}
