package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCommandRoutesExposesOnlyLoadGeneration(t *testing.T) {
	routes := commandRoutes()
	if len(routes) != 1 {
		t.Fatalf("routes = %v, want exactly load-generation", keysOf(routes))
	}
	if _, ok := routes["load-generation"]; !ok {
		t.Fatalf("routes missing load-generation: %v", keysOf(routes))
	}
}

func TestLoadGenerationIsSignalGuarded(t *testing.T) {
	if !signalGuardedCommands()["load-generation"] {
		t.Fatal("load-generation must run under the SIGINT/SIGTERM-cancellable context: it mutates a lifecycle manifest")
	}
	if signalGuardedCommands()["no-such-command"] {
		t.Fatal("signal guard membership test is vacuous")
	}
}

func TestRunUnknownCommandExitsWithUsageCode(t *testing.T) {
	_, stderr := captureOutput(t, func() {
		if code := run(context.Background(), "definitely-not-a-command", nil); code != 2 {
			t.Fatalf("run() exit code = %d, want 2", code)
		}
	})
	if !strings.Contains(stderr, "usage:") {
		t.Fatalf("unknown command must print usage, got %q", stderr)
	}
}

func TestRunTypedBackendRefusalExitsNonzero(t *testing.T) {
	t.Setenv(pgDSNEnv, "")
	stdout, stderr := captureOutput(t, func() {
		code := run(context.Background(), "load-generation", []string{"--project=prj-a", "--generation=gen-a"})
		if code != 1 {
			t.Fatalf("run() exit code = %d, want 1 for a typed refusal", code)
		}
	})
	if !strings.Contains(stderr, ErrBackendRequired.Error()) {
		t.Fatalf("stderr must name the backend refusal, got %q", stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout must stay empty on refusal, got %q", stdout)
	}
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

const dispatchSignalChildEnv = "LOOM_CLI_DISPATCH_SIGNAL_CHILD"
const dispatchSignalReadyEnv = "LOOM_CLI_DISPATCH_SIGNAL_READY"

func TestLoadGenerationSignalCancelsProcessContext(t *testing.T) {
	if os.Getenv(dispatchSignalChildEnv) == "1" {
		readyPath := os.Getenv(dispatchSignalReadyEnv)
		err := withSignalCancellation(context.Background(), func(ctx context.Context) error {
			if err := os.WriteFile(readyPath, []byte("ready"), 0o600); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		})
		if !errors.Is(err, context.Canceled) {
			os.Exit(2)
		}
		os.Exit(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	readyPath := filepath.Join(t.TempDir(), "ready")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLoadGenerationSignalCancelsProcessContext$")
	cmd.Env = append(os.Environ(), dispatchSignalChildEnv+"=1", dispatchSignalReadyEnv+"="+readyPath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal("child did not enter the guarded load context")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("send SIGTERM: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("guarded load did not cleanly observe SIGTERM: %v", err)
	}
}
