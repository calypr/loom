package arango

import (
	"context"
	"encoding/json"
	"testing"
)

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
