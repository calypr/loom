package arango

import (
	"context"
	"encoding/json"
	"testing"
)

func TestStringValueCoercesStoredScalars(t *testing.T) {
	for _, test := range []struct {
		value any
		want  string
	}{
		{value: nil, want: ""},
		{value: "text", want: "text"},
		{value: int64(42), want: "42"},
		{value: true, want: "true"},
	} {
		if got := StringValue(test.value); got != test.want {
			t.Errorf("StringValue(%v) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestReadOnlyDocumentScannerRejectsMissingClientAndVisitor(t *testing.T) {
	var client *Client
	if _, err := client.CollectionNamesReadOnly(context.Background()); err == nil {
		t.Fatal("nil client unexpectedly listed collections")
	}
	if err := client.VisitCollectionDocumentsRaw(context.Background(), "Patient", func(json.RawMessage) error { return nil }); err == nil {
		t.Fatal("nil client unexpectedly scanned documents")
	}
	client = &Client{}
	if err := client.VisitCollectionDocumentsRaw(context.Background(), "Patient", nil); err == nil {
		t.Fatal("nil visitor unexpectedly scanned documents")
	}
}
