package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/ingest"
	"github.com/gofiber/fiber/v3"
	"github.com/valyala/fasthttp"
)

type sourceLoadValidationLoader struct {
	uploadCalls int
	beginCalls  int
}

func (loader *sourceLoadValidationLoader) Begin(context.Context, string, string, ingest.SourceLoadPlan) (ingest.SourceLoadStatus, error) {
	loader.beginCalls++
	return ingest.SourceLoadStatus{}, nil
}

func (*sourceLoadValidationLoader) Status(context.Context, string, string) (ingest.SourceLoadStatus, error) {
	return ingest.SourceLoadStatus{AuthResourcePath: "project/data"}, nil
}

func (loader *sourceLoadValidationLoader) UploadFile(context.Context, string, string, string, string) (ingest.SourceFileReceipt, error) {
	loader.uploadCalls++
	return ingest.SourceFileReceipt{}, &ingest.SourceFileValidationError{
		Issues: []ingest.RowErrorSample{{
			File: "META/BodyStructure.ndjson", Line: 32, ResourceType: "BodyStructure", Category: "validation",
			Message: `META/BodyStructure.ndjson row 32: required field "includedStructure" is missing from BodyStructure.`,
		}},
		TotalErrors: 101,
		Truncated:   true,
	}
}

func (*sourceLoadValidationLoader) Publish(context.Context, string, string) (ingest.SourcePublishReceipt, error) {
	return ingest.SourcePublishReceipt{}, nil
}

type sourceLoadDeniedAuthorizer struct{}

func (sourceLoadDeniedAuthorizer) AuthorizeWrite(context.Context, *authscope.Principal, string, string) error {
	return authscope.ErrForbidden
}

func sourceLoadServePartialRequest(t *testing.T, app *fiber.App, method, path, contentType string, contentLength int, bodyPrefix []byte) *http.Response {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	server := &fasthttp.Server{Handler: app.Handler(), StreamRequestBody: true, DisablePreParseMultipartForm: true}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.ServeConn(serverConn) }()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
		select {
		case <-serverDone:
		case <-time.After(time.Second):
		}
	})
	if err := clientConn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set pipe deadline: %v", err)
	}
	_, err := fmt.Fprintf(clientConn,
		"%s %s HTTP/1.1\r\nHost: loom\r\nContent-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n",
		method, path, contentType, contentLength)
	if err != nil {
		t.Fatalf("write request headers: %v", err)
	}
	if len(bodyPrefix) > 0 {
		if _, err := clientConn.Write(bodyPrefix); err != nil {
			t.Fatalf("write request body prefix: %v", err)
		}
	}
	response, err := http.ReadResponse(bufio.NewReader(clientConn), &http.Request{Method: method})
	if err != nil {
		t.Fatalf("read response before request body: %v", err)
	}
	return response
}

func TestSourceLoadValidationDetailsSurviveGenerated422Adapter(t *testing.T) {
	loader := &sourceLoadValidationLoader{}
	loadHandler, err := loadapi.NewHandler(loadapi.Config{
		Service:      &loadapi.Service{},
		Authorizer:   authscope.AllowAllAuthorizer{},
		SourceLoader: loader,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.SetContext(authscope.ContextWithPrincipal(c.Context(), &authscope.Principal{Subject: "test"}))
		return c.Next()
	})
	loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(&HTTPRoutes{load: loadHandler}, nil), loomapi.FiberServerOptions{})

	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	part, err := writer.CreateFormFile("file", "META/BodyStructure.ndjson")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := io.WriteString(part, `{"resourceType":"BodyStructure","id":"bad"}`+"\n"); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/datasets/project-a/source-loads/task-1/files/BodyStructure", &multipartBody)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer response.Body.Close()
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				Issues []struct {
					Filename     string `json:"filename"`
					Row          int    `json:"row"`
					ResourceType string `json:"resourceType"`
					Category     string `json:"category"`
					Message      string `json:"message"`
				} `json:"issues"`
				TotalErrors int64 `json:"totalErrors"`
				Truncated   bool  `json:"truncated"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.StatusCode != http.StatusUnprocessableEntity || payload.Error.Code != "INVALID_DATA" {
		t.Fatalf("response status/code = %d/%q body=%#v", response.StatusCode, payload.Error.Code, payload)
	}
	if loader.uploadCalls != 1 {
		t.Fatalf("UploadFile calls = %d, want 1", loader.uploadCalls)
	}
	if len(payload.Error.Details.Issues) != 1 || payload.Error.Details.TotalErrors != 101 || !payload.Error.Details.Truncated {
		t.Fatalf("validation details were lost by the generated 422 adapter: %#v", payload.Error.Details)
	}
	issue := payload.Error.Details.Issues[0]
	if issue.Filename != "BodyStructure.ndjson" || issue.Row != 32 || issue.ResourceType != "BodyStructure" ||
		issue.Category != "validation" || issue.Message != `Required field "includedStructure" is missing.` {
		t.Fatalf("validation issue = %#v", issue)
	}
	if payload.Error.Message != "The source file contains invalid rows." {
		t.Fatalf("validation error message = %q", payload.Error.Message)
	}
}

func TestGeneratedRouterDoesNotReadMultipartBodyBeforeAuthorization(t *testing.T) {
	loader := &sourceLoadValidationLoader{}
	loadHandler, err := loadapi.NewHandler(loadapi.Config{
		Service:      &loadapi.Service{},
		Authorizer:   sourceLoadDeniedAuthorizer{},
		SourceLoader: loader,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	app := fiber.New(fiber.Config{StreamRequestBody: true, DisablePreParseMultipartForm: true})
	loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(&HTTPRoutes{load: loadHandler}, nil), loomapi.FiberServerOptions{})
	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	part, err := writer.CreateFormFile("file", "META/Patient.ndjson")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := io.WriteString(part, strings.Repeat("x", 1<<20)); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	prefixLength := min(8<<10, multipartBody.Len())
	response := sourceLoadServePartialRequest(t, app, http.MethodPut,
		"/api/v1/datasets/project-a/source-loads/task-1/files/Patient",
		writer.FormDataContentType(), multipartBody.Len(), multipartBody.Bytes()[:prefixLength])
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden || loader.uploadCalls != 0 {
		t.Fatalf("streaming auth response status=%d uploadCalls=%d", response.StatusCode, loader.uploadCalls)
	}
}

func TestGeneratedBeginRouteCapsPlanJSONBody(t *testing.T) {
	loader := &sourceLoadValidationLoader{}
	loadHandler, err := loadapi.NewHandler(loadapi.Config{
		Service:      &loadapi.Service{},
		Authorizer:   authscope.AllowAllAuthorizer{},
		SourceLoader: loader,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	app := fiber.New()
	loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(&HTTPRoutes{load: loadHandler}, nil), loomapi.FiberServerOptions{})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/datasets/project-a/source-loads/task-1", strings.NewReader(strings.Repeat(" ", (1<<20)+1)))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge || loader.beginCalls != 0 {
		t.Fatalf("oversized plan response status=%d beginCalls=%d", response.StatusCode, loader.beginCalls)
	}
}
