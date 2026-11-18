package server

import (
	"os"
	"strings"
	"testing"
)

func TestRegisteredRecipeExecutionUsesAuthorizedReader(t *testing.T) {
	source, err := os.ReadFile("wire_http.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "dataframeapi.NewRecipeExecutionReader(releases, scopes, nil)") {
		t.Fatal("HTTP registration no longer constructs the recipe execution reader")
	}
}
