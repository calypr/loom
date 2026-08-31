// Package graphqlapi owns Loom's primary GraphQL transport.
//
// HTTP wiring and error presentation live here. Query construction lives in
// query, dataframe access and response mapping in dataframe, resolver wiring in
// resolver, and the schema source in schema. Generated GraphQL types, executors,
// and resolver bindings live outside internal under generated/graphql/graph.
package graphqlapi
