package server

import "testing"

func TestRegisterRoutesRequiresExplorerHandlers(t *testing.T) {
	if err := registerRoutes(nil, nil, nil, nil); err == nil || err.Error() != "create Explorer HTTP routes: Explorer handlers are required" {
		t.Fatalf("registerRoutes() error = %v, want missing Explorer handlers", err)
	}
}
