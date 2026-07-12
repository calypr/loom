// Package dataframeexport connects validated dataframe execution to the
// row-only export encoders.
//
// It deliberately owns neither dataframe compilation nor artifact storage,
// jobs, delivery transports, or destination-specific behavior. A RowStream
// calls its Runner for every invocation. With a *dataframe.Service, the
// RunRequest is therefore catalog- and authorization-validated as part of
// each execution; this package neither caches that validation nor collects
// result rows.
//
// Inferred CSV columns require two executions: one to discover the column
// union and one to write rows. Those executions must observe the same logical
// dataframe and ordering, which requires an externally stable dataset
// generation or snapshot. Loom does not provide that generation/snapshot
// contract yet. Supplying explicit CSV columns uses exactly one execution.
package dataframeexport
