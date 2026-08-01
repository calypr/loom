// Package graphqlapi owns Loom's primary GraphQL transport.
//
// HTTP wiring and error presentation live here. Query construction lives in
// query, dataframe response mapping in materialization, and the flat endpoint
// handler in the sibling flat package. Generated GraphQL types, executors, and resolver
// bindings live outside internal under generated/graphql/graph.
package graphqlapi
