package elasticsearch

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestClientRetriesRetryableHTTPStatus(t *testing.T) {
	attempts := 0
	client, err := New(Options{URL: "http://elasticsearch.test", MaxRetries: 1, RetryBase: 0, HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return response(http.StatusServiceUnavailable, `{"error":"busy"}`), nil
		}
		return response(http.StatusOK, `{"count":3}`), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	count, err := client.Count(context.Background(), "loom_patients")
	if err != nil || count != 3 || attempts != 2 {
		t.Fatalf("retry result count=%d attempts=%d err=%v", count, attempts, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
