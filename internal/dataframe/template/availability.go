package template

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/fhirschema"
)

// Resolve computes availability without persistence or compiler access. The
// caller must first build the snapshot using its authorization and generation
// context.
func Resolve(definition Definition, snapshot CapabilitySnapshot) Availability {
	availability := Availability{
		ID: definition.ID, Version: definition.Version, Label: definition.Label,
		Description: definition.Description, Status: StatusUnavailable,
		CommonColumns: []SelectedColumn{}, AdvancedColumns: []SelectedColumn{},
		Traversals: []SelectedTraversal{}, Pivots: []SelectedPivot{},
		Missing: []MissingCapability{}, Reasons: []string{},
	}
	root, ok := firstAvailableRoot(definition.RootCandidates, snapshot.Resources)
	if !ok {
		availability.Reasons = append(availability.Reasons, "ROOT_RESOURCE_UNAVAILABLE")
		availability.Missing = append(availability.Missing, MissingCapability{Kind: "root", Code: "ROOT_RESOURCE_UNAVAILABLE", Label: definition.Label})
		availability.Starter = StarterRequest{}
		return availability
	}
	availability.RootResourceType = root
	rootCapability := resourceCapability(root, snapshot.Resources)
	for _, suggestion := range definition.SuggestedColumns {
		selected, found := selectColumn(suggestion, rootCapability.Fields)
		if !found {
			availability.Missing = append(availability.Missing, MissingCapability{SuggestionID: suggestion.ID, Kind: "column", Label: suggestion.Label, Code: "FIELD_UNAVAILABLE"})
			continue
		}
		if suggestion.Advanced {
			availability.AdvancedColumns = append(availability.AdvancedColumns, selected)
		} else {
			availability.CommonColumns = append(availability.CommonColumns, selected)
		}
	}
	for _, suggestion := range definition.SuggestedTraversals {
		selected, found := selectTraversal(suggestion, snapshot.Relationships)
		if !found {
			availability.Missing = append(availability.Missing, MissingCapability{SuggestionID: suggestion.ID, Kind: "traversal", Label: suggestion.Label, Code: "TRAVERSAL_UNAVAILABLE"})
			continue
		}
		availability.Traversals = append(availability.Traversals, selected)
	}
	for _, suggestion := range definition.SuggestedPivots {
		selected, found := selectPivot(suggestion, rootCapability.Fields)
		if !found {
			availability.Missing = append(availability.Missing, MissingCapability{SuggestionID: suggestion.ID, Kind: "pivot", Label: suggestion.Label, Code: "PIVOT_UNAVAILABLE"})
			continue
		}
		availability.Pivots = append(availability.Pivots, selected)
	}

	for _, missing := range availability.Missing {
		availability.Reasons = append(availability.Reasons, missing.Code)
	}
	if hasRequiredMissing(definition, availability.Missing) {
		availability.Status = StatusUnavailable
	} else if len(availability.Missing) > 0 {
		availability.Status = StatusPartial
	} else {
		availability.Status = StatusAvailable
	}
	availability.Starter = starterFor(definition, availability)
	return availability
}

func firstAvailableRoot(candidates []string, resources []ResourceCapability) (string, bool) {
	for _, candidate := range candidates {
		for _, resource := range resources {
			if resource.Present && resource.ResourceType == candidate && fhirschema.HasResource(candidate) {
				return candidate, true
			}
		}
	}
	return "", false
}

func resourceCapability(resourceType string, resources []ResourceCapability) ResourceCapability {
	for _, resource := range resources {
		if resource.ResourceType == resourceType {
			return resource
		}
	}
	return ResourceCapability{}
}

func selectColumn(suggestion ColumnSuggestion, fields []FieldCapability) (SelectedColumn, bool) {
	for _, candidate := range suggestion.FieldRefAlternatives {
		for _, field := range fields {
			if field.FieldRef == candidate {
				return SelectedColumn{ID: suggestion.ID, Label: suggestion.Label, FieldRef: field.FieldRef, Advanced: suggestion.Advanced}, true
			}
		}
	}
	return SelectedColumn{}, false
}

func selectTraversal(suggestion TraversalSuggestion, relationships []RelationshipCapability) (SelectedTraversal, bool) {
	for _, relationship := range relationships {
		if !contains(suggestion.FromResourceTypes, relationship.FromType) || !contains(suggestion.ToResourceTypes, relationship.ToType) {
			continue
		}
		if !fhirschema.HasResource(relationship.FromType) || !fhirschema.HasResource(relationship.ToType) {
			continue
		}
		if _, found, err := fhirschema.ResolveCompilerTraversal(relationship.FromType, relationship.Label, relationship.ToType); err != nil || !found {
			continue
		}
		return SelectedTraversal{ID: suggestion.ID, Label: suggestion.Label, SemanticRole: suggestion.SemanticRole, FromType: relationship.FromType, EdgeLabel: relationship.Label, ToType: relationship.ToType, Advanced: suggestion.Advanced}, true
	}
	return SelectedTraversal{}, false
}

func selectPivot(suggestion PivotSuggestion, fields []FieldCapability) (SelectedPivot, bool) {
	for _, candidate := range suggestion.FieldRefAlternatives {
		for _, field := range fields {
			if field.FieldRef != candidate || !field.PivotCandidate || len(field.PivotColumns) == 0 {
				continue
			}
			return SelectedPivot{ID: suggestion.ID, Label: suggestion.Label, FieldRef: field.FieldRef, Columns: cloneStrings(field.PivotColumns), Advanced: suggestion.Advanced}, true
		}
	}
	return SelectedPivot{}, false
}

func hasRequiredMissing(definition Definition, missing []MissingCapability) bool {
	for _, item := range missing {
		for _, suggestion := range definition.SuggestedColumns {
			if suggestion.ID == item.SuggestionID && suggestion.Required {
				return true
			}
		}
		for _, suggestion := range definition.SuggestedTraversals {
			if suggestion.ID == item.SuggestionID && suggestion.Required {
				return true
			}
		}
		for _, suggestion := range definition.SuggestedPivots {
			if suggestion.ID == item.SuggestionID && suggestion.Required {
				return true
			}
		}
	}
	return false
}

func starterFor(definition Definition, availability Availability) StarterRequest {
	starter := StarterRequest{RootResourceType: availability.RootResourceType, RowGrain: definition.RowGrain, Fields: []SelectedColumn{}, Traversals: []SelectedTraversal{}, Pivots: []SelectedPivot{}}
	for _, suggestion := range definition.SuggestedColumns {
		if !suggestion.DefaultSelected {
			continue
		}
		for _, selected := range append(append([]SelectedColumn{}, availability.CommonColumns...), availability.AdvancedColumns...) {
			if selected.ID == suggestion.ID {
				starter.Fields = append(starter.Fields, selected)
				break
			}
		}
	}
	for _, suggestion := range definition.SuggestedTraversals {
		if !suggestion.DefaultSelected {
			continue
		}
		for _, selected := range availability.Traversals {
			if selected.ID == suggestion.ID {
				starter.Traversals = append(starter.Traversals, selected)
				break
			}
		}
	}
	for _, suggestion := range definition.SuggestedPivots {
		if !suggestion.DefaultSelected {
			continue
		}
		for _, selected := range availability.Pivots {
			if selected.ID == suggestion.ID {
				starter.Pivots = append(starter.Pivots, selected)
				break
			}
		}
	}
	return starter
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(wanted) {
			return true
		}
	}
	return false
}

// ValidateStarter performs the local structural checks that can be proven
// without a request context. Production validation remains the existing
// dataframe input resolver and compiler.
func ValidateStarter(starter StarterRequest) error {
	if strings.TrimSpace(starter.RootResourceType) == "" || !fhirschema.HasResource(starter.RootResourceType) {
		return fmt.Errorf("starter root resource type %q is not a generated FHIR resource", starter.RootResourceType)
	}
	for _, traversal := range starter.Traversals {
		if _, found, err := fhirschema.ResolveCompilerTraversal(traversal.FromType, traversal.EdgeLabel, traversal.ToType); err != nil || !found {
			return fmt.Errorf("starter traversal %q is not present in generated FHIR metadata", traversal.ID)
		}
	}
	return nil
}
