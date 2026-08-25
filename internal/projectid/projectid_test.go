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

func TestAliasesPreserveLegacyStorageIdentity(t *testing.T) {
	want := []string{"HTAN_INT/BForePC", "HTAN_INT-BForePC"}
	got := Aliases("HTAN_INT/BForePC")
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Aliases() = %#v, want %#v", got, want)
	}
}

func TestRequireCanonical(t *testing.T) {
	if got, err := RequireCanonical("HTAN_INT-BForePC"); err != nil || got != "HTAN_INT/BForePC" {
		t.Fatalf("RequireCanonical legacy = %q, %v", got, err)
	}
	if _, err := RequireCanonical("BForePC"); err == nil {
		t.Fatal("RequireCanonical accepted project without program")
	}
}
