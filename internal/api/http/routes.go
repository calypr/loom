package httpapi

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v3"
)

func (s *HTTPServer) register() {
	s.app.Use(s.requestIDMiddleware, s.recoveryMiddleware, s.loggingMiddleware, s.authenticationMiddleware)
}

// Health computes the cached dependency health response for generated HTTP
// routes without requiring a Fiber context.
func (s *HTTPServer) Health(parent context.Context) (map[string]any, int) {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	if time.Since(s.lastHealth) < 30*time.Second {
		return healthBody(s.lastHealthResult), s.lastHealthResult.httpStatus
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	result := s.checkDependencies(ctx)
	s.lastHealth, s.lastHealthResult = time.Now(), result
	return healthBody(result), result.httpStatus
}

func (s *HTTPServer) Liveness(context.Context) (map[string]any, int) {
	return map[string]any{"status": "live"}, fiber.StatusOK
}

func (s *HTTPServer) Readiness(parent context.Context) (map[string]any, int) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	result := s.checkDependencies(ctx)
	if result.core != "ready" || result.dataframe == "backend_unavailable" {
		result.status = "not_ready"
		result.httpStatus = fiber.StatusServiceUnavailable
	}
	return healthBody(result), result.httpStatus
}

func (s *HTTPServer) checkDependencies(ctx context.Context) healthResult {
	result := healthResult{status: "ready", core: "ready", dataframe: "ready", httpStatus: fiber.StatusOK}
	if s.coreReadyCheck != nil {
		if err := s.coreReadyCheck(ctx); err != nil {
			s.logger.Error("core readiness check failed", "error", err)
			return healthResult{status: "not_ready", core: "not_ready", httpStatus: fiber.StatusServiceUnavailable}
		}
	}
	if !s.clickHouseEnabled {
		result.dataframe = "disabled"
	}
	if s.clickHouseEnabled && s.clickHouseReadyCheck != nil {
		if err := s.clickHouseReadyCheck(ctx); err != nil {
			s.logger.Error("dataframe readiness check failed", "error", err)
			result.status, result.dataframe = "degraded", "backend_unavailable"
		}
	}
	return result
}

func healthBody(result healthResult) map[string]any {
	body := map[string]any{"status": result.status, "core": result.core}
	if result.dataframe != "" {
		body["dataframe"] = result.dataframe
	}
	return body
}
