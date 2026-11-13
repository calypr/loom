package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

var (
	ErrInvalidAssignment = errors.New("postgres: invalid migration ownership assignment")
)

type SeriesID string

const (
	SeriesSource               SeriesID = "source"
	SeriesExplorerControl      SeriesID = "explorer_control"
	SeriesPublicationControl   SeriesID = "publication_control"
	SeriesDatasetRelease       SeriesID = "dataset_release"
	SeriesRecipeChains         SeriesID = "recipe_chains"
	SeriesRecipeExecRegistries SeriesID = "recipe_exec_registries"
)

type HistoryEntry struct {
	Version  int64  `json:"version"`
	Name     string `json:"name"`
	Checksum string `json:"checksum"`
}

type HistoryManifest struct {
	Series       SeriesID       `json:"series"`
	OwnerPackage string         `json:"owner_package"`
	Stores       []string       `json:"stores"`
	Entries      []HistoryEntry `json:"entries"`
}

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
			{12, "explorer_revision_activation_revival", "08a61bf6ca66a400112f51463f2c5e1c0a7f4cfbb2d707c0063f87829b1acfe9"},
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

func Manifest() []HistoryManifest {
	out := make([]HistoryManifest, 0, len(manifest))
	for _, m := range manifest {
		out = append(out, m.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Series < out[j].Series })
	return out
}

func LookupManifest(series SeriesID) (HistoryManifest, bool) {
	for _, m := range manifest {
		if m.Series == series {
			return m.clone(), true
		}
	}
	return HistoryManifest{}, false
}

func ManifestJSON() ([]byte, error) {
	return json.MarshalIndent(Manifest(), "", "  ")
}

func (m HistoryManifest) clone() HistoryManifest {
	out := m
	out.Stores = append([]string(nil), m.Stores...)
	out.Entries = append([]HistoryEntry(nil), m.Entries...)
	return out
}

type Assignment map[SeriesID]string

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

func (c *AssignmentConflict) Unwrap() error { return ErrInvalidAssignment }

func ValidateAssignment(assignment Assignment) error {
	bySchema := map[string][]SeriesID{}
	for series, schema := range assignment {
		if _, ok := LookupManifest(series); !ok {
			return fmt.Errorf("%w: unknown series %q", ErrInvalidAssignment, series)
		}
		if !ValidSchemaName(schema) {
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

func seriesConflict(schema string, a, b SeriesID) *AssignmentConflict {
	ma, okA := LookupManifest(a)
	mb, okB := LookupManifest(b)
	if !okA || !okB {
		return nil
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
