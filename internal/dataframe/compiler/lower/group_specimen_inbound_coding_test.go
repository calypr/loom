package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestSpecimenInboundObservationCodingGroupUsesExactExpandedSource(t *testing.T) {
	output := recipe.Output{
		Name: "specimen_observation_codings", RootResourceType: "Specimen", RootOccurrenceID: "specimen-root", RowGrain: "expanded",
		Fields: []recipe.Field{{Name: "id", ColumnID: "specimen-id", Expr: recipe.Expression{Select: "root.id"}}},
		Traversals: []recipe.Traversal{{
			Name: "focus_Specimen", OccurrenceID: "observation-owner", Alias: "observation", ToResourceType: "Observation",
		}},
		Expand: &recipe.Expansion{
			OwnerOccurrenceID: "observation-owner", From: recipe.Expression{Select: "observation.valueCodeableConcept.coding[]"},
			As: "coding", Ordinality: "coding_ordinal", EmptyPolicy: recipe.ExpansionPreserveParent,
		},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
	}
	compiledSource := compileDerivedTestOutput(t, output)
	if got := constructionSourceIdentity(compiledSource.OutputSchema); got != "__loom_expansion_identity" {
		t.Fatalf("source identity = %q, want expanded occurrence identity", got)
	}
	context, err := describeGroupFieldSourceContext(recipe.ConstructionSourceProjectionID, output.RootResourceType,
		"__loom_expansion_identity", &compiledSource.Plan)
	if err != nil || context == nil || context.SourceExpansion == nil {
		t.Fatalf("describe exact expanded source context: context=%#v err=%v", context, err)
	}
	if len(context.SourceExpansion.Route) != 1 || context.SourceExpansion.Route[0].EdgeLabel != "focus_Specimen" ||
		context.SourceExpansion.Route[0].Direction != string(ir.PhysicalInbound) ||
		context.SourceExpansion.OwnerResourceType != "Observation" || context.SourceExpansion.Selector != "valueCodeableConcept.coding[]" {
		t.Fatalf("expanded source shape = %#v, want Specimen → focus_Specimen INBOUND → Observation Coding", context.SourceExpansion)
	}
	makeGroupedOutput := func(source recipe.ConstructionGroupFieldSource) recipe.Output {
		grouped := output
		grouped.Construction = &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "specimen-id", Name: "id", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_codings", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_codings", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
					Keys:       []recipe.ConstructionGroupKey{{OutputColumnID: "coding-label", FieldSource: &source}},
					Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows"}},
				}},
				Outputs: []recipe.StageColumn{{ID: "coding-label", Name: "coding_label", Type: "string"}, {ID: "rows", Name: "rows", Type: "integer"}},
			}},
		}
		return grouped
	}
	nestedSource := recipe.ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: context.SourceStageID, SourceShapeID: context.SourceShapeID,
		Input: &recipe.ConstructionGroupFieldSourceInput{Kind: "CURRENT_STAGE", CurrentStage: &recipe.ConstructionGroupCurrentStageFieldSource{
			SourceRole: "CURRENT_ITEM", OccurrenceID: context.SourceExpansion.OwnerOccurrenceID,
			OwnerResourceType: context.SourceExpansion.OwnerResourceType, ResourceType: "Coding",
			CanonicalPath: context.SourceExpansion.Selector,
		}},
	}
	compiled := compileDerivedTestOutput(t, makeGroupedOutput(nestedSource))
	sequence := compiled.Plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 1 || sequence.SourceRowIdentity != "__loom_expansion_identity" ||
		sequence.Stages[0].InputStageID != recipe.ConstructionSourceProjectionID || sequence.Stages[0].Group == nil ||
		sequence.Stages[0].Group.CodingKey == nil {
		t.Fatalf("compiled expanded Coding Group = %#v", sequence)
	}
	coding := sequence.Stages[0].Group.CodingKey
	if coding.SourceStageID != recipe.ConstructionSourceProjectionID || coding.SourceShapeID != context.SourceShapeID ||
		coding.OwnerResourceType != "Observation" || coding.CodingPath != "valueCodeableConcept.coding[]" ||
		coding.SourceOccurrenceKeyColumn == "" || coding.MembershipColumn == "" {
		t.Fatalf("compiled Coding source binding = %#v, want exact source stage, shape, owner, selector, and occurrence projection", coding)
	}
	legacySource := recipe.ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: context.SourceStageID, SourceShapeID: context.SourceShapeID,
		SourceRole: "CURRENT_ITEM", OccurrenceID: context.SourceExpansion.OwnerOccurrenceID,
		OwnerResourceType: context.SourceExpansion.OwnerResourceType, ResourceType: "Coding", CanonicalPath: context.SourceExpansion.Selector,
	}
	legacy := compileDerivedTestOutput(t, makeGroupedOutput(legacySource)).Plan.StageSequence.Stages[0].Group.CodingKey
	if legacy.CodingPath != coding.CodingPath || legacy.SourceShapeID != coding.SourceShapeID ||
		legacy.OwnerOccurrenceID != coding.OwnerOccurrenceID || legacy.OwnerResourceType != coding.OwnerResourceType {
		t.Fatalf("nested and legacy Coding source bindings differ: nested=%#v legacy=%#v", coding, legacy)
	}
	for _, test := range []struct {
		name   string
		mutate func(*recipe.ConstructionGroupFieldSource)
	}{
		{name: "changed source shape", mutate: func(source *recipe.ConstructionGroupFieldSource) {
			source.SourceShapeID = "group-source-shape-v1:changed"
		}},
		{name: "non-Coding path", mutate: func(source *recipe.ConstructionGroupFieldSource) {
			source.Input.CurrentStage.CanonicalPath = "status"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			badSource := nestedSource
			badInput := *nestedSource.Input
			badCurrent := *nestedSource.Input.CurrentStage
			badInput.CurrentStage = &badCurrent
			badSource.Input = &badInput
			test.mutate(&badSource)
			if _, err := compileDerivedTestBundle(t, makeGroupedOutput(badSource)); err == nil || !strings.Contains(err.Error(), "CODING_IDENTITY fieldSource") {
				t.Fatalf("invalid Coding source error = %v, want exact source authority rejection", err)
			}
		})
	}
}
