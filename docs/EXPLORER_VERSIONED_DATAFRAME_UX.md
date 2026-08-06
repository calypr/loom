# Explorer UX for versioned dataframes

The Explorer repository is not present in this checkout. This specification
and the typed contract in [`examples/explorer-client`](../examples/explorer-client/README.md)
are the implementation handoff; they do not imply that a frontend patch was
made in Loom.

## Selection and default scope

Explorer stores one explicit selector with every view:
`recipe`, `translationVersion`, and `output`. The selector control lists exact
identities returned by dataset discovery and must allow two versions or two
recipes with the same output to be selected independently. Do not collapse
options by output name.

Changing the selector clears the row cursor, selected columns, and filters
that do not exist in the new dataset. The default view includes every project
authorized for the caller. Explorer never sends a project list to establish
authorization. A user-selected `project_id` equality or IN filter only narrows
the already-authorized cohort.

The deprecated `dataType` variable remains a compatibility path for saved
views during one migration window. New or resaved views write `selector` and
must never send both fields.

## Availability and rows

- `AVAILABLE`: render rows normally. A quiet summary may show project coverage.
- `DEGRADED`: render every returned row and keep filtering, sorting, paging,
  aggregation, and export enabled. Show the status banner above the table.
- `UNAVAILABLE`: show the status banner and diagnostics instead of the table.
  This state is reserved for a dataset with no source that Loom can serve.

Do not infer availability from `/readyz`. Readiness describes infrastructure;
dataset progress and failures come from dataset metadata.

Rows are open records. A column present only in some projects is a normal
sparse field: render a blank/null cell when absent. Do not treat a missing row
property as schema drift. Column capabilities come from `columns`, not from a
hard-coded resource model.

## Status banner

The compact banner shows availability, `includedProjectCount / expectedProjectCount`,
completeness, and six counts in this order: current, stale, building, failed,
missing, excluded. Zero counts may be visually muted but remain available to
screen readers. Suggested summaries:

- `AVAILABLE · 8/8 projects current`
- `DEGRADED · 6/8 included · 1 stale · 1 building`
- `UNAVAILABLE · 0/8 included · 5 building · 3 failed`

For `DEGRADED`, the banner explicitly says that available rows are still being
shown. A failed replacement must not blank rows from the prior active release.

## Authorized project diagnostics

An expandable region lists only `projectStatuses` returned by Loom. It never
merges project names from configuration, prior responses, URL state, or a
global project catalog, because those sources could reveal unauthorized
identities.

Each row shows project, state, generation, execution, last update, stable error
code, and retryability when present. Raw backend errors and physical table
names are never rendered. State treatments:

| State | Meaning | UI action |
| --- | --- | --- |
| `CURRENT` | Active generation and selected publication match | None |
| `STALE` | Older optional publication is still serving | Explain that rows remain usable |
| `BUILDING` | Replacement is queued/running/validating | Offer refresh/poll |
| `FAILED` | Publication failed; prior active data may remain | Show safe code and retryability |
| `MISSING` | No selected publication exists | Explain coverage gap |
| `EXCLUDED` | Source cannot join the federation or contract | Show safe incompatibility code |

Use `errorRetryable`/`retryable` exactly as returned. Never derive retryability
from HTTP status or message text.

## Loading, errors, and accessibility

Retain the previous successful table while a selector refresh is in flight.
Replace it only after a successful response for the current selector token.
Discard late responses for older tokens. GraphQL failures use stable
`errors[].extensions.code`, `retryable`, and `requestId`; display the request
ID in expandable support details.

The banner uses text and icons in addition to color. The diagnostic disclosure
is keyboard operable, announces count changes, and does not move focus during
polling.

## Acceptance fixtures

The checked-in fixtures cover available, degraded/building, degraded/stale,
degraded/failed, missing/excluded, unavailable, sparse columns, multiple
versions, legacy `dataType`, and unauthorized-project filtering. The external
Explorer should import or mechanically mirror them in component tests.

