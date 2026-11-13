// Command benchcli is the benchmark driver/verifier entry point the node
// orchestrator invokes. It has exactly two jobs: run a live matched
// benchmark, and verify a report.json artifact. Verification authority lives
// here in Go; node unit tests can never satisfy the live checks (ruling ii).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/calypr/loom/internal/dataframe/benchmark"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		reportPath := fs.String("report", "", "path to report.json")
		_ = fs.Parse(os.Args[2:])
		if *reportPath == "" {
			fmt.Fprintln(os.Stderr, "benchcli verify: --report is required")
			os.Exit(2)
		}
		data, err := os.ReadFile(*reportPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "benchcli verify: %v\n", err)
			os.Exit(1)
		}
		report, err := benchmark.LoadReport(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "benchcli verify: REJECTED: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("benchcli verify: OK run=%s status=%s claims=%d unsupported=%d\n",
			report.Identity.RunID, report.Status, len(report.Claims), len(report.Unsupported))
	case "run":
		// Fails closed until the exclusive window: no partial-environment
		// run may produce a success-shaped artifact. The granted-window
		// wave adds preflight (services reachable, budget, namespace
		// guards) and the live driver here.
		if os.Getenv("LOOM_BENCH_LIVE") != "1" {
			fmt.Fprintln(os.Stderr, "benchcli run: BLOCKED — live driver requires the granted exclusive window (LOOM_BENCH_LIVE=1) and is not yet wired")
			os.Exit(3)
		}
		fmt.Fprintln(os.Stderr, "benchcli run: BLOCKED — live driver not yet wired (lands with the granted PG window wave)")
		os.Exit(3)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: benchcli verify --report <report.json> | benchcli run")
}
