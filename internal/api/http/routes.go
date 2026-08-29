package httpapi

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v3"
)

func (s *HTTPServer) register() {
	s.app.Use(s.requestIDMiddleware, s.recoveryMiddleware, s.loggingMiddleware, s.authenticationMiddleware)
}

func (s *HTTPServer) HandleHealth(c fiber.Ctx) error {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	if time.Since(s.lastHealth) < 30*time.Second {
		return s.writeHealth(c, s.lastHealthResult)
	}
	ctx, cancel := context.WithTimeout(c.Context(), 2*time.Second)
	defer cancel()
	result := s.checkDependencies(ctx)
	s.lastHealth, s.lastHealthResult = time.Now(), result
	return s.writeHealth(c, result)
}

func (s *HTTPServer) HandleLiveness(c fiber.Ctx) error {
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"status": "live"})
}

func (s *HTTPServer) HandleReadiness(c fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 2*time.Second)
	defer cancel()
	result := s.checkDependencies(ctx)
	if result.core != "ready" || result.dataframe == "backend_unavailable" {
		result.status = "not_ready"
		result.httpStatus = fiber.StatusServiceUnavailable
	}
	return s.writeHealth(c, result)
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

func (s *HTTPServer) writeHealth(c fiber.Ctx, result healthResult) error {
	body := fiber.Map{"status": result.status, "core": result.core}
	if result.dataframe != "" {
		body["dataframe"] = result.dataframe
	}
	return c.Status(result.httpStatus).JSON(body)
}
