// Package postgres is the single owned factory for Loom's PostgreSQL 18
// backend: the only importable entry point to the concrete source/control
// store implementations (which live under internal/backends/postgres/internal
// and are unreachable to any importer outside this tree by Go internal rules).
//
// Contract (architecture decision, backend-architecture-20261109):
//   - Backend-owned concrete implementations: catalog, dataset, Explorer,
//     recipe, publication metadata, source execution, and the shared pool/
//     migration store all have exactly one home under internal/.
//   - Consumer-owned domain contracts: callers consume the existing domain
//     ports returned by Open; nothing outside the backend tree imports a
//     private adapter package or a driver.
//   - Compiler-owned renderers stay in the compiler: SQL rendering lives at
//     internal/dataframe/compiler/render/postgres, AQL rendering at
//     internal/dataframe/compiler/render/aql, and the compiled command is the
//     leaf (the compiler subtree never imports this tree or a driver — see
//     internal/dataframe/compiler/backend_leaf_guard_test.go).
//   - Startup-only factory: Open performs validated config checks,
//     readiness, the approved schema setup policy, adapter assembly, and
//     partial-failure cleanup. It returns typed domain ports plus narrow
//     runtime health/close hooks — never pgx pools or a universal Backend
//     service.
//   - ClickHouse remains the independent publication data plane: this
//     factory opens only its selected PostgreSQL connections.
package postgres
