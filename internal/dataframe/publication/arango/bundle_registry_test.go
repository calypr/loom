package arango

import (
	"regexp"
	"testing"
)

func TestPointerDocumentKeyAcceptsLogicalNamesWithNULSeparators(t *testing.T) {
	name := "HTAN_INT-BForePC\x00\x00aced-meta-default"
	first := pointerDocumentKey(name)
	second := pointerDocumentKey(name)
	if first != second {
		t.Fatalf("pointer key is not deterministic: %q != %q", first, second)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(first) {
		t.Fatalf("pointer key is not an Arango-safe SHA256: %q", first)
	}
}
