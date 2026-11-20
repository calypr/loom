package authoring

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublishRejectsInvalidReceipts(t *testing.T) {
	for _, response := range []string{
		`{"receiptId":"receipt-1","revisionId":"authoring-1","state":"READY"}`,
		`{"receiptId":"receipt-other","revisionId":"authoring-1","state":"ACTIVE"}`,
		`{"receiptId":"receipt-1","revisionId":"authoring-1","state":"FAILED"}`,
		`{"receiptId":"receipt-1","state":"ACTIVE"}`,
		`{"receiptId":"receipt-1","revisionId":"authoring-1","state":"ACTIVE"}{}`,
		`{"receiptId":"receipt-1","revisionId":"authoring-1","state":"ACTIVE"}` + strings.Repeat(" ", maxResponseSize),
	} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(writer, response)
		}))
		client, err := New(server.URL, "token", &http.Client{})
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		_, err = client.Publish(context.Background(), PublishRequest{
			Project: "HTAN_INT/BForePC", ExplorerID: "default", ReceiptID: "receipt-1",
			AuthResourcePath: "/programs/HTAN_INT/projects/BForePC",
		})
		server.Close()
		if err == nil {
			t.Errorf("Publish accepted an invalid response of %d bytes", len(response))
		}
	}
}
