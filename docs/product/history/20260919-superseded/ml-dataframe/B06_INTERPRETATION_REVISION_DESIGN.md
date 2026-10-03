# B06 interpretation revision design

## Decision

Loom versions one reusable feature interpretation per immutable revision. A
workspace feature either keeps its inline authoring meaning or pins one exact
interpretation revision. It never follows a library head.

This boundary matches the thing a researcher reviews: one feature's mapping,
selection, reduction, and approved normalization. It avoids coupling unrelated
features into a whole-library snapshot and reuses the typed B05 authoring
contracts instead of introducing a second mapping language.

```text
feature editor
  -> proposed exact interpretation revision
  -> candidate workspace (memory only)
  -> exact resolved inputs
  -> immutable candidate receipt
  -> bounded before/after comparison
  -> explicit draft-CAS apply
```

Creating a child revision changes no workspace, receipt, publication, or
dataset. Applying it changes one feature reference in one draft.

## Domain

`internal/explorer/interpretation.go` owns the project-scoped immutable
aggregate. IDs are string-backed named types with validating constructors.

```go
type InterpretationLibraryID string
type InterpretationRevisionID string
type InterpretationRuleID string
type InterpretationContentDigest string
type InterpretationPriority int32

type InterpretationRevision struct {
	ID               InterpretationRevisionID
	Project          string
	LibraryID        InterpretationLibraryID
	ParentRevisionID *InterpretationRevisionID
	ParentDigest     *InterpretationContentDigest
	ContentDigest    InterpretationContentDigest
	Applicability    InterpretationApplicability
	Rules            []InterpretationRule
	Author           string
	Explanation      string
	CreatedAt        time.Time
}

type InterpretationApplicability struct {
	ResourceTypes   []string
	SourceProfiles  []string
	SourceCanonical []string
	LogicalTypes    []string
	Cardinalities   []string
	SchemaDigests   []string
}

type InterpretationRule struct {
	ID         InterpretationRuleID
	Priority   *InterpretationPriority
	Match      InterpretationStructuralMatch
	Definition InterpretationFeatureDefinition
}

type InterpretationFeatureDefinition struct {
	Source      authoringv2.ColumnSource
	Contributor *authoringv2.ContributorPredicate
}
```

`ColumnSource` already owns aggregate reduction, temporal selection, and unit
normalization. `ContributorPredicate` already owns typed contributor matching.
The revision embeds those validated values; it does not accept AQL, recipe
expressions, arbitrary conversion coefficients, or an untyped options map.

`InterpretationStructuralMatch` is a closed structural matcher over the
observed catalog identity used by the first repair journey: resource type,
source profile/canonical, owning scope, concept system/code, extension URL
ancestry, logical type, and cardinality. Empty optional dimensions are
wildcards. Revision applicability and rule matching are conjunctive.

All matching rules inspect the same structural candidate. Slice order has no
meaning. A single match wins. With several matches, the unique greatest
explicit priority wins. A top-priority tie, or overlapping matches without a
defined winner, returns `AMBIGUOUS_MAPPING`. Statically provable ambiguity is
rejected when the revision is created. Runtime resolution repeats the check
against the exact capability snapshot.

The initial B06 repair is structural: it approves which FHIR key/value binding
and existing typed policies define the feature. It does not add a generic
value-recoding engine. Raw concepts that do not match remain unresolved and
browsable. Later value recoding, if justified by a real journey, must add a
closed typed operator through recipe, semantic IR, physical IR, and execution;
it must not be evaluated only by the preview API.

## Canonical identity and persistence

Canonical executable content contains applicability and rules. Canonicalization
normalizes owned authoring values, sorts and deduplicates set-like fields,
sorts rules by stable rule ID, preserves priority as data, and rejects invalid
closed variants and duplicate IDs.

`ContentDigest` is `sha256:` plus the SHA-256 of canonical executable content.
It excludes author, explanation, timestamps, project, lineage, and IDs.
`RevisionID` is derived from a separate canonical envelope containing project,
library ID, parent revision and digest, content digest, author, and explanation.
The server recomputes both identities before persistence.

Arango uses two collections:

- `loom_explorer_interpretation_libraries` stores the mutable authoring head;
- `loom_explorer_interpretation_revisions` stores immutable revisions.

Creation uses one transaction. It inserts the immutable revision and advances
the library head only when it equals the expected parent. An identical retry
succeeds. A stale parent returns `ErrInterpretationParentConflict`; revision-ID
reuse with different immutable content returns `ErrImmutableInterpretation`.
Every read filters by canonical project and exact revision ID.

The narrow repository contract is:

```go
type InterpretationRepository interface {
	ListInterpretationLibraries(context.Context, string) ([]InterpretationLibrary, error)
	GetInterpretationRevision(context.Context, string, InterpretationRevisionID) (*InterpretationRevision, error)
	CreateInterpretationRevision(context.Context, InterpretationRevision, *InterpretationRevisionID) (*InterpretationRevision, error)
}
```

The Explorer store may embed this interface for wiring, but lifecycle depends
on the narrow contract.

## Authoring reference and exact resolution

`authoringv2.Column` gains a closed interpretation mode. Existing inline fields
remain where they are during B06; they are not relocated merely for symmetry.

```go
type FeatureInterpretation struct {
	Kind   FeatureInterpretationKind // PINNED is the only non-nil variant
	Pinned *PinnedInterpretation
}

type PinnedInterpretation struct {
	RevisionID string
}
```

Nil is the single inline state. A non-nil `PINNED` value requires one revision
ID; there is no second explicit-inline representation with identical behavior.
The existing source remains a valid structural anchor for applicability, while
the resolved human definition is the sole executable source/contributor
meaning. Presentation metadata remains feature-local and is never part of an
interpretation.

During reconcile, lifecycle collects distinct exact revision IDs, loads each
once, validates project and applicability against the retained authorized
capability snapshot, selects one rule per pinned feature, and constructs:

```go
type ResolvedInputs struct {
	Populations     []ResolvedPopulation
	Interpretations []ResolvedInterpretation
}

type ResolvedInterpretation struct {
	OutputID       string
	Column         string
	OccurrenceID   string
	Revision       explorer.InterpretationRevision
	SelectedRuleID explorer.InterpretationRuleID
	Definition     explorer.InterpretationFeatureDefinition
}
```

The resolved input contains canonical revision content, not only its ID.
`CompileWorkspace` remains pure and performs no store lookup. The selected
human definition is the effective source/contributor meaning for that feature
and takes precedence over structural suggestions. `ResolvedInputsDigest`, the
compilation key, recipe meaning, and receipt identity cover the complete
resolved value. Old receipts execute without an interpretation-store lookup.

## Candidate comparison and apply

`POST .../authoring/v2/interpretation-preview` accepts current draft version
and digest, snapshot token, one target output/column, and one exact proposed
revision. Lifecycle:

1. checks the current draft identity and authorization;
2. compiles the saved workspace to a base receipt;
3. clones the workspace, pins the proposal in memory, and compiles a candidate
   receipt under the same generation and authorization scope;
4. compares the target feature through the normal execution path;
5. returns immutable receipt IDs and a bounded typed comparison.

The comparison reports `COMPLETE` or `INCOMPLETE`, scanned and known-total
counts, changed values, resolved/unresolved counts, ambiguity/type/unit reason
counts, and bounded raw before/after samples. Exhausting a row, time, or byte
limit is `INCOMPLETE`; it never implies a whole-population claim. The operation
writes neither the draft nor source FHIR.

Apply remains on the existing authoring command route. The command names the
candidate receipt, target feature, and exact revision. Before calling the pure
reducer, lifecycle reconstructs the proposed workspace and proves its digest
matches the candidate receipt's intent. It also verifies project, Explorer,
snapshot, generation, authorization scope, target, revision content, and
selected rule. Existing command idempotency and draft version/digest CAS then
commit one new draft. Cancel only clears local UI review state.

## UI

The feature editor adds an Interpretation section that:

- shows inline versus pinned meaning and exact revision provenance;
- browses applicable revisions without exposing irrelevant library entries;
- shows the human explanation and structural key/value binding;
- previews raw affected values, changed values, unresolved cases, and bounded
  completeness;
- keeps Review, Cancel, and Apply separate;
- disables Apply when the draft or reviewed proposal has gone stale.

The UI holds comparison state locally as `idle | loading | ready | error`. It
does not maintain a second editable workspace and does not optimistically claim
application. A successful command response reloads canonical server state and
invalidates the old receipt and preview.

## Implementation sequence

1. Domain, canonical identity, ambiguity validation, and immutable repository.
2. Arango collections, project-scoped reads, idempotent insert, and parent CAS.
3. Closed workspace reference, exact lifecycle resolution, resolved-input
   digest, pure compiler substitution, and old-receipt stability.
4. Candidate receipt and bounded comparison through the normal execution path.
5. Exact candidate-proof apply command and HTTP contracts.
6. Feature-editor browse/review/cancel/apply flow and live verification.

Each unit ends with focused executable proof. B06 closes only after the live
journey repairs one recurring Observation component binding, cancels without a
draft change, applies it, reloads it, retains raw unmatched concepts, denies
unauthorized access, and proves another dataset pinned to the parent is
unchanged. Performance evidence must show at most one store resolution per
distinct revision and keep the small-fixture comparison inside the warm
30-second loop.

## Rejected designs

- Whole-library revisions couple unrelated features and require a second
  `revision + definition` selection without improving receipt immutability.
- Mutable head references make reconcile time-dependent.
- Copying library content inline loses the exact approval lineage.
- Moving all B05 policy fields in B06 creates migration risk without user value.
- A preview-only evaluator can disagree with publication.
- Automatic repinning violates immutable consumer intent.
