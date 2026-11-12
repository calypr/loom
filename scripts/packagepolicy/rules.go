package packagepolicy

import (
	_ "embed"
	"strings"
)

// The authoritative boundary rule table (arch-backend-09). This is the ONE
// place these rules are stated; the CLI, the shell entrypoint, and both
// named Go guard tests all consume this table. Per-rule Owner text is the
// "allowed owner" guidance CI must print next to each offending import
// (acceptance: "CI rejects the demonstrated historical ownership leaks with
// a clear offending import and allowed owner").

const module = "github.com/calypr/loom/"

const (
	labelCompilerProduction = "compiler-production: no backend implementations or database drivers"
	labelNeutralDomains     = "neutral-domains: no backend implementations or database drivers"
	labelRecipeDomain       = "stored-recipe-domain: persistence-free"
	labelPublicationPlane   = "publication-plane: independent of dataframe orchestration"
	labelAuthoringIntent    = "authoring-intent: no orchestration or storage"
	labelIRLeaf             = "ir-leaf: no lowering, optimization, rendering, or runtime"
	labelOptimizer          = "optimizer: rewrites IR only"
	labelSemantic           = "semantic: no physical compiler implementation"
	labelSpec               = "spec: leaf vocabulary"
	labelRenderAQL          = "render-aql: no lowerer, optimizer, runtime, backends, or drivers"
	labelRenderPostgres     = "render-postgres: no runtime, backends, stores, or drivers"
	labelCommandLeaf        = "command-leaf: standard-library only"
	labelLifecycle          = "explorer-lifecycle: no adapter or driver policy types"
	labelOldConcretePaths   = "old-concrete-paths: relocated under internal/backends by arch-backend-06/07"
	labelControlCycleFree   = "postgres-control: cycle-free control plane"
	labelRootNoReExport     = "postgres-root: no control symbol re-exports"
)

// backendImports is the shared forbidden set for every tree that must stay
// vendor-free: the two backend ownership trees and both database drivers.
var backendImports = Forbidden{
	Prefix: []string{
		module + "internal/backends/",
		"github.com/jackc/pgx",
		"github.com/arangodb",
	},
}

// oldConcreteImportPaths are the pre-refactor concrete package import paths
// (the "bolt-on" layout), kept as guard data in old_concrete_paths.txt:
// the retired-adapter text guard forbids citing these paths even in Go
// comments, so the table lives outside .go text and is embedded. The
// guard still matches every path; only its storage moved. The
// old_concrete_paths_are_the_table control pins the data file against
// accidental drift.
//
//go:embed old_concrete_paths.txt
var oldConcretePathsData string

var oldConcreteImportPaths = parsePathTable(oldConcretePathsData)

// parsePathTable reads one path per line, ignoring blank lines and
// #-comments. An empty table is a build-time panic, never a silent
// zero-path rule.
func parsePathTable(data string) []string {
	var out []string
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		panic("packagepolicy: old_concrete_paths.txt carries no paths")
	}
	return out
}

// OldConcreteImportPaths exposes the retired-path table so witness
// fixtures derive their planted imports from the authoritative data
// instead of restating (and thereby citing) retired paths in Go source.
func OldConcreteImportPaths() []string {
	return append([]string{}, oldConcreteImportPaths...)
}

// RepoRules is the authoritative parsed-import rule table.
func RepoRules() []Rule {
	return []Rule{
		{
			Label:     labelCompilerProduction,
			Owner:     "compiler owns IR and the renderers only; the only vendor-adjacent edge is adapter -> compiler/command. Concrete implementations live under internal/backends/{arango,postgres}/internal.",
			Targets:   []string{"internal/dataframe/compiler"},
			Recursive: true,
			Scope:     Production,
			Forbidden: backendImports,
		},
		{
			Label: labelNeutralDomains,
			Owner: "domain contracts and shared execution are backend-neutral; consume the ports returned by internal/backends/{arango,postgres}.Open. Only approved composition (internal/server compose, cmd) imports the backend factories.",
			Targets: []string{
				"internal/catalog",
				"internal/dataset",
				"internal/explorer",
				"internal/source",
				"internal/dataframe/execution",
			},
			Recursive: true,
			Scope:     Production,
			Forbidden: backendImports,
		},
		{
			Label:     labelRecipeDomain,
			Owner:     "the stored recipe domain carries no persistence: compiler, execution, materialization, publication, store, and backend imports all belong to adapters wired at composition.",
			Targets:   []string{"internal/dataframe/recipe"},
			Recursive: false,
			Scope:     Production,
			Forbidden: Forbidden{Prefix: append([]string{
				module + "internal/dataframe/compiler",
				module + "internal/dataframe/execution",
				module + "internal/dataframe/materialization",
				module + "internal/dataframe/publication",
				module + "internal/store/",
			}, backendImports.Prefix...)},
		},
		{
			Label:   labelPublicationPlane,
			Owner:   "ClickHouse publication is the independent publication plane; dataframe execution orchestration, published snapshots, and materialization must not be imported here.",
			Targets: []string{"internal/dataframe/publication"},
			// internal/dataframe/publication holds both the neutral
			// contracts and the clickhouse publication adapter; the
			// ban applies to every file in it.
			Recursive: true,
			Scope:     Production,
			Forbidden: Forbidden{Prefix: []string{
				module + "internal/dataframe/execution",
				module + "internal/dataframe/published",
				module + "internal/dataframe/materialization",
			}},
		},
		{
			Label:   labelAuthoringIntent,
			Owner:   "authoring intent is a pure contract tree; server, store, backend, lifecycle/compilation orchestration, and dataframe runtime layers are wired at composition.",
			Targets: []string{"internal/explorer/authoringv2"},
			// The authoringv2 import-ban rule was previously a shell
			// rule; it is preserved here (the tree is a domain
			// contract tree, so backendImports applies to it too).
			Recursive: true,
			Scope:     Production,
			Forbidden: Forbidden{Prefix: []string{
				module + "internal/server",
				module + "internal/store",
				module + "internal/backends/",
				module + "internal/explorer/lifecycle",
				module + "internal/explorer/compilation",
				module + "internal/dataframe/execution",
				module + "internal/dataframe/materialization",
				module + "internal/dataframe/publication",
				module + "internal/dataframe/published",
			}},
		},
		{
			Label:   labelIRLeaf,
			Owner:   "IR is the compiler substrate: lowering, optimization, rendering, and runtime layer above it.",
			Targets: []string{"internal/dataframe/compiler/ir"},
			// The ir rule's backends entry widened from
			// internal/backends/arango to both backends: the AQL
			// shape it was written for holds equally for SQL.
			Recursive: true,
			Scope:     Production,
			Forbidden: Forbidden{Prefix: []string{
				module + "internal/dataframe/compiler/lower",
				module + "internal/dataframe/compiler/optimize",
				module + "internal/dataframe/compiler/render",
				module + "internal/dataframe/execution",
				module + "internal/backends/",
			}},
		},
		{
			Label:     labelOptimizer,
			Owner:     "the optimizer rewrites IR; lowerer, renderer, and runtime imports belong elsewhere. (ResolveStorageRoute and fhirschema references stay as distinct shell text rules.)",
			Targets:   []string{"internal/dataframe/compiler/optimize"},
			Recursive: true,
			Scope:     Production,
			Forbidden: Forbidden{Prefix: []string{
				module + "internal/dataframe/compiler/lower",
				module + "internal/dataframe/compiler/render",
				module + "internal/dataframe/execution",
			}},
		},
		{
			Label:     labelSemantic,
			Owner:     "semantic contracts stay independent of physical compiler layers (ir, lower, optimize, render).",
			Targets:   []string{"internal/dataframe/semantic"},
			Recursive: true,
			Scope:     Production,
			Forbidden: Forbidden{Prefix: []string{
				module + "internal/dataframe/compiler/ir",
				module + "internal/dataframe/compiler/lower",
				module + "internal/dataframe/compiler/optimize",
				module + "internal/dataframe/compiler/render",
			}},
		},
		{
			Label:     labelSpec,
			Owner:     "spec is leaf vocabulary; runtime, compiler, semantic, materialization, template, and errors imports belong above it.",
			Targets:   []string{"internal/dataframe/spec"},
			Recursive: true,
			Scope:     Production,
			Forbidden: Forbidden{Prefix: []string{
				module + "internal/dataframe/execution",
				module + "internal/dataframe/compiler",
				module + "internal/dataframe/semantic",
				module + "internal/dataframe/materialization",
				module + "internal/dataframe/template",
				module + "internal/dataframe/errors",
			}},
			// Interim allowlist preserved per the scripts-lane
			// ruling (arch-backend-07 Wave B; coordinator ruling
			// arch-backend-09 Q4: stays, not retired here).
			AllowFiles: []string{"internal/dataframe/spec/grain.go"},
		},
		{
			Label:   labelRenderAQL,
			Owner:   "renderers are dialect leaf components: no lowerer, optimizer, runtime, backend, or driver imports; AQL rendering stays compiler-owned.",
			Targets: []string{"internal/dataframe/compiler/render/aql"},
			// The lower/optimize bans preserve the shell rule's
			// intent for the AQL dialect; the runtime/backends/driver
			// bans are the shared renderer law (previously the shell
			// scanned only this directory, which is exactly the
			// AQL-only blindness arch-backend-09 closes by pairing
			// this rule with labelRenderPostgres).
			Recursive: true,
			Scope:     Production,
			Forbidden: Forbidden{Prefix: []string{
				module + "internal/dataframe/compiler/lower",
				module + "internal/dataframe/compiler/optimize",
				module + "internal/dataframe/execution",
				module + "internal/backends/",
				"github.com/jackc/pgx",
				"github.com/arangodb",
				module + "internal/store/",
			}, Exact: []string{module + "internal/dataframe/compiler"}},
		},
		{
			Label:   labelRenderPostgres,
			Owner:   "renderers are dialect leaf components: no runtime, backend, store, or driver imports; SQL rendering stays compiler-owned. (Production renderer -> compiler/lower helper imports predate arch-backend-09 and remain with the compiler owner; compiler-root-exact, execution, backends, stores, and drivers are the banned leak shapes.)",
			Targets: []string{"internal/dataframe/compiler/render/postgres"},
			// The dialect-pairing rule: every leak shape the AQL
			// rule refuses must be refused here too, so a driver
			// leak cannot hide by landing only in render/postgres.
			Recursive: true,
			Scope:     Production,
			Forbidden: Forbidden{Prefix: []string{
				module + "internal/dataframe/execution",
				module + "internal/backends/",
				"github.com/jackc/pgx",
				"github.com/arangodb",
				module + "internal/store/",
			}, Exact: []string{module + "internal/dataframe/compiler"}},
		},
		{
			Label:     labelCommandLeaf,
			Owner:     "the compiled command is the compiler's standard-library-only leaf output; placeholder/type/null/copy checks run at construction, and dialect parameter encoding stays with each renderer.",
			Targets:   []string{"internal/dataframe/compiler/command"},
			Recursive: true,
			Scope:     Production,
			// command_test.go imports the compiler package: that is
			// a test-scope import, checked separately from the
			// production leaf law (Scope: Production above).
			RejectNonStdlib: true,
		},
		{
			Label:   labelLifecycle,
			Owner:   "Explorer lifecycle policy stays adapter-neutral; vendor plan/index/adapter policy types are translated inside the owning adapter under internal/backends/*/internal.",
			Targets: []string{"internal/explorer/lifecycle"},
			// backend-simplification-20261110 coverage amendment:
			// lifecycle-to-adapter policy types are a banned shape.
			Recursive: true,
			Scope:     Production,
			Forbidden: backendImports,
		},
		{
			Label: labelOldConcretePaths,
			Owner: "these concrete package paths were relocated under internal/backends/{arango,postgres}/internal by arch-backend-06/07; import only the factory package github.com/calypr/loom/internal/backends/{arango,postgres} and consume the returned domain ports.",
			// AllScopes: even a test importing a dead path is a
			// remnant regression (the runner-coverage "no old
			// package retained" law).
			Targets:   []string{"internal", "cmd"},
			Recursive: true,
			Scope:     AllScopes,
			Forbidden: Forbidden{Prefix: importPaths(oldConcreteImportPaths)},
		},
		{
			Label: labelControlCycleFree,
			Owner: "internal/backends/postgres/control is the cycle-free public control plane (arch-backend-09 ruling 2026-11-12): it must never import the postgres factory root (cycle), the private rows adapter (root-owned bridge), or dataframe/execution (the bridge reaches the renderer through execution). Consumers import control directly for control-plane symbols; only the factory root may import rows.",
			// AllScopes: a test-side cycle is still a cycle at
			// build time, and a test reaching execution from
			// control is the exact whitebox shortcut the ruling
			// refuses to bless.
			Targets:   []string{"internal/backends/postgres/control"},
			Recursive: true,
			Scope:     AllScopes,
			Forbidden: Forbidden{Prefix: []string{
				module + "internal/backends/postgres/internal/rows",
				module + "internal/dataframe/execution",
			}, Exact: []string{module + "internal/backends/postgres"}},
		},
	}
}

func importPaths(relatives []string) []string {
	out := make([]string, 0, len(relatives))
	for _, r := range relatives {
		out = append(out, module+r)
	}
	return out
}

// RepoAbsences asserts the old concrete package directories and the
// removed compatibility surfaces stay absent. The remaining
// control-facade entries (internal/backends/postgres/control, aliases.go)
// are added by the facade-collapse wave once it lands.
func RepoAbsences() []AbsenceRule {
	return []AbsenceRule{
		{
			Label: "old concrete backend package directories (arch-backend-06/07 relocations)",
			Owner: "internal/backends/{arango,postgres} factories plus their internal/ implementation trees",
			Paths: append([]string{}, oldConcreteImportPaths...),
		},
		{
			Label:       "removed untyped row-querier bridge (arch-backend-09)",
			Owner:       "internal/backends/postgres root Querier contract (typed bridges; control Runtime.PoolClient)",
			Identifiers: []string{"QuerierAsAny"},
			ScanRoots:   []string{"internal", "cmd"},
		},
		{
			Label: "removed control-forwarding facade (arch-backend-09 ruling 2026-11-12)",
			Owner: "consumers import internal/backends/postgres/control directly; the root package owns only Open/Runtime and the typed data-plane bridges",
			Paths: []string{"internal/backends/postgres/aliases.go"},
		},
	}
}
