package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/benchmark"
)

// deployer turns CLI flags into driver arm configs. All participating arms
// are either attached (running servers + caller tokens) or spawned
// (binary + benchmark fence); mixing is refused: the attached server's
// trusted issuer cannot be assumed to be the benchmark fence.
type deployer struct {
	flags *runFlags

	fence             *benchmark.MockFence
	unrestrictedToken string
	scopedToken       string

	arango   benchmark.ArmConfig
	postgres benchmark.ArmConfig
}

const (
	benchmarkAdminSubject  = "bench-admin"
	benchmarkScopedSubject = "bench-scoped"
	benchmarkDecoyPath     = "/programs/loom/projects/loom-bench/decoy"
)

func (d *deployer) prepare(ctx context.Context) error {
	f := d.flags
	type legSpec struct {
		backend   string
		attachURL string
		dsn       string
		arm       *benchmark.ArmConfig
	}
	var legs []legSpec
	if f.scope == benchmark.RunScopeBothArms {
		legs = append(legs, legSpec{benchmark.BackendArango, f.arangoAttachURL, f.arangoDSN, &d.arango})
	}
	legs = append(legs, legSpec{benchmark.BackendPostgres, f.postgresAttachURL, f.postgresDSN, &d.postgres})

	allAttached, allSpawned := true, true
	for _, l := range legs {
		if strings.TrimSpace(l.attachURL) == "" {
			allAttached = false
		}
		if strings.TrimSpace(l.attachURL) != "" {
			allSpawned = false
		}
	}
	if !allAttached && !allSpawned {
		return fmt.Errorf("mixing attached and spawned arms is refused: the attached server's trusted issuer is not the benchmark fence")
	}
	if allAttached {
		if strings.TrimSpace(f.tokenUnrestricted) == "" || strings.TrimSpace(f.tokenScoped) == "" {
			return fmt.Errorf("attached arms require --token-unrestricted and --token-scoped")
		}
		d.unrestrictedToken = f.tokenUnrestricted
		d.scopedToken = f.tokenScoped
		for _, l := range legs {
			*l.arm = benchmark.ArmConfig{BaseURL: l.attachURL}
			if l.dsn == "" {
				return fmt.Errorf("%s attached arm still needs --%s-dsn for the live version probe", l.backend, l.backend)
			}
		}
		return nil
	}
	// Spawned: one benchmark fence is the single auth authority.
	if strings.TrimSpace(f.binary) == "" {
		return fmt.Errorf("spawned arms require --binary")
	}
	if strings.TrimSpace(f.tokenUnrestricted) != "" || strings.TrimSpace(f.tokenScoped) != "" {
		return fmt.Errorf("spawned arms receive tokens from the benchmark fence; drop the token flags")
	}
	fence, err := benchmark.NewMockFence()
	if err != nil {
		return fmt.Errorf("benchmark fence: %w", err)
	}
	d.fence = fence
	fence.Grant(benchmarkAdminSubject, f.authResourcePath, benchmarkDecoyPath)
	fence.Grant(benchmarkScopedSubject, benchmarkDecoyPath) // dataset path EXCLUDED: production denial shape
	d.unrestrictedToken = "Bearer " + benchmark.MakeBearerToken(fence.URL(), benchmarkAdminSubject)
	d.scopedToken = "Bearer " + benchmark.MakeBearerToken(fence.URL(), benchmarkScopedSubject)
	for _, l := range legs {
		if strings.TrimSpace(l.dsn) == "" {
			return fmt.Errorf("spawned %s arm requires --%s-dsn", l.backend, l.backend)
		}
		// One signing secret per arm run; scrubbed from arm logs.
		cursorSecret := randomRunIDSuffix() + randomRunIDSuffix()
		var backendYAML string
		switch l.backend {
		case benchmark.BackendPostgres:
			host, port, database, user, password, sslmode, err := parsePostgresDSN(l.dsn)
			if err != nil {
				return fmt.Errorf("%s dsn: %w", l.backend, err)
			}
			backendYAML = benchmark.PostgresBackendYAML(host, port, database, user, password, sslmode, "loom_bench")
		case benchmark.BackendArango:
			rawURL, database, err := parseArangoDSN(l.dsn)
			if err != nil {
				return fmt.Errorf("%s dsn: %w", l.backend, err)
			}
			backendYAML = benchmark.ArangoBackendYAML(rawURL, database)
		}
		*l.arm = benchmark.ArmConfig{
			BinaryPath:   f.binary,
			ConfigYAML:   benchmark.BuildServerConfigYAML("127.0.0.1:0", l.backend, backendYAML, fence.URL(), cursorSecret),
			ScrubSecrets: []string{l.dsn, cursorSecret},
		}
	}
	return nil
}

func (d *deployer) close() {
	if d.fence != nil {
		d.fence.Close()
	}
}

func (d *deployer) probe(ctx context.Context, cfg benchmark.ArmConfig, backend string) (string, error) {
	switch backend {
	case benchmark.BackendArango:
		return probeArangoVersion(ctx, d.flags.arangoDSN)
	case benchmark.BackendPostgres:
		return probePostgresVersion(ctx, d.flags.postgresDSN)
	}
	return "", fmt.Errorf("unknown backend %q", backend)
}
