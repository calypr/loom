package server

import (
	"errors"
	"testing"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func TestClassifyDataframeQueryErrorPreservesArangoMemoryLimit(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError:     true,
		Code:         500,
		ErrorNum:     shared.ErrResourceLimit,
		ErrorMessage: "AQL: query would use more memory than allowed",
	}

	err := classifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("classifyDataframeQueryError() = %v, want dataframe user error", err)
	}
	if userErr.Code() != string(dataframeerrors.CodeQueryMemoryLimitExceeded) {
		t.Fatalf("code = %q, want %q", userErr.Code(), dataframeerrors.CodeQueryMemoryLimitExceeded)
	}
	if got := userErr.Details()["backend"]; got != "arangodb" {
		t.Fatalf("backend = %v, want arangodb", got)
	}
	if !errors.Is(err, driverErr) {
		t.Fatal("classified error did not preserve the driver cause")
	}
}

func TestClassifyDataframeQueryErrorPreservesArangoOutOfMemory(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError:     true,
		Code:         500,
		ErrorNum:     shared.ErrOutOfMemory,
		ErrorMessage: "out of memory",
	}

	err := classifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeQueryBackendOutOfMemory) {
		t.Fatalf("classifyDataframeQueryError() = %#v, want %s", userErr, dataframeerrors.CodeQueryBackendOutOfMemory)
	}
}

func TestClassifyDataframeQueryErrorPreservesOtherArangoResourceLimit(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError:     true,
		Code:         500,
		ErrorNum:     shared.ErrResourceLimit,
		ErrorMessage: "resource limit exceeded",
	}

	err := classifyDataframeQueryError(driverErr)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeQueryResourceLimitExceeded) {
		t.Fatalf("classifyDataframeQueryError() = %#v, want %s", userErr, dataframeerrors.CodeQueryResourceLimitExceeded)
	}
}

func TestClassifyDataframeQueryErrorLeavesOtherFailuresUntouched(t *testing.T) {
	want := errors.New("visitor failed")
	if got := classifyDataframeQueryError(want); !errors.Is(got, want) || got != want {
		t.Fatalf("classifyDataframeQueryError() = %v, want original error", got)
	}
}
