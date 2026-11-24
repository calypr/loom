package digest

import (
	"strings"
	"testing"
)

func TestIsSHA256(t *testing.T) {
	valid := strings.Repeat("a", 64)
	for _, test := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "lowercase hexadecimal", value: valid, want: true},
		{name: "uppercase hexadecimal", value: strings.Repeat("A", 64)},
		{name: "short", value: valid[:63]},
		{name: "non-hexadecimal", value: strings.Repeat("g", 64)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsSHA256(test.value); got != test.want {
				t.Fatalf("IsSHA256(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}
