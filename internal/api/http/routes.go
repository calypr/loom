package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"
)

func (s *HTTPServer) register() {
	s.app.Use(s.requestIDMiddleware, s.recoveryMiddleware, s.loggingMiddleware, s.authenticationMiddleware)
}

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

const (
	readinessCheckBudget  = 2 * time.Second
	readinessCollectGrace = 500 * time.Millisecond
)

var errReadinessCheckTimedOut = errors.New("readiness check exceeded its budget")

func runBoundedCheck(parent context.Context, check func(context.Context) error) <-chan error {
	out := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(parent, readinessCheckBudget)
		defer cancel()
		out <- check(ctx)
	}()
	return out
}

func awaitCheck(parent context.Context, results <-chan error, deadlineAt time.Time) error {
	select {
	case err := <-results:
		return err
	default:
	}
	if parent.Err() != nil {
		return context.Cause(parent)
	}
	remaining := time.Until(deadlineAt)
	if remaining <= 0 {
		return errReadinessCheckTimedOut
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case err := <-results:
		return err
	case <-timer.C:
		return errReadinessCheckTimedOut
	case <-parent.Done():
		return context.Cause(parent)
	}
}

func (s *HTTPServer) checkDependencies(parent context.Context) healthResult {
	result := healthResult{status: "ready", core: "ready", dataframe: "ready", httpStatus: fiber.StatusOK}
	var coreErr, clickHouseErr error
	var coreResults, clickHouseResults <-chan error
	if s.coreReadyCheck != nil {
		coreResults = runBoundedCheck(parent, s.coreReadyCheck)
	}
	if s.clickHouseEnabled && s.clickHouseReadyCheck != nil {
		clickHouseResults = runBoundedCheck(parent, s.clickHouseReadyCheck)
	}
	deadlineAt := time.Now().Add(readinessCheckBudget + readinessCollectGrace)
	if coreResults != nil {
		coreErr = awaitCheck(parent, coreResults, deadlineAt)
	}
	if clickHouseResults != nil {
		clickHouseErr = awaitCheck(parent, clickHouseResults, deadlineAt)
	}
	if coreErr != nil {
		s.logger.Error("core readiness check failed", "error", coreErr)
		result.core = "not_ready"
		if s.coreState != nil {
			result.coreState = s.coreState(coreErr)
		}
	}
	if !s.clickHouseEnabled {
		result.dataframe = "disabled"
	} else if clickHouseErr != nil {
		s.logger.Error("dataframe readiness check failed", "error", clickHouseErr)
		result.dataframe = "backend_unavailable"
	}
	switch {
	case result.core != "ready":
		result.status, result.httpStatus = "not_ready", fiber.StatusServiceUnavailable
	case result.dataframe == "backend_unavailable":
		result.status, result.dataframe = "degraded", "backend_unavailable"
	}
	return result
}

func healthBody(result healthResult) map[string]any {
	body := map[string]any{"status": result.status, "core": result.core}
	if result.dataframe != "" {
		body["dataframe"] = result.dataframe
	}
	if result.coreState != "" {
		body["core_state"] = result.coreState
	}
	return body
}
