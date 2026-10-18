package lower

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestGroupFieldSourceContextPinsExactRouteAndCodingSelector(t *testing.T) {
	const selector = "valueCodeableConcept.coding[]"
	memberRoute := groupSourceTestPlan(t, selector, nil, "member-observation", "hasMember_Observation")
	derivedRoute := groupSourceTestPlan(t, selector, nil, "derived-observation", "derivedFrom_Observation")
	member, err := describeGroupFieldSourceContext("source_projection", "Observation", "__loom_expansion_identity", &memberRoute)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := describeGroupFieldSourceContext("source_projection", "Observation", "__loom_expansion_identity", &derivedRoute)
	if err != nil {
		t.Fatal(err)
	}
	if member == nil || derived == nil {
		t.Fatal("compiler did not expose the exact expanded source contexts")
	}
	if member.SourceShapeID == derived.SourceShapeID {
		t.Fatal("distinct Observation owner routes shared a source shape binding")
	}
	if member.SourceExpansion.OwnerOccurrenceID != "member-observation" ||
		member.SourceExpansion.OwnerResourceType != "Observation" || member.SourceExpansion.Selector != selector ||
		len(member.SourceExpansion.Route) != 1 || member.SourceExpansion.Route[0].EdgeLabel != "hasMember_Observation" {
		t.Fatalf("member route context = %#v", member.SourceExpansion)
	}
	if derived.SourceExpansion.OwnerOccurrenceID != "derived-observation" ||
		derived.SourceExpansion.Route[0].EdgeLabel != "derivedFrom_Observation" {
		t.Fatalf("derived route context = %#v", derived.SourceExpansion)
	}
	if member.SourceIdentityColumn != "__loom_expansion_identity" {
		t.Fatalf("expanded source identity = %q", member.SourceIdentityColumn)
	}
}

func TestGroupFieldSourceContextRejectsGroupedAndUnprovenSourceRows(t *testing.T) {
	grouped := groupSourceTestPlan(t, "identifier[]", nil, "", "")
	grouped.Operations = append(grouped.Operations[:1], append([]ir.PhysicalOperation{{Kind: ir.PhysicalGroupRowsOp, GroupRows: &ir.PhysicalGroupRows{}}}, grouped.Operations[1:]...)...)
	if context, err := describeGroupFieldSourceContext("source_projection", "Observation", "_key", &grouped); err != nil || context != nil {
		t.Fatalf("grouped source context = %#v, %v; want no capability", context, err)
	}
	root := groupSourceTestPlan(t, "identifier[]", nil, "", "")
	if context, err := describeGroupFieldSourceContext("source_projection", "Observation", "__loom_row_id", &root); err != nil || context != nil {
		t.Fatalf("unproven source identity context = %#v, %v; want no capability", context, err)
	}
	if context, err := describeGroupFieldSourceContext("source_projection", "Observation", "_key", &root); err != nil || context != nil {
		t.Fatalf("expanded source with root-only identity context = %#v, %v; want no capability", context, err)
	}
}

func TestGroupFieldSourceContextAcceptsOnlyTypedScopedPopulationRoots(t *testing.T) {
	plan := groupSourceTestPlan(t, "identifier[]", nil, "", "")
	root := plan.Operations[0].RootScan
	root.Population = &ir.PhysicalPopulationRootSource{
		MemberScan: ir.PhysicalCollectionScan{Variable: "population_member", CollectionBindKey: "selection_members"},
		MemberID:   ir.PhysicalValue{Variable: "population_member", Path: []string{"id"}},
		MemberFilters: []ir.PhysicalFilter{
			populationMemberFilter("selectionId", "population_selection_id"),
			populationMemberFilter("project", "population_project"),
			populationMemberFilter("generation", "dataset_generation"),
			populationMemberFilter("resourceType", "population_resource_type"),
		},
		ResourceOperations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{Variable: "population_source", CollectionBindKey: "population_resource_collection"}},
			{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{Variable: "population_source_scope_allowed", Operator: "AUTH_RESOURCE_PATH_ALLOWED", Inputs: []ir.PhysicalValue{
				{Variable: "population_source", Path: []string{"auth_resource_path"}}, {BindKey: "auth_resource_paths"}, {BindKey: "auth_resource_paths_unrestricted"},
			}}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "population_source_scope_allowed"}, Right: &ir.PhysicalValue{BindKey: "scope_allowed"}}}},
		},
		RootKey: ir.PhysicalValue{Variable: "population_source", Path: []string{"_key"}},
	}
	context, err := describeGroupFieldSourceContext("source_projection", "Observation", "__loom_expansion_identity", &plan)
	if err != nil || context == nil {
		t.Fatalf("typed population context = %#v, %v", context, err)
	}

	root.Population.ResourceOperations = root.Population.ResourceOperations[1:]
	if context, err := describeGroupFieldSourceContext("source_projection", "Observation", "__loom_expansion_identity", &plan); err != nil || context != nil {
		t.Fatalf("population without its typed resource scan/auth scope = %#v, %v; want no capability", context, err)
	}
}

func populationMemberFilter(field, bindKey string) ir.PhysicalFilter {
	right := ir.PhysicalValue{BindKey: bindKey}
	return ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "population_member", Path: []string{field}}, Right: &right}}
}

func TestGroupFieldSourceContextExposesOnlyExactDirectRootIdentity(t *testing.T) {
	plan := ir.PhysicalPlan{
		RootOccurrenceID: "root-observation",
		BindVars:         map[string]any{"root_collection": "Observation"},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{}},
		},
	}
	context, err := describeGroupFieldSourceContext("source_projection", "Observation", "_key", &plan)
	if err != nil || context == nil {
		t.Fatalf("direct root context = %#v, %v", context, err)
	}
	if context.SourceExpansion != nil || context.SourceIdentityColumn != "_key" || len(context.FieldOwners) != 1 ||
		context.FieldOwners[0].Role != "ROOT" || context.FieldOwners[0].OccurrenceID != "root-observation" {
		t.Fatalf("direct root context = %#v", context)
	}
}

func TestGroupAncestorFieldSourceUsesItsExactRepeatedBoundary(t *testing.T) {
	plan := groupSourceTestPlan(t, "component[].valueQuantity.value", []ir.PhysicalUnnestAncestor{{StepIndex: 0, Variable: "exact_component"}}, "", "")
	context, err := describeGroupFieldSourceContext("source_projection", "Observation", "__loom_expansion_identity", &plan)
	if err != nil || context == nil {
		t.Fatalf("describe component expansion: context=%#v err=%v", context, err)
	}
	var ancestor CompiledGroupFieldOwner
	for _, owner := range context.FieldOwners {
		if owner.Role == "ANCESTOR" {
			ancestor = owner
		}
	}
	if ancestor.BoundarySelector != "component[]" || ancestor.StepIndex != 0 || ancestor.OwnerResourceType != "Observation" {
		t.Fatalf("ancestor boundary = %#v", ancestor)
	}
	if context.SourceExpansion.Selector != "component[].valueQuantity.value" || len(context.SourceExpansion.Ancestors) != 1 ||
		context.SourceExpansion.Ancestors[0].BoundarySelector != "component[]" {
		t.Fatalf("component expansion shape = %#v", context.SourceExpansion)
	}
}

func groupSourceTestPlan(t *testing.T, selectorPath string, ancestors []ir.PhysicalUnnestAncestor, ownerOccurrence, edgeLabel string) ir.PhysicalPlan {
	t.Helper()
	selector, err := spec.ParseSelector(selectorPath)
	if err != nil {
		t.Fatal(err)
	}
	plan := ir.PhysicalPlan{
		RootOccurrenceID: "root-observation",
		BindVars:         map[string]any{"root_collection": "Observation"},
		Operations:       []ir.PhysicalOperation{{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}}},
	}
	owner := ir.PhysicalUnnestOwner{OccurrenceID: "root-observation", ResourceType: "Observation", RootVariable: "root", OwnerVariable: "root"}
	ownerVariable := "root"
	if ownerOccurrence != "" {
		owner.OccurrenceID, ownerVariable = ownerOccurrence, "expanded_observation"
		const edgeCollection, edgeLabelBind, fromType, toType = "route_edges", "route_edge_label", "route_from_type", "route_to_type"
		plan.BindVars[edgeCollection], plan.BindVars[edgeLabelBind] = "fhir_edge", edgeLabel
		plan.BindVars[fromType], plan.BindVars[toType] = "Observation", "Observation"
		owner.OwnerVariable = ownerVariable
		owner.Route = []ir.PhysicalUnnestRouteStep{{OccurrenceID: ownerOccurrence, Traversal: ir.PhysicalTraversal{
			SourceVariable: "root", TargetVariable: ownerVariable, EdgeVariable: "route_edge",
			Direction: ir.PhysicalInbound, EdgeCollectionBindKey: edgeCollection, EdgeLabelBindKey: edgeLabelBind,
			SourceTypeBindKey: fromType, TargetTypeBindKey: toType,
		}}}
	}
	unnest := ir.PhysicalUnnest{
		Owner: owner, OutputVariable: "coding_item", Ordinality: "coding_ordinal", HasItemVariable: "has_coding_item",
		Expression: ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
			NullBehavior: ir.PhysicalEmptyOnNull, Extract: &ir.PhysicalExtract{
				Source:       ir.PhysicalValue{Variable: ownerVariable, Path: []string{"payload"}},
				ResourceType: "Observation", Selector: selector,
			}},
		Ancestors: append([]ir.PhysicalUnnestAncestor(nil), ancestors...), EmptyPolicy: ir.PhysicalUnnestExclude,
	}
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalUnnestOp, Unnest: &unnest},
		ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{}},
	)
	return plan
}
