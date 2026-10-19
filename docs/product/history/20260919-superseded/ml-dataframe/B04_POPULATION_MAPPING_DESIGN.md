# B04 population mapping report

## Problem

A saved selection may contain resources that do not contribute to any final
dataframe row. Loom must report those resources separately without changing
row grain, inventing null rows, weakening receipt authorization, or charging
ordinary Preview and publication for provenance they do not consume.

`mapped` means that a selected resource contributes to at least one final
emitted row after required navigation, authored filters, and explicit
expansion. Reaching a population-qualified root is not sufficient.

## User contract

Builder exposes an explicit **Check selected-resource coverage** action for a
reconciled receipt and output. A complete result shows:

```text
12,430 selected · 12,417 produce rows · 13 need attention
```

The repair list contains bounded, authorized resource references. It does not
claim why a resource missed, expose graph internals, or insert a synthetic row
into Preview. Any authoring command or receipt/output/scope change invalidates
the displayed report.

The report is a separate strict endpoint. Ordinary Preview and Publish do not
gain an evidence flag.

## Ownership

`internal/dataframe/compiler` owns a dedicated population-mapping terminal
over the same final-row plan used by the dataframe. It emits the internal
reverse relation `(selected member, stable row identity)` after every
row-eliminating and row-expanding operation.

`internal/dataframe/execution` owns the request-scoped report operation. It
streams and deduplicates the reverse relation, compares it with the immutable
selection membership, and returns exact counts plus one bounded unmapped page.
It never exposes the hidden relation as a dataframe column.

`internal/explorer/lifecycle` constructs the complete report binding from the
validated receipt and current selection header. Transport supplies only the
receipt, output, page size, and authenticated cursor. Lifecycle revalidates
project, explorer, generation, authorization scope, receipt, output, selection
revision, and membership digest before each report page.

The selection, workspace, compilation receipt, and published materialization
do not store mapping results. B04 does not introduce workers, leases, TTL
collections, polling, or durable quality artifacts.

## Execution shape

Normal population lowering returns to a typed bounded existence semijoin:

```text
FILTER LENGTH((
  ... exact route and scoped member filters ...
  LIMIT 1
  RETURN 1
)) > 0
```

It does not sort or return `__loom_population_members`.

The dedicated mapping compiler entry point retains matched selected IDs only
inside the request-specific plan, carries them through the normal final-row
semantics, and emits internal `(member, row)` witnesses. Two files reaching
one specimen produce two mapped members and one dataframe row. A qualified
root eliminated by a required relationship or inner expansion produces no
witness.

The first implementation uses existing root paging and backend-ordered witness
streaming. It must measure before adding external spill machinery. If bounded
memory cannot be proven with backend ordering, a request-scoped external merge
is the allowed fallback; a durable evidence store is not.

Every later unmapped page may rerun the complete mapping operation. The API
therefore bounds page count and selected cardinality from measured limits. A
future B07 quality artifact may replace recomputation without changing the
meaning of this report.

## Domain sketch

```go
type PopulationMappingBinding struct {
    ReceiptID, OutputID, Project, ExplorerID string
    Generation, ScopeDigest                  string
    SelectionRevisionID, MembershipDigest   string
    ResourceType                            string
}

type PopulationMappingStatus string

const (
    PopulationMappingComplete   PopulationMappingStatus = "COMPLETE"
    PopulationMappingIncomplete PopulationMappingStatus = "INCOMPLETE"
)

type PopulationMappingCounts struct {
    Selected, Mapped, Unmapped, EmittedRows int64
}

type PopulationMappingReport struct {
    Binding    PopulationMappingBinding
    Status     PopulationMappingStatus
    Counts     *PopulationMappingCounts // present only for COMPLETE
    Unmapped   []explorer.ResourceRef
    NextCursor string
}

func CompilePopulationMappingOutputWithPolicy(
    lower.CompiledRecipeOutput,
    recipe.RuntimeBindings,
    ir.PhysicalOptimizationPolicy,
) (CompiledPopulationMappingQuery, error)

func (e *Engine) PopulationMapping(
    context.Context,
    Resolved,
    PopulationMappingRequest,
    PopulationMemberReader,
) (PopulationMappingReport, error)

func (s *Service) PopulationMapping(
    context.Context,
    PopulationMappingRequest,
) (PopulationMappingReport, error)
```

The concrete APIs may refine names, but they must keep one dedicated compiler
entry point and one deep execution operation. Do not thread a general
population mode through unrelated compiler callers.

## Invariants

- Normal Preview and publication never construct matched-member arrays.
- Mapping uses final emitted-row semantics, not a capped Preview page or root
  reachability.
- Every selected identity belongs to exactly one complete-report bucket.
- Counts are absent after timeout, cancellation, resource exhaustion, stale
  identity, or backend failure; partial counts are never labeled exact.
- Cursors are authenticated and bind receipt, output, selection revision,
  membership digest, generation, scope digest, and last member identity.
- Full identities are bounded and paginated. Physical keys, collection names,
  AQL, auth paths, and miss reasons never cross the API boundary.
- No dataframe row is created, removed, or altered by requesting evidence.

## Verification

1. Prove ordinary direct/reversed population plans use one bounded existence
   match and contain neither `SORTED_UNIQUE` nor the hidden population column.
2. Prove the mapping terminal observes required navigation, filters, inner and
   outer expansion, and the same stable row identity as normal output.
3. With files 001, 002, and unlinked 004 selected for a Specimen output, prove
   `selected=3`, `mapped=2`, `unmapped=1`, `emittedRows=1`, and only file 004 is
   returned for repair.
4. Reject tampered/cross-scope cursors before reading member identities. Prove
   incomplete execution has no counts.
5. Drive attach, reconcile, ordinary Preview, explicit coverage check, repair
   list, reload, and Publish through the local verifier.
6. Compare ordinary sparse/dense population plans before and after the bounded
   existence change. Measure report latency, query scans, witness rows, and
   peak memory before selecting admission and page limits.

## Synthesis

The execution-owned synchronous design is the base because it preserves the
required semantics with the smallest new surface. The lifecycle-evidence
alternative contributed authenticated cursors, repeated authorization checks,
strict incomplete-result policy, and measured admission limits. Its durable
run store, scheduler, leases, polling API, TTL cleanup, and compiler-version
migration were rejected as premature B07 infrastructure.
