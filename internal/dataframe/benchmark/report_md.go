package benchmark

import (
	"fmt"
	"sort"
	"strings"
)

// RenderMarkdown turns a verified report into the human-facing report.md.
// It renders only what VerifyReport certified: every number here is
// recomputed evidence, every absence is named.

func RenderMarkdown(report *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Backend benchmark: Arango/AQL vs PostgreSQL/SQL — %s\n\n", report.Identity.RunID)
	fmt.Fprintf(&b, "**Status: %s**\n\n", report.Status)
	if len(report.StatusReasons) > 0 {
		b.WriteString("Reasons:\n\n")
		for _, r := range report.StatusReasons {
			fmt.Fprintf(&b, "- %s\n", r)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Run scope and legs\n\n")
	fmt.Fprintf(&b, "Scope: `%s`\n\n", report.Plan.Scope)
	b.WriteString("| backend | mode | capability |\n|---|---|---|\n")
	for _, leg := range report.Plan.Legs {
		capability := "—"
		if leg.Mode == "not_run" {
			capability = leg.Capability
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", leg.Backend, leg.Mode, capability)
	}
	b.WriteString("\n")
	if len(report.Plan.PlannedNotRun) > 0 {
		b.WriteString("Planned/not_run disclosure rows (never silently skipped):\n\n")
		b.WriteString("| workload | backend | reason |\n|---|---|---|\n")
		rows := append([]PlannedNotRun(nil), report.Plan.PlannedNotRun...)
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].WorkloadID != rows[j].WorkloadID {
				return rows[i].WorkloadID < rows[j].WorkloadID
			}
			return rows[i].Backend < rows[j].Backend
		})
		for _, row := range rows {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", row.WorkloadID, row.Backend, row.Reason)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Identity\n\n")
	fmt.Fprintf(&b, "- Source commit: `%s`\n", report.Identity.SourceCommit)
	fmt.Fprintf(&b, "- Dataset digest (sha256): `%s` (preset `%s`)\n", report.Identity.DatasetDigest, report.Identity.Preset)
	fmt.Fprintf(&b, "- Arango version: %s\n", versionOrAbsent(report, BackendArango))
	fmt.Fprintf(&b, "- PostgreSQL version: %s\n", versionOrAbsent(report, BackendPostgres))
	fmt.Fprintf(&b, "- ClickHouse disabled during runs: %v\n", report.Identity.ClickHouseDisabled)
	fmt.Fprintf(&b, "- Config: warmup=%d iterations=%d concurrency=%d rounds=%d\n\n",
		report.Identity.Config.Warmup, report.Identity.Config.Iterations,
		report.Identity.Config.Concurrency, report.Identity.Config.Rounds)

	b.WriteString("## Measurement controls\n\n")
	fmt.Fprintf(&b, "- Stability: per-cell CV ceiling %.2f; unstable cells excluded from claims", report.Controls.StabilityCVLimit)
	if len(report.Controls.UnstableCells) == 0 {
		b.WriteString(" (none).\n")
	} else {
		fmt.Fprintf(&b, ": %s.\n", strings.Join(report.Controls.UnstableCells, ", "))
	}
	fmt.Fprintf(&b, "- Self-ratio (backend vs itself across halves): arango=%s, postgres=%.3f — each must sit at 1±0.05.\n",
		selfRatioCell(report, BackendArango), report.Controls.SelfRatioPostgres)
	fmt.Fprintf(&b, "- Empty-result floor: identical request returning no rows took %.3f ms (client overhead context).\n", report.Controls.EmptyFloorMillis)
	fmt.Fprintf(&b, "- Ratio policy: %s — speed ratios are computed only for workloads where both arms passed correctness and completed without timeout.\n\n",
		report.Controls.RatioPolicy)

	b.WriteString("## Correctness gates (all must pass before any timing is claimable)\n\n")
	b.WriteString("| workload | arango | postgres | claimable |\n|---|---|---|---|\n")
	gates := append([]WorkloadGateReport(nil), report.GateReports...)
	sort.Slice(gates, func(i, j int) bool { return gates[i].WorkloadID < gates[j].WorkloadID })
	for _, g := range gates {
		fmt.Fprintf(&b, "| %s | %s | %s | %v |\n", g.WorkloadID, g.Arango.Outcome, g.Postgres.Outcome, g.Claimable)
	}
	b.WriteString("\n")

	b.WriteString("## Timings (warm samples only; raw samples in report.json)\n\n")
	b.WriteString("| workload | backend | cache | n | failed | p50 ms | p95 ms |\n|---|---|---|---|---|---|---|\n")
	summaries := append([]SummaryRow(nil), report.Summaries...)
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].WorkloadID != summaries[j].WorkloadID {
			return summaries[i].WorkloadID < summaries[j].WorkloadID
		}
		if summaries[i].Backend != summaries[j].Backend {
			return summaries[i].Backend < summaries[j].Backend
		}
		return summaries[i].Cache < summaries[j].Cache
	})
	for _, row := range summaries {
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d | %.3f | %.3f |\n",
			row.WorkloadID, row.Backend, row.Cache, row.Summary.Count, row.Summary.FailedCount,
			row.Summary.MedianMillis, row.Summary.P95Millis)
	}
	b.WriteString("\n")

	b.WriteString("## Speed claims\n\n")
	if len(report.Claims) == 0 {
		if report.Plan.Scope == RunScopePostgresOnly {
			b.WriteString("None: no cross-backend speed claims are made in postgres-only scope. The Arango leg is CI-targeted (see the leg table).\n\n")
		} else {
			b.WriteString("None passed the ratio-only-both-passed policy.\n\n")
		}
	} else {
		b.WriteString("| workload | arango p50 ms | postgres p50 ms | arango/postgres |\n|---|---|---|---|\n")
		for _, c := range report.Claims {
			fmt.Fprintf(&b, "| %s | %.3f | %.3f | %.3f |\n",
				c.WorkloadID, c.Arango.MedianMillis, c.Postgres.MedianMillis, c.Ratio.ArangoOverPostgres)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Unsupported and exclusions\n\n")
	if len(report.Unsupported) == 0 {
		b.WriteString("No unsupported cases.\n\n")
	} else {
		b.WriteString("Unsupported cases (published, not silently dropped):\n\n")
		for _, u := range report.Unsupported {
			fmt.Fprintf(&b, "- %s\n", u)
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(report.Exclusions) == "" {
		b.WriteString("No workloads excluded from performance claims.\n")
	} else {
		fmt.Fprintf(&b, "%s\n", report.Exclusions)
	}
	return b.String()
}

func versionOrAbsent(report *Report, backend string) string {
	for _, leg := range report.Plan.Legs {
		if leg.Backend == backend && leg.Mode == "not_run" {
			return fmt.Sprintf("not observed (leg not run: %s)", leg.Capability)
		}
	}
	if backend == BackendArango {
		return report.Identity.ArangoVersion
	}
	return report.Identity.PostgresVersion
}

func selfRatioCell(report *Report, backend string) string {
	value := report.Controls.SelfRatioArango
	if backend == BackendPostgres {
		value = report.Controls.SelfRatioPostgres
	}
	for _, leg := range report.Plan.Legs {
		if leg.Backend == backend && leg.Mode == "not_run" {
			return "not run (leg not run)"
		}
	}
	return fmt.Sprintf("%.3f", value)
}
