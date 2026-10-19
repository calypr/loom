package graphqlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	gqlhandler "github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/calypr/loom/generated/graphql/graph/executor"
	"github.com/calypr/loom/internal/api/graphql/graph/resolver"
	httpapi "github.com/calypr/loom/internal/api/http"
	fiberadaptor "github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type RouteConfig struct{ Handler, Playground, Sandbox http.Handler }

func NewHandler(root *resolver.Resolver, loggers ...*slog.Logger) http.Handler {
	logger := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	server := gqlhandler.NewDefaultServer(executor.NewExecutableSchema(executor.Config{
		Resolvers: root,
	}))
	server.SetErrorPresenter(func(ctx context.Context, err error) *gqlerror.Error {
		requestID := httpapi.RequestIDFromContext(ctx)
		presented := presentGraphQLError(err, requestID)
		if presented != nil {
			code, _ := presented.Extensions["code"].(string)
			cause := presented.Err
			if cause == nil {
				cause = err
			}
			logger.Error("graphql operation failed",
				"request_id", requestID,
				"code", code,
				"message", presented.Message,
				"cause", errorChain(cause),
			)
		}
		return presented
	})
	server.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		return next(root.WithOperationContext(ctx))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ctx, ok := fiberadaptor.LocalContextFromHTTPRequest(r); ok {
			r = r.WithContext(ctx)
		}
		if requestID := httpapi.RequestIDFromContext(r.Context()); r.Header.Get("X-Request-ID") == "" && requestID != "" {
			r.Header.Set("X-Request-ID", requestID)
		}
		r = r.WithContext(httpapi.ContextWithRequestID(r.Context(), r.Header.Get("X-Request-ID")))
		serveGraphQLResponse(w, r, server)
	})
}

type bufferedGraphQLResponse struct {
	header http.Header
	body   bytes.Buffer
	spill  *os.File
	status int
}

func (w *bufferedGraphQLResponse) Header() http.Header { return w.header }

func (w *bufferedGraphQLResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *bufferedGraphQLResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.spill != nil {
		return w.spill.Write(body)
	}
	if w.body.Len()+len(body) <= maxGraphQLResponseMemoryBytes {
		return w.body.Write(body)
	}
	spill, err := os.CreateTemp("", "loom-graphql-response-*")
	if err != nil {
		return 0, err
	}
	if _, err := spill.Write(w.body.Bytes()); err != nil {
		_ = spill.Close()
		_ = os.Remove(spill.Name())
		return 0, err
	}
	w.body.Reset()
	w.spill = spill
	return w.spill.Write(body)
}

func (w *bufferedGraphQLResponse) Flush() {}

func (w *bufferedGraphQLResponse) reader() (io.Reader, error) {
	if w.spill == nil {
		return bytes.NewReader(w.body.Bytes()), nil
	}
	if _, err := w.spill.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return w.spill, nil
}

func (w *bufferedGraphQLResponse) close() error {
	if w.spill == nil {
		return nil
	}
	name := w.spill.Name()
	err := w.spill.Close()
	if removeErr := os.Remove(name); err == nil {
		err = removeErr
	}
	w.spill = nil
	return err
}

func serveGraphQLResponse(destination http.ResponseWriter, request *http.Request, next http.Handler) {
	captured := &bufferedGraphQLResponse{header: make(http.Header)}
	defer func() { _ = captured.close() }()
	next.ServeHTTP(captured, request)
	status := captured.status
	if status == 0 {
		status = http.StatusOK
	}
	reader, err := captured.reader()
	if err != nil {
		destination.WriteHeader(http.StatusInternalServerError)
		return
	}
	if status < http.StatusBadRequest {
		var failureStatus int
		if captured.spill == nil {
			failureStatus = graphqlFailureStatus(captured.body.Bytes())
		} else {
			failureStatus = graphqlFailureStatusReader(reader)
			_, _ = captured.spill.Seek(0, io.SeekStart)
		}
		if failureStatus != 0 {
			status = failureStatus
		}
	}
	for key, values := range captured.header {
		for _, value := range values {
			destination.Header().Add(key, value)
		}
	}
	destination.WriteHeader(status)
	_, _ = io.Copy(destination, reader)
}

const maxGraphQLResponseMemoryBytes = 8 << 20

func graphqlFailureStatusReader(reader io.Reader) int {
	decoder := json.NewDecoder(reader)
	first, err := decoder.Token()
	if err != nil {
		return 0
	}
	opening, ok := first.(json.Delim)
	if !ok || opening != '{' {
		return 0
	}
	hasErrors := false
	dataNull := true
	statuses := make([]int, 0, 1)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return 0
		}
		name, ok := key.(string)
		if !ok {
			return 0
		}
		value, err := decoder.Token()
		if err != nil {
			return 0
		}
		switch name {
		case "data":
			dataNull = value == nil
			if err := skipJSONValue(decoder, value); err != nil {
				return 0
			}
		case "errors":
			hasErrors = true
			if err := collectGraphQLErrorStatuses(decoder, value, &statuses); err != nil {
				return 0
			}
		default:
			if err := skipJSONValue(decoder, value); err != nil {
				return 0
			}
		}
	}
	if _, err := decoder.Token(); err != nil || !hasErrors || !dataNull {
		return 0
	}
	status := http.StatusBadRequest
	for _, candidate := range statuses {
		if candidate >= http.StatusInternalServerError {
			return candidate
		}
		if candidate > status {
			status = candidate
		}
	}
	return status
}

func collectGraphQLErrorStatuses(decoder *json.Decoder, value any, statuses *[]int) error {
	opening, ok := value.(json.Delim)
	if !ok || opening != '[' {
		return errors.New("graphql errors is not an array")
	}
	for decoder.More() {
		value, err := decoder.Token()
		if err != nil {
			return err
		}
		opening, ok := value.(json.Delim)
		if !ok || opening != '{' {
			if err := skipJSONValue(decoder, value); err != nil {
				return err
			}
			continue
		}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("graphql error key is not a string")
			}
			field, err := decoder.Token()
			if err != nil {
				return err
			}
			if name == "extensions" {
				collectGraphQLExtensionStatus(decoder, field, statuses)
				continue
			}
			if err := skipJSONValue(decoder, field); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

func collectGraphQLExtensionStatus(decoder *json.Decoder, value any, statuses *[]int) {
	opening, ok := value.(json.Delim)
	if !ok || opening != '{' {
		_ = skipJSONValue(decoder, value)
		return
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return
		}
		name, ok := key.(string)
		if !ok {
			return
		}
		field, err := decoder.Token()
		if err != nil {
			return
		}
		if name == "code" {
			if code, ok := field.(string); ok {
				*statuses = append(*statuses, httpapi.StatusForErrorCode(code))
				continue
			}
		}
		if err := skipJSONValue(decoder, field); err != nil {
			return
		}
	}
	_, _ = decoder.Token()
}

func skipJSONValue(decoder *json.Decoder, value any) error {
	delimiter, ok := value.(json.Delim)
	if !ok || (delimiter != '{' && delimiter != '[') {
		return nil
	}
	for decoder.More() {
		next, err := decoder.Token()
		if err != nil {
			return err
		}
		if delimiter == '{' {
			if _, ok := next.(string); !ok {
				return errors.New("json object key is not a string")
			}
			next, err = decoder.Token()
			if err != nil {
				return err
			}
		}
		if err := skipJSONValue(decoder, next); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

func graphqlFailureStatus(body []byte) int {
	var payload struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Extensions map[string]any `json:"extensions"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &payload) != nil || len(payload.Errors) == 0 {
		return 0
	}
	data := bytes.TrimSpace(payload.Data)
	if len(data) > 0 && !bytes.Equal(data, []byte("null")) {
		// GraphQL can legitimately return useful partial data alongside field
		// errors. Keep HTTP 200 for that case; only failed operations are lifted
		// into the HTTP error contract.
		return 0
	}
	status := http.StatusBadRequest
	for _, graphErr := range payload.Errors {
		code, _ := graphErr.Extensions["code"].(string)
		candidate := httpapi.StatusForErrorCode(code)
		if candidate >= http.StatusInternalServerError {
			return candidate
		}
		if candidate > status {
			status = candidate
		}
	}
	return status
}

func errorChain(err error) string {
	if err == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	for current := err; current != nil; current = errors.Unwrap(current) {
		parts = append(parts, current.Error())
	}
	return strings.Join(parts, " <- ")
}

func NewPlaygroundHandler(endpoint string) http.Handler {
	return playground.Handler("FHIR GraphQL Playground", endpoint)
}

func NewApolloSandboxHandler(endpoint string) http.Handler {
	page := template.Must(template.New("apollo-sandbox").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>FHIR GraphQL Apollo Sandbox</title>
    <style>
      html, body, #embedded-sandbox {
        width: 100%;
        height: 100%;
        margin: 0;
        overflow: hidden;
      }
      body {
        font-family: sans-serif;
        background: #0f172a;
      }
    </style>
  </head>
  <body>
    <div id="embedded-sandbox"></div>
    <script
      src="https://embeddable-sandbox.cdn.apollographql.com/_latest/embeddable-sandbox.umd.production.min.js"
      crossorigin="anonymous">
    </script>
    <script>
      new window.EmbeddedSandbox({
        target: "#embedded-sandbox",
        initialEndpoint: {{.EndpointJSON}},
        includeCookies: false
      });
    </script>
  </body>
</html>`))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpointJSON, err := json.Marshal(endpoint)
		if err != nil {
			http.Error(w, "failed to render apollo sandbox", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(w, struct {
			EndpointJSON template.JS
		}{
			EndpointJSON: template.JS(endpointJSON),
		})
	})
}
