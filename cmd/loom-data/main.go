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

func commandRoutes() map[string]func(context.Context, []string) error {
	return map[string]func(context.Context, []string) error{
		"load-generation": func(ctx context.Context, args []string) error {
			return dispatchLoadGeneration(ctx, args, defaultRunner())
		},
	}
}

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

func printJSON(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
