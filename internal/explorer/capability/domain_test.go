package capability

import "testing"

func TestIsRepeatedCardinality(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: "required_one"},
		{value: "optional_one"},
		{value: "OPTIONAL_ONE"},
		{value: "scalar"},
		{value: "many", want: true},
		{value: "MANY", want: true},
		{value: "unknown_observed_many", want: true},
	} {
		if got := IsRepeatedCardinality(test.value); got != test.want {
			t.Errorf("IsRepeatedCardinality(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}
