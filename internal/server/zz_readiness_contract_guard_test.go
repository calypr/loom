package server

// Readiness contract guard (PR #148 verifier finding F2): the CI/verifier
// check-2 contract runs this package with a pinned -run list over the
// readiness battery. A pattern that silently matches zero tests (the
// declared 'PostgresReadiness' pattern matched nothing in base, head, or
// history) passes vacuously and cannot adjudicate anything.
//
// Guard shape chosen per the coordinator ruling ("your choice, state it"):
//
//   - every battery leg calls markReadinessLeg(t, name) as its first
//     statement, BEFORE any URL-gated skip, recording invocation in this
//     process;
//   - TestMain enforces, after m.Run(), that a CONTRACT-SCHEDULED run
//     invoked the full battery. A run is contract-scheduled when its
//     -test.run pattern names the whole battery (matches at least two
//     battery names) — a single-leg -run is a developer run and is left
//     alone. Contract-scheduled runs must invoke every pinned leg: a
//     renamed or dropped leg, or a zero-match pattern, forces exit 1
//     instead of a vacuous pass.
//
// Full list is the single source of truth here and must be mirrored in
// the PR body and the check-2 argv. Documented residual: a contract run
// whose pattern matches fewer than two battery names is indistinguishable
// from a developer single-leg run inside the package; the amended argv is
// coordinator-recorded, pinned in the PR body, and re-executed by the
// verifier's registry-fidelity duty on rebind.

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"sync"
	"testing"
)

// readinessBattery is the pinned contract list: the exact names the
// amended check-2 argv must enumerate.
var readinessBattery = []string{
	"TestPostgresRuntimeHealthReadinessIsIndependentAndSchemaAware",
	"TestPostgresRuntimeReadinessReportsNeverMigratedSchema",
	"TestPostgresRuntimeReadinessReportsMissingSchema",
	"TestPostgresRuntimeVerifySchemaIsReadOnly",
	"TestPostgreSQLFirstQueryRouteLivenessAndReadiness",
	"TestHealthWiringPerCompositionArm",
	"TestPostgreSQLCoreReadinessStateClassification",
}

var (
	readinessInvokedMu sync.Mutex
	readinessInvoked   = map[string]bool{}
)

// markReadinessLeg records a battery invocation. Call it as the first
// statement of each leg so URL-gated skips still count as scheduled
// invocations while genuinely renamed/removed functions do not.
func markReadinessLeg(t *testing.T, name string) {
	t.Helper()
	readinessInvokedMu.Lock()
	defer readinessInvokedMu.Unlock()
	readinessInvoked[name] = true
}

func TestMain(m *testing.M) {
	rc := m.Run()
	if fail := readinessContractViolation(); fail != "" {
		fmt.Fprintf(os.Stderr, "readiness contract guard: %s\n", fail)
		if rc == 0 {
			rc = 1
		}
	}
	os.Exit(rc)
}

func readinessContractViolation() string {
	var pattern string
	if f := flag.Lookup("test.run"); f != nil {
		pattern = f.Value.String()
	}
	readinessInvokedMu.Lock()
	defer readinessInvokedMu.Unlock()
	if pattern == "" {
		// Full package run: every leg was scheduled by definition; a
		// missing registration means the function vanished from the
		// package while the contract list still names it.
		pattern = ".*"
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		// m.Run already failed on an invalid pattern; nothing to add.
		return ""
	}
	matched := 0
	for _, name := range readinessBattery {
		if re.MatchString(name) {
			matched++
		}
	}
	if matched < 2 {
		// Developer single-leg (or unrelated) run: not the contract.
		return ""
	}
	var missing []string
	for _, name := range readinessBattery {
		if !readinessInvoked[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Sprintf("contract-scheduled -run %q invoked an incomplete battery: never invoked %v; pinned battery %v — a renamed or dropped leg must fail the contract, not pass vacuously (F2)", pattern, missing, readinessBattery)
	}
	return ""
}
