internal/explorer has become a dumping ground, but the deeper issue is not simply its size. It mixes a legitimate Explorer control-plane domain with legacy migration formats, persistence adapters, test
  infrastructure, capability discovery, compilation artifacts, and lifecycle orchestration.

  The August 25 “refactor” explains the sudden loss of comprehensibility: it added 13,444 lines across 61 Explorer-related files—roughly 6,369 lines under internal/explorer and another 7,075 under internal/server.

  ## What Explorer is supposed to be

  Explorer is the user-facing control plane over the dataframe system:

  catalog evidence + authorization + active dataset generation
                              ↓
                    explorer/capability
                              ↓
  Builder document → authoringv2 → explorer/compilation
                              ↓
                      dataframe recipe
                              ↓
               immutable compilation receipt
                    ↙                     ↘
          dataframe runtime       dataframe publication
              preview              ClickHouse + activation

  Its clean responsibility would be:

  > Own the durable lifecycle of a user-authored view, from mutable intent through an immutable compilation receipt to an active revision.

  The key production path is visible in server/server.go:377: authorize an exact capability snapshot, translate the V2 document, compile through the dataframe engine, and persist a content-addressed receipt.

  Explorer does not—and should not—own physical query planning or AQL:

  - dataframe/recipe defines semantic recipe inputs.
  - dataframe/semantic resolves and validates them.
  - dataframe/compiler lowers them into physical plans and AQL.
  - dataframe/runtime runs previews and queries.
  - dataframe/publication materializes outputs.
  - dataframe/published reads and federates those published outputs.

  The native translator in explorer/compilation/translate.go:121 is appropriately placed: it translates Explorer-specific intent into a generic dataframe recipe without executing it.

  ## Where the hygiene breaks down

  This subtree is actually seven Go packages, not one package, and some of the new subdivisions are sensible—particularly authoringv2, capability, and compilation. The root explorer package is the real mixed
  aggregate.

  The clearest problems are:

  - explorer/types.go:90 combines identity, drafts, active revisions, recipes, physical columns, dataset selectors, materializations, publication state, migration metadata, and diagnostics in one persistence
    model.

  - explorer/store.go:71 defines one very broad store interface covering repository config, drafts, receipts, revisions, activation, statistics, and destructive migration cleanup.
  - explorer/service.go:31 mixes genuine lifecycle rules with many one-line persistence wrappers and several older lifecycle APIs.
  - V1 compatibility occupies explorer/authoring_v1.go:18, explorer/state_v1.go:10, bootstrap, migration commands, and large server adapters even though V1 is no longer an HTTP surface.
  - There are parallel domain models: root Compilation/EmittedColumn and explorer/compilation.Result/EmittedColumn. server/server.go:418 manually copies between them.
  - authoringv2/migration.go makes the V2 package depend backward on root V1 types. Migration should depend on both versions, rather than V2 depending on legacy.
  - Much of the application logic lives in the transport package. There are 6,287 production lines in internal/server/explorer*.go, including the 1,969-line server/explorer_v2_lifecycle.go:1. server is doing
    orchestration that belongs in an Explorer application layer.

  So the strongest architectural smell is actually the pair internal/explorer + internal/server/explorer_*, not the Explorer directory in isolation.

  ## Is there dead code?

  Yes—enough to be meaningful, though it is not the main source of the sprawl.

  A whole-program Go reachability pass reported:

  - 79 Explorer functions unreachable from production executables.
  - 61 of those become reachable only when tests are included.
  - 18 remain unreachable even with tests.

  The strongest deletion candidates include:

  - Unused service methods such as ListConfigs, Config, SaveConfig, InsertAuthoringRevision, InsertReadyRevisionV2, and InsertFailedRevisionV2.
  - Compatibility aliases such as ValidateID, NewSnapshotStore, NewMemoryRepository, and Translate.
  - Six unused V2 decoder aliases.
  - MigrateV1Bundle and bootstrap.Digest.

  Most of the remaining 61 are test infrastructure living in production files, especially the 499-line root MemoryStore, the capability memory repository, ProbeFuncs, and SliceObserver. They should either move to
  _test.go/testutil or acquire a demonstrated production/local-deployment entry point.

  This means there is measurable dead/test-only surface, but most of the recent bulk is active code representing overlapping architectures and compatibility layers—not merely forgotten functions.

  ## Recommended boundary cleanup

  In order:

  1. Delete the 18 genuinely unreachable wrappers and APIs; relocate test-only stores and probes.
  2. Quarantine V1, bootstrap, and migration logic under an explicit explorer/legacy or explorer/migration boundary.
  3. Split Store into focused repositories: drafts/config, receipts, and revisions/activation.
  4. Move compile/preview/publish use cases from internal/server into an Explorer application package; leave Fiber route decoding and response mapping in server.
  5. Make receipt/output-contract types canonical and remove the duplicate compilation DTOs.

  The coherent pieces—capability, authoringv2, native translation, and immutable receipts—should remain. The cleanup should preserve persisted JSON shapes, receipt hashing, Arango collection contracts, revision
  IDs, scope semantics, and the offline migration commands. No files were changed during this audit.
