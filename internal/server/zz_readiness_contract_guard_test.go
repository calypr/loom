package server

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"sync"
	"testing"
)

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
		pattern = ".*"
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ""
	}
	matched := 0
	for _, name := range readinessBattery {
		if re.MatchString(name) {
			matched++
		}
	}
	if matched < 2 {
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
