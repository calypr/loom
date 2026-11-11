// Command loom-data is the backend-aware data-plane CLI. It provides one
// explicit generation-load entrypoint that routes by a required backend tag:
//
//	loom-data load-generation --backend arango   # identical contract to
//	                                             # arango-fhir-proto load-generation
//	loom-data load-generation --backend postgres # the pg-ingest-03 lifecycle loader
//
// The backend tag is resolved and validated before any backend-specific
// setting is parsed or opened (the tag-first dispatch law of the PostgreSQL
// command leaf, internal/backends/postgres/internal/rows/rows.go:158-181, and the
// exhaustive physical-backend dispatch of
// internal/dataframe/execution/engine.go:1679-1701). The backend is never
// inferred from a DSN and never falls back to another backend on error
// (internal/server/options.go:403-417 law): a refused invocation is a typed
// sentinel naming the setting, not a heuristic.
//
// The Arango route preserves the existing arango-fhir-proto load-generation
// flag surface, defaults, JSON summary output, and exit codes byte-for-byte;
// cmd/arango-fhir-proto stays unchanged. The PostgreSQL route drives
// ingest.LoadPostgresGeneration, which leaves the generation STAGED and never
// activates it: a successful PostgreSQL load means the manifest was read back
// STAGED-verified by this CLI, and nothing here implies activation —
// activation remains the separate explicit ActivatePostgresGeneration CAS.
//
// Diagnostics on the PostgreSQL route are rendered through one boundary
// redactor so no credential value from a DSN ever reaches stdout or stderr
// (internal/server/options.go:520-551 precedent; the pgx parse-echo path at
// internal/backends/postgres/internal/store/client.go:141-143 makes this a CLI obligation).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	os.Exit(run(context.Background(), os.Args[1], os.Args[2:]))
}

// run is the exit-code-deciding seam shared by main() and its tests so that
// command wiring stays observable without executing os.Exit. Exit codes
// mirror cmd/arango-fhir-proto: 2 for missing/unknown commands (usage), 1 for
// a handler error, 0 on success. Flag parse errors keep the flag package's
// own exit code 2 through flag.ExitOnError inside the handlers, exactly as
// the Arango CLI does (cmd/arango-fhir-proto/main.go:38-63).
func run(ctx context.Context, command string, args []string) int {
	handler, ok := commandRoutes()[command]
	if !ok {
		usage()
		return 2
	}
	var err error
	if signalGuardedCommands()[command] {
		err = withSignalCancellation(ctx, func(signalCtx context.Context) error {
			return handler(signalCtx, args)
		})
	} else {
		err = handler(ctx, args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// commandRoutes is the single dispatch table. load-generation mutates a
// dataset lifecycle manifest, so PostgreSQL requests are routed to the
// completed pg-ingest lifecycle loader and Arango requests to the existing
// implementation; one tag selects exactly one loader and the tag is never
// guessed from connection settings.
func commandRoutes() map[string]func(context.Context, []string) error {
	return map[string]func(context.Context, []string) error{
		"load-generation": func(ctx context.Context, args []string) error {
			return dispatchLoadGeneration(ctx, args, defaultRunner())
		},
	}
}

// signalGuardedCommands lists the commands that mutate a dataset lifecycle
// manifest or an immutable generation. A cancelled run there can leave a
// half-mutated manifest, so run() executes them under a context that cancels
// on SIGINT/SIGTERM, mirroring cmd/arango-fhir-proto/main.go:82-99.
func signalGuardedCommands() map[string]bool {
	return map[string]bool{
		"load-generation": true,
	}
}

func withSignalCancellation(ctx context.Context, run func(context.Context) error) error {
	signalCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return run(signalCtx)
}

// usage names both explicit backend forms and states the PostgreSQL
// STAGED-not-activated boundary explicitly: the user-visible contract must
// never imply that a successful PostgreSQL load activated anything.
func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  loom-data load-generation --backend arango --generation OPAQUE_ID [flags]
      # immutable complete META directory load into ArangoDB, identical flags,
      #   output, and exit contract to arango-fhir-proto load-generation
  loom-data load-generation --backend postgres --generation OPAQUE_ID --project PROJECT --pg-schema SCHEMA (--pg-dsn DSN | LOOM_DATA_PG_DSN) [flags]
      # one bounded PostgreSQL generation load; on success the generation is
      #   STAGED (verified) and NOT activated — activation is a separate
      #   explicit operation, never performed by this command
  --backend is required and must be arango or postgres; it is never inferred
    from a DSN, and a load never falls back to another backend.
`)
}

// printJSON is the single-stdout-line summary contract, identical to
// cmd/arango-fhir-proto/main.go:754-761.
func printJSON(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
