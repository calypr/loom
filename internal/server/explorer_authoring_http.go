package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func decodeAuthoringStrict(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func authoringRead(c fiber.Ctx, authorizeRead explorerConfigReadAuthorizer) error {
	if err := authorizeRead(c.Context(), principalFromFiber(c), explorerProjectParam(c)); err != nil {
		return authoringHTTPError(c, &explorer.AuthoringError{Status: 403, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: "FORBIDDEN", Message: "forbidden"}, Cause: err})
	}
	return nil
}

func authoringWrite(c fiber.Ctx, authorizer authscope.Authorizer) error {
	project := explorerProjectParam(c)
	path := authscope.NormalizeAuthResourcePath(strings.TrimSpace(c.Query("auth_resource_path")))
	if err := authorizer.AuthorizeWrite(c.Context(), principalFromFiber(c), project, path); err != nil {
		return authoringHTTPError(c, &explorer.AuthoringError{Status: 403, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: "authorization", Code: "FORBIDDEN", Message: "forbidden"}, Cause: err})
	}
	return nil
}

func requestIDFromFiber(c fiber.Ctx) string {
	value, _ := c.Locals("request_id").(string)
	return value
}

func malformedRouteError(stage string, err error) error {
	return &explorer.AuthoringError{Status: 400, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: "MALFORMED_AUTHORING_REQUEST", JSONPath: "$", Message: err.Error()}, Cause: err}
}

func explorerConflict(stage, code, message string, details map[string]any) error {
	return &explorer.AuthoringError{Status: 409, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: code, Message: message, Details: details}}
}

func explorerUnavailable(stage, code, message string) error {
	return &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: code, Message: message}}
}

func authoringHTTPError(c fiber.Ctx, err error) error {
	requestID := requestIDFromFiber(c)
	var applicationErr *lifecycle.Error
	if errors.As(err, &applicationErr) {
		status := lifecycleErrorStatus(applicationErr.Class)
		details := applicationErr.Details
		if details == nil {
			details = map[string]any{}
		}
		logAuthoringRequestFailure(c, requestID, status, applicationErr.Stage, applicationErr.Code, applicationErr.Path, applicationErr.Message, applicationErr.Details, applicationErr.Cause)
		diagnostic := fiber.Map{"severity": "error", "stage": applicationErr.Stage, "code": applicationErr.Code, "path": applicationErr.Path, "message": applicationErr.Message, "requestId": requestID}
		return c.Status(status).JSON(fiber.Map{"code": applicationErr.Code, "message": applicationErr.Message, "diagnostics": []fiber.Map{diagnostic}, "requestId": requestID, "details": details})
	}
	var value *explorer.AuthoringError
	if errors.As(err, &value) {
		value.Diagnostic.RequestID = requestID
		cause := value.Cause
		if cause == nil {
			cause = err
		}
		logAuthoringRequestFailure(c, requestID, value.Status, value.Diagnostic.Stage, value.Diagnostic.Code, value.Diagnostic.JSONPath, value.Diagnostic.Message, value.Diagnostic.Details, cause)
		details := value.Diagnostic.Details
		if details == nil {
			details = map[string]any{}
		}
		diagnostic := fiber.Map{"severity": strings.ToLower(value.Diagnostic.Severity), "stage": value.Diagnostic.Stage, "code": value.Diagnostic.Code, "path": value.Diagnostic.JSONPath, "message": value.Diagnostic.Message, "requestId": requestID}
		return c.Status(value.Status).JSON(fiber.Map{"code": value.Diagnostic.Code, "message": value.Diagnostic.Message, "diagnostics": []fiber.Map{diagnostic}, "requestId": requestID, "details": details})
	}
	if errors.Is(err, explorer.ErrNotFound) {
		return c.Status(404).JSON(fiber.Map{"code": "NOT_FOUND", "message": "Explorer resource not found", "diagnostics": []any{}, "requestId": requestID, "details": fiber.Map{}})
	}
	logAuthoringRequestFailure(c, requestID, 500, "internal", "INTERNAL_ERROR", "", "internal server error", nil, err)
	return c.Status(500).JSON(fiber.Map{"code": "INTERNAL_ERROR", "message": "internal server error", "diagnostics": []any{}, "requestId": requestID, "details": fiber.Map{}})
}

func logAuthoringRequestFailure(c fiber.Ctx, requestID string, status int, stage, code, jsonPath, message string, details map[string]any, cause error) {
	attrs := []any{
		"request_id", requestID,
		"method", c.Method(),
		"path", c.Path(),
		"status", status,
		"project", explorerProjectParam(c),
		"explorer_id", strings.TrimSpace(c.Params("explorerId")),
		"stage", stage,
		"code", code,
		"json_path", jsonPath,
		"message", message,
		"details", details,
	}
	if cause != nil {
		attrs = append(attrs, "cause", cause, "cause_type", fmt.Sprintf("%T", cause))
	}
	slog.Error("Explorer authoring request failed", attrs...)
}

func decodeStrict(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func principalFromFiber(c fiber.Ctx) *authscope.Principal {
	principal, _ := c.Locals("principal").(*authscope.Principal)
	return principal
}

func subjectFromFiber(c fiber.Ctx) string {
	if principal := principalFromFiber(c); principal != nil {
		return principal.Subject
	}
	return ""
}

func authorizeExplorerWrite(c fiber.Ctx, authorizer authscope.Authorizer, project string) error {
	path := authscope.NormalizeAuthResourcePath(strings.TrimSpace(c.Query("auth_resource_path")))
	if err := authorizer.AuthorizeWrite(c.Context(), principalFromFiber(c), project, path); err != nil {
		return explorerV2Error(c, 403, "FORBIDDEN", "forbidden")
	}
	return nil
}

func explorerV2Error(c fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(fiber.Map{"error": fiber.Map{"code": code, "message": message}})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "Explorer"
}
