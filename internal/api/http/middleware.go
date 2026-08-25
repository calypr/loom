package httpapi

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

const maxLoggedErrorResponseBytes = 32 * 1024

// errorResponseLogAttrs extracts the useful, server-owned diagnostic fields
// from responses that were already written by a handler. Authoring routes
// intentionally write their 4xx response and return nil, so the Fiber error
// handler cannot log the underlying diagnostic. Keeping this extraction in
// the request middleware gives every route the same failure visibility while
// bounding the raw packet retained in logs.
func errorResponseLogAttrs(body []byte) []any {
	if len(body) == 0 {
		return nil
	}
	logged := body
	truncated := false
	if len(logged) > maxLoggedErrorResponseBytes {
		logged = logged[:maxLoggedErrorResponseBytes]
		truncated = true
	}
	attrs := []any{"response_body", string(logged)}
	if truncated {
		attrs = append(attrs, "response_body_truncated", true, "response_body_bytes", len(body))
	}

	var envelope struct {
		Error struct {
			Code       string          `json:"code"`
			Message    string          `json:"message"`
			RequestID  string          `json:"requestId"`
			Diagnostic json.RawMessage `json:"diagnostic"`
		} `json:"error"`
		Diagnostics json.RawMessage `json:"diagnostics"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return attrs
	}
	if envelope.Error.Code != "" {
		attrs = append(attrs, "error_code", envelope.Error.Code)
	}
	if envelope.Error.Message != "" {
		attrs = append(attrs, "error_message", envelope.Error.Message)
	}
	if envelope.Error.RequestID != "" {
		attrs = append(attrs, "error_request_id", envelope.Error.RequestID)
	}
	if len(envelope.Error.Diagnostic) > 0 && string(envelope.Error.Diagnostic) != "null" {
		attrs = append(attrs, "error_diagnostic", string(envelope.Error.Diagnostic))
	}
	if len(envelope.Diagnostics) > 0 && string(envelope.Diagnostics) != "null" {
		attrs = append(attrs, "error_diagnostics", string(envelope.Diagnostics))
	}
	return attrs
}

func (s *HTTPServer) requestIDMiddleware(c fiber.Ctx) error {
	requestID := c.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.NewString()
	}
	c.Locals("request_id", requestID)
	c.Set("X-Request-ID", requestID)
	c.SetContext(ContextWithRequestID(c.Context(), requestID))
	return c.Next()
}

func (s *HTTPServer) recoveryMiddleware(c fiber.Ctx) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Error("panic recovered", "request_id", requestIDFromCtx(c), "path", c.Path(), "panic", recovered)
			err = dataframeerrors.Wrap(errors.New("panic recovered"), dataframeerrors.CodeInternalError, "")
		}
	}()
	return c.Next()
}

func (s *HTTPServer) loggingMiddleware(c fiber.Ctx) error {
	start := time.Now()
	err := c.Next()
	if err != nil {
		if c.Response().StatusCode() < 400 {
			c.Status(MapDataframeError(err, requestIDFromCtx(c)).Status)
		}
	}
	status := c.Response().StatusCode()
	duration := time.Since(start).Milliseconds()
	if status >= 400 {
		attrs := []any{
			"request_id", requestIDFromCtx(c),
			"method", c.Method(),
			"path", c.Path(),
			"url", c.OriginalURL(),
			"status", status,
			"duration_ms", duration,
		}
		attrs = append(attrs, errorResponseLogAttrs(c.Response().Body())...)
		if err != nil {
			attrs = append(attrs, "handler_error", err)
		}
		s.logger.Error("http request failed", attrs...)
	}
	s.logger.Info("http request", "request_id", requestIDFromCtx(c), "method", c.Method(), "path", c.Path(), "status", status, "duration_ms", duration)
	return err
}

func (s *HTTPServer) authenticationMiddleware(c fiber.Ctx) error {
	// Health probes must remain available before credentials are configured and
	// are deliberately not project/data APIs.
	switch c.Path() {
	case "/health", "/livez", "/readyz":
		return c.Next()
	}
	principal, err := s.authn.Authenticate(c.Context(), c.GetReqHeaders())
	if err != nil {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeUnauthenticated, "")
	}
	c.Locals("principal", principal)
	c.SetContext(authscope.ContextWithPrincipal(c.Context(), principal))
	return c.Next()
}

func requestIDFromCtx(c fiber.Ctx) string {
	if requestID, ok := c.Locals("request_id").(string); ok && requestID != "" {
		return requestID
	}
	return ""
}
