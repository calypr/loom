package engine

import (
	"testing"

	"github.com/google/uuid"
)

func TestComputeNamedUUID(t *testing.T) {
	name := "example"
	for _, operation := range []string{"uuid3", "uuid5"} {
		got, err := computeNamedUUID(operation, []any{uuid.NameSpaceDNS.String(), name})
		if err != nil || got == nil {
			t.Fatalf("%s: got %v, err %v", operation, got, err)
		}
	}
	got, err := computeNamedUUID("uuid5", []any{"textual", name})
	if err != nil || got == nil {
		t.Fatalf("text namespace: got %v, err %v", got, err)
	}
	if got, err := computeNamedUUID("uuid5", []any{uuid.NameSpaceDNS.String(), nil}); err != nil || got != nil {
		t.Fatalf("nil propagation: got %v, err %v", got, err)
	}
	if _, err := computeNamedUUID("uuid5", []any{"only"}); err == nil {
		t.Fatal("expected insufficient arguments error")
	}
	if _, err := computeNamedUUID("uuid4", []any{"ns", name}); err == nil {
		t.Fatal("expected unsupported operation error")
	}
}
