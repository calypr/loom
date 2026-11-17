package recipeimport

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

type PlanEdit struct {
	StepChanges  []authoringv2.ImportedSourceStepChange
	Relationship *authoringv2.ImportedRelationshipChange
	RuleCreate   *authoringv2.SourceRuleCreate
}

type PlanEditInput struct {
	PinnedFieldIDs  map[string]string
	Occurrences     []authoringv2.NetworkOccurrence
	NewOccurrenceID string
}

func EditPlan(original recipe.Output, plan authoringv2.ImportedSourcePlan, catalog authoringv2.CatalogSnapshot, edit PlanEdit, input PlanEditInput) (authoringv2.ImportedSourcePlan, error) {
	changes, relationship, rule := edit.StepChanges, edit.Relationship, edit.RuleCreate
	count := 0
	if changes != nil {
		count++
		if err := authoringv2.ValidateImportedSourceStepBatch(changes); err != nil {
			return authoringv2.ImportedSourcePlan{}, err
		}
	}
	if relationship != nil {
		count++
		if err := relationship.Validate(); err != nil {
			return authoringv2.ImportedSourcePlan{}, err
		}
	}
	if rule != nil {
		count++
		if err := rule.Validate(); err != nil {
			return authoringv2.ImportedSourcePlan{}, err
		}
	}
	if count != 1 {
		return authoringv2.ImportedSourcePlan{}, fmt.Errorf("source plan edit requires exactly one intent")
	}
	plan, err := HydrateSourcePlan(original, &plan)
	if err != nil {
		return authoringv2.ImportedSourcePlan{}, err
	}
	baseStepCount := len(SourcePlan(original).Steps)
	if rule != nil {
		addition := authoringv2.ImportedSourceRuleAddition{
			StepID: fmt.Sprintf("ruleAdditions[%d]", len(plan.RuleAdditions)),
			Kind:   rule.Kind, OccurrenceID: rule.OccurrenceID, Enabled: true, Parameters: relativeSourceRuleParameters(rule.OccurrenceID, rule.Parameters),
		}
		plan.RuleAdditions = append(plan.RuleAdditions, addition)
	}
	for _, change := range changes {
		if change.Parameters != nil && change.Parameters.FieldGroup != nil {
			if err := VerifyPinnedFieldBindings(plan.FieldBindings, input.PinnedFieldIDs); err != nil {
				return authoringv2.ImportedSourcePlan{}, err
			}
			bound := map[string]bool{}
			for _, binding := range plan.FieldBindings {
				bound[binding.FieldID] = true
			}
			for _, field := range change.Parameters.FieldGroup.Fields {
				id := input.PinnedFieldIDs[field.FieldID]
				if id == "" {
					return authoringv2.ImportedSourcePlan{}, fmt.Errorf("source field %q has no unambiguous emitted identity in the pinned projection; resolve its source collision before editing", field.FieldID)
				}
				if !bound[field.FieldID] {
					plan.FieldBindings = append(plan.FieldBindings, authoringv2.ImportedSourceFieldBinding{FieldID: field.FieldID, ColumnID: id})
					bound[field.FieldID] = true
				}
			}
		}
		additionFound := false
		for i := range plan.RuleAdditions {
			addition := &plan.RuleAdditions[i]
			if addition.StepID != change.StepID {
				continue
			}
			if change.MaxColumns != nil || change.ValueMode != "" {
				return authoringv2.ImportedSourcePlan{}, fmt.Errorf("dynamic addition edits do not support a column limit or value mode")
			}
			if change.Parameters != nil {
				if err := change.Parameters.Validate(addition.Kind); err != nil {
					return authoringv2.ImportedSourcePlan{}, err
				}
				addition.Parameters = relativeSourceRuleParameters(addition.OccurrenceID, change.Parameters)
			}
			addition.Enabled = change.Enabled
			additionFound = true
			break
		}
		if additionFound {
			continue
		}
		found := false
		for i := range plan.Steps {
			step := &plan.Steps[i]
			if step.ID != change.StepID {
				continue
			}
			if !step.Editable {
				return authoringv2.ImportedSourcePlan{}, fmt.Errorf("this source step cannot be changed")
			}
			if change.MaxColumns != nil && step.MaxColumns == nil {
				return authoringv2.ImportedSourcePlan{}, fmt.Errorf("this source step has no column limit")
			}
			if change.ValueMode != "" && step.ValueMode == "" {
				return authoringv2.ImportedSourcePlan{}, fmt.Errorf("this source step has no value policy")
			}
			if change.Parameters != nil {
				if err := change.Parameters.Validate(step.Kind); err != nil {
					return authoringv2.ImportedSourcePlan{}, err
				}
				step.Parameters = change.Parameters
			}
			step.Enabled = change.Enabled
			if change.MaxColumns != nil {
				value := *change.MaxColumns
				step.MaxColumns = &value
			}
			if change.ValueMode != "" {
				step.ValueMode = change.ValueMode
			}
			found = true
		}
		if !found {
			return authoringv2.ImportedSourcePlan{}, fmt.Errorf("source step was not found")
		}
	}

	if relationship != nil {
		if plan.Relationships == nil {
			plan.Relationships = &authoringv2.ImportedRelationshipOverlay{}
		}
		overlay := plan.Relationships
		occurrences := map[string]authoringv2.NetworkOccurrence{}
		for _, o := range input.Occurrences {
			if o.Enabled {
				occurrences[o.OccurrenceID] = o
			}
		}
		switch relationship.Action {
		case "ADD":
			parent, ok := occurrences[relationship.ParentOccurrenceID]
			if !ok {
				return authoringv2.ImportedSourcePlan{}, fmt.Errorf("selected parent occurrence is unavailable")
			}
			var edge *authoringv2.CatalogEdge
			for i := range catalog.Edges {
				if catalog.Edges[i].ID == relationship.EdgeID {
					edge = &catalog.Edges[i]
					break
				}
			}
			if edge == nil {
				return authoringv2.ImportedSourcePlan{}, fmt.Errorf("selected catalog edge is unavailable")
			}
			var from, to *authoringv2.CatalogNode
			for i := range catalog.Nodes {
				node := &catalog.Nodes[i]
				if node.ID == edge.FromNodeID {
					from = node
				}
				if node.ID == edge.ToNodeID {
					to = node
				}
			}
			if from == nil || to == nil || from.ResourceType != parent.ResourceType {
				return authoringv2.ImportedSourcePlan{}, fmt.Errorf("selected catalog edge does not extend this occurrence")
			}
			// Reject a resource repeated along this ancestor path. Independent paths
			// to the same resource type retain separate occurrence identities.
			for ancestor := parent; ; {
				if ancestor.ResourceType == to.ResourceType {
					return authoringv2.ImportedSourcePlan{}, fmt.Errorf("cyclic source relationship is unsupported")
				}
				if ancestor.ParentOccurrenceID == "" {
					break
				}
				next, ok := occurrences[ancestor.ParentOccurrenceID]
				if !ok {
					return authoringv2.ImportedSourcePlan{}, fmt.Errorf("source ancestor is unavailable")
				}
				ancestor = next
			}
			id := input.NewOccurrenceID
			addition := authoringv2.ImportedRelationship{OccurrenceID: id, ParentOccurrenceID: parent.OccurrenceID, CatalogEdgeID: edge.ID, Relationship: edge.Label, ResourceType: to.ResourceType, StorageDirection: edge.StorageDirection, MatchMode: relationship.MatchMode}
			// Related identifiers remain lists even when the preview happens to have
			// one match. Catalog authority is required for the copied scalar field.
			for _, field := range catalog.Candidates {
				if field.NodeID == to.ID && (field.FieldPath == "id" || field.FieldPath == to.ResourceType+".id") && !field.Repeated {
					addition.Fields = []authoringv2.ImportedRelationshipField{{CandidateID: field.ID, ColumnID: id + "_id", Column: id + "_id", FieldPath: field.FieldPath, Label: field.Label, ValueMode: "ALL"}}
					break
				}
			}
			if len(addition.Fields) == 0 {
				return authoringv2.ImportedSourcePlan{}, fmt.Errorf("related source requires an authorized scalar identifier field")
			}
			overlay.Additions = append(overlay.Additions, addition)
		case "MATCH", "REMOVE":
			occurrence, ok := occurrences[relationship.OccurrenceID]
			if !ok || occurrence.OccurrenceID == authoringv2.RootOccurrenceID {
				return authoringv2.ImportedSourcePlan{}, fmt.Errorf("selected relationship occurrence is unavailable")
			}
			if relationship.Action == "REMOVE" {
				for _, o := range input.Occurrences {
					if o.Enabled && o.ParentOccurrenceID == occurrence.OccurrenceID {
						return authoringv2.ImportedSourcePlan{}, fmt.Errorf("remove dependent source relationships explicitly before their parent")
					}
				}
				overlay.Removals = append(overlay.Removals, occurrence.OccurrenceID)
				matches := overlay.MatchOverrides[:0]
				for _, m := range overlay.MatchOverrides {
					if m.OccurrenceID != occurrence.OccurrenceID {
						matches = append(matches, m)
					}
				}
				overlay.MatchOverrides = matches
			} else {
				found := false
				for i := range overlay.MatchOverrides {
					if overlay.MatchOverrides[i].OccurrenceID == occurrence.OccurrenceID {
						overlay.MatchOverrides[i].MatchMode = relationship.MatchMode
						found = true
					}
				}
				if !found {
					overlay.MatchOverrides = append(overlay.MatchOverrides, authoringv2.ImportedRelationshipMatch{OccurrenceID: occurrence.OccurrenceID, MatchMode: relationship.MatchMode})
				}
			}
		}
		if err := ValidateRelationshipOverlay(original, &plan, catalog); err != nil {
			return authoringv2.ImportedSourcePlan{}, err
		}
	}
	plan.Steps = plan.Steps[:baseStepCount]
	ruleSteps, err := sourceRuleSteps(plan.Steps, &plan)
	if err != nil {
		return authoringv2.ImportedSourcePlan{}, err
	}
	plan.Steps = append(plan.Steps, ruleSteps...)
	effectiveCandidate, err := ApplySourcePlan(original, &plan)
	if err != nil {
		return authoringv2.ImportedSourcePlan{}, err
	}
	for i := range changes {
		change := &changes[i]
		if change.Parameters != nil && change.Parameters.Traversal != nil {
			route, err := ImportRoute(effectiveCandidate, catalog)
			if err != nil {
				return authoringv2.ImportedSourcePlan{}, fmt.Errorf("edited source traversal: %w", err)
			}
			occurrence := ""
			for _, step := range plan.Steps {
				if step.ID == change.StepID {
					occurrence = step.OccurrenceID
				}
			}
			var selected *authoringv2.RouteNode
			var find func(*authoringv2.RouteNode)
			find = func(n *authoringv2.RouteNode) {
				if n.OccurrenceID == occurrence {
					selected = n
				}
				for i := range n.Children {
					find(&n.Children[i])
				}
			}
			find(&route)
			if selected == nil || selected.CatalogEdgeID == "" {
				return authoringv2.ImportedSourcePlan{}, fmt.Errorf("edited source traversal %q must resolve to exactly one authorized catalog relationship", change.StepID)
			}
		}
	}

	return plan, nil
}

func VerifyPinnedFieldBindings(bindings []authoringv2.ImportedSourceFieldBinding, expected map[string]string) error {
	for _, binding := range bindings {
		if id := expected[binding.FieldID]; id == "" || id != binding.ColumnID {
			return fmt.Errorf("source field binding %q does not match its immutable pinned field identity", binding.FieldID)
		}
	}
	return nil
}
