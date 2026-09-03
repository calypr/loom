package server

import "github.com/calypr/loom/internal/explorer"

func malformedRouteError(stage string, err error) error {
	return &explorer.AuthoringError{Status: 400, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: "MALFORMED_AUTHORING_REQUEST", JSONPath: "$", Message: err.Error()}, Cause: err}
}

func explorerConflict(stage, code, message string, details map[string]any) error {
	return &explorer.AuthoringError{Status: 409, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: code, Message: message, Details: details}}
}

func explorerUnavailable(stage, code, message string) error {
	return &explorer.AuthoringError{Status: 503, Diagnostic: explorer.AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: code, Message: message}}
}
