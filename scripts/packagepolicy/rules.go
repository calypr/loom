package packagepolicy

import (
	_ "embed"
	"strings"
)

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

var backendImports = Forbidden{
	Prefix: []string{
		module + "internal/backends/",
		"github.com/jackc/pgx",
		"github.com/arangodb",
	},
}

//go:embed old_concrete_paths.txt
var oldConcretePathsData string

var oldConcreteImportPaths = parsePathTable(oldConcretePathsData)

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

func OldConcreteImportPaths() []string {
	return append([]string{}, oldConcreteImportPaths...)
}

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
			Label:     labelPublicationPlane,
			Owner:     "ClickHouse publication is the independent publication plane; dataframe execution orchestration, published snapshots, and materialization must not be imported here.",
			Targets:   []string{"internal/dataframe/publication"},
			Recursive: true,
			Scope:     Production,
			Forbidden: Forbidden{Prefix: []string{
				module + "internal/dataframe/execution",
				module + "internal/dataframe/published",
				module + "internal/dataframe/materialization",
			}},
		},
		{
			Label:     labelAuthoringIntent,
			Owner:     "authoring intent is a pure contract tree; server, store, backend, lifecycle/compilation orchestration, and dataframe runtime layers are wired at composition.",
			Targets:   []string{"internal/explorer/authoringv2"},
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
			Label:     labelIRLeaf,
			Owner:     "IR is the compiler substrate: lowering, optimization, rendering, and runtime layer above it.",
			Targets:   []string{"internal/dataframe/compiler/ir"},
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
			AllowFiles: []string{"internal/dataframe/spec/grain.go"},
		},
		{
			Label:     labelRenderAQL,
			Owner:     "renderers are dialect leaf components: no lowerer, optimizer, runtime, backend, or driver imports; AQL rendering stays compiler-owned.",
			Targets:   []string{"internal/dataframe/compiler/render/aql"},
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
			Label:     labelRenderPostgres,
			Owner:     "renderers are dialect leaf components: no runtime, backend, store, or driver imports; SQL rendering stays compiler-owned. (Production renderer -> compiler/lower helper imports predate arch-backend-09 and remain with the compiler owner; compiler-root-exact, execution, backends, stores, and drivers are the banned leak shapes.)",
			Targets:   []string{"internal/dataframe/compiler/render/postgres"},
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
			Label:           labelCommandLeaf,
			Owner:           "the compiled command is the compiler's standard-library-only leaf output; placeholder/type/null/copy checks run at construction, and dialect parameter encoding stays with each renderer.",
			Targets:         []string{"internal/dataframe/compiler/command"},
			Recursive:       true,
			Scope:           Production,
			RejectNonStdlib: true,
		},
		{
			Label:     labelLifecycle,
			Owner:     "Explorer lifecycle policy stays adapter-neutral; vendor plan/index/adapter policy types are translated inside the owning adapter under internal/backends/*/internal.",
			Targets:   []string{"internal/explorer/lifecycle"},
			Recursive: true,
			Scope:     Production,
			Forbidden: backendImports,
		},
		{
			Label:     labelOldConcretePaths,
			Owner:     "these concrete package paths were relocated under internal/backends/{arango,postgres}/internal by arch-backend-06/07; import only the factory package github.com/calypr/loom/internal/backends/{arango,postgres} and consume the returned domain ports.",
			Targets:   []string{"internal", "cmd"},
			Recursive: true,
			Scope:     AllScopes,
			Forbidden: Forbidden{Prefix: importPaths(oldConcreteImportPaths)},
		},
		{
			Label:     labelControlCycleFree,
			Owner:     "internal/backends/postgres/control is the cycle-free public control plane (arch-backend-09 ruling 2026-11-12): it must never import the postgres factory root (cycle), the private rows adapter (root-owned bridge), or dataframe/execution (the bridge reaches the renderer through execution). Consumers import control directly for control-plane symbols; only the factory root may import rows.",
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
