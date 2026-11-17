package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/gofiber/fiber/v3"
)

func mscGetWithHeaders(t *testing.T, app *fiber.App, id string, headers [][2]string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, twinRoutePrefix+id, nil)
	for _, header := range headers {
		request.Header.Add(header[0], header[1])
	}
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("GET %s%s: %v", twinRoutePrefix, id, err)
	}
	return response
}

func mscReadResponse(t *testing.T, response *http.Response) (int, string, http.Header) {
	t.Helper()
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	header := response.Header.Clone()
	if header == nil {
		header = http.Header{}
	}
	return response.StatusCode, string(payload), header
}

var mscHeaderVariations = []struct {
	name    string
	headers [][2]string
}{
	{"no-headers", nil},
	{"bearer-token", [][2]string{{"Authorization", "Bearer some.opaque.token"}}},
	{"basic-credentials", [][2]string{{"Authorization", "Basic dXNlcjpwYXNz"}}},
	{"empty-authorization", [][2]string{{"Authorization", ""}}},
	{"duplicate-authorization", [][2]string{
		{"Authorization", "Bearer first"},
		{"Authorization", "Bearer second"},
	}},
	{"x-request-id", [][2]string{{"X-Request-Id", "probe-request-1"}}},
	{"forwarded", [][2]string{{"X-Forwarded-For", "203.0.113.9"}, {"X-Forwarded-Proto", "https"}}},
	{"accept-star", [][2]string{{"Accept", "*/*"}}},
	{"accept-json", [][2]string{{"Accept", "application/json"}}},
	{"accept-xml", [][2]string{{"Accept", "application/xml"}}},
	{"user-agent-and-cors", [][2]string{{"User-Agent", "probe/1.0"}, {"Origin", "https://evil.example"}}},
	{"cookie", [][2]string{{"Cookie", "session=leaky"}}},
	{"x-forwarded-host", [][2]string{{"X-Forwarded-Host", "admin.example"}}},
}

func TestRESTRecipeExecutionMalformedScopeIsHeaderIndistinguishable(t *testing.T) {
	for _, variation := range mscHeaderVariations {
		variation := variation
		t.Run(variation.name, func(t *testing.T) {
			probe := &mscProbe{pathsByProjectGeneration: mscKnownPaths()}
			app := twinHTTPStack(mscRoutesWith(probe, nil), mscMemberPrincipal())

			malformedStatus, malformedBody, malformedHeader := mscReadResponse(t,
				mscGetWithHeaders(t, app, mscMalformedExecID, variation.headers))
			missingStatus, missingBody, missingHeader := mscReadResponse(t,
				mscGetWithHeaders(t, app, mscMissingID, variation.headers))

			if malformedStatus != http.StatusNotFound {
				t.Fatalf("malformed-scope read must stay 404 under headers %v, got %d %s", variation.headers, malformedStatus, malformedBody)
			}
			if malformedStatus != missingStatus || malformedBody != missingBody {
				t.Fatalf("headers %v turned the concealment into an oracle: malformed=%d %q vs missing=%d %q",
					variation.headers, malformedStatus, malformedBody, missingStatus, missingBody)
			}
			if !mscHeadersEqual(malformedHeader, missingHeader) {
				t.Fatalf("headers %v leak a distinguishable response header set: malformed=%v vs missing=%v",
					variation.headers, malformedHeader, missingHeader)
			}
			mscAssertConcealment(t, malformedStatus, malformedBody)
			for _, forbidden := range []string{"Authorization", "Www-Authenticate", "Set-Cookie"} {
				if values := malformedHeader.Values(forbidden); len(values) != 0 {
					t.Fatalf("concealed response must not carry %s under headers %v, got %v", forbidden, variation.headers, values)
				}
			}
		})
	}
}

func TestRESTRecipeExecutionMalformedScopeFaultClassificationIsHeaderInvariant(t *testing.T) {
	bearer := &authscope.Principal{
		Subject:             "operator-bearer",
		Projects:            []string{mscHomeProject},
		AuthResourcePaths:   []string{mscGrantedPath},
		AuthorizationHeader: "Bearer not-a-real-token",
	}
	probe := &mscProbe{pathsByProjectGeneration: mscKnownPaths()}
	app := twinHTTPStack(mscRoutesWith(probe, nil), bearer)
	status, body, _ := mscReadResponse(t, mscGetWithHeaders(t, app, mscMalformedExecID,
		[][2]string{{"Authorization", "Bearer not-a-real-token"}, {"X-Request-Id", "fault-probe"}}))
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "BACKEND_UNAVAILABLE") {
		t.Fatalf("bearer header must not collapse the authorization-backend fault into the concealed 404, got %d %s", status, body)
	}

	probeFaulty := &mscProbe{pathsByProjectGeneration: mscKnownPaths(), fault: errors.New("arangodb connection reset")}
	appFaulty := twinHTTPStack(mscRoutesWith(probeFaulty, nil), mscMemberPrincipal())
	for _, variation := range mscHeaderVariations {
		faultyStatus, faultyBody, _ := mscReadResponse(t, mscGetWithHeaders(t, appFaulty, mscMalformedExecID, variation.headers))
		if faultyStatus != http.StatusServiceUnavailable || !strings.Contains(faultyBody, "BACKEND_UNAVAILABLE") {
			t.Fatalf("resolver-path fault must stay the retryable 503 under headers %v, got %d %s", variation.headers, faultyStatus, faultyBody)
		}
	}
	for _, variation := range mscHeaderVariations {
		missingStatus, missingBody, _ := mscReadResponse(t, mscGetWithHeaders(t, appFaulty, mscMissingID, variation.headers))
		if missingStatus != http.StatusNotFound || !strings.Contains(missingBody, twinConcealedError) {
			t.Fatalf("missing-id read must stay the concealed 404 under headers %v even with a faulty resolver, got %d %s", variation.headers, missingStatus, missingBody)
		}
	}
}

func mscHeadersEqual(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for name, values := range a {
		other, ok := b[name]
		if !ok || len(values) != len(other) {
			return false
		}
		for i := range values {
			if values[i] != other[i] {
				return false
			}
		}
	}
	return true
}
