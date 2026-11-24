package ports

type ErrorClass string

const (
	ClassMalformed     ErrorClass = "malformed"
	ClassForbidden     ErrorClass = "forbidden"
	ClassNotFound      ErrorClass = "not_found"
	ClassConflict      ErrorClass = "conflict"
	ClassUnprocessable ErrorClass = "unprocessable"
	ClassUnavailable   ErrorClass = "unavailable"
	ClassInternal      ErrorClass = "internal"
)

type Error struct {
	Class   ErrorClass
	Stage   string
	Code    string
	Path    string
	Message string
	Details map[string]any
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "Explorer lifecycle error"
	}
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
