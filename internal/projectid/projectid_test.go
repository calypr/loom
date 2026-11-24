package projectid

import "testing"

func TestCanonicalAcceptsSlashAndLegacyIDs(t *testing.T) {
	for _, test := range []struct {
		input, want string
	}{
		{"HTAN_INT/BForePC", "HTAN_INT/BForePC"},
		{"HTAN_INT-BForePC", "HTAN_INT/BForePC"},
		{"HTAN_INT-BFore-PC", "HTAN_INT/BFore-PC"},
		{"HTAN_INT%2FBForePC", "HTAN_INT/BForePC"},
	} {
		if got := Canonical(test.input); got != test.want {
			t.Fatalf("Canonical(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestCanonicalAndLegacy(t *testing.T) {
	for _, test := range []struct{ raw, canonical, legacy string }{
		{" HTAN_INT/BForePC ", "HTAN_INT/BForePC", "HTAN_INT-BForePC"},
		{"HTAN_INT-BFore-PC", "HTAN_INT/BFore-PC", "HTAN_INT-BFore-PC"},
		{"HTAN_INT%2FBForePC", "HTAN_INT/BForePC", "HTAN_INT-BForePC"},
		{"HTAN_INT%252FBForePC", "HTAN_INT%2FBForePC", "HTAN_INT%2FBForePC"},
		{"HTAN_INT%2ZBForePC", "HTAN_INT%2ZBForePC", "HTAN_INT%2ZBForePC"},
		{"lowercase-dashed", "lowercase-dashed", "lowercase-dashed"},
		{"   ", "", ""},
	} {
		canonical, legacy := CanonicalAndLegacy(test.raw)
		if canonical != test.canonical || legacy != test.legacy {
			t.Fatalf("forms for %q = (%q, %q), want (%q, %q)", test.raw, canonical, legacy, test.canonical, test.legacy)
		}
	}
}
