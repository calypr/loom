package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPreviewIndexRunRejectsInvalidMutationIntentBeforeOpeningDatabase(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, test := range []struct {
		name, database, mode, message string
		authorize                     bool
	}{
		{name: "unknown mode", database: "loom_test_guard", mode: "unknown", message: "unsupported mode"},
		{name: "unauthorized apply", database: "loom_test_guard", mode: "apply", message: "requires -authorize-local-migration"},
		{name: "write flag on dry-run", database: "loom_test_guard", mode: "dry-run", authorize: true, message: "only valid with -mode apply"},
		{name: "production database", database: "production", mode: "apply", authorize: true, message: "refusing database"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := runPreviewIndexMigration(server.URL, test.database, test.mode, test.authorize)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("run error=%v, want %q", err, test.message)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("rejected command reached Arango %d times", requests.Load())
	}
}
