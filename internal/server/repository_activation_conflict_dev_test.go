package server

import (
	"flag"
	"strings"
	"testing"
)

func TestDevRepositoryActivationConflictOnceIsNoAuthOnly(t *testing.T) {
	t.Setenv("LOOM_DEV_REPOSITORY_ACTIVATION_CONFLICT_ONCE", "true")

	options, err := parseServerOptions([]string{"--no-auth", "--dataframer-recipe", "recipe.json"}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseServerOptions() error = %v", err)
	}
	if !options.Server.DevRepositoryActivationConflictOnce {
		t.Fatalf("no-auth dev fault knob was not applied: %#v", options.Server)
	}

	secure := DefaultConfig()
	secure.Auth.Basic.Username = "loom"
	secure.Auth.Basic.Password = "secret"
	if err := applyDevFaultOverrides(&secure); err != nil {
		t.Fatalf("authenticated override check = %v", err)
	}
	if secure.Server.DevRepositoryActivationConflictOnce {
		t.Fatalf("authenticated config enabled the repository activation fault: %#v", secure.Server)
	}

	t.Setenv("LOOM_DEV_REPOSITORY_ACTIVATION_CONFLICT_ONCE", "not-a-bool")
	faulty := DefaultConfig()
	faulty.Server.AllowUnauthenticated = true
	faulty.Auth.AllowUnauthenticated = true
	if err := applyDevFaultOverrides(&faulty); err == nil || !strings.Contains(err.Error(), "LOOM_DEV_REPOSITORY_ACTIVATION_CONFLICT_ONCE") {
		t.Fatalf("invalid repository activation fault error = %v", err)
	}
}
