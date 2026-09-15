# Published dataframe

## Sub-features

- Verify the physical ClickHouse table and patient-grain row count.
- Query dataset metadata, sorted rows, count, and facets through GraphQL.
- Repeat publication and verify the execution and selector remain stable.

## How to get to it (user POV)

Run `make acceptance-real`; publication automatically drives this read surface.

## Driving it with HTTP

Resolve the physical output from the execution registry, inspect it directly in
ClickHouse, then issue the four GraphQL operations on `/graphql/graph`.
The driver fails on any GraphQL `errors` member and compares counts to the
checked-in oracle.

## Gotchas

The oracle is independent from fixture refresh. Review an explicit candidate
oracle change separately; do not make upstream drift silently alter expected
results.
