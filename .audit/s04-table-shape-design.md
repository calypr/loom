# S04 table-shape design

## Decision

S04 adds one optional table shape to each authored document. Source-backed columns remain in `Document.Columns`. The table shape owns one optional row reshape and a bounded set of named derived columns.

The reshape is either a grouped pivot or an unpivot. It is not another `ColumnSource`. A grouped pivot consumes the table's authored output columns after recoding, time selection, unit normalization, and aggregation. The existing `recipe.Pivot` remains the implementation for correlated key and value lookups inside one FHIR row. It does not implement the new grouped table pivot.

The compiler emits ordinary named and typed columns after applying the table shape. Preview, Publish, Viewer, and artifact export continue to consume one immutable receipt and one public output contract.

## User contract

The Builder exposes **Table shape** controls backed by server capabilities. The browser can:

- select group, category, and value columns for a pivot;
- freeze the included categories and their output names;
- select duplicate, missing-cell, and unlisted-category policies;
- select compatible columns for an unpivot;
- add a named derived column with one typed binary operation;
- inspect bounded before-and-after rows, contributors, exclusions, and declared information loss;
- cancel without changing the draft; and
- apply a receipt-backed proposal, then reload the saved definition.

The browser never sends FHIR selectors, query text, inferred types, recipe calls, unit compatibility rules, or result types.

## Saved model

`authoringv2.Document` gains `TableShape *TableShape`. `TableShape` has these parts:

```go
type TableShape struct {
	Reshape *TableReshape        `json:"reshape,omitempty"`
	Derived []DerivedConstruction `json:"derived,omitempty"`
}

type TableReshape struct {
	Kind    ReshapeKind          `json:"kind"`
	Pivot   *PivotConstruction   `json:"pivot,omitempty"`
	Unpivot *UnpivotConstruction `json:"unpivot,omitempty"`
}
```

The package uses strict tagged unions. `TableReshape` accepts exactly one payload that matches its kind. Arithmetic operands accept exactly one column or numeric-literal payload. The server assigns every construction ID.

A pivot stores:

- an ordered, nonempty group-key list;
- one category column and one value column;
- frozen typed category keys and stable public output names;
- an explicit duplicate policy;
- an explicit missing-cell policy; and
- an explicit unlisted-category policy.

An unpivot stores an ordered input list, typed output descriptors for the key and value columns, and an explicit null-row policy.

Each derived definition stores one output descriptor, one binary operator, two typed operands, a missing-input policy, and a division-by-zero policy when the operator is divide. An operand may reference a base column, a pivot output, or another derived output. Multiple named definitions express nested calculations. The compiler rejects unknown references and dependency cycles.

## Execution order

The compiler applies these stages:

1. Compile rows, routes, source columns, transformations, reductions, and recoding into the base relation.
2. Apply one grouped pivot or one unpivot when the document has a reshape.
3. Apply derived columns after a grouped pivot or when no reshape exists.
4. Emit the final schema, presentations, emissions, evidence, and public contract.

The first S04 slice rejects derived columns combined with unpivot. A later change may add that combination after defining and testing its order.

A grouped pivot emits the group keys followed by the frozen category outputs. It removes other base columns and records that loss. An unpivot preserves unselected columns in their existing order, removes its selected inputs, and appends the key and value outputs.

## Required policies

The first grouped-pivot implementation supports the smallest policies needed by J04:

- duplicate cells use `ERROR` plus only deterministic reducers proven by the capability response;
- missing cells use `NULL` or `ERROR`; and
- new categories use `ERROR` or `EXCLUDE_WITH_EVIDENCE`.

The compiler does not advertise first or last selection until it can prove a stable source order. Numeric zero remains distinct from missing, recorded null, and a synthesized fill value.

Arithmetic uses these rules:

- add, subtract, and multiply return integer only for two integer operands;
- any decimal operand produces decimal;
- divide always produces decimal;
- missing input is `PROPAGATE_NULL` or `ERROR`; and
- division by zero is `NULL` or `ERROR`.

Add and subtract require two unitless operands or equal normalized unit identities. Multiply and divide accept unitless operands. They may scale one normalized unit-bearing column by a unitless literal. The compiler rejects any result dimension that the current type system cannot represent.

## Identity and evidence

A pivot row ID is a canonical typed encoding of the ordered group-key tuple plus the pivot construction ID. An unpivot row ID extends the input row ID with the unpivot construction ID and the selected source-column key. Derived columns do not change row identity.

`EmittedColumn` records the construction ID and direct input column keys. Its authored-column set remains the sorted transitive base-column set. The compiler extends the typed cell-trace path instead of adding free-form evidence roles.

Pivot evidence records the category and value contributors selected by the duplicate policy. Derived evidence unions the leaf contributors and adds no contributor for literals. An unpivot value cell records the source cell selected for that row. Compiler-generated key cells may identify their source column without inventing a source-resource contributor.

## Proposal lifecycle

The capability read is scoped to the project, Explorer, output, snapshot token, draft version, and draft digest. It returns opaque choices, complete frozen categories, valid policies, compatible operands, and exact refusal reasons.

The proposal endpoint accepts an `ADD`, `REPLACE`, or `REMOVE` change. The server resolves the choice IDs, clones the workspace, changes only `Document.TableShape`, compiles a candidate receipt, and executes bounded before-and-after previews. The response labels sampled evidence.

Apply sends only `APPLY_TABLE_SHAPE_PROPOSAL`, the output ID, and the proposal receipt ID. The command must be alone in its batch. The candidate receipt binds the draft version, draft digest, snapshot token, output ID, base document digest, and candidate workspace digest. Apply rejects stale or unrelated receipts and verifies that the candidate changed only the requested table shape.

## Implementation order

1. Add strict authoring types, server construction IDs, clone and digest coverage, migration, and graph validation.
2. Add server-owned capabilities and exact refusal codes.
3. Adapt the existing row-definition receipt proposal and apply flow.
4. Add a distinct grouped-pivot semantic node and physical operation.
5. Add flat derived definitions through semantic checking, physical validation, lowering, and AQL rendering.
6. Add terminal unpivot as a distinct physical row operation.
7. Extend output metadata, receipt contracts, and executed cell evidence.
8. Update OpenAPI, generated Go, strict Zod schemas, and Builder controls.
9. Run J04 through the real controls, then compare Preview, Viewer, and the downloaded typed artifact.

Each unit ends with focused executable checks. J04 must prove exact values, contributors, row identities, saved reload, an unrelated unchanged column, time and unit reduction, recoding, grouped pivot, and a derived column.
