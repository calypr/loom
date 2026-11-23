package compilation

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type semanticColumnCompiler struct {
	project               string
	explorerID            string
	document              authoringv2.Document
	snapshot              capability.Snapshot
	occurrences           map[string]semanticOccurrence
	nodes                 map[string]*semanticRecipeNode
	expansion             *recipe.Expansion
	columnTransformations []recipe.ColumnTransformation
	emitted               []explorer.EmittedColumn
	mappings              []explorer.IdentityMapping
	presentation          PresentationConfig
	contract              explorer.PublicOutputContract
	countEmissions        map[string]int
	presentationOrder     int
}

func (columns *semanticColumnCompiler) compile(index int, column authoringv2.Column) error {
	project := columns.project
	document, snapshot := columns.document, columns.snapshot
	occurrences, nodes, expansion := columns.occurrences, columns.nodes, columns.expansion
	occurrence := occurrences[column.OccurrenceID]
	alias := semanticAlias(column.OccurrenceID)
	leaf := column.Column
	logicalType := firstNonEmpty(column.LogicalType, "string")
	if column.Source.Lookup != nil && column.Source.Lookup.Identifier != nil {
		logicalType = firstNonEmpty(column.Source.Lookup.Identifier.LogicalType, logicalType)
	}
	if column.Source.Lookup != nil && column.Source.Lookup.Extension != nil {
		logicalType = firstNonEmpty(column.Source.Lookup.Extension.LogicalType, logicalType)
	}
	filterable, chartable := column.Filter != nil, column.Chart != nil
	sourceJSON, _ := json.Marshal(column.Source.Normalized())
	candidateID := "source_" + shortHash(column.OccurrenceID+"\x00"+string(sourceJSON)+"\x00"+column.Column)
	projectionMode := firstNonEmpty(strings.ToUpper(column.Source.ProjectionMode()), "FIRST")
	if column.ValueTransformation != nil && projectionMode == "INDEXED" {
		return fail("capability", "UNSUPPORTED_VALUE_TRANSFORMATION_SHAPE", fmt.Sprintf("$.columns[%d].valueTransformation", index), "exact category recoding requires one scalar public projection, not an indexed expansion", map[string]any{"shape": "indexed_columns"}, nil)
	}
	if column.Source.Kind == authoringv2.SourceOwnerRecords {
		projectionMode = string(capability.ConstructionChoiceOwnerRecords)
	}
	sourcePath := column.Source.FieldPath()
	sourceRepeated := false
	choiceArm := ""
	structuralSuitability := "scalar"
	var lossReasons []string
	lossless := true
	mlReady := false

	switch column.Source.Kind {
	case authoringv2.SourceField:
		candidate, found := semanticFieldCandidate(snapshot, occurrence.graph.ID, sourcePath)
		if !found {
			return fail("intent", "STALE_FIELD", fmt.Sprintf("$.columns[%d].source.field.path", index), "field is not present on the resolved capability node", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": sourcePath}, nil)
		}
		capabilityMode, supported := capabilityProjectionMode(projectionMode)
		advertised := supported && slices.Contains(candidate.ProjectionModes, capabilityMode)
		relatedScalarAll := projectionMode == string(capability.ConstructionChoiceAll) &&
			column.OccurrenceID != authoringv2.RootOccurrenceID &&
			!capability.IsRepeatedCardinality(candidate.Cardinality) &&
			slices.Contains(candidate.ProjectionModes, capability.ProjectionScalar)
		if !advertised && !relatedScalarAll {
			return fail("capability", "UNSUPPORTED_PROJECTION_MODE", fmt.Sprintf("$.columns[%d].source.projectionMode", index), "projection mode is not advertised by the resolved capability candidate", map[string]any{"candidateId": candidate.ID, "projectionMode": projectionMode, "advertisedModes": candidate.ProjectionModes}, nil)
		}
		if authoredType := strings.TrimSpace(column.LogicalType); authoredType != "" && authoredType != strings.TrimSpace(candidate.LogicalType) {
			return fail("capability", "CAPABILITY_LOGICAL_TYPE_MISMATCH", fmt.Sprintf("$.columns[%d].logicalType", index), "logical type does not match the resolved capability candidate", map[string]any{"candidateId": candidate.ID, "expected": candidate.LogicalType, "actual": authoredType}, nil)
		}
		candidateID = candidate.ID
		sourceRepeated = len(candidate.RepeatedBoundaries) > 0
		logicalType = firstNonEmpty(candidate.LogicalType, "string")
		filterable = filterable && supportsOperation(candidate.SupportedOperations, capability.OperationFilter)
		chartable = chartable && supportsOperation(candidate.SupportedOperations, capability.OperationChart)
		path := strings.TrimPrefix(strings.TrimSpace(sourcePath), "root.")
		choiceArm = choiceArmForPath(path)
		if projectionMode == "INDEXED" {
			if indexedProjectionOverlapsExpandedScope(document.Rows.Expanded, column.OccurrenceID, candidate.RepeatedBoundaries) {
				scopePath := strings.TrimPrefix(strings.TrimSpace(document.Rows.Expanded.ScopePath), "root.")
				return fail("capability", "INDEXED_PROJECTION_OVERLAPS_EXPANDED_SCOPE", fmt.Sprintf("$.columns[%d].source.projectionMode", index), "this INDEXED column addresses an owner-level repeated position that also defines the expanded rows; choose a non-indexed projection or restore one row per source record", map[string]any{"fieldPath": path, "scopePath": scopePath, "projectionMode": projectionMode}, nil)
			}
			indexed, counts, expandErr := expandIndexedProjection(leaf, path, candidate.RepeatedBoundaries)
			if expandErr != nil {
				code := "INDEXED_BOUNDARY_INVALID"
				switch {
				case strings.Contains(expandErr.Error(), "complete repeated-boundary evidence"):
					code = "SHAPE_PROFILE_INCOMPLETE"
				case strings.Contains(expandErr.Error(), "observed"):
					code = "INDEXED_BOUNDARY_TOO_WIDE"
				case strings.Contains(expandErr.Error(), "physical columns"):
					code = "INDEXED_OUTPUT_TOO_WIDE"
				}
				return fail("lower", code, fmt.Sprintf("$.columns[%d].source.fieldPath", index), expandErr.Error(), map[string]any{"fieldPath": path, "maximum": maxIndexedBoundaryItems}, expandErr)
			}
			emissionIDs := make([]string, 0, len(indexed)+len(counts))
			for _, item := range indexed {
				nodes[column.OccurrenceID].fields = append(nodes[column.OccurrenceID].fields, recipe.Field{Name: item.Leaf, FieldRef: sourcePath, Expr: recipe.Expression{Select: alias + "." + item.Selector}, ValueMode: recipe.ValueModeFirst})
				publicColumn := indexedLeaf(column.Column, item.Coordinates)
				var lossReasons []string
				structuralSuitability := "scalar"
				lossless := true
				if column.OccurrenceID != authoringv2.RootOccurrenceID {
					lossless = false
					lossReasons = append(lossReasons, "RELATED_RESOURCE_FIRST_LOSSY")
					structuralSuitability = "requires-review"
				}
				emission := explorer.EmittedColumn{EmissionID: publicColumn, OutputID: document.Output.ID, NodeID: occurrence.graph.ID, SelectionID: candidateID, CandidateID: candidateID, OccurrenceID: column.OccurrenceID, ProjectionMode: projectionMode, AuthoredColumns: []string{column.Column}, PublicColumn: publicColumn, Label: indexedLabel(column.Label, item.Coordinates), LogicalType: logicalType, Nullable: true, Shape: "indexed_scalar", StructuralSuitability: structuralSuitability, LossReasons: lossReasons, SourceResourceType: occurrence.graph.ResourceType, SourcePath: path, ChoiceArm: choiceArm, Coordinates: append([]capability.RepeatedCoordinate(nil), item.Coordinates...), Lossless: lossless, MLReady: false, Filterable: filterable, Chartable: chartable}
				columns.emitted = append(columns.emitted, emission)
				mergeContractQuality(&columns.contract, emission)
				columns.contract.Columns = append(columns.contract.Columns, publicColumnContract(emission))
				columns.presentation.Columns = append(columns.presentation.Columns, presentationColumn(column, index, columns.presentationOrder, emission))
				columns.presentationOrder++
				emissionIDs = append(emissionIDs, emission.EmissionID)
			}
			for _, count := range counts {
				key := column.OccurrenceID + "\x00" + count.Leaf
				publicColumn := count.Leaf
				if existing, ok := columns.countEmissions[key]; ok {
					columns.emitted[existing].AuthoredColumns = append(columns.emitted[existing].AuthoredColumns, column.Column)
					columns.contract.Columns[existing].AuthoredColumns = append(columns.contract.Columns[existing].AuthoredColumns, column.Column)
					emissionIDs = append(emissionIDs, columns.emitted[existing].EmissionID)
					continue
				}
				nodes[column.OccurrenceID].fields = append(nodes[column.OccurrenceID].fields, recipe.Field{Name: count.Leaf, FieldRef: count.Selector, Expr: recipe.Expression{Call: "length", Args: []recipe.Expression{{Select: alias + "." + count.Selector}}}, ValueMode: recipe.ValueModeAuto})
				lossReasons := []string(nil)
				structuralSuitability := "scalar"
				lossless := column.OccurrenceID == authoringv2.RootOccurrenceID
				if column.OccurrenceID != authoringv2.RootOccurrenceID {
					lossReasons = append(lossReasons, "RELATED_RESOURCE_FIRST_LOSSY")
					structuralSuitability = "requires-review"
				}
				emission := explorer.EmittedColumn{EmissionID: publicColumn, OutputID: document.Output.ID, NodeID: occurrence.graph.ID, SelectionID: "boundary_" + shortHash(key), CandidateID: candidateID, OccurrenceID: column.OccurrenceID, ProjectionMode: "COUNT", AuthoredColumns: []string{column.Column}, PublicColumn: publicColumn, Label: count.Leaf, LogicalType: "integer", Nullable: false, Shape: "repeated_count", StructuralSuitability: structuralSuitability, LossReasons: lossReasons, SourceResourceType: occurrence.graph.ResourceType, SourcePath: count.Selector, Coordinates: append([]capability.RepeatedCoordinate(nil), count.Coordinates...), Lossless: lossless, MLReady: false}
				columns.emitted = append(columns.emitted, emission)
				mergeContractQuality(&columns.contract, emission)
				columns.contract.Columns = append(columns.contract.Columns, publicColumnContract(emission))
				columns.presentation.Columns = append(columns.presentation.Columns, presentationColumn(column, index, columns.presentationOrder, emission))
				columns.presentationOrder++
				columns.countEmissions[key] = len(columns.emitted) - 1
				emissionIDs = append(emissionIDs, emission.EmissionID)
			}
			columns.mappings = append(columns.mappings, explorer.IdentityMapping{OutputID: document.Output.ID, CandidateID: candidateID, OccurrenceID: column.OccurrenceID, ProjectionMode: projectionMode, EmissionIDs: emissionIDs})
			return nil
		}
		selector := alias + "." + path
		if document.Rows.Kind == authoringv2.RowDefinitionExpanded && document.Rows.Expanded != nil {
			if suffix, rebased := expandedItemFieldSuffix(document.Rows.Expanded, column.OccurrenceID, sourcePath); rebased && expansion != nil {
				selector = expansion.As + "." + suffix
			}
		}
		nodes[column.OccurrenceID].fields = append(nodes[column.OccurrenceID].fields, recipe.Field{Name: leaf, ColumnID: column.ColumnID, Label: column.Label, FieldRef: sourcePath, Expr: recipe.Expression{Select: selector}, ValueMode: projectionValueMode(projectionMode)})
	case authoringv2.SourceProjectID:
		logicalType = "string"
		literal, _ := json.Marshal(project)
		nodes[column.OccurrenceID].fields = append(nodes[column.OccurrenceID].fields, recipe.Field{Name: leaf, ColumnID: column.ColumnID, Label: column.Label, FieldRef: "project.id", Expr: recipe.Expression{Literal: literal}, ValueMode: recipe.ValueModeFirst})
	case authoringv2.SourceCodedValue:
		pivot, pivotErr := semanticCodedValuePivot(column, leaf)
		if pivotErr != nil {
			return fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), pivotErr.Error(), nil, pivotErr)
		}
		nodes[column.OccurrenceID].pivots = appendSemanticPivot(nodes[column.OccurrenceID].pivots, pivot)
	case authoringv2.SourceOwnerRecords:
		if column.Source.OwnerRecords == nil {
			return fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), "owner-record source is missing its typed payload", nil, nil)
		}
		ownerRecords := column.Source.OwnerRecords
		nodes[column.OccurrenceID].ownerRecords = append(nodes[column.OccurrenceID].ownerRecords, recipe.OwnerRecordProjection{
			Name: leaf, FieldRef: sourcePath, Binding: ownerRecords.Binding, Key: ownerRecords.Key,
		})
		logicalType = "object"
		choiceArm = choiceArmForPath(ownerRecords.Binding.ValuePath)
	case authoringv2.SourceExtensionByURL:
		if column.Source.Lookup == nil || column.Source.Lookup.Extension == nil {

			dynamic, dynamicErr := semanticFixedLookup(column, occurrence.graph.ResourceType, alias, leaf, logicalType)
			if dynamicErr != nil {
				return fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), dynamicErr.Error(), nil, dynamicErr)
			}
			nodes[column.OccurrenceID].dynamics = append(nodes[column.OccurrenceID].dynamics, dynamic)
			break
		}
		pivot, pivotErr := semanticExtensionPivot(column, leaf)
		if pivotErr != nil {
			return fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), pivotErr.Error(), nil, pivotErr)
		}
		nodes[column.OccurrenceID].pivots = appendSemanticPivot(nodes[column.OccurrenceID].pivots, pivot)
	case authoringv2.SourceAggregate:
		aggregateType, err := columns.compileAggregate(index, column, occurrence, alias)
		if err != nil {
			return err
		}
		logicalType = aggregateType
		projectionMode = strings.ToUpper(strings.TrimSpace(column.Source.Aggregate.Operation))
		if sourcePath == "" {
			sourcePath = "$resource"
		}
	default:
		dynamic, dynamicErr := semanticFixedLookup(column, occurrence.graph.ResourceType, alias, leaf, logicalType)
		if dynamicErr != nil {
			return fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), dynamicErr.Error(), nil, dynamicErr)
		}
		nodes[column.OccurrenceID].dynamics = append(nodes[column.OccurrenceID].dynamics, dynamic)
	}

	return columns.emitColumn(index, column, document, semanticColumnState{
		occurrence: occurrence, candidateID: candidateID, projectionMode: projectionMode,
		sourcePath: sourcePath, logicalType: logicalType, choiceArm: choiceArm,
		sourceRepeated: sourceRepeated, filterable: filterable, chartable: chartable,
		structuralSuitability: structuralSuitability, lossReasons: lossReasons,
		lossless: lossless, mlReady: mlReady,
	})

}

type semanticColumnState struct {
	occurrence            semanticOccurrence
	candidateID           string
	projectionMode        string
	sourcePath            string
	logicalType           string
	choiceArm             string
	sourceRepeated        bool
	filterable            bool
	chartable             bool
	structuralSuitability string
	lossReasons           []string
	lossless              bool
	mlReady               bool
}

func (columns *semanticColumnCompiler) emitColumn(index int, column authoringv2.Column, document authoringv2.Document, state semanticColumnState) error {
	occurrence := state.occurrence
	projectionMode := state.projectionMode
	sourcePath := state.sourcePath
	logicalType := state.logicalType
	choiceArm := state.choiceArm
	sourceRepeated := state.sourceRepeated
	filterable := state.filterable
	chartable := state.chartable
	structuralSuitability := state.structuralSuitability
	lossReasons := state.lossReasons
	lossless := state.lossless
	mlReady := state.mlReady
	candidateID := state.candidateID
	visible := true
	orderValue := columns.presentationOrder
	pinned := false
	if column.Table != nil {
		if column.Table.Visible != nil {
			visible = *column.Table.Visible
		}
		if column.Table.Order != nil {
			orderValue = *column.Table.Order
		}
		pinned = column.Table.Pinned
	} else {
		visible = false
	}
	emissionID := column.Column
	shape := "scalar"
	if column.Source.Kind == authoringv2.SourceOwnerRecords {
		shape = "record_list"
		structuralSuitability = "array"
		lossless = true
		lossReasons = nil
	}
	if projectionMode == "ALL" || projectionMode == "DISTINCT" {
		shape = "array"
		structuralSuitability = "array"
	}
	if column.Source.Kind == authoringv2.SourceAggregate && column.Source.Aggregate != nil {
		switch strings.ToUpper(strings.TrimSpace(column.Source.Aggregate.Operation)) {
		case "COLLECT", "DISTINCT_VALUES":
			shape = "array"
			structuralSuitability = "array"
		}
	}
	if column.ValueTransformation != nil {
		if column.Source.Kind == authoringv2.SourceCodedValue {
			return fail("capability", "CODED_VALUE_RECODE_UNAVAILABLE", fmt.Sprintf("$.columns[%d].valueTransformation", index), "coded value recoding is unavailable because the scalar transformation cannot preserve both Coding.system and Coding.code", map[string]any{"sourceKind": column.Source.Kind}, nil)
		}
		if !strings.EqualFold(strings.TrimSpace(logicalType), "string") {
			return fail("capability", "UNSUPPORTED_VALUE_TRANSFORMATION_TYPE", fmt.Sprintf("$.columns[%d].valueTransformation", index), "exact category recoding requires a scalar string value", map[string]any{"logicalType": logicalType}, nil)
		}
		if shape != "scalar" {
			return fail("capability", "UNSUPPORTED_VALUE_TRANSFORMATION_SHAPE", fmt.Sprintf("$.columns[%d].valueTransformation", index), "exact category recoding requires a scalar column", map[string]any{"shape": shape}, nil)
		}
		transformation := column.ValueTransformation.Clone()
		columns.columnTransformations = append(columns.columnTransformations, recipe.ColumnTransformation{
			Column: column.Column, Transformation: transformation,
		})
	}
	if column.Source.Kind == authoringv2.SourceField {
		if column.OccurrenceID != authoringv2.RootOccurrenceID && column.Source.Field != nil && (projectionMode == "VALUE" || projectionMode == "FIRST" || projectionMode == "INDEXED") {
			lossless = false
			lossReasons = append(lossReasons, "RELATED_RESOURCE_FIRST_LOSSY")
			structuralSuitability = "requires-review"
		}
		if column.OccurrenceID != authoringv2.RootOccurrenceID && (projectionMode == "ALL" || projectionMode == "DISTINCT") {
			lossless = false
			lossReasons = append(lossReasons, "RELATED_RESOURCE_ALL_LOSSY")
			structuralSuitability = "requires-review"
		}
		if sourceRepeated && projectionMode == "DISTINCT" {
			lossless = false
			lossReasons = append(lossReasons, "DISTINCT_VALUES_REDUCTION")
		}
		if sourceRepeated && projectionMode == "FIRST" {
			lossless = false
			lossReasons = append(lossReasons, "FIELD_FIRST_REDUCTION")
			structuralSuitability = "requires-review"
		}
	} else if column.Source.Kind == authoringv2.SourceAggregate {
		lossless = false
		lossReasons = append(lossReasons, "AGGREGATE_REDUCTION")
		if column.Source.Aggregate != nil && strings.EqualFold(column.Source.Aggregate.Operation, "FIRST_ORDERED") {
			lossReasons = append(lossReasons, "TEMPORAL_SELECTION")
			if column.Source.Aggregate.Ordering != nil && strings.EqualFold(column.Source.Aggregate.Ordering.TiePolicy, "RESOURCE_KEY") {
				lossReasons = append(lossReasons, "RESOURCE_KEY_TIE_BREAK")
			}
		}
		if column.Source.Aggregate != nil && (strings.EqualFold(column.Source.Aggregate.Operation, "DISTINCT_VALUES") || strings.EqualFold(column.Source.Aggregate.Operation, "COLLECT")) {
			shape = "array"
			structuralSuitability = "array"
			if strings.EqualFold(column.Source.Aggregate.Operation, "DISTINCT_VALUES") {
				lossReasons = append(lossReasons, "DISTINCT_VALUES_REDUCTION")
			} else {
				lossReasons = append(lossReasons, "COLLECT_ASSOCIATION_LOSS")
			}
		} else if column.Source.Aggregate != nil && strings.EqualFold(column.Source.Aggregate.Operation, "REQUIRE_ONE") {
			lossless = true
			lossReasons = nil
		}
	} else if column.Source.Kind != authoringv2.SourceProjectID && column.Source.Kind != authoringv2.SourceOwnerRecords {
		lossless = false
		lossReasons = append(lossReasons, "RELATED_LOOKUP_REDUCTION")
		structuralSuitability = "requires-review"
	}
	var unitNormalization *explorer.PublicUnitNormalization
	if column.Source.Kind == authoringv2.SourceAggregate && column.Source.Aggregate != nil {
		unitNormalization = publicUnitNormalization(column.Source.Aggregate.UnitNormalization)
	}
	emission := explorer.EmittedColumn{EmissionID: emissionID, OutputID: document.Output.ID, NodeID: occurrence.graph.ID, SelectionID: candidateID, CandidateID: candidateID, OccurrenceID: column.OccurrenceID, ProjectionMode: projectionMode, AuthoredColumns: []string{column.Column}, PublicColumn: column.Column, Label: column.Label, LogicalType: logicalType, Nullable: true, Shape: shape, SourceResourceType: occurrence.graph.ResourceType, SourcePath: sourcePath, ChoiceArm: choiceArm, Lossless: lossless, MLReady: mlReady, StructuralSuitability: structuralSuitability, LossReasons: append([]string(nil), lossReasons...), Filterable: filterable, Chartable: chartable, UnitNormalization: unitNormalization}
	columns.emitted = append(columns.emitted, emission)
	mergeContractQuality(&columns.contract, emission)
	columns.mappings = append(columns.mappings, explorer.IdentityMapping{OutputID: document.Output.ID, CandidateID: candidateID, OccurrenceID: column.OccurrenceID, ProjectionMode: projectionMode, EmissionIDs: []string{emissionID}})
	presented := PresentationColumn{EmissionID: emissionID, PublicColumn: column.Column, Label: column.Label, Visible: visible, Order: orderValue, PhysicalOrder: columns.presentationOrder, Pinned: pinned}
	if column.Filter != nil {
		presented.FilterLabel = firstNonEmpty(column.Filter.Label, column.Label)
		presented.FilterOrder = index
		if column.Filter.Order != nil {
			presented.FilterOrder = *column.Filter.Order
		}
	}
	if column.Chart != nil {
		presented.ChartType, presented.ChartTitle = column.Chart.Type, column.Chart.Title
		presented.ChartOrder = index
		if column.Chart.Order != nil {
			presented.ChartOrder = *column.Chart.Order
		}
	}
	columns.presentation.Columns = append(columns.presentation.Columns, presented)
	columns.contract.Columns = append(columns.contract.Columns, publicColumnContract(emission))
	columns.presentationOrder++
	return nil
}
