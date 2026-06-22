package writeapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/google/uuid"
)

type Authenticator interface {
	Authenticate(ctx context.Context, headers map[string][]string) (*Principal, error)
}

type Authorizer interface {
	AuthorizeWrite(ctx context.Context, principal *Principal, project, authResourcePath string) error
}

type StaticAuthenticator struct {
	Principal Principal
}

func (a StaticAuthenticator) Authenticate(ctx context.Context, headers map[string][]string) (*Principal, error) {
	principal := a.Principal
	if principal.Subject == "" {
		principal.Subject = "anonymous"
	}
	return &principal, nil
}

type AllowAllAuthorizer struct{}

func (AllowAllAuthorizer) AuthorizeWrite(ctx context.Context, principal *Principal, project, authResourcePath string) error {
	return nil
}

type HTTPConfig struct {
	Service                  *Service
	Authenticator            Authenticator
	Authorizer               Authorizer
	GraphQLHandler           http.Handler
	GraphQLPlaygroundHandler http.Handler
	ApolloSandboxHandler     http.Handler
	Logger                   *slog.Logger
	BodyLimit                int
	ReadBufferSize           int
}

type HTTPServer struct {
	app                         *fiber.App
	service                     *Service
	authn                       Authenticator
	authz                       Authorizer
	logger                      *slog.Logger
	cfgGraphQLHandler           http.Handler
	cfgGraphQLPlaygroundHandler http.Handler
	cfgApolloSandboxHandler     http.Handler
}

type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string { return e.Message }

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func NewHTTPServer(cfg HTTPConfig) (*HTTPServer, error) {
	if cfg.Service == nil {
		return nil, errors.New("service is required")
	}
	if cfg.Authenticator == nil {
		cfg.Authenticator = BearerTokenAuthenticator{}
	}
	if cfg.Authorizer == nil {
		cfg.Authorizer = AllowAllAuthorizer{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.BodyLimit <= 0 {
		cfg.BodyLimit = 1024 * 1024 * 1024
	}
	if cfg.ReadBufferSize <= 0 {
		cfg.ReadBufferSize = 1024 * 1024
	}

	server := &HTTPServer{
		service:                     cfg.Service,
		authn:                       cfg.Authenticator,
		authz:                       cfg.Authorizer,
		logger:                      cfg.Logger,
		cfgGraphQLHandler:           cfg.GraphQLHandler,
		cfgGraphQLPlaygroundHandler: cfg.GraphQLPlaygroundHandler,
		cfgApolloSandboxHandler:     cfg.ApolloSandboxHandler,
	}
	app := fiber.New(fiber.Config{
		BodyLimit:      cfg.BodyLimit,
		ReadBufferSize: cfg.ReadBufferSize,
		ErrorHandler: func(c fiber.Ctx, err error) error {
			requestID := requestIDFromCtx(c)
			var apiErr *apiError
			if errors.As(err, &apiErr) {
				return c.Status(apiErr.Status).JSON(errorEnvelope{
					Error: errorBody{Code: apiErr.Code, Message: apiErr.Message, RequestID: requestID},
				})
			}
			server.logger.Error("unhandled request error", "request_id", requestID, "path", c.Path(), "error", err.Error())
			return c.Status(fiber.StatusInternalServerError).JSON(errorEnvelope{
				Error: errorBody{Code: "internal_error", Message: "internal server error", RequestID: requestID},
			})
		},
	})
	server.app = app
	server.register()
	return server, nil
}

func (s *HTTPServer) App() *fiber.App {
	return s.app
}

func (s *HTTPServer) register() {
	s.app.Use(s.requestIDMiddleware, s.recoveryMiddleware, s.loggingMiddleware, s.authenticationMiddleware)
	s.app.Get("/healthz", func(c fiber.Ctx) error {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "ok"})
	})
	if s.cfgGraphQLPlaygroundHandler != nil {
		s.app.Get("/graphql", adaptor.HTTPHandlerWithContext(s.cfgGraphQLPlaygroundHandler))
	}
	if s.cfgApolloSandboxHandler != nil {
		s.app.Get("/apollo", adaptor.HTTPHandlerWithContext(s.cfgApolloSandboxHandler))
	}
	if s.cfgGraphQLHandler != nil {
		s.app.Post("/graphql", adaptor.HTTPHandlerWithContext(s.cfgGraphQLHandler))
	}
	group := s.app.Group("/api/v1")
	group.Post("/imports", s.createImport)
	group.Get("/imports/:id", s.getImport)
	group.Get("/imports/:id/events", s.getImportEvents)
}

func (s *HTTPServer) requestIDMiddleware(c fiber.Ctx) error {
	requestID := c.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.NewString()
	}
	c.Locals("request_id", requestID)
	c.Set("X-Request-ID", requestID)
	return c.Next()
}

func (s *HTTPServer) recoveryMiddleware(c fiber.Ctx) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Error("panic recovered", "request_id", requestIDFromCtx(c), "path", c.Path(), "panic", recovered)
			err = &apiError{Status: fiber.StatusInternalServerError, Code: "internal_error", Message: "internal server error"}
		}
	}()
	return c.Next()
}

func (s *HTTPServer) loggingMiddleware(c fiber.Ctx) error {
	start := time.Now()
	err := c.Next()
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && c.Response().StatusCode() < 400 {
			c.Status(apiErr.Status)
		}
	}
	s.logger.Info("http request", "request_id", requestIDFromCtx(c), "method", c.Method(), "path", c.Path(), "status", c.Response().StatusCode(), "duration_ms", time.Since(start).Milliseconds())
	return err
}

func (s *HTTPServer) authenticationMiddleware(c fiber.Ctx) error {
	principal, err := s.authn.Authenticate(c.Context(), c.GetReqHeaders())
	if err != nil {
		return &apiError{Status: fiber.StatusUnauthorized, Code: "unauthorized", Message: err.Error()}
	}
	c.Locals("principal", principal)
	c.SetContext(ContextWithPrincipal(c.Context(), principal))
	return c.Next()
}

func (s *HTTPServer) createImport(c fiber.Ctx) error {
	if !c.IsMultipart() {
		return &apiError{Status: fiber.StatusUnsupportedMediaType, Code: "unsupported_media_type", Message: "expected multipart/form-data"}
	}
	form, err := c.Req().MultipartForm()
	if err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "invalid_multipart_form", Message: err.Error()}
	}
	fileCount := 0
	for _, files := range form.File {
		fileCount += len(files)
	}
	if fileCount != 1 {
		return &apiError{Status: fiber.StatusBadRequest, Code: "invalid_file_count", Message: "exactly one uploaded file is required"}
	}

	project := strings.TrimSpace(c.Req().FormValue("project"))
	if project == "" {
		return &apiError{Status: fiber.StatusBadRequest, Code: "missing_project", Message: "project is required"}
	}
	resourceType := strings.TrimSpace(c.Req().FormValue("resource_type"))
	if resourceType == "" {
		return &apiError{Status: fiber.StatusBadRequest, Code: "missing_resource_type", Message: "resource_type is required"}
	}
	authResourcePath := strings.TrimSpace(c.Req().FormValue("auth_resource_path"))
	truncate, err := parseOptionalBool(c.Req().FormValue("truncate"))
	if err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "invalid_truncate", Message: err.Error()}
	}
	useGeneric, err := parseOptionalBool(c.Req().FormValue("use_generic"))
	if err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "invalid_use_generic", Message: err.Error()}
	}

	principal, _ := c.Locals("principal").(*Principal)
	if err := s.authz.AuthorizeWrite(c.Context(), principal, project, authResourcePath); err != nil {
		return &apiError{Status: fiber.StatusForbidden, Code: "forbidden", Message: err.Error()}
	}

	fileHeader, err := c.Req().FormFile("file")
	if err != nil {
		return &apiError{Status: fiber.StatusBadRequest, Code: "missing_file", Message: "file upload is required"}
	}
	stagedPath, err := stageUploadedFile(fileHeader)
	if err != nil {
		return &apiError{Status: fiber.StatusInternalServerError, Code: "stage_failed", Message: err.Error()}
	}

	req := ImportRequest{
		Project:          project,
		ResourceType:     resourceType,
		AuthResourcePath: authResourcePath,
		Truncate:         truncate,
		UseGeneric:       useGeneric,
		StagedFilePath:   stagedPath,
		OriginalFilename: fileHeader.Filename,
	}
	if principal != nil {
		req.SubmittedBy = principal.Subject
	}
	op, err := s.service.Submit(c.Context(), req)
	if err != nil {
		_ = os.Remove(stagedPath)
		return &apiError{Status: fiber.StatusBadRequest, Code: "invalid_import_request", Message: err.Error()}
	}

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"import_id":          op.ID,
		"status":             op.Status,
		"status_url":         op.StatusURL,
		"events_url":         op.EventsURL,
		"project":            op.Project,
		"resource_type":      op.ResourceType,
		"auth_resource_path": op.AuthResourcePath,
		"original_filename":  op.OriginalFilename,
		"submitted_at":       op.SubmittedAt,
	})
}

func (s *HTTPServer) getImport(c fiber.Ctx) error {
	id := c.Params("id")
	op, ok := s.service.Get(id)
	if !ok {
		return &apiError{Status: fiber.StatusNotFound, Code: "import_not_found", Message: fmt.Sprintf("import %s not found", id)}
	}
	return c.Status(fiber.StatusOK).JSON(op)
}

func (s *HTTPServer) getImportEvents(c fiber.Ctx) error {
	id := c.Params("id")
	events, ok := s.service.Events(id)
	if !ok {
		return &apiError{Status: fiber.StatusNotFound, Code: "import_not_found", Message: fmt.Sprintf("import %s not found", id)}
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"import_id": id,
		"events":    events,
	})
}

func requestIDFromCtx(c fiber.Ctx) string {
	if requestID, ok := c.Locals("request_id").(string); ok && requestID != "" {
		return requestID
	}
	return ""
}

func parseOptionalBool(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid boolean value %q", raw)
	}
	return value, nil
}

func stageUploadedFile(fileHeader *multipart.FileHeader) (string, error) {
	src, err := fileHeader.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	ext := filepath.Ext(fileHeader.Filename)
	if ext == "" {
		ext = ".ndjson"
	}
	dst, err := os.CreateTemp("", "arango-fhir-upload-*"+ext)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(dst.Name())
		return "", err
	}
	if err := dst.Close(); err != nil {
		os.Remove(dst.Name())
		return "", err
	}
	return dst.Name(), nil
}
