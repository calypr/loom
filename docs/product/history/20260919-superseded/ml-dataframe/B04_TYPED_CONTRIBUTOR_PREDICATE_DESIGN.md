# B04 typed contributor predicate design

## Decision

A contributor predicate belongs to one dataframe feature. It is stored on the
authored `Column`, beside `Column.Source`, because contributor selection is
independent of the feature's reduction, time window, and unit policy.

The public authoring contract stores a catalog candidate identity. It never
stores an AQL expression or accepts a user-authored FHIR selector. Compilation
resolves the candidate against the column occurrence and the pinned catalog,
then hands one `recipe.Filter` to the existing dataframe pipeline.

```text
Column.Contributor (catalog intent)
  -> recipe.Aggregate.Where (resolved execution recipe)
  -> spec.TypedFilter (canonical semantic predicate)
  -> PhysicalAggregate.Predicate (typed executable predicate)
  -> bound AQL inside that aggregate's item loop
```

Contributor predicates restrict only the values supplied to their owning
feature. They never filter root rows, become required-route matches, or change
population membership. Two optional occurrences through the same relationship
may therefore count different contributor subsets without contaminating each
other.

## Authoring shape

The first writable surface is intentionally small:

```go
type ContributorPredicate struct {
	CandidateID string                 `json:"candidateId"`
	Operator    ContributorOperator    `json:"operator"`
	Quantifier  ContributorQuantifier  `json:"quantifier,omitempty"`
	Value       *ContributorValue      `json:"value,omitempty"`
}

type ContributorValue struct {
	Kind   ContributorValueKind `json:"kind"`
	String *string              `json:"string,omitempty"`
	Code   *ContributorCode     `json:"code,omitempty"`
}

type ContributorCode struct {
	Code string `json:"code"`
}
```

`Column` gains `Contributor *ContributorPredicate`. B04 enables it only for
aggregate columns. B05 may enable the same type for other feature sources once
those sources have a feature-local recipe carrier.

Supported combinations are:

| Operator | Value | Cardinality |
| --- | --- | --- |
| `EXISTS` | none | scalar, or repeated with explicit `ANY` |
| `EQUALS` | exactly one `STRING` | scalar, or repeated with explicit `ANY` |
| `EQUALS` | exactly one uncorrelated `CODE` | scalar, or repeated with explicit `ANY` |

All other operators, values, Boolean trees, and quantifiers are rejected before
save. A scalar candidate forbids a quantifier. Compilation derives the selector,
value kind, and repeatedness from the selected catalog candidate and generated
FHIR schema; the browser does not coordinate those facts.

## Ownership and signatures

- `internal/explorer/authoringv2` owns the strict predicate union, shape
  validation, catalog-bound command validation, normalization, and cloning.
- `internal/explorer/compilation` resolves `CandidateID` on the authored
  occurrence and constructs `recipe.Filter`. Its `FieldRef` is the candidate
  ID, not the output column name.
- `internal/dataframe/semantic` retains the complete `*spec.TypedFilter` on
  `SemanticAggregate`; it does not decompose the filter into selector, string,
  and kind fields.
- `internal/dataframe/compiler/lower` owns one typed-filter-to-physical helper:

```go
func lowerTypedPredicate(
	physical *ir.PhysicalPlan,
	resourceType string,
	source ir.PhysicalValue,
	filter spec.TypedFilter,
	bindPrefix string,
) (*ir.PhysicalPredicateExpression, error)
```

  Aggregate lowering supplies a bind prefix derived from occurrence/source and
  feature identity so sibling literals cannot collide.
- `internal/dataframe/compiler/render/aql` rebinds the complete predicate
  expression to the aggregate item. Sharing may reuse only the unfiltered,
  authorized neighbor scan; every contributor predicate remains consumer-local.

The B04 slice does not add concept target IDs, correlated Coding bindings, a
new traversal-sharing key, or aggregate value-candidate migration. Those
changes require their own complete literal execution path and are not needed
to prove independent status counts.

## Migration

The writable semantics version advances once the public shape changes.
`SourceWhere` is removed from the current JSON, OpenAPI, and TypeScript
contracts. An internal Go-only compatibility carrier remains for migration.
Private persisted-draft wires may decode both the pre-v3 flat
`wherePath`/`whereEquals` form and the v3 nested `where.{path,equals}` form.

Migration occurs only where the mutable draft bytes and pinned catalog are
both available. It resolves the legacy path to exactly one candidate on the
column occurrence, proves string-versus-code and scalar-versus-repeated meaning,
normalizes repeated predicates to explicit `ANY`, and records the interpretation
in the existing `Workspace.MigrationDecisions`. Ambiguous migration aborts
transactionally and leaves the old draft untouched. Repeated migration is a
digest-stable no-op.

Immutable receipts and published artifacts never enter this migration path.
They retain their frozen recipe and compiler identity and require explicit
recompilation to adopt current semantics.

## Required proof

The acceptance fixture contains two distinct optional Observation occurrences
through the same relationship. One feature counts `registered`; the other
counts `cancelled`.

Executable verification must prove:

- strict current authoring rejects legacy paths and unsupported predicate
  combinations;
- both legacy wire forms migrate identically when catalog evidence is complete,
  while ambiguous drafts remain unchanged;
- semantic output contains two complete typed predicates;
- physical output has distinct binds and leaves both predicates inside their
  owning aggregates, with no contributor filter on the root or traversal;
- literal Arango execution returns `1` and `2` for a Patient with matching
  Observations, and retains a Patient with no matching contributors as `0` and
  `0`;
- Builder command, reconcile, preview, publish, Viewer, and reload retain the
  exact authored meaning.
