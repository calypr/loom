package publication

import (
	"errors"
	"strings"
	"testing"
)

func TestRefValidation(t *testing.T) {
	ref, err := NewRef("project-a", "generation-a")
	if err != nil || ref != (Ref{"project-a", "generation-a"}) {
		t.Fatalf("NewRef = %#v, %v", ref, err)
	}
	for _, ref := range []Ref{{}, {Project: " project", Generation: "g"}, {Project: "p", Generation: "g\n"}, {Project: strings.Repeat("p", 513), Generation: "g"}} {
		if !errors.Is(ref.Validate(), ErrInvalidDatasetRef) {
			t.Errorf("Validate(%#v) should fail", ref)
		}
	}
}
