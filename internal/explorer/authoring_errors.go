package explorer

type AuthoringDiagnostic struct {
	Severity  string         `json:"severity"`
	Stage     string         `json:"stage"`
	Code      string         `json:"code"`
	JSONPath  string         `json:"jsonPath"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"requestId,omitempty"`
}

type AuthoringError struct {
	Status     int
	Diagnostic AuthoringDiagnostic
	Cause      error
}

func (e *AuthoringError) Error() string {
	if e == nil {
		return "Explorer authoring error"
	}
	return e.Diagnostic.Code + ": " + e.Diagnostic.Message
}

func (e *AuthoringError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
