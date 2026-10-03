package compilation

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/lineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

// recipeConstruction maps durable construction intent into the recipe's
// resolved compiler contract. Source names come from the resolved public
// emissions, while IDs and labels come from authored projection slots.
func recipeConstruction(authored *authoringv2.Construction, columns []authoringv2.Column, emitted []explorer.EmittedColumn) (*recipe.Construction, error) {
	if authored == nil {
		return nil, nil
	}
	var sourceColumns []recipe.StageColumn
	terminalCombine := len(authored.Steps) == 1 && authored.Steps[0].Operation.Kind == authoringv2.ConstructionOperationCombine
	if terminalCombine {
		if len(columns) != 0 || len(emitted) != 0 || len(authored.SourceProjections) != 0 {
			return nil, fmt.Errorf("terminal combine cannot also declare source projection columns")
		}
	} else {
		var err error
		sourceColumns, err = recipeConstructionSourceColumns(columns, emitted)
		if err != nil {
			return nil, err
		}
		for _, projection := range authored.SourceProjections {
			name := authoringv2.ConstructionSourceProjectionName(projection.ColumnID)
			for _, existing := range sourceColumns {
				if existing.ID == projection.ColumnID || existing.Name == name {
					return nil, fmt.Errorf("source projection %q collides with the resolved public source schema", projection.ColumnID)
				}
			}
			sourceColumns = append(sourceColumns, recipe.StageColumn{
				ID: projection.ColumnID, Name: name, Label: projection.Label, Type: projection.LogicalType,
			})
		}
	}
	construction := &recipe.Construction{
		Version:       authored.Version,
		SourceColumns: sourceColumns,
		Steps:         make([]recipe.ConstructionStep, 0, len(authored.Steps)),
	}
	for index, step := range authored.Steps {
		mapped, err := recipeConstructionStep(step)
		if err != nil {
			return nil, fmt.Errorf("steps[%d]: %w", index, err)
		}
		construction.Steps = append(construction.Steps, mapped)
	}
	return construction, nil
}

func recipeConstructionSourceColumns(columns []authoringv2.Column, emitted []explorer.EmittedColumn) ([]recipe.StageColumn, error) {
	columnsByName := make(map[string]authoringv2.Column, len(columns))
	seenIDs := make(map[string]bool, len(columns))
	for index, column := range columns {
		if !requiredConstructionID(column.ColumnID) {
			return nil, fmt.Errorf("columns[%d] requires a stable columnId", index)
		}
		if seenIDs[column.ColumnID] {
			return nil, fmt.Errorf("columns[%d] duplicates stable columnId %q", index, column.ColumnID)
		}
		seenIDs[column.ColumnID] = true
		if _, exists := columnsByName[column.Column]; exists || strings.TrimSpace(column.Column) == "" {
			return nil, fmt.Errorf("columns[%d] has an empty or duplicate authored public name %q", index, column.Column)
		}
		columnsByName[column.Column] = column
	}

	result := make([]recipe.StageColumn, 0, len(emitted))
	emissionIDs, publicNames := map[string]bool{}, map[string]bool{}
	emissionCounts := make(map[string]int, len(columns))
	childIDs := make(map[string]bool, len(emitted))
	for emissionIndex, emission := range emitted {
		if strings.TrimSpace(emission.EmissionID) == "" || strings.TrimSpace(emission.PublicColumn) == "" {
			return nil, fmt.Errorf("resolved public emission %d has an empty emissionId or publicColumn", emissionIndex)
		}
		if emissionIDs[emission.EmissionID] {
			return nil, fmt.Errorf("resolved source schema duplicates emissionId %q", emission.EmissionID)
		}
		if publicNames[emission.PublicColumn] {
			return nil, fmt.Errorf("resolved source schema duplicates public column %q", emission.PublicColumn)
		}
		emissionIDs[emission.EmissionID], publicNames[emission.PublicColumn] = true, true
		if len(emission.AuthoredColumns) == 0 {
			return nil, fmt.Errorf("resolved public emission %q has no authored source slot", emission.EmissionID)
		}
		owners := make([]authoringv2.Column, 0, len(emission.AuthoredColumns))
		ownerNames := make(map[string]bool, len(emission.AuthoredColumns))
		for _, authoredName := range emission.AuthoredColumns {
			column, exists := columnsByName[authoredName]
			if !exists || ownerNames[authoredName] {
				return nil, fmt.Errorf("resolved emission %q has an unknown or duplicate authored source slot %q", emission.EmissionID, authoredName)
			}
			if column.OccurrenceID != emission.OccurrenceID {
				return nil, fmt.Errorf("resolved emission %q does not match authored source occurrence %q", emission.EmissionID, column.OccurrenceID)
			}
			ownerNames[authoredName] = true
			owners = append(owners, column)
			emissionCounts[authoredName]++
		}

		var stageColumn recipe.StageColumn
		switch emission.Shape {
		case "indexed_scalar":
			if len(owners) != 1 || !isIndexedSource(owners[0]) {
				return nil, fmt.Errorf("resolved indexed emission %q must have exactly one INDEXED source slot", emission.EmissionID)
			}
			owner := owners[0]
			coordinates, err := checkedSourceCoordinates(owner, emission.Coordinates, len(emission.Coordinates))
			if err != nil {
				return nil, fmt.Errorf("resolved indexed emission %q: %w", emission.EmissionID, err)
			}
			child := lineage.SourceChild{
				Kind: lineage.IndexedValueChild, ParentColumnIDs: []string{owner.ColumnID},
				OccurrenceID: owner.OccurrenceID, SourcePath: owner.Source.Field.Path, Coordinates: coordinates,
			}
			childID, child, err := lineage.StableSourceChildID(child)
			if err != nil {
				return nil, fmt.Errorf("resolved indexed emission %q: %w", emission.EmissionID, err)
			}
			if childIDs[childID] {
				return nil, fmt.Errorf("resolved source schema duplicates generated child column ID %q", childID)
			}
			childIDs[childID] = true
			stageColumn = recipe.StageColumn{ID: childID, SourceChild: &child}
		case "repeated_count":
			child, err := repeatedCountSourceChild(owners, emission)
			if err != nil {
				return nil, fmt.Errorf("resolved repeated-count emission %q: %w", emission.EmissionID, err)
			}
			childID, child, err := lineage.StableSourceChildID(child)
			if err != nil {
				return nil, fmt.Errorf("resolved repeated-count emission %q: %w", emission.EmissionID, err)
			}
			if childIDs[childID] {
				return nil, fmt.Errorf("resolved source schema duplicates generated child column ID %q", childID)
			}
			childIDs[childID] = true
			stageColumn = recipe.StageColumn{ID: childID, SourceChild: &child}
		default:
			if len(owners) != 1 || isIndexedSource(owners[0]) {
				return nil, fmt.Errorf("resolved source slot %q has unsupported multi-emission shape %q", owners[0].Column, emission.Shape)
			}
			owner := owners[0]
			stageColumn.ID = owner.ColumnID
		}

		if stageColumn.SourceChild == nil {
			owner := owners[0]
			stageColumn.Label, stageColumn.Type = owner.Label, owner.LogicalType
		} else {
			stageColumn.Type = emission.LogicalType
			stageColumn.Nullable = emission.Nullable
			stageColumn.Label = firstNonEmpty(emission.Label, emission.PublicColumn)
		}
		stageColumn.Name = emission.PublicColumn
		if strings.TrimSpace(stageColumn.Label) == "" {
			stageColumn.Label = emission.PublicColumn
		}
		result = append(result, stageColumn)
	}
	for index, column := range columns {
		count := emissionCounts[column.Column]
		if count == 0 || (!isIndexedSource(column) && count != 1) {
			return nil, fmt.Errorf("columns[%d] authored slot %q resolves to %d public emissions; expected exactly one unless INDEXED", index, column.Column, count)
		}
	}
	return result, nil
}

func isIndexedSource(column authoringv2.Column) bool {
	return column.Source.Kind == authoringv2.SourceField && column.Source.Field != nil && strings.EqualFold(strings.TrimSpace(column.Source.ProjectionMode()), "INDEXED")
}

func checkedSourceCoordinates(owner authoringv2.Column, source []capability.RepeatedCoordinate, expectedCount int) ([]lineage.Coordinate, error) {
	if owner.Source.Field == nil {
		return nil, fmt.Errorf("INDEXED source slot has no field payload")
	}
	boundaries := repeatedSourceBoundaries(owner.Source.Field.Path)
	if len(source) != expectedCount || len(boundaries) != expectedCount || expectedCount == 0 {
		return nil, fmt.Errorf("coordinates do not match the source path repeated boundaries")
	}
	coordinates := make([]lineage.Coordinate, len(source))
	for index, coordinate := range source {
		if coordinate.Index < 0 || coordinate.Index >= coordinate.Width || coordinate.BoundaryPath != boundaries[index] {
			return nil, fmt.Errorf("coordinate %d does not match source boundary %q", index, boundaries[index])
		}
		coordinates[index] = lineage.Coordinate{BoundaryPath: coordinate.BoundaryPath, Index: coordinate.Index}
	}
	return coordinates, nil
}

func repeatedCountSourceChild(owners []authoringv2.Column, emission explorer.EmittedColumn) (lineage.SourceChild, error) {
	if len(owners) == 0 {
		return lineage.SourceChild{}, fmt.Errorf("requires at least one INDEXED source owner")
	}
	ownerIDs := make([]string, 0, len(owners))
	for _, owner := range owners {
		if !isIndexedSource(owner) || owner.OccurrenceID != emission.OccurrenceID {
			return lineage.SourceChild{}, fmt.Errorf("shared count owners must be INDEXED source slots from one occurrence")
		}
		ownerIDs = append(ownerIDs, owner.ColumnID)
	}
	owner := owners[0]
	for _, candidate := range owners[1:] {
		if candidate.ColumnID < owner.ColumnID {
			owner = candidate
		}
	}
	var coordinates []lineage.Coordinate
	var targetBoundary string
	for index, candidate := range owners {
		current, err := checkedSourcePrefixCoordinates(candidate, emission.Coordinates)
		if err != nil {
			return lineage.SourceChild{}, fmt.Errorf("owner %q: %w", candidate.ColumnID, err)
		}
		boundaries := repeatedSourceBoundaries(candidate.Source.Field.Path)
		if len(current) >= len(boundaries) {
			return lineage.SourceChild{}, fmt.Errorf("count coordinate has no remaining repeated source boundary")
		}
		if index == 0 {
			coordinates, targetBoundary = current, boundaries[len(current)]
		} else if targetBoundary != boundaries[len(current)] || !sameLineageCoordinates(coordinates, current) {
			return lineage.SourceChild{}, fmt.Errorf("shared count owners resolve to different structural boundaries")
		}
	}
	return lineage.SourceChild{
		Kind: lineage.RepeatedCountChild, ParentColumnIDs: ownerIDs, OccurrenceID: owner.OccurrenceID,
		SourcePath: owner.Source.Field.Path, BoundaryPath: targetBoundary, Coordinates: coordinates,
	}, nil
}

func checkedSourcePrefixCoordinates(owner authoringv2.Column, source []capability.RepeatedCoordinate) ([]lineage.Coordinate, error) {
	if owner.Source.Field == nil {
		return nil, fmt.Errorf("INDEXED source slot has no field payload")
	}
	boundaries := repeatedSourceBoundaries(owner.Source.Field.Path)
	if len(source) > len(boundaries) {
		return nil, fmt.Errorf("count coordinates exceed source repeated boundaries")
	}
	coordinates := make([]lineage.Coordinate, len(source))
	for index, coordinate := range source {
		if coordinate.Index < 0 || coordinate.Index >= coordinate.Width || coordinate.BoundaryPath != boundaries[index] {
			return nil, fmt.Errorf("coordinate %d does not match source boundary %q", index, boundaries[index])
		}
		coordinates[index] = lineage.Coordinate{BoundaryPath: coordinate.BoundaryPath, Index: coordinate.Index}
	}
	return coordinates, nil
}

func repeatedSourceBoundaries(path string) []string {
	path = strings.TrimPrefix(strings.TrimSpace(path), "root.")
	parts := strings.Split(path, ".")
	boundaries := make([]string, 0)
	for index, part := range parts {
		if !strings.HasSuffix(part, "[]") {
			continue
		}
		boundaries = append(boundaries, strings.Join(parts[:index+1], "."))
	}
	return boundaries
}

func sameLineageCoordinates(left, right []lineage.Coordinate) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func requiredConstructionID(value string) bool {
	return strings.TrimSpace(value) != "" && strings.TrimSpace(value) == value
}

func recipeConstructionStep(step authoringv2.ConstructionStep) (recipe.ConstructionStep, error) {
	inputs := make([]recipe.ConstructionInputRef, 0, len(step.Inputs))
	for _, input := range step.Inputs {
		inputs = append(inputs, recipe.ConstructionInputRef{
			Kind: recipe.ConstructionInputKind(input.Kind), StepID: input.StepID,
			TableID: input.TableID, RevisionID: input.RevisionID, OutputID: input.OutputID,
		})
	}
	operation, err := recipeConstructionOperation(step.Operation)
	if err != nil {
		return recipe.ConstructionStep{}, err
	}
	outputs := make([]recipe.StageColumn, 0, len(step.Outputs))
	for _, output := range step.Outputs {
		outputs = append(outputs, recipe.StageColumn{
			ID: output.ID, Name: output.Name, Label: output.Label, Type: output.Type, Nullable: output.Nullable,
		})
	}
	values := make([]recipe.ConstructionRowValue, 0, len(step.RowValues))
	for _, value := range step.RowValues {
		values = append(values, recipe.ConstructionRowValue{InputColumnID: value.InputColumnID, OutputColumnID: value.OutputColumnID, Policy: recipe.ConstructionRowValuePolicy(value.Policy)})
	}
	return recipe.ConstructionStep{ID: step.ID, Inputs: inputs, Operation: operation, Outputs: outputs, RowValues: values}, nil
}

func recipeConstructionOperation(authored authoringv2.ConstructionOperation) (recipe.ConstructionOperation, error) {
	operation := recipe.ConstructionOperation{Kind: recipe.ConstructionOperationKind(authored.Kind)}
	switch authored.Kind {
	case authoringv2.ConstructionOperationPivot:
		if authored.Pivot == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("pivot payload is required")
		}
		pivot := authored.Pivot
		mapped := &recipe.ConstructionPivot{
			ConstructionID: pivot.ConstructionID, GroupKeyIDs: append([]string(nil), pivot.GroupKeyIDs...),
			CategoryColumnID: pivot.CategoryColumnID, ValueColumnID: pivot.ValueColumnID,
			DuplicatePolicy:        recipe.PivotDuplicatePolicy(pivot.DuplicatePolicy),
			MissingCellPolicy:      recipe.PivotMissingCellPolicy(pivot.MissingCellPolicy),
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryPolicy(pivot.UnlistedCategoryPolicy),
			Categories:             make([]recipe.ConstructionPivotCategory, 0, len(pivot.Categories)),
		}
		for index, category := range pivot.Categories {
			key, err := recipeTableScalar(category.Key, tableScalarPivotCategoryKey)
			if err != nil {
				return recipe.ConstructionOperation{}, fmt.Errorf("pivot.categories[%d]: %w", index, err)
			}
			mapped.Categories = append(mapped.Categories, recipe.ConstructionPivotCategory{Key: key, OutputColumnID: category.OutputColumnID})
		}
		operation.Pivot = mapped
	case authoringv2.ConstructionOperationDerive:
		if authored.Derive == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("derive payload is required")
		}
		left, err := recipeConstructionOperand(authored.Derive.Left)
		if err != nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("derive.left: %w", err)
		}
		right, err := recipeConstructionOperand(authored.Derive.Right)
		if err != nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("derive.right: %w", err)
		}
		derive := authored.Derive
		operation.Derive = &recipe.ConstructionDerive{
			ConstructionID: derive.ConstructionID, OutputColumnID: derive.OutputColumnID,
			Operation: recipe.DerivedOperation(derive.Operation), Left: left, Right: right,
			MissingInputPolicy:   recipe.MissingInputPolicy(derive.MissingInputPolicy),
			DivisionByZeroPolicy: recipe.DivisionByZeroPolicy(derive.DivisionByZeroPolicy),
		}
	case authoringv2.ConstructionOperationFilter:
		if authored.Filter == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("filter payload is required")
		}
		values := make([]recipe.FilterValue, 0, len(authored.Filter.Values))
		for index, value := range authored.Filter.Values {
			if !recipe.FilterValueKind(value.Kind).Valid() {
				return recipe.ConstructionOperation{}, fmt.Errorf("filter.values[%d] has unsupported kind %q", index, value.Kind)
			}
			mapped := recipe.FilterValue{
				Kind: recipe.FilterValueKind(value.Kind), String: value.String, Boolean: value.Boolean,
				Integer: value.Integer, Decimal: value.Decimal, Date: value.Date, DateTime: value.DateTime,
			}
			if value.Code != nil {
				mapped.Code = &recipe.CodeValue{System: value.Code.System, Code: value.Code.Code, Display: value.Code.Display}
			}
			values = append(values, mapped)
		}
		operation.Filter = &recipe.ConstructionFilter{
			ColumnID: authored.Filter.ColumnID, Operator: recipe.FilterOperator(authored.Filter.Operator), Values: values,
		}
	case authoringv2.ConstructionOperationUnpivot:
		if authored.Unpivot == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("unpivot payload is required")
		}
		unpivot := authored.Unpivot
		mapped := &recipe.ConstructionUnpivot{
			ConstructionID: unpivot.ConstructionID, KeyOutputColumnID: unpivot.KeyOutputColumnID,
			ValueOutputColumnID: unpivot.ValueOutputColumnID,
			NullRowPolicy:       recipe.UnpivotNullRowPolicy(unpivot.NullRowPolicy),
			Inputs:              make([]recipe.ConstructionUnpivotInput, 0, len(unpivot.Inputs)),
		}
		for index, input := range unpivot.Inputs {
			key, err := recipeTableScalar(input.Key, tableScalarUnpivotKey)
			if err != nil {
				return recipe.ConstructionOperation{}, fmt.Errorf("unpivot.inputs[%d]: %w", index, err)
			}
			mapped.Inputs = append(mapped.Inputs, recipe.ConstructionUnpivotInput{ColumnID: input.ColumnID, Key: key})
		}
		operation.Unpivot = mapped
	case authoringv2.ConstructionOperationGroup:
		if authored.Group == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("group payload is required")
		}
		group := authored.Group
		mapped := &recipe.ConstructionGroup{
			ConstructionID:   group.ConstructionID,
			MissingKeyPolicy: recipe.ConstructionGroupMissingKeyPolicy(group.MissingKeyPolicy.Normalized()),
			Keys:             make([]recipe.ConstructionGroupKey, 0, len(group.Keys)),
			Aggregates:       make([]recipe.ConstructionGroupAggregate, 0, len(group.Aggregates)),
		}
		for _, key := range group.Keys {
			mapped.Keys = append(mapped.Keys, recipe.ConstructionGroupKey{
				InputColumnID: key.InputColumnID, OutputColumnID: key.OutputColumnID,
			})
		}
		for _, aggregate := range group.Aggregates {
			mapped.Aggregates = append(mapped.Aggregates, recipe.ConstructionGroupAggregate{
				Operation:     recipe.ConstructionGroupAggregateOp(aggregate.Operation),
				InputColumnID: aggregate.InputColumnID, OutputColumnID: aggregate.OutputColumnID,
			})
		}
		operation.Group = mapped
	case authoringv2.ConstructionOperationCodedGroup:
		if authored.CodedGroup == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("codedGroup payload is required")
		}
		coded := authored.CodedGroup
		mapped := &recipe.ConstructionCodedGroup{
			ConstructionID: coded.ConstructionID,
			Source: recipe.ConstructionCodedGroupSource{
				OccurrenceID: coded.Source.OccurrenceID,
				ResourceType: coded.Source.ResourceType,
				CodingPath:   coded.Source.CodingPath,
				FHIRType:     coded.Source.FHIRType,
				Cardinality:  coded.Source.Cardinality,
				Shape:        coded.Source.Shape,
				Route:        make([]recipe.ConstructionRelatedRouteStep, 0, len(coded.Source.Route)),
			},
			MissingKeyPolicy:                  recipe.ConstructionGroupMissingKeyPolicy(coded.MissingKeyPolicy),
			SystemOutputColumnID:              coded.SystemOutputColumnID,
			VersionOutputColumnID:             coded.VersionOutputColumnID,
			CodeOutputColumnID:                coded.CodeOutputColumnID,
			DistinctSourceCountOutputColumnID: coded.DistinctSourceCountOutputColumnID,
		}
		for _, hop := range coded.Source.Route {
			mapped.Source.Route = append(mapped.Source.Route, recipe.ConstructionRelatedRouteStep{
				EdgeID: hop.EdgeID, FromNodeID: hop.FromNodeID, ToNodeID: hop.ToNodeID,
				FromResourceType: hop.FromResourceType, ToResourceType: hop.ToResourceType,
				Relationship: hop.Relationship, StorageDirection: hop.StorageDirection, MatchMode: hop.MatchMode,
			})
		}
		operation.CodedGroup = mapped
	case authoringv2.ConstructionOperationCodedPivot:
		if authored.CodedPivot == nil || authored.CodedPivot.Source == nil || authored.CodedPivot.SourceChoiceID != "" {
			return recipe.ConstructionOperation{}, fmt.Errorf("codedPivot must contain durable source facts after proposal")
		}
		coded := authored.CodedPivot
		source := coded.Source
		family := source.Family
		mapped := &recipe.ConstructionCodedPivot{
			ConstructionID: coded.ConstructionID,
			Source: recipe.ConstructionCodedPivotSource{
				BindingID: family.BindingID, ResourceType: family.ResourceType, SourcePath: family.SourcePath,
				SourceCanonical: family.SourceCanonical, SourceProfile: family.SourceProfile,
				OwningScope: family.OwningScope, KeyPath: family.KeyPath, ValuePath: family.ValuePath,
				ChoiceArms: append([]string(nil), family.ChoiceArms...), LogicalType: family.LogicalType,
				RuleVersion: family.RuleVersion, SchemaVersion: family.SchemaVersion,
				CandidateID: source.CandidateID, NodeID: source.NodeID, FieldPath: source.FieldPath,
				Route: make([]recipe.ConstructionRelatedRouteStep, 0, len(source.Route)),
			},
			DuplicatePolicy:   recipe.PivotDuplicatePolicy(coded.DuplicatePolicy),
			MissingCellPolicy: recipe.PivotMissingCellPolicy(coded.MissingCellPolicy),
			Categories:        make([]recipe.ConstructionCodedPivotCategory, 0, len(coded.Categories)),
		}
		for _, hop := range source.Route {
			mapped.Source.Route = append(mapped.Source.Route, recipe.ConstructionRelatedRouteStep{
				EdgeID: hop.EdgeID, FromNodeID: hop.FromNodeID, ToNodeID: hop.ToNodeID,
				FromResourceType: hop.FromResourceType, ToResourceType: hop.ToResourceType,
				Relationship: hop.Relationship, StorageDirection: hop.StorageDirection, MatchMode: hop.MatchMode,
			})
		}
		for _, category := range coded.Categories {
			if category.ChoiceID != "" {
				return recipe.ConstructionOperation{}, fmt.Errorf("codedPivot category choice IDs must be cleared after proposal")
			}
			mapped.Categories = append(mapped.Categories, recipe.ConstructionCodedPivotCategory{
				System: category.System, Code: category.Code, OutputColumnID: category.OutputColumnID,
			})
		}
		operation.CodedPivot = mapped
	case authoringv2.ConstructionOperationExpand:
		if authored.Expand == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("expand payload is required")
		}
		expand := authored.Expand
		operation.Expand = &recipe.ConstructionExpand{
			ConstructionID: expand.ConstructionID, InputColumnID: expand.InputColumnID,
			OutputColumnID: expand.OutputColumnID, OrdinalColumnID: expand.OrdinalColumnID,
			EmptyPolicy: recipe.ExpansionEmptyPolicy(expand.EmptyPolicy),
		}
	case authoringv2.ConstructionOperationRelatedSource:
		if authored.RelatedSource == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("relatedSource payload is required")
		}
		related := authored.RelatedSource
		predicate, err := constructionRelatedPredicate(related.ContributorRule.Predicate)
		if err != nil {
			return recipe.ConstructionOperation{}, err
		}
		mapped := &recipe.ConstructionRelatedSource{
			AnchorColumnID: related.AnchorColumnID, ChoiceID: related.ChoiceID,
			SourceOccurrenceID: related.SourceOccurrenceID,
			Source: recipe.ConstructionRelatedFieldSource{
				CandidateID: related.Source.CandidateID, NodeID: related.Source.NodeID,
				ResourceType: related.Source.ResourceType, Path: related.Source.Path,
				Cardinality: related.Source.Cardinality, LogicalType: related.Source.LogicalType,
				RepeatedBoundaries: constructionRelatedRepeatedBoundaries(related.Source.RepeatedBoundaries),
			},
			ContributorPolicy: related.ContributorRule.Policy, Predicate: predicate, Form: string(related.Form),
			OutputColumnID: related.OutputColumnID,
			Route:          make([]recipe.ConstructionRelatedRouteStep, 0, len(related.Route)),
		}
		for _, hop := range related.Route {
			mapped.Route = append(mapped.Route, recipe.ConstructionRelatedRouteStep{
				EdgeID: hop.EdgeID, FromNodeID: hop.FromNodeID, ToNodeID: hop.ToNodeID,
				FromResourceType: hop.FromResourceType, ToResourceType: hop.ToResourceType,
				Relationship: hop.Relationship, StorageDirection: hop.StorageDirection, MatchMode: hop.MatchMode,
			})
		}
		operation.RelatedSource = mapped
	case authoringv2.ConstructionOperationRelatedExpand:
		if authored.RelatedExpand == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("relatedExpand payload is required")
		}
		related := authored.RelatedExpand
		predicate, err := constructionRelatedPredicate(related.ContributorRule.Predicate)
		if err != nil {
			return recipe.ConstructionOperation{}, err
		}
		mapped := &recipe.ConstructionRelatedExpand{
			AnchorColumnID: related.AnchorColumnID, ChoiceID: related.ChoiceID,
			TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
			ContributorPolicy: related.ContributorRule.Policy, ContributorPredicate: predicate,
			ContributorChoiceID:   related.ContributorChoiceID,
			EmptyPolicy:           recipe.ExpansionEmptyPolicy(related.EmptyPolicy),
			RelatedRecordColumnID: related.RelatedRecordColumnID,
			Route:                 make([]recipe.ConstructionRelatedRouteStep, 0, len(related.Route)),
		}
		if related.ContributorSource != nil {
			source := related.ContributorSource
			mapped.ContributorSource = &recipe.ConstructionRelatedFieldSource{
				CandidateID: source.CandidateID, NodeID: source.NodeID, ResourceType: source.ResourceType,
				Path: source.Path, Cardinality: source.Cardinality, LogicalType: source.LogicalType,
				RepeatedBoundaries: constructionRelatedRepeatedBoundaries(source.RepeatedBoundaries),
			}
		}
		for _, hop := range related.Route {
			mapped.Route = append(mapped.Route, recipe.ConstructionRelatedRouteStep{
				EdgeID: hop.EdgeID, FromNodeID: hop.FromNodeID, ToNodeID: hop.ToNodeID,
				FromResourceType: hop.FromResourceType, ToResourceType: hop.ToResourceType,
				Relationship: hop.Relationship, StorageDirection: hop.StorageDirection, MatchMode: hop.MatchMode,
			})
		}
		operation.RelatedExpand = mapped
	case authoringv2.ConstructionOperationRelatedEligibility:
		if authored.RelatedEligibility == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("relatedEligibility payload is required")
		}
		related := authored.RelatedEligibility
		predicate, err := constructionRelatedPredicate(related.ContributorRule.Predicate)
		if err != nil {
			return recipe.ConstructionOperation{}, err
		}
		mapped := &recipe.ConstructionRelatedEligibility{
			AnchorColumnID: related.AnchorColumnID, ChoiceID: related.ChoiceID,
			TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
			ContributorPolicy: related.ContributorRule.Policy, ContributorPredicate: predicate,
			ContributorChoiceID: related.ContributorChoiceID, MatchKind: related.Match.Kind,
			Threshold: related.Match.Threshold,
			Route:     make([]recipe.ConstructionRelatedRouteStep, 0, len(related.Route)),
		}
		if related.ContributorSource != nil {
			source := related.ContributorSource
			mapped.ContributorSource = &recipe.ConstructionRelatedFieldSource{
				CandidateID: source.CandidateID, NodeID: source.NodeID, ResourceType: source.ResourceType,
				Path: source.Path, Cardinality: source.Cardinality, LogicalType: source.LogicalType,
				RepeatedBoundaries: constructionRelatedRepeatedBoundaries(source.RepeatedBoundaries),
			}
		}
		for _, hop := range related.Route {
			mapped.Route = append(mapped.Route, recipe.ConstructionRelatedRouteStep{
				EdgeID: hop.EdgeID, FromNodeID: hop.FromNodeID, ToNodeID: hop.ToNodeID,
				FromResourceType: hop.FromResourceType, ToResourceType: hop.ToResourceType,
				Relationship: hop.Relationship, StorageDirection: hop.StorageDirection, MatchMode: hop.MatchMode,
			})
		}
		operation.RelatedEligibility = mapped
	case authoringv2.ConstructionOperationRelatedField:
		if authored.RelatedField == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("relatedField payload is required")
		}
		related := authored.RelatedField
		operation.RelatedField = &recipe.ConstructionRelatedField{
			ChoiceID: related.ChoiceID,
			Source: recipe.ConstructionRelatedFieldSource{
				CandidateID: related.Source.CandidateID, NodeID: related.Source.NodeID,
				ResourceType: related.Source.ResourceType, Path: related.Source.Path,
				Cardinality: related.Source.Cardinality, LogicalType: related.Source.LogicalType,
				RepeatedBoundaries: constructionRelatedRepeatedBoundaries(related.Source.RepeatedBoundaries),
			},
			OutputColumnID: related.OutputColumnID,
		}
	case authoringv2.ConstructionOperationCombine:
		if authored.Combine == nil {
			return recipe.ConstructionOperation{}, fmt.Errorf("combine payload is required")
		}
		combine := authored.Combine
		mapped := &recipe.ConstructionCombine{
			Kind:             recipe.ConstructionCombineKind(combine.Kind),
			Keys:             make([]recipe.ConstructionCombineKey, 0, len(combine.Keys)),
			Projections:      make([]recipe.ConstructionCombineProjection, 0, len(combine.Projections)),
			JoinType:         recipe.ConstructionCombineJoinType(combine.JoinType),
			RightMatchPolicy: recipe.ConstructionCombineRightMatchPolicy(combine.RightMatchPolicy),
			MembershipMode:   recipe.ConstructionCombineMembershipMode(combine.MembershipMode),
		}
		for _, key := range combine.Keys {
			mapped.Keys = append(mapped.Keys, recipe.ConstructionCombineKey{
				LeftColumnID: key.LeftColumnID, RightColumnID: key.RightColumnID,
			})
		}
		for _, projection := range combine.Projections {
			mapped.Projections = append(mapped.Projections, recipe.ConstructionCombineProjection{
				OutputColumnID: projection.OutputColumnID, InputIndex: projection.InputIndex, InputColumnID: projection.InputColumnID,
			})
		}
		operation.Combine = mapped
	default:
		return recipe.ConstructionOperation{}, fmt.Errorf("unsupported operation kind %q", authored.Kind)
	}
	return operation, nil
}

func constructionRelatedPredicate(predicate *authoringv2.ContributorPredicate) (*recipe.ConstructionRelatedPredicate, error) {
	if predicate == nil {
		return nil, nil
	}
	mapped := &recipe.ConstructionRelatedPredicate{
		CandidateID: predicate.CandidateID,
		Operator:    recipe.FilterOperator(predicate.Operator),
		Quantifier:  recipe.ArrayQuantifier(predicate.Quantifier),
	}
	if predicate.Value == nil {
		return mapped, nil
	}
	value := &recipe.FilterValue{Kind: recipe.FilterValueKind(predicate.Value.Kind)}
	switch predicate.Value.Kind {
	case authoringv2.ContributorString:
		if predicate.Value.String != nil {
			stringValue := *predicate.Value.String
			value.String = &stringValue
		}
	case authoringv2.ContributorValueCode:
		if predicate.Value.Code != nil {
			value.Code = &recipe.CodeValue{Code: predicate.Value.Code.Code}
		}
	default:
		return nil, fmt.Errorf("unsupported related-source contributor value kind %q", predicate.Value.Kind)
	}
	mapped.Value = value
	return mapped, nil
}

func constructionRelatedRepeatedBoundaries(boundaries []capability.RepeatedBoundary) []recipe.ConstructionRelatedRepeatedBoundary {
	if len(boundaries) == 0 {
		return nil
	}
	mapped := make([]recipe.ConstructionRelatedRepeatedBoundary, 0, len(boundaries))
	for _, boundary := range boundaries {
		mapped = append(mapped, recipe.ConstructionRelatedRepeatedBoundary{Path: boundary.Path, MaxItems: boundary.MaxItems})
	}
	return mapped
}

func recipeConstructionOperand(operand authoringv2.ConstructionOperand) (recipe.ConstructionOperand, error) {
	result := recipe.ConstructionOperand{Kind: recipe.DerivedOperandKind(operand.Kind), ColumnID: operand.ColumnID}
	switch operand.Kind {
	case authoringv2.ConstructionColumnOperand:
		return result, nil
	case authoringv2.ConstructionLiteralOperand:
		if operand.Literal == nil {
			return recipe.ConstructionOperand{}, fmt.Errorf("literal payload is required")
		}
		literal := &recipe.DerivedLiteral{Kind: recipe.NumericKind(operand.Literal.Kind), Integer: operand.Literal.Integer, Decimal: operand.Literal.Decimal}
		result.ColumnID = ""
		result.Literal = literal
		return result, nil
	default:
		return recipe.ConstructionOperand{}, fmt.Errorf("unsupported operand kind %q", operand.Kind)
	}
}
