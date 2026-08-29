package schema

import generatedschema "github.com/calypr/loom/generated/fhirschema"

// These aliases keep generated FHIR metadata private to this package while
// allowing the schema facade to expose stable, hand-written semantics.
type TraversalSpec = generatedschema.Traversal
type generatedProperty = generatedschema.Property

var (
	generatedResourceTypes = generatedschema.ResourceTypes
	generatedDefinitions   = generatedschema.Definitions
	generatedTraversals    = generatedschema.Traversals
)
