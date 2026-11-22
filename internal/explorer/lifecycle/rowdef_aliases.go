package lifecycle

import (
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/rowdef"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type RowChoiceKind = rowdef.RowChoiceKind

const (
	RowChoiceFieldGroup = rowdef.RowChoiceFieldGroup
	RowChoiceExpanded   = rowdef.RowChoiceExpanded
)

type RowDefinitionChoice = rowdef.RowDefinitionChoice
type RowDefinitionChoicePolicy = rowdef.RowDefinitionChoicePolicy
type RowChoiceResolveRequest = rowdef.RowChoiceResolveRequest
type ResolvedRowChoice = rowdef.ResolvedRowChoice
type RowChoiceResolver = rowdef.RowChoiceResolver
type RowChoicePlanner = rowdef.RowChoicePlanner
type SchemaRowChoiceResolver = rowdef.SchemaRowChoiceResolver

func NewSchemaRowChoiceResolver(index *fhirschema.Index) (*SchemaRowChoiceResolver, error) {
	return rowdef.NewSchemaRowChoiceResolver(index)
}

func fhirIdentifierPath(path string) bool { return rowdef.FHIRIdentifierPath(path) }
func applicableRowChoiceKinds(facts capability.RowChoiceFacts) []capability.RowChoiceKind {
	return rowdef.ApplicableRowChoiceKinds(facts)
}
func resolveRowChoiceOccurrence(snapshot capability.Snapshot, rootResourceType string, authoredRoot authoringv2.RouteNode, occurrenceID, expectedNodeID string) (capability.RowChoiceOccurrence, error) {
	return rowdef.ResolveRowChoiceOccurrence(snapshot, rootResourceType, authoredRoot, occurrenceID, expectedNodeID)
}
func capabilityRowChoiceKind(kind RowChoiceKind) (capability.RowChoiceKind, error) {
	return rowdef.CapabilityRowChoiceKind(kind)
}
func lifecycleRowChoiceKind(kind capability.RowChoiceKind) RowChoiceKind {
	return rowdef.LifecycleRowChoiceKind(kind)
}
func rowChoicePolicies(kind capability.RowChoiceKind) []RowDefinitionChoicePolicy {
	return rowdef.RowChoicePolicies(kind)
}
func rowChoiceOccurrenceSummary(choice capability.RowChoice) string {
	return rowdef.OccurrenceSummary(choice)
}
func rowChoiceRouteSummary(choice capability.RowChoice) string {
	return rowdef.RouteSummary(choice)
}
