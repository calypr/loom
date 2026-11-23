package recipeimport

import (
	"fmt"
	"slices"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

type PlanEdit struct {
	StepChanges  []authoringv2.ImportedSourceStepChange
	Relationship *authoringv2.ImportedRelationshipChange
	RuleCreate   *ws.SourceRuleCreate
}

type PlanEditInput struct {
	PinnedFieldIDs  map[string]string
	Occurrences     []ws.NetworkOccurrence
	NewOccurrenceID string
}

func EditPlan(original recipe.Output, plan ws.ImportedSourcePlan, catalog ws.CatalogSnapshot, edit PlanEdit, input PlanEditInput) (ws.ImportedSourcePlan, error) {
	changes, relationship, rule := edit.StepChanges, edit.Relationship, edit.RuleCreate
	count := 0
	if changes != nil {
		count++
		if err := authoringv2.ValidateImportedSourceStepBatch(changes); err != nil {
			return ws.ImportedSourcePlan{}, err
		}
	}
	if relationship != nil {
		count++
		if err := relationship.Validate(); err != nil {
			return ws.ImportedSourcePlan{}, err
		}
	}
	if rule != nil {
		count++
		if err := rule.Validate(); err != nil {
			return ws.ImportedSourcePlan{}, err
		}
	}
	if count != 1 {
		return ws.ImportedSourcePlan{}, fmt.Errorf("source plan edit requires exactly one intent")
	}
	plan, err := HydrateSourcePlan(original, &plan)
	if err != nil {
		return ws.ImportedSourcePlan{}, err
	}
	pinnedSteps, err := retainedPinnedSteps(SourcePlan(original).Steps, plan.RemovedStepIDs)
	if err != nil {
		return ws.ImportedSourcePlan{}, err
	}
	baseStepCount := len(pinnedSteps)
	var newPinnedRemovals []string
	if rule != nil {
		limit := ws.MaxImportedRuleColumns
		if rule.MaxColumns != nil {
			limit = *rule.MaxColumns
		}
		ruleIndex := max(plan.NextRuleIndex, len(plan.RuleAdditions))
		plan.NextRuleIndex = ruleIndex + 1
		addition := ws.ImportedSourceRuleAddition{
			StepID: fmt.Sprintf("ruleAdditions[%d]", ruleIndex),
			Kind:   rule.Kind, OccurrenceID: rule.OccurrenceID, Parameters: relativeSourceRuleParameters(rule.OccurrenceID, rule.Parameters), MaxColumns: &limit,
		}
		if addition.Kind == SourcePlanCatalogProjection {
			params := *addition.Parameters
			projection := *params.Catalog
			projection.Name = availableCatalogRuleName(plan.Steps, addition.OccurrenceID, projection.Name)
			params.Catalog = &projection
			addition.Parameters = &params
		}
		plan.RuleAdditions = append(plan.RuleAdditions, addition)
	}
	for _, change := range changes {
		if change.Remove {
			index := slices.IndexFunc(plan.RuleAdditions, func(addition ws.ImportedSourceRuleAddition) bool {
				return addition.StepID == change.StepID
			})
			if index >= 0 {
				plan.NextRuleIndex = max(plan.NextRuleIndex, len(plan.RuleAdditions))
				plan.RuleAdditions = slices.Delete(plan.RuleAdditions, index, index+1)
			} else {
				plan.RemovedStepIDs = append(plan.RemovedStepIDs, change.StepID)
				newPinnedRemovals = append(newPinnedRemovals, change.StepID)
			}
			continue
		}
		if change.Parameters != nil && change.Parameters.FieldGroup != nil {
			if err := VerifyPinnedFieldBindings(plan.FieldBindings, input.PinnedFieldIDs); err != nil {
				return ws.ImportedSourcePlan{}, err
			}
			bound := map[string]bool{}
			for _, binding := range plan.FieldBindings {
				bound[binding.FieldID] = true
			}
			for _, field := range change.Parameters.FieldGroup.Fields {
				id := input.PinnedFieldIDs[field.FieldID]
				if id == "" {
					return ws.ImportedSourcePlan{}, fmt.Errorf("source field %q has no unambiguous emitted identity in the pinned projection; resolve its source collision before editing", field.FieldID)
				}
				if !bound[field.FieldID] {
					plan.FieldBindings = append(plan.FieldBindings, ws.ImportedSourceFieldBinding{FieldID: field.FieldID, ColumnID: id})
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
			if change.ValueMode != "" {
				return ws.ImportedSourcePlan{}, fmt.Errorf("source rule edits do not support a value policy")
			}
			if change.Parameters != nil {
				if err := change.Parameters.Validate(addition.Kind); err != nil {
					return ws.ImportedSourcePlan{}, err
				}
				addition.Parameters = relativeSourceRuleParameters(addition.OccurrenceID, change.Parameters)
			}
			if change.MaxColumns != nil {
				addition.MaxColumns = change.MaxColumns
			}
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
				return ws.ImportedSourcePlan{}, fmt.Errorf("this source step cannot be changed")
			}
			if change.MaxColumns != nil && step.MaxColumns == nil {
				return ws.ImportedSourcePlan{}, fmt.Errorf("this source step has no column limit")
			}
			if change.ValueMode != "" && step.ValueMode == "" {
				return ws.ImportedSourcePlan{}, fmt.Errorf("this source step has no value policy")
			}
			if change.Parameters != nil {
				if err := change.Parameters.Validate(step.Kind); err != nil {
					return ws.ImportedSourcePlan{}, err
				}
				step.Parameters = change.Parameters
			}
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
			return ws.ImportedSourcePlan{}, fmt.Errorf("source step was not found")
		}
	}

	if relationship != nil {
		if plan.Relationships == nil {
			plan.Relationships = &ws.ImportedRelationshipOverlay{}
		}
		overlay := plan.Relationships
		occurrences := map[string]ws.NetworkOccurrence{}
		for _, o := range input.Occurrences {
			if o.Enabled {
				occurrences[o.OccurrenceID] = o
			}
		}
		switch relationship.Action {
		case "ADD":
			parent, ok := occurrences[relationship.ParentOccurrenceID]
			if !ok {
				return ws.ImportedSourcePlan{}, fmt.Errorf("selected parent occurrence is unavailable")
			}
			var edge *ws.CatalogEdge
			for i := range catalog.Edges {
				if catalog.Edges[i].ID == relationship.EdgeID {
					edge = &catalog.Edges[i]
					break
				}
			}
			if edge == nil {
				return ws.ImportedSourcePlan{}, fmt.Errorf("selected catalog edge is unavailable")
			}
			var from, to *ws.CatalogNode
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
				return ws.ImportedSourcePlan{}, fmt.Errorf("selected catalog edge does not extend this occurrence")
			}

			for ancestor := parent; ; {
				if ancestor.ResourceType == to.ResourceType {
					return ws.ImportedSourcePlan{}, fmt.Errorf("cyclic source relationship is unsupported")
				}
				if ancestor.ParentOccurrenceID == "" {
					break
				}
				next, ok := occurrences[ancestor.ParentOccurrenceID]
				if !ok {
					return ws.ImportedSourcePlan{}, fmt.Errorf("source ancestor is unavailable")
				}
				ancestor = next
			}
			id := input.NewOccurrenceID
			addition := ws.ImportedRelationship{OccurrenceID: id, ParentOccurrenceID: parent.OccurrenceID, CatalogEdgeID: edge.ID, Relationship: edge.Label, ResourceType: to.ResourceType, StorageDirection: edge.StorageDirection, MatchMode: relationship.MatchMode}

			for _, field := range catalog.Candidates {
				if field.NodeID == to.ID && (field.FieldPath == "id" || field.FieldPath == to.ResourceType+".id") && !field.Repeated {
					addition.Fields = []ws.ImportedRelationshipField{{CandidateID: field.ID, ColumnID: id + "_id", Column: id + "_id", FieldPath: field.FieldPath, Label: field.Label, ValueMode: "ALL"}}
					break
				}
			}
			if len(addition.Fields) == 0 {
				return ws.ImportedSourcePlan{}, fmt.Errorf("related source requires an authorized scalar identifier field")
			}
			overlay.Additions = append(overlay.Additions, addition)
		case "MATCH", "REMOVE":
			occurrence, ok := occurrences[relationship.OccurrenceID]
			if !ok || occurrence.OccurrenceID == ws.RootOccurrenceID {
				return ws.ImportedSourcePlan{}, fmt.Errorf("selected relationship occurrence is unavailable")
			}
			if relationship.Action == "REMOVE" {
				for _, o := range input.Occurrences {
					if o.Enabled && o.ParentOccurrenceID == occurrence.OccurrenceID {
						return ws.ImportedSourcePlan{}, fmt.Errorf("remove dependent source relationships explicitly before their parent")
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
					overlay.MatchOverrides = append(overlay.MatchOverrides, ws.ImportedRelationshipMatch{OccurrenceID: occurrence.OccurrenceID, MatchMode: relationship.MatchMode})
				}
			}
		}
		if err := ValidateRelationshipOverlay(original, &plan, catalog); err != nil {
			return ws.ImportedSourcePlan{}, err
		}
	}
	plan.Steps, err = retainedPinnedSteps(plan.Steps[:baseStepCount], newPinnedRemovals)
	if err != nil {
		return ws.ImportedSourcePlan{}, err
	}
	ruleSteps, err := sourceRuleSteps(plan.Steps, &plan)
	if err != nil {
		return ws.ImportedSourcePlan{}, err
	}
	plan.Steps = append(plan.Steps, ruleSteps...)
	effectiveCandidate, err := ApplySourcePlan(original, &plan)
	if err != nil {
		return ws.ImportedSourcePlan{}, err
	}
	for i := range changes {
		change := &changes[i]
		if change.Parameters != nil && change.Parameters.Traversal != nil {
			route, err := ImportRoute(effectiveCandidate, catalog)
			if err != nil {
				return ws.ImportedSourcePlan{}, fmt.Errorf("edited source traversal: %w", err)
			}
			occurrence := ""
			for _, step := range plan.Steps {
				if step.ID == change.StepID {
					occurrence = step.OccurrenceID
				}
			}
			var selected *ws.RouteNode
			var find func(*ws.RouteNode)
			find = func(n *ws.RouteNode) {
				if n.OccurrenceID == occurrence {
					selected = n
				}
				for i := range n.Children {
					find(&n.Children[i])
				}
			}
			find(&route)
			if selected == nil || selected.CatalogEdgeID == "" {
				return ws.ImportedSourcePlan{}, fmt.Errorf("edited source traversal %q must resolve to exactly one authorized catalog relationship", change.StepID)
			}
		}
	}

	return plan, nil
}

func VerifyPinnedFieldBindings(bindings []ws.ImportedSourceFieldBinding, expected map[string]string) error {
	for _, binding := range bindings {
		if id := expected[binding.FieldID]; id == "" || id != binding.ColumnID {
			return fmt.Errorf("source field binding %q does not match its immutable pinned field identity", binding.FieldID)
		}
	}
	return nil
}

func availableCatalogRuleName(steps []ws.ImportedSourceStep, occurrenceID, preferred string) string {
	parentID := ""
	if occurrenceID != "" {
		for _, step := range steps {
			if step.Kind == SourcePlanTraversal && step.OccurrenceID == occurrenceID {
				parentID = step.ID
				break
			}
		}
	}
	names := make(map[string]bool)
	for _, step := range steps {
		if step.Kind == SourcePlanCatalogProjection && step.ParentID == parentID {
			names[effectiveSourceKey(step)] = true
		}
	}
	name := preferred
	for suffix := 2; names[name]; suffix++ {
		name = fmt.Sprintf("%s_%d", preferred, suffix)
	}
	return name
}
