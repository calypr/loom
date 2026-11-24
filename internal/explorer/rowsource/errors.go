package rowsource

import (
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
)

func unavailable(stage, code, message string, cause error) error {
	return support.Unavailable(stage, code, message, cause)
}

func conflict(code, message string, cause error) error {
	return support.Conflict("construction-proposal", code, message, nil, cause)
}

func unprocessable(code, message string, cause error) error {
	return support.Unprocessable("construction-proposal", code, message, cause)
}
