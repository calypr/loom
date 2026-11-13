package postgres

import (
	"regexp"
	"strings"
	"testing"
)

func TestValidSchemaNameVerdictTable(t *testing.T) {
	cases := []struct {
		schema string
		want   bool
	}{
		{"", false},
		{"a", true},
		{"_", true},
		{strings.Repeat("a", 63), true},
		{strings.Repeat("a", 64), false},
		{"z" + strings.Repeat("_z", 31), true},
		{"z" + strings.Repeat("_z", 32), false},
		{"_mix9_case", true},
		{"a9_", true},
		{"9lead", false},
		{"Upper", false},
		{"A", false},
		{"a.b", false},
		{"a b", false},
		{" a", false},
		{"a ", false},
		{"a-b", false},
		{"a;b", false},
		{"a\"b", false},
		{"a列", false},
		{"ä", false},
		{"a\x00b", false},
		{"\n", false},
	}
	for _, tc := range cases {
		if got := ValidSchemaName(tc.schema); got != tc.want {
			t.Errorf("ValidSchemaName(%q) = %v, want %v", tc.schema, got, tc.want)
		}
	}
}

func TestValidSchemaNameMatchesCanonicalRegexp(t *testing.T) {
	canonical := regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	corpus := []string{
		"", "a", "_", "9", "A", "z_", "a.b", "a b", "a-b", "a\tb", "a\nb",
		"a\x00b", "ä", "列", "a列", "9_", "_9", "a9", "A_", "__", "a__b9",
		strings.Repeat("a", 62), strings.Repeat("a", 63), strings.Repeat("a", 64),
		strings.Repeat("a", 65), "z" + strings.Repeat("_z", 30), "z" + strings.Repeat("_z", 31),
		strings.Repeat("_", 63), strings.Repeat("_", 64),
		"public", "loom_test_1", "verify_pgcomp_1_abc",
		"schema; DROP TABLE x", "a'b", `a"b`, "a\\b", "%", "*", "a%",
	}
	alphabet := []string{"a", "Z", "9", "_", ".", "-", " ", "\t", "列"}
	var build func(prefix string, depth int)
	build = func(prefix string, depth int) {
		if depth == 0 {
			corpus = append(corpus, prefix)
			return
		}
		for _, ch := range alphabet {
			build(prefix+ch, depth-1)
		}
	}
	build("", 2)
	for _, schema := range corpus {
		if want := canonical.MatchString(schema); ValidSchemaName(schema) != want {
			t.Fatalf("ValidSchemaName(%q) diverges from canonical regexp: got %v, want %v", schema, !want, want)
		}
	}
}
