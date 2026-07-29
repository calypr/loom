// Package dataframe is Loom's public dataframe facade.
//
// Request contracts live in spec, schema-backed planning in semantic, physical
// planning in compiler, and catalog-aware execution in runtime. The facade
// keeps the public service and compiler contracts discoverable while the
// implementation remains in those owning subpackages.
package dataframe
