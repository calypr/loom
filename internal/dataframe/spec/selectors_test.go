package spec

import (
	"reflect"
	"testing"
)

func TestParseDirectScalarSelector(t *testing.T) {
	segments, err := ParseDirectScalarSelector("valueQuantity.value")
	if err != nil || !reflect.DeepEqual(segments, []string{"valueQuantity", "value"}) {
		t.Fatalf("direct scalar path = %#v, %v", segments, err)
	}
	for _, path := range []string{"component[].code", "component[0].code", "name.where(use='official').family", ""} {
		if segments, err := ParseDirectScalarSelector(path); err == nil {
			t.Errorf("selector %q was accepted as direct scalar path %#v", path, segments)
		}
	}
}
