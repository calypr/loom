// Authoritative PostgreSQL migration-history ownership record (arch-backend-05).
//
// This file records authority, nothing else. Each production PostgreSQL
// schema has exactly one history owner: the package that embeds the complete
// ordered SQL for that schema's ledger and whose MigrateSchema/OwnerMigrations
// is the only production path that applies it. The runner in migrate.go owns
// the migration protocol; this file names who owns each history's content.
//
// Guarantees this record makes auditable:
//   - Complete: every runtime-needed production history is listed with every
//     version/name/checksum it contains. Test-only assembly (os.DirFS over
//     tests/ or testdata/ fragments) is not a legitimate source of production
//     DDL; fragments listed here live in their owner package's embed.
//   - Immutable: entries restate the applied identity of already-released
//     migrations. A new migration is appended by its owner as a NEW version
//     and appended here in the same change; editing or removing an existing
//     entry here, or its SQL bytes, is refused by the runner's ledger checks
//     and by TestPostgresMigrationOwnershipContract.
//   - Exclusive: ValidateAssignment rejects, before any DDL or connection
//     open, assigning two series whose pinned ledgers disagree at any shared
//     version to one schema. Sharing a database is never treated as
//     cross-domain atomicity: each domain method remains independently
//     atomic.
//
// This is not a migration engine: it executes nothing, discovers nothing, and
// imports no owner package. TestPostgresMigrationOwnershipContract (external
// test package) imports every owner and fails on any drift between this
// record and the owners' embedded bytes.
package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Sentinel error for ownership validation failures.
var (
	// ErrInvalidAssignment reports a schema-ownership assignment this
	// record refuses: an unknown series, a schema name the runner would
	// reject, or two series with conflicting pinned history assigned to
	// one schema.
	ErrInvalidAssignment = errors.New("postgres: invalid migration ownership assignment")
)

// SeriesID names one authoritative migration history (one schema's ledger).
type SeriesID string

// The six production series. The set is closed for production PostgreSQL
// state: a new schema role needs a new series registered here together with
// an owner package that embeds its complete history.
const (
	// SeriesSource is the source/graph store ledger owned by
	// internal/backends/postgres/internal/store (resource, edge, inventory, field profile,
	// batch ledger, generation manifest).
	SeriesSource SeriesID = "source"
	// SeriesExplorerControl is the Explorer control-plane ledger owned by
	// internal/backends/postgres/internal/explorer (owner, drafts, receipts, revisions,
	// capabilities, interpretations, selections, explicit groups).
	SeriesExplorerControl SeriesID = "explorer_control"
	// SeriesPublicationControl is the publication control-plane ledger
	// owned by internal/backends/postgres/internal/publication (bundle catalog,
	// leases, checkpoints, private artifact catalog, read pins).
	SeriesPublicationControl SeriesID = "publication_control"
	// SeriesDatasetRelease is the dataset release/generation lane ledger
	// owned by internal/backends/postgres/internal/dataset (generation manifest, project
	// releases, active-release pointers).
	SeriesDatasetRelease SeriesID = "dataset_release"
	// SeriesRecipeChains is the recipe revision-chain ledger owned by
	// internal/backends/postgres/internal/recipes.
	SeriesRecipeChains SeriesID = "recipe_chains"
	// SeriesRecipeExecRegistries is the persistent recipe registry ledger
	// owned by internal/backends/postgres/internal/recipeexec.
	SeriesRecipeExecRegistries SeriesID = "recipe_exec_registries"
)

// HistoryEntry is one applied-or-pending migration's identity: version,
// name, and the lowercase hex SHA-256 of the exact SQL bytes the ledger
// recorded (or will record) when it was applied.
type HistoryEntry struct {
	Version  int64  `json:"version"`
	Name     string `json:"name"`
	Checksum string `json:"checksum"`
}

// HistoryManifest records one series' authority: its owner package (the
// only place its SQL bytes live and the only production path that applies
// them) and the production stores whose tables that history creates.
type HistoryManifest struct {
	Series       SeriesID       `json:"series"`
	OwnerPackage string         `json:"owner_package"`
	Stores       []string       `json:"stores"`
	Entries      []HistoryEntry `json:"entries"`
}

// manifest is the authoritative record. Keep entries in ascending version
// order matching the owner package's embed.
var manifest = []HistoryManifest{
	{
		Series:       SeriesSource,
		OwnerPackage: "internal/backends/postgres/internal/store",
		Stores: []string{
			"internal/backends/postgres/internal/store",
			"internal/backends/postgres/internal/catalog",
			"internal/ingest (generation load, resource/edge writers)",
			"internal/dataframe/compiler/render/postgres (storage bindings)",
		},
		Entries: []HistoryEntry{
			{1, "ledger", "db70f7758495f7557b3a694149b80ed6e5d4cb74ea5aa7d59a1a2eaa46fc9cde"},
			{2, "fhir_resource", "d6c737db696eea1f9e9b8cd02158d14f98dfae9d9cc1d406b3c03bcc1b9fdaf0"},
			{3, "dataset_generation_manifest", "06c614680a9c1cd4ba2ef0ac0471053bcb3d8e471c36d3852e70682887a72f5d"},
			{4, "edge_occurrence", "aca2b36e1b839919434efb5b8e6c3d1939d7c182d5e15898288ac3f1f0ccb79b"},
			{5, "resource_inventory", "acebb614e6e5c7488f01c2ead47a3389da584bd940d866927816c64fd92e4146"},
			{6, "semantic_inventory_build", "e69d0dfdd3fc67438b65df7da73ea12edb61324ec8356cbdc4abc1ae5172af3c"},
			{7, "field_profile", "202245110193213c9ae5183bfdd3a6856e88d405d26c742d5de686775beae41a"},
			{8, "batch_write_ledger", "cad1877917eb24a7f7d4cb11c578b83f007fff92ceddc3ca79989a71742b05f4"},
			{9, "semantic_inventory_contribution", "41579b3c86d17c5912195ccaa6ff8655e6a864c01a01f2a23fa1cb4aa6883b02"},
		},
	},
	{
		Series:       SeriesExplorerControl,
		OwnerPackage: "internal/backends/postgres/internal/explorer",
		Stores: []string{
			"internal/backends/postgres/internal/explorer (owner, drafts, receipts, revisions, capabilities, interpretations, selections, selection members, explicit groups, publish store)",
		},
		Entries: []HistoryEntry{
			{1, "ledger", "46a99180c6c6c7d41ca13520e020b71827725ec8059cce0142e278fb7577f086"},
			{2, "explorer_owner", "fc85316901b79234695695f972d8abc17d29a726293b0ff6584c96aed1cc7056"},
			{3, "explorer_draft_revision", "4bf4fe35485cb7adc6ea46812b0ae004e89ff935a14f14dd80a300bb3cc86c3f"},
			{4, "capability_artifacts", "bec3e49f897b28ef71d7d69a5f7c71bb11d13b7c631ee26724de36a89d639222"},
			{5, "explorer_compilation_receipt", "f32b3f9c821b5451e64b3d2972fb49e61f4e0c5bfcf88c69bc5b6aa7298fc33a"},
			{6, "explorer_revision", "3f6c44bff72d7837634e8c170f0b97e256e38d35f7c844fad27f4fbda38ecc09"},
			{7, "interpretation_library", "5e308b21a3f567b3066c46458606dd361a9c90646db54ee71618902a544a9ba2"},
			{8, "selection_revisions", "562a61ef33786cf674f1c87552e2dc8e70691b34116b7f415d5f81960ed8d65d"},
			{9, "explorer_explicit_groups", "c635cb5ef7c3c04655b4f45b01ba0fe834a7f181f70638f300878caf16c9a14f"},
			{10, "explorer_explicit_group_memberships", "02e0c9ab8bf9864e7d9c82471eacc7406fe2301b025e459784044c4a44489ccd"},
			{11, "explorer_explicit_group_definition_guard_hardening", "fb45d8f8c312f8f6adb119465da5ed89ac0ddd14929a2c26aa4c917db6d704ec"},
		},
	},
	{
		Series:       SeriesPublicationControl,
		OwnerPackage: "internal/backends/postgres/internal/publication",
		Stores: []string{
			"internal/backends/postgres/internal/publication (bundle registry, leases, read pins)",
			"internal/backends/postgres/internal/chartifact (private artifact catalog)",
		},
		Entries: []HistoryEntry{
			{1, "ledger", "db70f7758495f7557b3a694149b80ed6e5d4cb74ea5aa7d59a1a2eaa46fc9cde"},
			{4, "publication_bundle_catalog", "adcb4683ffd1611b9c68e7a49aaa4b8670d106d65df927cbb8e11866be224943"},
			{6, "publication_lease_checkpoints", "5a2fc578d1b24ee048275b060856d23b7e1a35ff251519bfb46bbc4c1e8a917c"},
			{7, "private_clickhouse_artifacts", "e827a5a1515df903a540a76858da6ad8e5afaede01f5c0b096f9f87a9b662cfb"},
			{9, "publication_read_pins", "d538a21e219f22e5384d43a777c45cd69eec5dcd968f49e5a8ea2b8fddf4f62d"},
		},
	},
	{
		Series:       SeriesDatasetRelease,
		OwnerPackage: "internal/backends/postgres/internal/dataset",
		Stores: []string{
			"internal/backends/postgres/internal/dataset (generation manifest, project releases, active-release pointers)",
			"internal/backends/postgres/internal/explorer (publish store release lane)",
		},
		Entries: []HistoryEntry{
			{1, "ledger", "db70f7758495f7557b3a694149b80ed6e5d4cb74ea5aa7d59a1a2eaa46fc9cde"},
			{3, "dataset_generation_manifest", "06c614680a9c1cd4ba2ef0ac0471053bcb3d8e471c36d3852e70682887a72f5d"},
			{8, "project_release", "1d581ec39731958dd0f79cdab18e92861a380007d3e85d6de4940800378480b1"},
		},
	},
	{
		Series:       SeriesRecipeChains,
		OwnerPackage: "internal/backends/postgres/internal/recipes",
		Stores: []string{
			"internal/backends/postgres/internal/recipes (revision chains)",
		},
		Entries: []HistoryEntry{
			{1, "ledger", "12d5f68a77f1d4e42023a7100355a1568ff870c71644014454067c9df12099dc"},
			{3, "recipe_revision_chains", "a99114804e9ed55de3a0951c7573a6578529a602a166aa5af7a3d0d2dd685059"},
		},
	},
	{
		Series:       SeriesRecipeExecRegistries,
		OwnerPackage: "internal/backends/postgres/internal/recipeexec",
		Stores: []string{
			"internal/backends/postgres/internal/recipeexec (persistent recipe registries)",
		},
		Entries: []HistoryEntry{
			{1, "ledger", "12d5f68a77f1d4e42023a7100355a1568ff870c71644014454067c9df12099dc"},
			{2, "recipe_exec_registries", "f8b6a4317268fc135aa4a301bfa5d9e40da96d7ff8ee2686b5d93fa8d4906c8b"},
		},
	},
}

// Manifest returns a deep copy of the authoritative ownership record, sorted
// by series ID so rendering is deterministic.
func Manifest() []HistoryManifest {
	out := make([]HistoryManifest, 0, len(manifest))
	for _, m := range manifest {
		out = append(out, m.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Series < out[j].Series })
	return out
}

// LookupManifest returns the manifest entry for one series.
func LookupManifest(series SeriesID) (HistoryManifest, bool) {
	for _, m := range manifest {
		if m.Series == series {
			return m.clone(), true
		}
	}
	return HistoryManifest{}, false
}

// ManifestJSON renders the authoritative record as indented JSON for on-demand
// reports. The authored contract stays this file; rendered output is disposable.
func ManifestJSON() ([]byte, error) {
	return json.MarshalIndent(Manifest(), "", "  ")
}

func (m HistoryManifest) clone() HistoryManifest {
	out := m
	out.Stores = append([]string(nil), m.Stores...)
	out.Entries = append([]HistoryEntry(nil), m.Entries...)
	return out
}

// Assignment maps each production series to the schema name a deployment
// assigns it. An absent series is unassigned (the factory supplies defaults).
type Assignment map[SeriesID]string

// AssignmentConflict describes the first shared version at which two series
// pinned to one schema disagree: the same version with different names or
// checksums means one ledger can never validate both histories.
type AssignmentConflict struct {
	Schema    string
	SeriesA   SeriesID
	SeriesB   SeriesID
	Version   int64
	NameA     string
	ChecksumA string
	NameB     string
	ChecksumB string
}

func (c *AssignmentConflict) Error() string {
	return fmt.Sprintf("postgres: series %s and %s cannot share schema %q: version %d is %s checksum %s in %s but %s checksum %s in %s",
		c.SeriesA, c.SeriesB, c.Schema, c.Version, c.NameA, c.ChecksumA[:12], c.SeriesA, c.NameB, c.ChecksumB[:12], c.SeriesB)
}

// Unwrap makes every ownership refusal, including a concrete history
// conflict, detectable with errors.Is(err, ErrInvalidAssignment).
func (c *AssignmentConflict) Unwrap() error { return ErrInvalidAssignment }

// ValidateAssignment validates explicit schema-ownership before any factory
// opens a connection or applies DDL. It rejects unknown series, schema names
// the runner would refuse, and any two distinct series assigned to one schema
// whose pinned entries disagree at a shared version (different name or
// checksum). Series that pin a shared version identically (byte-equal
// immutable SQL) may share a schema; assignment of a single series to
// multiple schemas is not checked here.
func ValidateAssignment(assignment Assignment) error {
	bySchema := map[string][]SeriesID{}
	for series, schema := range assignment {
		if _, ok := LookupManifest(series); !ok {
			return fmt.Errorf("%w: unknown series %q", ErrInvalidAssignment, series)
		}
		if !schemaNamePattern.MatchString(schema) {
			return fmt.Errorf("%w: series %q schema %q must match [a-z_][a-z0-9_]{0,62}",
				ErrInvalidAssignment, series, schema)
		}
		bySchema[schema] = append(bySchema[schema], series)
	}
	schemas := make([]string, 0, len(bySchema))
	for schema := range bySchema {
		schemas = append(schemas, schema)
	}
	sort.Strings(schemas)
	for _, schema := range schemas {
		series := bySchema[schema]
		sort.Slice(series, func(i, j int) bool { return series[i] < series[j] })
		for i := 0; i < len(series); i++ {
			for j := i + 1; j < len(series); j++ {
				if conflict := seriesConflict(schema, series[i], series[j]); conflict != nil {
					return conflict
				}
			}
		}
	}
	return nil
}

// seriesConflict returns the first pinned-version disagreement between two
// series, or nil when their ledgers can coexist on one schema.
func seriesConflict(schema string, a, b SeriesID) *AssignmentConflict {
	ma, okA := LookupManifest(a)
	mb, okB := LookupManifest(b)
	if !okA || !okB {
		return nil // ValidateAssignment already rejects unknown series.
	}
	byVersion := make(map[int64]HistoryEntry, len(ma.Entries))
	for _, e := range ma.Entries {
		byVersion[e.Version] = e
	}
	for _, eb := range mb.Entries {
		if ea, shared := byVersion[eb.Version]; shared && (ea.Name != eb.Name || ea.Checksum != eb.Checksum) {
			return &AssignmentConflict{
				Schema: schema, SeriesA: a, SeriesB: b, Version: eb.Version,
				NameA: ea.Name, ChecksumA: ea.Checksum, NameB: eb.Name, ChecksumB: eb.Checksum,
			}
		}
	}
	return nil
}
