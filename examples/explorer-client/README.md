# Explorer client contract

This directory is a framework-neutral handoff for the external Explorer
repository. `types.ts` defines the exact selector, federation metadata, sparse
rows, status counts, and structured error shapes. `queries.ts` contains typed
variable builders, while `explorer.graphql` is usable by GraphQL code
generators and command-line clients.

Run the contract and fixture checks from the Loom root:

```bash
make explorer-contract-test
```

With Loom running locally, execute the checked-in exact-selector query:

```bash
make explorer-query
```

Override `EXPLORER_GRAPHQL_URL`, `EXPLORER_QUERY`, or `EXPLORER_VARIABLES` as needed.
The external Explorer implementation should generate its framework-specific
client from `explorer.graphql`; Loom does not vendor or fabricate that app.
