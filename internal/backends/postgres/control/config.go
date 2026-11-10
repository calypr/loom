package control

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
)

// Series names one authoritative PostgreSQL migration history the factory
// assigns to a schema. The six values must stay exactly equal to the
// arch-backend-05 manifest (internal/backends/postgres/internal/store/migration_ownership.go);
// the ownership contract test asserts set equality against the manifest, so
// drift here fails the build's named test.
type Series string

const (
	SeriesSource               Series = "source"
	SeriesExplorerControl      Series = "explorer_control"
	SeriesPublicationControl   Series = "publication_control"
	SeriesDatasetRelease       Series = "dataset_release"
	SeriesRecipeChains         Series = "recipe_chains"
	SeriesRecipeExecRegistries Series = "recipe_exec_registries"
)

// AllSeries lists every series the factory can assign, in manifest order.
func AllSeries() []Series {
	return []Series{
		SeriesSource,
		SeriesExplorerControl,
		SeriesPublicationControl,
		SeriesDatasetRelease,
		SeriesRecipeChains,
		SeriesRecipeExecRegistries,
	}
}

// SeriesSchemas maps each assigned series to the schema name a deployment
// gives it. An absent series is unassigned; the factory supplies defaults
// only where the composition explicitly permits them.
type SeriesSchemas map[Series]string

// SetupPolicy is the approved schema setup policy, required explicitly so no
// deployment silently creates or silently skips DDL.
type SetupPolicy string

const (
	// SetupMigrate applies each series' owner migrations into its assigned
	// schema (idempotent, advisory-locked through the store). Schemas are
	// caller-owned: the migrate runner refuses an absent or unassigned
	// schema with the named invalid-schema error and never creates one, so
	// the caller must create the schema itself before Open under this
	// policy.
	SetupMigrate SetupPolicy = "migrate"
	// SetupVerify converges each assigned schema onto its series' migrated
	// state through the same idempotent, advisory-locked runner as
	// SetupMigrate: pending migrations are applied, a schema the runner has
	// already validated is a no-op, and an absent or unassigned schema is
	// refused (schemas stay caller-owned, as under SetupMigrate).
	SetupVerify SetupPolicy = "verify"
	// SetupNone performs no schema setup at all: the caller has arranged
	// (or does not need) schema state, matching the historical bare-pool
	// open. Schema-name assignment validation and control-plane assembly
	// still run; only the migrate/verify pass is skipped.
	SetupNone SetupPolicy = "none"
)

// ApplicationNamePrefix tags every connection this backend's pool opens so
// operators and contract tests can witness exactly which connections a
// runtime owns (mirrors the loom-ing03-<nonce> convention proven by
// pg-ingest-03). The empty Config.ApplicationName selects a generated
// loom-pg-<nonce> name at Open.
const ApplicationNamePrefix = "loom-pg-"

// applicationNamePattern is a libpq-safe identifier: no whitespace, quotes,
// or separators that could smuggle additional connection parameters.
var applicationNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// Config is the fully explicit factory configuration. Pool bounds and DSN
// shape are validated by the store's authoritative Config.Validate (no
// second policy); the factory adds the connection-tag policy, the explicit
// setup policy, and the series-assignment gate delegated to the store's
// ValidateAssignment before any pool exists.
type Config struct {
	// DSN is the PostgreSQL connection string (URL or keyword/value form).
	DSN string
	// MaxConns bounds the pooled connections. Must be >= 1.
	MaxConns int32
	// MinConns is the number of idle connections kept warm. Must satisfy
	// 0 <= MinConns <= MaxConns.
	MinConns int32
	// AcquireTimeout bounds waiting for a free connection. Must be positive.
	AcquireTimeout time.Duration
	// ApplicationName tags this runtime's backends in pg_stat_activity.
	// Empty selects a generated loom-pg-<nonce> name at Open; a set name
	// must carry the loom-pg- prefix and libpq-safe characters.
	ApplicationName string
	// Schemas assigns each migration series to a schema name.
	Schemas SeriesSchemas
	// Setup is the approved schema setup policy; the zero value is refused.
	Setup SetupPolicy
}

// Validate checks everything reachable without a network connection or the
// driver: pool bounds via the store authority, the connection-tag policy,
// the explicit setup policy, and the series assignment via the store's
// authoritative ValidateAssignment (unknown series, runner-refused schema
// names, and shared-schema ledger conflicts all fail here, before any pool).
// Driver-level DSN parse failures surface at Open with ErrInvalidConfig in
// the chain.
func (c Config) Validate() error {
	storeCfg := pgstore.Config{
		DSN:            c.DSN,
		MaxConns:       c.MaxConns,
		MinConns:       c.MinConns,
		AcquireTimeout: c.AcquireTimeout,
	}
	if err := storeCfg.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	if c.ApplicationName != "" {
		if !strings.HasPrefix(c.ApplicationName, ApplicationNamePrefix) {
			return fmt.Errorf("%w: ApplicationName %q must carry the own-prefix %q so connection witnesses stay unambiguous",
				ErrInvalidConfig, c.ApplicationName, ApplicationNamePrefix)
		}
		if !applicationNamePattern.MatchString(c.ApplicationName) {
			return fmt.Errorf("%w: ApplicationName %q must match %s", ErrInvalidConfig, c.ApplicationName, applicationNamePattern)
		}
	}
	switch c.Setup {
	case SetupMigrate, SetupVerify, SetupNone:
	default:
		return fmt.Errorf("%w: Setup %q is not one of %q, %q; the schema setup policy must be explicit",
			ErrInvalidConfig, string(c.Setup), SetupMigrate, SetupVerify)
	}
	for series, schema := range c.Schemas {
		if strings.TrimSpace(schema) == "" {
			return fmt.Errorf("%w: series %q is assigned a blank schema name", ErrSeriesAssignment, series)
		}
	}
	assignment := pgstore.Assignment{}
	for series, schema := range c.Schemas {
		assignment[pgstore.SeriesID(series)] = schema
	}
	if err := pgstore.ValidateAssignment(assignment); err != nil {
		return fmt.Errorf("%w: %w", ErrSeriesAssignment, err)
	}
	return nil
}
