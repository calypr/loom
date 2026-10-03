# Superseded product plans

Archived on 2026-09-19. Do not execute these plans or use their completion states to accept new work.

The replacement is [Build dataframes from the schema](../../ML_DATAFRAMER_DELIVERY_PLAN.md), with one [current ledger](../../ml-dataframer/execution.json).

This archive preserves 27 planning, prototype, and evidence files byte-for-byte, including uncommitted plan updates. [manifest.json](manifest.json) records each original path, archive path, and SHA-256. The active validator checks those hashes. Nothing was purged. Restore an original document from its mapped archive file if needed, without overwriting a current replacement unintentionally.

The relative layout among historical product documents is preserved. Source paths, old commands, and references outside that layout describe the historical checkout; they are not instructions to run against the current implementation.

## Map earlier work to the focused plan

| Earlier material | Current disposition |
| --- | --- |
| F1-F4 Patient-first proposal | Superseded. No Patient-only workflow or separate DatasetDesign adapter. |
| B01-B08 implementation, design, and verification | Retained implementation history. Reuse tested capabilities without treating them as completed S journeys. |
| F00 metadata/walker work | Reuse in S01. The isolated datatype-union experiment remains rejected and unintegrated. |
| C01 and C03 discovery/recognition | S01 schema-derived choices and actual column authoring. Broad metadata administration UI deferred. |
| C02 source selection | S03 authorized starting records and row construction. |
| C04 construction and tracing | S01 preserving execution, S02 editable graph, and S04 explicit operations. |
| C05 and C07 time/units/recoding | S04 typed transformations. |
| C06 interpretation | S02 explicit schema-valid bindings through existing interpretation ownership. Cross-project mapping administration deferred. |
| C08 ML representation | S04 explicit shape operations and S05 truthful typed artifacts. Learned preprocessing and training deferred. |
| C09 complete Check | S05 existing publication validation. Separate asynchronous Check framework deferred. |
| C10 copy/refresh | Preserve existing behavior. No redesign in this plan. |
| C11 and C12 export/release | S05 literal artifact parity and integrated acceptance. |

The old catalog mockup remains a historical experiment. `scripts/verify_ml_dataframer_prototype.mjs` points here and does not count as current application acceptance.
