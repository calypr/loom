package control

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
)

type Series string

const (
	SeriesSource               Series = "source"
	SeriesExplorerControl      Series = "explorer_control"
	SeriesPublicationControl   Series = "publication_control"
	SeriesDatasetRelease       Series = "dataset_release"
	SeriesRecipeChains         Series = "recipe_chains"
	SeriesRecipeExecRegistries Series = "recipe_exec_registries"
)

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

type SeriesSchemas map[Series]string

type SetupPolicy string

const (
	SetupMigrate SetupPolicy = "migrate"
	SetupNone    SetupPolicy = "none"
)

const ApplicationNamePrefix = "loom-pg-"

var applicationNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

var assignedSchemaNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

type Config struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	AcquireTimeout  time.Duration
	ApplicationName string
	Schemas         SeriesSchemas
	Setup           SetupPolicy
}

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
	case SetupMigrate, SetupNone:
	default:
		return fmt.Errorf("%w: Setup %q is not one of %q, %q; the schema setup policy must be explicit",
			ErrInvalidConfig, string(c.Setup), SetupMigrate, SetupNone)
	}
	for series, schema := range c.Schemas {
		if strings.TrimSpace(schema) == "" {
			return fmt.Errorf("%w: series %q is assigned a blank schema name", ErrSeriesAssignment, series)
		}
		if !assignedSchemaNamePattern.MatchString(schema) {
			return fmt.Errorf("%w: series %q schema %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchemaName, series, schema)
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
