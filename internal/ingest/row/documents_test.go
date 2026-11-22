package row

import (
	"testing"
)

func TestVertexFromFHIRWithExtraRejectsNonConcreteResourceTypes(t *testing.T) {
	if _, err := VertexFromFHIRWithExtra("project", "PractitionerQualification", map[string]any{"id": "qualification-1"}, nil); err == nil {
		t.Fatal("backbone definition was accepted as a graph vertex")
	}
}
