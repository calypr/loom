# Every named interface

  ## Authentication, GraphQL, bulk loading, ingest

     #    Interface                                                            Production reality                                                     Definitive call
  ━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
     1    internal/authscope/principal.go:68                                   Four real implementations: static, basic, Calypr, bearer token         KEEP. Real authentication strategy boundary
  ─────  ───────────────────────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────  ───────────────────────────────────────────────────────
     2    internal/authscope/principal.go:72                                   AllowAllAuthorizer and ScopeAuthorizer; many consumers                 KEEP
  ─────  ───────────────────────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────  ───────────────────────────────────────────────────────
     3    internal/authscope/scope_resolver.go:14                              External Fence client boundary                                         KEEP
  ─────  ───────────────────────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────  ───────────────────────────────────────────────────────
     4    internal/api/graphql/graph/resolver/resolver.go:21                   One implementation, execution.Control; one repository consumer         COLLAPSE TO CONCRETE execution.Control
  ─────  ───────────────────────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────  ───────────────────────────────────────────────────────
     5    internal/api/graphql/graph/resolver/resolver.go:79                   One server implementation; no test implementation                      COLLAPSE TO READ/WRITE FUNCTIONS
  ─────  ───────────────────────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────  ───────────────────────────────────────────────────────
     6    internal/api/graphql/graph/resolver/recipe_execution_reader.go:17    One method, one Arango implementation                                  COLLAPSE TO GetExecution FUNCTION
  ─────  ───────────────────────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────  ───────────────────────────────────────────────────────
     7    internal/api/bulk/load/service.go:54                                 Duplicates manifest reading plus one activation command                DELETE/COMBINE into manifest and activation callbacks
  ─────  ───────────────────────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────  ───────────────────────────────────────────────────────
     8    internal/api/bulk/load/service.go:59                                 Two read methods used by one service                                   COLLAPSE TO TWO FUNCTIONS
  ─────  ───────────────────────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────  ───────────────────────────────────────────────────────
     9    internal/api/bulk/load/service.go:64                                 One command method, one production implementation                      COLLAPSE TO FUNCTION
  ─────  ───────────────────────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────  ───────────────────────────────────────────────────────
    10    internal/api/bulk/load/service.go:68                                 One resource-load command                                              COLLAPSE TO FUNCTION
  ─────  ───────────────────────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────  ───────────────────────────────────────────────────────
    11    internal/api/bulk/load/snapshot_service.go:18                        Only LocalSnapshotBlobs; tests also use the concrete type              COLLAPSE TO CONCRETE
  ─────  ───────────────────────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────  ───────────────────────────────────────────────────────
    12    internal/ingest/row_builder.go:40                                    Strategy selected internally between generated and generic builders    COLLAPSE TO BUILD FUNCTION; retain concrete builders
  ─────  ───────────────────────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────  ───────────────────────────────────────────────────────
    13    internal/ingest/generated_load.go:13                                 Satisfied by all 23 generated FHIR roots                               COMBINE INTO GENERATED fhir.ConcreteResource

  The last call explicitly preserves all 23 typed FHIR Go types. It only moves Validate and ExtractEdges into the existing generated contract and deletes the ingest-local duplicate.

  ## Catalog, dataset, and shared Arango capabilities

     #    Interface                                           Production reality                                                             Definitive call
  ━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
    14    internal/catalog/arango/store.go:17                 Catalog genuinely uses query, insert, AQL, bootstrap, and collection checks    KEEP, but embed shared Arango primitives
  ─────  ──────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────
    15    internal/catalog/types.go:169                       Cohesive three-part evidence read with independent failure reporting           KEEP
  ─────  ──────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────
    16    internal/catalog/write_persist.go:11                Identical to other Arango ExecuteAQL contracts                                 COMBINE into shared store/arango.AQLExecutor
  ─────  ──────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────
    17    internal/store/arango/client.go:31                  Merely exposes QueryRows; name adds no contract                                COMBINE into shared RowQueryer
  ─────  ──────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────
    18    internal/dataset/snapshot.go:151                    Real snapshot lifecycle boundary                                               KEEP/TRIM: remove federation-only ListSnapshotProjects
  ─────  ──────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────
    19    internal/dataset/inventory.go:8                     Only feeds unauthenticated federation candidate discovery                      DELETE with federation
  ─────  ──────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────
    20    internal/dataset/retention.go:19                    Cohesive metadata-retention boundary                                           KEEP
  ─────  ──────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────
    21    internal/dataset/retention.go:24                    One method, one filesystem implementation                                      COLLAPSE TO FUNCTION
  ─────  ──────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────
    22    internal/dataset/release.go:84                      One method, one publication-registry adapter                                   COLLAPSE TO FUNCTION
  ─────  ──────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────
    23    internal/dataset/release.go:88                      One method, one production implementation                                      COLLAPSE TO FUNCTION
  ─────  ──────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────
    24    internal/dataset/release.go:92                      Real release persistence boundary                                              KEEP/TRIM: remove federation-only ListReleaseProjects
  ─────  ──────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────
    25    internal/dataset/active_resolver.go:9               One method delegated by several consumers                                      COLLAPSE TO FUNCTION
  ─────  ──────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────
    26    internal/dataset/arango/manifest_lifecycle.go:26    Duplicate QueryRows contract                                                   COMBINE into shared RowQueryer
  ─────  ──────────────────────────────────────────────────  ─────────────────────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────
    27    internal/server/release_verifier.go:15              Exists solely for federation project discovery                                 DELETE

  The shared Arango vocabulary should be:

  type RowQueryer interface {
      QueryRows(context.Context, string, int, map[string]any, RowVisitor) error
  }

  type BatchInserter interface {
      InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error
  }

  type AQLExecutor interface {
      ExecuteAQL(context.Context, string, map[string]any) error
  }

  Packages compose these primitives. Do not replace the duplicates with a new mega-ArangoClient.

  ## Explorer

     #    Interface                                        Production reality                                                   Definitive call
  ━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
    28    internal/explorer/store.go:24                    All 14 methods are used against real Arango persistence              KEEP, but combine CreateInteractive and CreateRepository into Create
  ─────  ───────────────────────────────────────────────  ───────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────────────
    29    internal/explorer/lifecycle/types.go:24          Mirrors *explorer.Service; exists primarily for one fake             COLLAPSE TO CONCRETE *explorer.Service
  ─────  ───────────────────────────────────────────────  ───────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────────────
    30    internal/explorer/capability/repository.go:20    Real content-addressed snapshot persistence                          KEEP
  ─────  ───────────────────────────────────────────────  ───────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────────────
    31    internal/explorer/capability/builder.go:45       Production already materializes the slice before building            DELETE, replace with Evidence.Resources
  ─────  ───────────────────────────────────────────────  ───────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────────────
    32    capability.RelationshipEvidence                  Same in-memory evidence object                                       DELETE, replace with value
  ─────  ───────────────────────────────────────────────  ───────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────────────
    33    capability.FieldEvidence                         Same in-memory evidence object                                       DELETE, replace with value
  ─────  ───────────────────────────────────────────────  ───────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────────────
    34    capability.Observer                              No typed consumer; only embeds the previous three                    DELETE OUTRIGHT
  ─────  ───────────────────────────────────────────────  ───────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────────────
    35    internal/explorer/capability/builder.go:90       One implementation; tests already use callbacks                      COLLAPSE TO CALLBACK STRUCT
  ─────  ───────────────────────────────────────────────  ───────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────────────
    36    internal/explorer/arango/store.go:19             Useful query/transaction provider seam with isolated Arango tests    KEEP
  ─────  ───────────────────────────────────────────────  ───────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────────────
    37    internal/server/explorer_capability.go:73        No interface-valued consumer                                         DELETE
  ─────  ───────────────────────────────────────────────  ───────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────────────
    38    ExplorerCapabilityExecutionResolver              No interface-valued consumer                                         DELETE

  The capability replacement should be a value, not another repository abstraction:

  type Evidence struct {
      Resources     []ResourceObservation
      Relationships []RelationshipObservation
      Fields        []FieldObservation

      ResourcesAvailable     bool
      RelationshipsAvailable bool
      FieldsAvailable        bool
  }

  The availability state is necessary to preserve fail-closed behavior. An unavailable evidence source must not become indistinguishable from a valid empty result.

  ## Compiler, recipe, and errors

     #    Interface                                               Production reality                                                        Definitive call
  ━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
    39    internal/dataframe/compiler/capability/probe.go:104     No implementation, configuration, or test                                 DELETE with CostEstimate, CostLimit, and both checks
  ─────  ──────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────
    40    internal/dataframe/errors/errors.go:71                  Real transport-neutral error classification contract                      KEEP
  ─────  ──────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────
    41    internal/dataframe/execution/engine.go:29               Name-based recipe loading is still live                                   COMBINE with VersionedRegistry; retain this name
  ─────  ──────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────
    42    execution.VersionedRegistry                             Same implementation; currently discovered by assertion                    DELETE INTO Registry
  ─────  ──────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────
    43    internal/dataframe/recipe/revisions.go:23               All methods used by GraphQL and digest-based resolution                   KEEP
  ─────  ──────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────
    44    internal/dataframe/recipe/schema/resolve.go:55          One callback method                                                       COLLAPSE TO FUNCTION
  ─────  ──────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────
    45    internal/dataframe/recipe/exec/persistent.go:15         Base of a runtime optional hierarchy                                      COMBINE with VersionedStore; retain this name
  ─────  ──────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────
    46    recipe/exec.VersionedStore                              Same production and test implementations already implement all methods    DELETE INTO Store
  ─────  ──────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────
    47    internal/dataframe/recipe/exec/arango/registry.go:18    Identical to publication Arango query/write client                        COMBINE through shared Arango capabilities

  If the queued publication worker is removed, Engine.MaterializeVersion may lose its only runtime consumer. That should be rechecked after worker deletion; the storage contract can still support immutable
  versions without keeping every execution wrapper.

  ## Publication and ClickHouse

  The current layering is:

  Publish
    -> publication.Target
    -> clickhouse.Target
    -> IdentityBundleStore
    -> AtomicBundleTx
    -> clickhouse transaction adapter
    -> publication.Transaction
    -> clickHouseBundleTx

  That is too many names for one ClickHouse transaction.

     #    Interface                                                       Production reality                                                    Definitive call
  ━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
    48    internal/dataframe/publication/bundle.go:18                     ClickHouse-only intermediate transaction                              COMBINE INTO Transaction and concrete ClickHouse transaction
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    49    AtomicBundleSchemaFinalizer                                     Never used by its declared name                                       DELETE
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    50    internal/dataframe/publication/bundle.go:198                    Ten methods mixing reads, pointers, executions, queues, and leases    SPLIT/REPLACE, then delete this declaration
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    51    SelectorExecutionCatalog                                        Federation-only optimized fallback                                    DELETE
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    52    ExactExecutionCatalog                                           One release-verification method                                       COLLAPSE TO FUNCTION
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    53    internal/dataframe/publication/types.go:76                      Useful backend-neutral boundary used by the streaming publisher       KEEP
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    54    ObjectValueTarget                                               Optional capability, but production always supports objects           DELETE; make object support part of the target contract
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    55    internal/dataframe/publication/types.go:89                      Correct publication transaction abstraction                           KEEP/EXPAND with mandatory finalization and abort behavior
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    56    SchemaFinalizer                                                 Optional extension for behavior production requires                   DELETE INTO Transaction
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    57    internal/dataframe/publication/arango/bundle_registry.go:21     Duplicate shared Arango method                                        COMBINE into shared AQLExecutor
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    58    internal/dataframe/publication/arango/registry.go:11            Same query/write contract as recipe registry                          COMBINE through shared Arango capabilities
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    59    internal/dataframe/publication/clickhouse/bundle_store.go:26    One concrete implementation behind an extra target wrapper            DELETE; make ClickHouseBundleStore implement Target
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    60    ClaimedIdentityBundleStore                                      Exists for the queued worker resume path                              DELETE WITH WORKER
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    61    internal/dataframe/publication/clickhouse/bundle_store.go:47    Real storage boundary between publication and ClickHouse client       KEEP/COMBINE DropColumns into it
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    62    bundleColumnDropper                                             Optional assertion for required production behavior                   DELETE INTO BundleClickHouseStore
  ─────  ──────────────────────────────────────────────────────────────  ────────────────────────────────────────────────────────────────────  ──────────────────────────────────────────────────────────────
    63    BundleClickHouseColumnStore                                     Declared but never consumed by name                                   DELETE

  The replacement for BundleCatalog should be two cohesive ports:

  type PublicationExecutionStore interface {
      FindExecutionByKey(...)
      SaveExecution(...)
      GetPointer(...)
      PublishExecution(...)
      ListExecutions(...) // startup crash reconciliation
  }

  type PublicationLeaseStore interface {
      AcquireBundleLease(...)
      RenewBundleLease(...)
      ReleaseBundleLease(...)
  }

  Other consumers should receive exact functions such as GetExecution, FindPublishedOutput, or ListProjectOutputs; they should not depend on either large store interface.

  ClickHouseBundleStore should directly implement publication.Target. That removes target.go’s adapter ladder and lets its concrete transaction directly satisfy the mandatory publication transaction.

  ## Federation and published reads

     #    Interface                                        Production reality                                            Definitive call
  ━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━
    64    internal/dataframe/published/federation.go:68    Legacy fallback from execution metadata                       DELETE WITH FEDERATION
  ─────  ───────────────────────────────────────────────  ────────────────────────────────────────────────────────────  ────────────────────────
    65    ProjectStatusResolver                            Implemented only by server federation status resolver         DELETE
  ─────  ───────────────────────────────────────────────  ────────────────────────────────────────────────────────────  ────────────────────────
    66    ReleaseExecutionResolver                         Same implementation; release-aware federation fallback        DELETE
  ─────  ───────────────────────────────────────────────  ────────────────────────────────────────────────────────────  ────────────────────────
    67    FederationSnapshotResolver                       Same implementation; latest federation iteration              DELETE
  ─────  ───────────────────────────────────────────────  ────────────────────────────────────────────────────────────  ────────────────────────
    68    internal/dataframe/published/read.go:17          Clean ClickHouse read boundary used by rows and aggregates    KEEP

  # Federation: delete the feature, not single-project reading

  Federation is intertwined with the current dataframe GraphQL API, but it is separable from Explorer publication.

  Delete:

  - internal/dataframe/published/federation.go
  - internal/dataframe/published/federation_query.go
  - federation tests
  - internal/server/federation_status.go
  - all four federation interfaces
  - SelectorExecutionCatalog
  - ExecutionProjectSource and ProjectInventory
  - ListSnapshotProjects, ListReleaseProjects, ListExecutionProjects
  - candidate-project discovery wiring
  - federation cache entries and cross-project status generation
  - availability/completeness/project-status GraphQL fields

  The existing projectDataframeDatasets(projectId:) query is the correct anchor. Change other dataframe reads to require an explicit project:

  input DataframeRowsInput {
    projectId: String!
    selector: DataframeSelectorInput!
    ...
  }

  Do the same for dataset, aggregate, aggregations, and export inputs. Then:

  principal
    -> authorize explicit project
    -> resolve that project's active release
    -> find one published output
    -> query one ClickHouse table

  This removes:

  - catalog-wide publication scans;
  - active-release joins across every candidate project;
  - schema reconciliation;
  - cross-project authorization maps;
  - multi-table UNION queries;
  - federation status calculations;
  - several retry/fallback paths.

  The aggregate code can remain, but it should plan against one physical table. The current batching of multiple GraphQL aggregate root fields is valuable and independent of federation.

  # Publication: retain direct Explorer save, delete the iterations around it

  Explorer publication is real and required:

  compile receipt
    -> materialize receipt
    -> verify queryable outputs
    -> activate release
    -> persist active Explorer revision

  Keep that.

  Delete the parallel/legacy entry points:

  - startDataframeMaterialization GraphQL mutation;
  - ClickHouse publication worker and worker tests;
  - worker polling goroutine;
  - publication retry/attempt configuration;
  - ClaimedIdentityBundleStore;
  - BeginClaimedBundleFor;
  - queued-command resume logic;
  - retry-specific fields and transitions once stored compatibility is handled;
  - old lifecycle.Config.Materialize fallback;
  - old bundle-based explorerBundleMaterializer;
  - old preview callback when PreviewReceipt is always configured;
  - standalone materializeDataframeRecipeBundle if the supported product flow is exclusively Explorer preview → publish/save;
  - GraphQL activateProjectRelease, while retaining internal release activation for Explorer publication.

  Keep startup reconciliation for genuinely abandoned staging tables, but remove its “requeue bounded retry work” behavior. Reconciliation should only clean stale private tables and mark abandoned executions
  failed.

  # Hidden interfaces not included in the original 68

  There are another 14 interface-shaped constructs:

  - 10 optional runtime assertions in publication;
  - one anonymous ReadManifest assertion in snapshot loading;
  - two anonymous catalog writer parameter interfaces;
  - one incomplete compile-time assertion for execution.Control.

  The publication assertions cover:

  - idempotent status;
  - existing outputs;
  - abort;
  - schema finalization;
  - final schema digest;
  - output metadata.

  Every production ClickHouse transaction supports these. Make them mandatory on the retained transaction contract or eliminate them with the adapter layer. Optional capability probing is making the happy path
  significantly harder to read.

  The snapshot manifest assertion is a legacy recovery path and should become an explicit callback temporarily, then be deleted when manifest-only legacy generations are no longer supported.

  The Control assertion omits Run, even though RecipeControl requires it. Delete it when RecipeControl becomes concrete.

  # Expected impact

  A conservative estimate for the federation, worker, adapter, and associated test cleanup is:

  - 2,500–4,000 net Go lines removed;
  - 68 named internal interfaces reduced to approximately 24;
  - all 14 hidden interface literals/assertions removed;
  - fewer background goroutines and publication state transitions;
  - fewer Arango scans during dataframe reads;
  - single-table ClickHouse queries instead of federation unions;
  - substantially smaller mocks and fixtures;
  - no change to the 23 typed FHIR Go roots.

  # Recommended execution order

  1. Introduce the three shared Arango primitives and remove duplicate client method sets.
  2. Delete CostPolicy and the two unused server capability resolver interfaces.
  3. Replace Explorer capability evidence interfaces with one evidence value.
  4. Delete federation and change dataframe GraphQL reads to explicit-project semantics.
  5. Delete the queued publication worker and standalone materialization mutations.
  6. Collapse the ClickHouse publication adapter ladder while preserving direct receipt publication.
  7. Convert one-method bulk, dataset, recipe, and resolver interfaces to functions.
  8. Collapse the two recipe registry hierarchies.
  9. Collapse lifecycle.Store to the concrete Explorer service.
  10. Regenerate GraphQL, run all tests, then update Graphify.

  The first major implementation slice should be steps 1–4. That removes the clearest duplication and the abandoned federation feature before we reshape publication around the one remaining product workflow.
