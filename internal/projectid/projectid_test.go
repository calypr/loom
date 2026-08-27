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
