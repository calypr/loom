package explorer

func MalformedRouteError(stage string, err error) error {
	return &AuthoringError{Status: 400, Diagnostic: AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: "MALFORMED_AUTHORING_REQUEST", JSONPath: "$", Message: err.Error()}, Cause: err}
}

func ExplorerConflict(stage, code, message string, details map[string]any) error {
	return &AuthoringError{Status: 409, Diagnostic: AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: code, Message: message, Details: details}}
}

func ExplorerUnavailable(stage, code, message string) error {
	return &AuthoringError{Status: 503, Diagnostic: AuthoringDiagnostic{Severity: "ERROR", Stage: stage, Code: code, Message: message}}
}
