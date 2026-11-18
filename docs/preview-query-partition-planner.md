# Preview query partition planner

## Problem

Wide preview queries can spend more time in ArangoDB's optimizer than in execution. Splitting the terminal projection can reduce individual query complexity, but every partition may retain the same upstream operations. A column count alone does not bound that repeated work.

## Usage

`CompileRecipeOutputPageWithPolicy` returns the ordinary selected-root query and, for eligible bounded previews, optional projection partitions. `OutputStream` uses the partitions only inside a shared read snapshot and validates the complete rows before returning them. The ordinary query remains the fallback.

## Design

The compiler considers only root-preserving plans with a direct root `_key` identity. It keeps source scope, authorization, filters, ordering, traversal, sets, error-sensitive producers, and hidden preview metadata. A typed reference walk can remove unused direct scalar value or literal bindings; unsupported references and plans decline the optimization. It renders independently validated partitions, groups public projections using their typed serialized size and a column cap, and places hidden metadata in a separate partition. Before rendering partitions, it checks operation and projection counts and the serialized full-plan footprint. It also limits the number of partitions, per-query footprint, and aggregate retained plans and query bytes, including repeated prefixes. These structural limits are heuristics, not guarantees about database planning time or execution cost.

The full resolved plan and receipt fingerprint are unchanged. Partitions use the same selected roots and snapshot, and the executor rejects missing or duplicate identities, unexpected columns, inconsistent ordering, and conflicting values before exposing a page.

## Synthesis decision

Typed dependency analysis is the base because terminal-only slicing repeats unrelated producers. The mandatory scope and effect spine and differential checks come from the hybrid design. Measuring the complete rendered footprint comes from the conservative rendering alternative; online `EXPLAIN` and an externally configurable weighted cost model were rejected because they add request-time planning and policy without a proven benefit.

## Tradeoffs and limits

- Only proven non-failing bindings are removable. Unknown or potentially failing work remains or prevents partitioning.
- Splitting can repeat shared database work. A snapshot provides consistency, not shared computation.
- Group, Pivot, Expand, construction-stage, and other row-changing shapes retain their existing query path until a separate identity and dependency proof exists.
- The structural cost budget is calibrated for bounded resource use and the observed wide preview; it does not predict latency for every dataset or query.

## Validation

The planner's tests cover complete-plan limits, identity eligibility, metadata, dependency pruning, and conservative fallback. Acceptance must also compare full and partitioned results on real data, including authorization and error behavior; a local query timing does not replace authenticated Builder acceptance.
