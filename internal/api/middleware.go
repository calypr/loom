package api

import (
	"errors"
	"time"

	"github.com/calypr/loom/internal/authscope"

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
	c.SetContext(authscope.ContextWithPrincipal(c.Context(), principal))
	return c.Next()
}

func requestIDFromCtx(c fiber.Ctx) string {
	if requestID, ok := c.Locals("request_id").(string); ok && requestID != "" {
		return requestID
	}
	return ""
}
