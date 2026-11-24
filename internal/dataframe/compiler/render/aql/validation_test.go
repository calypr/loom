package aql

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestCollectionBindKeysWalksNestedPhysicalChildrenAndStageCollections(t *testing.T) {
	keys := []string{
		"root", "page_candidate", "population_root", "population_member", "population_resource",
		"cohort_root", "cohort_revision", "cohort_selection", "cohort_selection_members", "cohort_memberships",
		"route_edge", "route_scope", "expression_lookup",
		"stage_revision", "stage_selection", "stage_definitions", "stage_memberships", "stage_selection_members", "stage_resource",
	}
	binds := make(map[string]any, len(keys)+1)
	for _, key := range keys {
		binds[key] = "collection_" + key
	}
	binds["scalar_value"] = "ordinary scalar"

	plan := ir.PhysicalPlan{
		BindVars: binds,
		Operations: []ir.PhysicalOperation{
			{
				Kind: ir.PhysicalRootScanOp,
				RootScan: &ir.PhysicalRootScan{
					Variable: "root", CollectionBindKey: "root",
					PageCandidateSeed: &ir.PhysicalRootPageCandidateSeed{Subplan: ir.PhysicalSubplan{Operations: []ir.PhysicalOperation{{
						Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{Variable: "page", CollectionBindKey: "page_candidate"},
					}}}},
				},
			},
			{
				Kind: ir.PhysicalRootScanOp,
				RootScan: &ir.PhysicalRootScan{
					Variable: "population_root", CollectionBindKey: "population_root",
					Population: &ir.PhysicalPopulationRootSource{
						MemberScan:         ir.PhysicalCollectionScan{Variable: "members", CollectionBindKey: "population_member"},
						ResourceOperations: []ir.PhysicalOperation{{Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{Variable: "resource", CollectionBindKey: "population_resource"}}},
					},
				},
			},
			{
				Kind: ir.PhysicalRootScanOp,
				RootScan: &ir.PhysicalRootScan{
					Variable: "cohort_root", CollectionBindKey: "cohort_root",
					CohortSource: &ir.PhysicalCohortRootSource{
						RevisionCollectionBindKey: "cohort_revision", SelectionCollectionBindKey: "cohort_selection",
						SelectionMembersCollectionBindKey: "cohort_selection_members", MembershipsCollectionBindKey: "cohort_memberships",
					},
				},
			},
			{
				Kind: ir.PhysicalUnnestOp,
				Unnest: &ir.PhysicalUnnest{Owner: ir.PhysicalUnnestOwner{Route: []ir.PhysicalUnnestRouteStep{{
					Traversal: ir.PhysicalTraversal{EdgeCollectionBindKey: "route_edge"},
					Scope:     []ir.PhysicalOperation{{Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{Variable: "route_scope", CollectionBindKey: "route_scope"}}},
				}}}},
			},
			{
				Kind: ir.PhysicalExpressionLetOp,
				ExpressionLet: &ir.PhysicalExpressionLet{Expression: ir.PhysicalExpression{
					Kind: ir.PhysicalSubplanExpression,
					Subplan: &ir.PhysicalSubplan{Operations: []ir.PhysicalOperation{{
						Kind:         ir.PhysicalKeySetLookupOp,
						KeySetLookup: &ir.PhysicalKeySetLookup{CollectionBindKey: "expression_lookup", Keys: ir.PhysicalValue{BindKey: "scalar_value"}},
					}}},
				}},
			},
		},
		StageSequence: &ir.PhysicalStageSequence{Stages: []ir.PhysicalConstructionStage{{
			ID: "cohort-stage",
			CohortGroup: &ir.PhysicalStageCohortGroup{Rows: ir.PhysicalGroupRows{
				RevisionCollectionBindKey: "stage_revision", SelectionCollectionBindKey: "stage_selection",
				DefinitionsCollectionBindKey: "stage_definitions", MembershipsCollectionBindKey: "stage_memberships",
				SelectionMembersCollectionBindKey: "stage_selection_members", ResourceCollectionBindKey: "stage_resource",
			}},
		}}},
	}

	got, err := collectionBindKeys(plan)
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		want[key] = struct{}{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collection bind keys = %#v, want %#v", got, want)
	}
}

func TestCollectionBindKeysIncludesNestedCollectionsInReturnProjection(t *testing.T) {
	plan := ir.PhysicalPlan{
		Version: 1,
		BindVars: map[string]any{
			"root_collection":   "Patient",
			"nested_collection": "Observation",
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{
				Name: "observations",
				Expression: &ir.PhysicalExpression{
					Kind: ir.PhysicalSubplanExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
					Subplan: &ir.PhysicalSubplan{
						Captures:   []string{"root"},
						Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{Variable: "observation", CollectionBindKey: "nested_collection"}}},
						Return:     ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: "observation", Path: []string{"_key"}}},
					},
				},
			}}}},
		},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("fixture must be a valid physical plan: %v", err)
	}

	got, err := collectionBindKeys(plan)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{}{"root_collection": {}, "nested_collection": {}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collection bind keys = %#v, want %#v", got, want)
	}
}
