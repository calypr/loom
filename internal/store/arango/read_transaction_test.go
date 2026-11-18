package arango

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestWithReadSnapshotDeclaresStrictCollectionsAndReadsCursor(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/_db/db/_api/transaction/begin":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["allowImplicit"] != false {
				t.Fatalf("allowImplicit = %#v, want explicit false", body["allowImplicit"])
			}
			collections := body["collections"].(map[string]any)["read"].([]any)
			if !reflect.DeepEqual(collections, []any{"DocumentReference", "fhir_edge"}) {
				t.Fatalf("read collections = %#v", collections)
			}
			w.Write([]byte(`{"result":{"id":"tx-1"}}`))
		case r.URL.Path == "/_db/db/_api/cursor" && r.Method == http.MethodPost:
			if r.Header.Get("x-arango-trx-id") != "tx-1" {
				t.Fatalf("transaction header = %q", r.Header.Get("x-arango-trx-id"))
			}
			var body struct {
				Options struct {
					MaxNumberOfPlans int `json:"maxNumberOfPlans"`
				} `json:"options"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Options.MaxNumberOfPlans != 1 {
				t.Fatalf("snapshot optimizer plan cap = %d, want 1", body.Options.MaxNumberOfPlans)
			}
			w.Write([]byte(`{"result":[{"_key":"a"}],"hasMore":true,"id":"cursor-1"}`))
		case r.URL.Path == "/_db/db/_api/cursor/cursor-1" && r.Method == http.MethodPut:
			if r.Header.Get("x-arango-trx-id") != "tx-1" {
				t.Fatalf("cursor transaction header = %q", r.Header.Get("x-arango-trx-id"))
			}
			w.Write([]byte(`{"result":[{"_key":"b"}],"hasMore":false}`))
		case r.URL.Path == "/_db/db/_api/transaction/tx-1" && r.Method == http.MethodPut:
			w.Write([]byte(`{"result":{"status":"committed"}}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client := &Client{rawURL: server.URL, dbName: "db", client: server.Client()}
	var rows []string
	err := client.WithReadSnapshot(context.Background(), []string{"fhir_edge", "DocumentReference", "DocumentReference"}, func(ctx context.Context, queryer RowQueryer) error {
		return queryer.QueryRows(WithQueryMaxNumberOfPlans(ctx, 1), "FOR d IN @@docs RETURN d", 1, map[string]interface{}{"@docs": "DocumentReference"}, func(row map[string]interface{}) error {
			rows = append(rows, row["_key"].(string))
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, []string{"a", "b"}) {
		t.Fatalf("rows = %#v", rows)
	}
	if !reflect.DeepEqual(calls, []string{
		"POST /_db/db/_api/transaction/begin",
		"POST /_db/db/_api/cursor",
		"PUT /_db/db/_api/cursor/cursor-1",
		"PUT /_db/db/_api/transaction/tx-1",
	}) {
		t.Fatalf("request order = %#v", calls)
	}
}

func TestWithReadSnapshotAbortsCallbackFailure(t *testing.T) {
	var lastMethod, lastPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod, lastPath = r.Method, r.URL.Path
		if r.URL.Path == "/_db/db/_api/transaction/begin" {
			w.Write([]byte(`{"result":{"id":"tx-2"}}`))
			return
		}
		if r.Method != http.MethodDelete || r.URL.Path != "/_db/db/_api/transaction/tx-2" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"result":{"status":"aborted"}}`))
	}))
	defer server.Close()
	client := &Client{rawURL: server.URL, dbName: "db", client: server.Client()}
	wantErr := errors.New("query failed")
	err := client.WithReadSnapshot(context.Background(), []string{"Documents"}, func(context.Context, RowQueryer) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want callback failure", err)
	}
	if lastMethod != http.MethodDelete || !strings.HasSuffix(lastPath, "/tx-2") {
		t.Fatalf("final request = %s %s, want transaction abort", lastMethod, lastPath)
	}
}

func TestWithReadSnapshotAbortsWhenFinalVisitorCancelsQuery(t *testing.T) {
	var committed, aborted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/transaction/begin"):
			w.Write([]byte(`{"result":{"id":"tx-cancel"}}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/_api/cursor"):
			w.Write([]byte(`{"result":[{"_key":"a"}],"hasMore":false}`))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/transaction/tx-cancel"):
			committed = true
			w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/transaction/tx-cancel"):
			aborted = true
			w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client := &Client{rawURL: server.URL, dbName: "db", client: server.Client()}
	err := client.WithReadSnapshot(context.Background(), []string{"docs"}, func(ctx context.Context, queryer RowQueryer) error {
		queryCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		return queryer.QueryRows(queryCtx, "RETURN 1", 1, nil, func(map[string]any) error {
			cancel()
			return nil
		})
	})
	if !errors.Is(err, context.Canceled) || committed || !aborted {
		t.Fatalf("error=%v committed=%v aborted=%v", err, committed, aborted)
	}
}

func TestWithReadSnapshotAbortsAfterCommitFailure(t *testing.T) {
	var aborted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/transaction/begin"):
			w.Write([]byte(`{"result":{"id":"tx-3"}}`))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/transaction/tx-3"):
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/transaction/tx-3"):
			aborted = true
			w.Write([]byte(`{"result":{"status":"aborted"}}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client := &Client{rawURL: server.URL, dbName: "db", client: server.Client()}
	err := client.WithReadSnapshot(context.Background(), []string{"docs"}, func(context.Context, RowQueryer) error { return nil })
	if err == nil || !aborted {
		t.Fatalf("error=%v, abort attempted=%v", err, aborted)
	}
}
