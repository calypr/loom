package execution

import (
	"encoding/json"
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

// pg-exec-03 acceptance pins: the exact public values the Loom named-UUID
// contract must keep on PostgreSQL rows. The literals are the RFC 4122
// MD5/SHA1 values over the DNS namespace and the name "www.widgets.com",
// verified independently of this repository by direct computation against
// github.com/google/uuid (the coordinator's verifier re-derives both).
const (
	exec03AcceptedUUID5Literal = "21f7f8de-8051-5b89-8680-0195ef798b6a"
	exec03AcceptedUUID3Literal = "3d813cbb-47fb-32ba-91df-831e1593ac29"
	exec03DNSNamespaceString   = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
)

func TestComputeNamedUUIDPinsAcceptedPublicValues(t *testing.T) {
	got, err := computeNamedUUID("uuid5", []any{exec03DNSNamespaceString, "www.widgets.com"})
	if err != nil || got != exec03AcceptedUUID5Literal {
		t.Fatalf("uuid5 = %#v (err %v), want %s", got, err, exec03AcceptedUUID5Literal)
	}
	got, err = computeNamedUUID("uuid3", []any{exec03DNSNamespaceString, "www.widgets.com"})
	if err != nil || got != exec03AcceptedUUID3Literal {
		t.Fatalf("uuid3 = %#v (err %v), want %s", got, err, exec03AcceptedUUID3Literal)
	}
}

func TestComputeNamedUUIDTextualNamespaceNormalization(t *testing.T) {
	// A textual namespace that parses as a UUID is used verbatim, including
	// case-insensitive spelling; a textual namespace that does not parse
	// derives through MD5 over the DNS namespace. All three spellings of
	// the DNS namespace produce the identical public value.
	parsed, err := computeNamedUUID("uuid5", []any{exec03DNSNamespaceString, "www.widgets.com"})
	if err != nil || parsed != exec03AcceptedUUID5Literal {
		t.Fatalf("parsed namespace = %#v (err %v), want %s", parsed, err, exec03AcceptedUUID5Literal)
	}
	upper, err := computeNamedUUID("uuid5", []any{"6BA7B810-9DAD-11D1-80B4-00C04FD430C8", "www.widgets.com"})
	if err != nil || upper != exec03AcceptedUUID5Literal {
		t.Fatalf("uppercase namespace = %#v (err %v), want %s", upper, err, exec03AcceptedUUID5Literal)
	}
	// Textual normalization: "www.widgets.com" does not parse, so it
	// derives through MD5(DNS, "www.widgets.com") — which is exactly the
	// accepted UUID3 literal — and the UUID5 below pins that derivation
	// (literal computed directly with github.com/google/uuid).
	derived, err := computeNamedUUID("uuid5", []any{"www.widgets.com", "www.widgets.com"})
	if err != nil || derived != "fcdbc73e-6d1c-5393-a6d5-79f7dbbe9862" {
		t.Fatalf("textual namespace = %#v (err %v), want fcdbc73e-6d1c-5393-a6d5-79f7dbbe9862", derived, err)
	}
	// The derivation really differs from the DNS namespace itself: the
	// literal pins are discriminating, not aliases.
	other, err := computeNamedUUID("uuid5", []any{"other", "www.widgets.com"})
	if err != nil || other == exec03AcceptedUUID5Literal {
		t.Fatalf("textual namespace %q collided with the DNS-derived witness: %#v (err %v)", "other", other, err)
	}
}

func TestComputeNamedUUIDMultipleNameConcatenationIsOrdered(t *testing.T) {
	// Names concatenate in argument order with fmt.Sprint before hashing:
	// the split spelling of the single-name accepted witness must equal it.
	joined, err := computeNamedUUID("uuid5", []any{exec03DNSNamespaceString, "www.", "widgets", ".com"})
	if err != nil || joined != exec03AcceptedUUID5Literal {
		t.Fatalf("multi-name concat = %#v (err %v), want %s", joined, err, exec03AcceptedUUID5Literal)
	}
	// Order matters (independent literals, computed directly against the
	// textual namespace derived through MD5 over DNS).
	ab, err := computeNamedUUID("uuid5", []any{"www.widgets.com", "a", "b"})
	if err != nil || ab != "46d2b645-361a-5bb3-9f79-6aefcc93bf51" {
		t.Fatalf("ab = %#v (err %v)", ab, err)
	}
	ba, err := computeNamedUUID("uuid5", []any{"www.widgets.com", "b", "a"})
	if err != nil || ba != "45ebaedb-38ed-57b9-bb5c-8bb96e736cd8" {
		t.Fatalf("ba = %#v (err %v)", ba, err)
	}
	if ab == ba {
		t.Fatal("argument order did not affect the named UUID")
	}
	// Mixed non-string name arguments use fmt.Sprint, identical to the
	// textual spelling and pinned to its independently computed literal.
	mixed, err := computeNamedUUID("uuid5", []any{"www.widgets.com", "count=", 1})
	if err != nil {
		t.Fatalf("mixed-name: %v", err)
	}
	if mixed != "8712a807-232e-566a-887b-cdca14b3a40e" {
		t.Fatalf("mixed-name = %#v, want 8712a807-232e-566a-887b-cdca14b3a40e", mixed)
	}
	textual, err := computeNamedUUID("uuid5", []any{"www.widgets.com", "count=1"})
	if err != nil || mixed != textual {
		t.Fatalf("mixed %#v != textual %#v (err %v)", mixed, textual, err)
	}
}

func TestComputeNamedUUIDNilPropagatesThroughPostgreSQLShape(t *testing.T) {
	// A nil argument propagates null (optional recipe expressions must not
	// mint an ID from the text "<nil>") — including when the marker arrives
	// as a PostgreSQL JSONB document with a JSON null argument.
	for _, args := range [][]any{{exec03DNSNamespaceString, nil}, {nil, "www.widgets.com"}} {
		got, err := computeNamedUUID("uuid5", args)
		if err != nil || got != nil {
			t.Fatalf("nil propagation for %#v: got %#v (err %v)", args, got, err)
		}
	}
	marker := `{"__loom_exact_uuid_operation":"uuid5","__loom_exact_uuid_args":["6ba7b810-9dad-11d1-80b4-00c04fd430c8",null]}`
	got, err := materializePostQueryValue(json.RawMessage(marker))
	if err != nil || got != nil {
		t.Fatalf("JSONB null argument materialized to %#v (err %v), want untyped nil", got, err)
	}
}
