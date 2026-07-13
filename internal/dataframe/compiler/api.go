// Package compiler is the stable dataframe compiler facade.
//
// Compilation proceeds through spec, semantic, ir, lower, optimize, and
// render/aql. The facade keeps the historical public symbols available while
// each implementation layer remains independently navigable and testable.
package compiler
