package httpapi

import (
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func TestDiagnosticErrorCauseCapsTypedSafeMessages(t *testing.T) {
	message := strings.Repeat("safe diagnostic ", 32)
	cause := diagnosticErrorCause(&lifecycle.Error{Code: "SAFE_TEST", Message: message})
	if len(cause) != maxDiagnosticCauseBytes || !strings.HasSuffix(cause, "...") {
		t.Fatalf("diagnostic cause length/suffix = %d/%q, want exactly %d bytes with ellipsis", len(cause), cause[len(cause)-3:], maxDiagnosticCauseBytes)
	}
	if strings.Contains(cause, "\uFFFD") {
		t.Fatalf("diagnostic cause contains invalid UTF-8 replacement: %q", cause)
	}
}

func TestDiagnosticErrorCausePreservesOrdinaryErrorsAndRedactsSensitiveShapes(t *testing.T) {
	ordinary := errors.New("category result lacks expected field")
	if got := diagnosticErrorCause(ordinary); got != ordinary.Error() {
		t.Fatalf("ordinary cause = %q, want original diagnostic %q", got, ordinary.Error())
	}

	cases := []struct {
		name   string
		error  string
		secret string
	}{
		{name: "authorization header", error: "request failed: Authorization: Bearer private-auth-value", secret: "private-auth-value"},
		{name: "token assignment", error: "request failed: token=private-token-value", secret: "private-token-value"},
		{name: "query payload", error: "AQL execution failed; query=FOR d IN users FILTER d.api_token == 'private-query-value'", secret: "private-query-value"},
		{name: "bind values", error: "category result failed; bind_vars=map[x:private-bind-value]", secret: "private-bind-value"},
		{name: "request body", error: `request body: {"resource":"private-body-value"}`, secret: "private-body-value"},
		{name: "URL credentials", error: "backend at https://user:private-password-value@example.test refused connection", secret: "private-password-value"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := diagnosticErrorCause(errors.New(test.error))
			if strings.Contains(got, test.secret) {
				t.Fatalf("diagnostic cause %q retained sensitive value %q", got, test.secret)
			}
			if !strings.Contains(got, "[redacted") {
				t.Fatalf("diagnostic cause = %q, want a targeted redaction", got)
			}
			if strings.Contains(got, "unclassified execution failure") {
				t.Fatalf("diagnostic cause discarded the original failure: %q", got)
			}
		})
	}
}
