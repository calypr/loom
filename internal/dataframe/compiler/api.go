// Package compiler orchestrates dataframe compilation.
//
// Compilation proceeds through spec, semantic, ir, lower, optimize, and
// render/aql, with each layer owning its canonical types and transformations.
package compiler
