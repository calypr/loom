// Package arango is the single owned factory for Loom's ArangoDB 3.12
// backend: the only importable entry point to the concrete control/data
// store implementations (which live under internal/backends/arango/internal
// and are unreachable to any importer outside this tree by Go internal
// rules).
//
// Contract (architecture decision, backend-architecture-20261109,
// mirroring the PostgreSQL factory at internal/backends/postgres):
//   - Backend-owned concrete implementations: catalog, dataset lifecycle,
//     Explorer, recipe registry/revisions, published-dataframe metadata,
//     and the private artifact catalog each have exactly one home under
//     internal/.
//   - Consumer-owned domain contracts: callers consume the domain ports on
//     the Runtime returned by Open; nothing outside the backend tree
//     imports a private adapter package or the ArangoDB driver.
//   - Compiler-owned renderers stay in the compiler: AQL rendering lives at
//     internal/dataframe/compiler/render/aql, SQL at render/postgres, and
//     the compiled command is the leaf (the compiler subtree never imports
//     this tree or a driver — see
//     internal/dataframe/compiler/backend_leaf_guard_test.go).
//   - Startup-only factory: Open performs validated config checks, opens
//     exactly one lifecycle client, applies the required collection
//     bootstrap policy, assembles adapters, and cleans up on partial
//     failure. It returns typed ports plus the owned client for the
//     exported row/transaction/explain/profile surface — never driver
//     handles, and never a universal Backend service.
//   - ClickHouse remains the independent publication data plane: this
//     factory opens only ArangoDB; composition roots wire ClickHouse
//     themselves and drive the artifact catalog bootstrap through the
//     exported specs.
package arango
