package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	graphapi "github.com/calypr/loom/internal/api/graphql/graph"
)

func transportCanary(label string) string {
	return `{"data":null,"errors":[{"message":"` + label + ` goroutine 1 [running]: /srv/loom/stack.go:42","extensions":{"code":"SOMETHING_UNKNOWN"}}],"d":"postgres://u:p` + "a" + `SSWORD@localhost/db"}`
}

var transportLeaks = []string{"SECRETCANARY", "goroutine", "paSSWORD", "stack.go"}

func TestGraphQLTransport405IsRouterAuthoredAndWrapperNeverSeesIt(t *testing.T) {
	var called atomic.Bool
	canary := transportCanary("SECRETCANARY-405")
	routes := &HTTPRoutes{graphql: graphapi.RouteConfig{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called.Store(true)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(canary))
	})}}
	for _, path := range []string{"/graphql/graph", "/graphql/dataframe"} {
		request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"query":"query { __typename }"}`))
		request.Header.Set("Content-Type", "application/json")
		response, err := graphQLWireApp(routes).Test(request)
		if err != nil {
			t.Fatalf("PUT %s: %v", path, err)
		}
		body := readAll(t, response)
		t.Logf("PUT %s -> wire status=%d allow=%q content-type=%q body=%s", path, response.StatusCode, response.Header.Get("Allow"), response.Header.Get("Content-Type"), body)
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("PUT %s wire status = %d, want 405 (body %s)", path, response.StatusCode, body)
		}
		if !strings.Contains(body, `"METHOD_NOT_ALLOWED"`) {
			t.Fatalf("PUT %s body is not the mapped service envelope: %s", path, body)
		}
		for _, probe := range transportLeaks {
			if strings.Contains(body, probe) {
				t.Fatalf("PUT %s leaked stub body to wire: %s", path, body)
			}
		}
		if called.Load() {
			t.Fatalf("PUT %s reached the wrapper/native handler; 405 did not originate at the router", path)
		}
	}
}

func TestGraphQLTransportWrapper405ExitsAsRedacted500(t *testing.T) {
	canary := transportCanary("SECRETCANARY-405W")
	routes := &HTTPRoutes{graphql: graphapi.RouteConfig{Handler: stubGraphQLHandler(http.StatusMethodNotAllowed, canary)}}
	value, err := routes.ExecuteGraphQL(context.Background(), loomapi.ExecuteGraphQLRequestObject{Body: &loomapi.RawJSON{}})
	if value != nil {
		t.Fatalf("405 must not return a response object, got %T", value)
	}
	if err == nil || err.Error() != "executeGraphQL returned undocumented HTTP status 405" {
		t.Fatalf("405 wrapper error = %v, want unexpectedResponseStatus text only", err)
	}
	frameValue, frameErr := routes.ExecuteDataframeGraphQL(context.Background(), loomapi.ExecuteDataframeGraphQLRequestObject{Body: &loomapi.RawJSON{}})
	if frameValue != nil || frameErr == nil || frameErr.Error() != "executeDataframeGraphQL returned undocumented HTTP status 405" {
		t.Fatalf("405 dataframe wrapper = %T/%v", frameValue, frameErr)
	}
	for _, path := range []string{"/graphql/graph", "/graphql/dataframe"} {
		status, wire := postGraphQLWire(t, routes, path)
		t.Logf("POST %s with stub-405 handler -> wire status=%d body=%s", path, status, wire)
		if status != http.StatusInternalServerError {
			t.Fatalf("wrapper 405 must exit as redacted 500, got %d (%s)", status, wire)
		}
		for _, probe := range transportLeaks {
			if strings.Contains(wire, probe) {
				t.Fatalf("wrapper 405 leaked upstream body to wire: %s", wire)
			}
		}
		var decoded struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(wire), &decoded); err != nil || decoded.Error.Code != "INTERNAL_ERROR" {
			t.Fatalf("wrapper 405 wire envelope wrong: %s (err %v)", wire, err)
		}
	}
}

func TestGraphQLTransportStatusMatrix(t *testing.T) {
	cases := []struct {
		name   string
		write  func(w http.ResponseWriter, body string)
		json   bool
		expect int // wire status
	}{
		{"405", func(w http.ResponseWriter, b string) { w.WriteHeader(405); _, _ = w.Write([]byte(b)) }, true, 500},
		{"415", func(w http.ResponseWriter, b string) { w.WriteHeader(415); _, _ = w.Write([]byte(b)) }, true, 500},
		{"501", func(w http.ResponseWriter, b string) { w.WriteHeader(501); _, _ = w.Write([]byte(b)) }, true, 500},
		{"204", func(w http.ResponseWriter, b string) { w.WriteHeader(204); _, _ = w.Write([]byte(b)) }, true, 500},
		{"304", func(w http.ResponseWriter, b string) { w.WriteHeader(304); _, _ = w.Write([]byte(b)) }, true, 500},
		{"1xx101", func(w http.ResponseWriter, b string) { w.WriteHeader(101); _, _ = w.Write([]byte(b)) }, true, 500},
		{"double405then500", func(w http.ResponseWriter, b string) {
			w.WriteHeader(405)
			w.WriteHeader(500)
			_, _ = w.Write([]byte(b))
		}, true, 500},
		{"405-nonjson", func(w http.ResponseWriter, b string) { w.WriteHeader(405); _, _ = w.Write([]byte(b)) }, false, 500},
		{"415-nonjson-gqlgen-like", func(w http.ResponseWriter, b string) { w.WriteHeader(415); _, _ = w.Write([]byte(b)) }, false, 500},
	}
	for _, testCase := range cases {
		label := "SECRETCANARY-" + testCase.name
		body := transportCanary(label)
		if !testCase.json {
			body = label + " not-json <html> SECRETCANARY paSSWORD"
		}
		stub := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			testCase.write(w, body)
		})
		routes := &HTTPRoutes{graphql: graphapi.RouteConfig{Handler: stub}}
		_, err := routes.ExecuteGraphQL(context.Background(), loomapi.ExecuteGraphQLRequestObject{Body: &loomapi.RawJSON{}})
		if err == nil {
			t.Fatalf("%s: expected wrapper error for undocumented status", testCase.name)
		}
		for _, probe := range transportLeaks {
			if strings.Contains(err.Error(), probe) {
				t.Fatalf("%s: wrapper error string leaked upstream body: %v", testCase.name, err)
			}
		}
		t.Logf("%-22s wrapper error = %q", testCase.name, err.Error())
		for _, path := range []string{"/graphql/graph", "/graphql/dataframe"} {
			status, wire := postGraphQLWire(t, routes, path)
			t.Logf("%-22s POST %-18s -> status=%d body=%s", testCase.name, path, status, wire)
			if status != testCase.expect {
				t.Fatalf("%s %s: wire status = %d, want %d (%s)", testCase.name, path, status, testCase.expect, wire)
			}
			for _, probe := range transportLeaks {
				if strings.Contains(wire, probe) {
					t.Fatalf("%s %s: upstream canary reached the wire: %s", testCase.name, path, wire)
				}
			}
		}
	}
}

func TestGraphQLTransportRecorderKeepsFirstCodeOnDoubleWriteHeader(t *testing.T) {
	recorder := httptest.NewRecorder()
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(405)
		w.WriteHeader(500)
		_, _ = w.Write([]byte("SECRETCANARY-DOUBLE"))
	})
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/graphql/graph", nil))
	t.Logf("recorder after WriteHeader(405)+WriteHeader(500): code=%d body=%q (httptest recorder silently keeps the first code; the superfluous-WriteHeader log line comes from the real net/http server, not ResponseRecorder)", recorder.Code, recorder.Body.String())
	if recorder.Code != 405 {
		t.Fatalf("recorder code = %d, want first-written 405", recorder.Code)
	}
}

func readAll(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	raw := make([]byte, 0, 512)
	buf := make([]byte, 256)
	for {
		n, err := response.Body.Read(buf)
		raw = append(raw, buf[:n]...)
		if err != nil {
			break
		}
	}
	return string(raw)
}
