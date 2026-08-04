package httpapi

import (
	"errors"
	"time"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

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
	s.logger.Info("http request", "request_id", requestIDFromCtx(c), "method", c.Method(), "path", c.Path(), "status", c.Response().StatusCode(), "duration_ms", time.Since(start).Milliseconds())
	return err
}

func (s *HTTPServer) authenticationMiddleware(c fiber.Ctx) error {
	// Health probes must remain available before credentials are configured and
	// are deliberately not project/data APIs.
	if c.Path() == "/health" {
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
