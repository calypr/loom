package httpapi

import (
	"context"
	"testing"
)

func TestRequestIDContextUsesPrivateKey(t *testing.T) {
	ctx := context.WithValue(context.Background(), "loom.graphql.request_id", "spoofed")
	if got := RequestIDFromContext(ctx); got != "" {
		t.Fatalf("plain string context key leaked request ID %q", got)
	}
	if got := RequestIDFromContext(ContextWithRequestID(ctx, "request-1")); got != "request-1" {
		t.Fatalf("RequestIDFromContext() = %q, want request-1", got)
	}
}
