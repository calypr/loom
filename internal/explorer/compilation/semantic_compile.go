package compilation

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type semanticOccurrence struct {
	node  authoringv2.RouteNode
	graph capability.Node
	edge  *capability.Edge
}

type semanticRecipeNode struct {
	fields     []recipe.Field
	dynamics   []recipe.DynamicColumn
	pivots     []recipe.Pivot
	aggregates []recipe.Aggregate
}

func compileSemanticDocument(ctx context.Context, project, explorerID string, document authoringv2.Document, snapshot capability.Snapshot) (Result, error) {
	if err := contextErr(ctx); err != nil {
		return Result{}, err
	}
	occurrences, order, err := resolveSemanticRoute(document, snapshot)
	if err != nil {
		return Result{}, err
	}
	root := occurrences[authoringv2.RootOccurrenceID]
	rowGrain, ok := spec.InferRowGrain(root.graph.ResourceType)
	if !ok || !root.graph.RowRootEligible {
		return Result{}, fail("lower", "UNSUPPORTED_ROW_ROOT", "$.rootResourceType", "root resource type is not an eligible recipe row root", map[string]any{"resourceType": root.graph.ResourceType}, nil)
	}

	nodes := make(map[string]*semanticRecipeNode, len(occurrences))
	for id := range occurrences {
		nodes[id] = &semanticRecipeNode{}
	}
	emitted := make([]explorer.EmittedColumn, 0, len(document.Columns))
	mappings := make([]explorer.IdentityMapping, 0, len(document.Columns))
	presentation := PresentationConfig{OutputID: document.Output.ID, Title: document.Output.Title, Columns: make([]PresentationColumn, 0, len(document.Columns))}
	contract := explorer.PublicOutputContract{OutputID: document.Output.ID, RootResourceType: root.graph.ResourceType, RowGrain: string(rowGrain), RowMultiplication: "none", Lossless: true, MLReady: true, StructuralSuitability: "scalar", Columns: make([]explorer.PublicOutputColumn, 0, len(document.Columns))}
	countEmissions := map[string]int{}
	presentationOrder := 0

	for index, column := range document.Columns {
		occurrence := occurrences[column.OccurrenceID]
		alias := semanticAlias(column.OccurrenceID)
		leaf := column.Column
		logicalType := firstNonEmpty(column.LogicalType, "string")
		filterable, chartable := column.Filter != nil, column.Chart != nil
		sourceJSON, _ := json.Marshal(column.Source.Normalized())
		candidateID := "source_" + shortHash(column.OccurrenceID+"\x00"+string(sourceJSON)+"\x00"+column.Column)
		projectionMode := firstNonEmpty(strings.ToUpper(column.Source.ProjectionMode()), "FIRST")
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
				return Result{}, fail("intent", "STALE_FIELD", fmt.Sprintf("$.columns[%d].source.field.path", index), "field is not present on the resolved capability node", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": sourcePath}, nil)
			}
			capabilityMode, supported := capabilityProjectionMode(projectionMode)
			if !supported || !containsProjectionMode(candidate.ProjectionModes, capabilityMode) {
				return Result{}, fail("capability", "UNSUPPORTED_PROJECTION_MODE", fmt.Sprintf("$.columns[%d].source.projectionMode", index), "projection mode is not advertised by the resolved capability candidate", map[string]any{"candidateId": candidate.ID, "projectionMode": projectionMode, "advertisedModes": candidate.ProjectionModes}, nil)
			}
			if authoredType := strings.TrimSpace(column.LogicalType); authoredType != "" && authoredType != strings.TrimSpace(candidate.LogicalType) {
				return Result{}, fail("capability", "CAPABILITY_LOGICAL_TYPE_MISMATCH", fmt.Sprintf("$.columns[%d].logicalType", index), "logical type does not match the resolved capability candidate", map[string]any{"candidateId": candidate.ID, "expected": candidate.LogicalType, "actual": authoredType}, nil)
			}
			candidateID = candidate.ID
			sourceRepeated = len(candidate.RepeatedBoundaries) > 0
			logicalType = firstNonEmpty(candidate.LogicalType, "string")
			filterable = filterable && supportsOperation(candidate.SupportedOperations, capability.OperationFilter)
			chartable = chartable && supportsOperation(candidate.SupportedOperations, capability.OperationChart)
			path := strings.TrimPrefix(strings.TrimSpace(sourcePath), "root.")
			choiceArm = choiceArmForPath(path)
			if projectionMode == "INDEXED" {
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
					return Result{}, fail("lower", code, fmt.Sprintf("$.columns[%d].source.fieldPath", index), expandErr.Error(), map[string]any{"fieldPath": path, "maximum": maxIndexedBoundaryItems}, expandErr)
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
					emitted = append(emitted, emission)
					mergeContractQuality(&contract, emission)
					contract.Columns = append(contract.Columns, publicColumnContract(emission))
					presentation.Columns = append(presentation.Columns, presentationColumn(column, index, presentationOrder, emission))
					presentationOrder++
					emissionIDs = append(emissionIDs, emission.EmissionID)
				}
				for _, count := range counts {
					key := column.OccurrenceID + "\x00" + count.Leaf
					publicColumn := count.Leaf
					if existing, ok := countEmissions[key]; ok {
						emitted[existing].AuthoredColumns = append(emitted[existing].AuthoredColumns, column.Column)
						contract.Columns[existing].AuthoredColumns = append(contract.Columns[existing].AuthoredColumns, column.Column)
						emissionIDs = append(emissionIDs, emitted[existing].EmissionID)
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
					emitted = append(emitted, emission)
					mergeContractQuality(&contract, emission)
					contract.Columns = append(contract.Columns, publicColumnContract(emission))
					presentation.Columns = append(presentation.Columns, presentationColumn(column, index, presentationOrder, emission))
					presentationOrder++
					countEmissions[key] = len(emitted) - 1
					emissionIDs = append(emissionIDs, emission.EmissionID)
				}
				mappings = append(mappings, explorer.IdentityMapping{OutputID: document.Output.ID, CandidateID: candidateID, OccurrenceID: column.OccurrenceID, ProjectionMode: projectionMode, EmissionIDs: emissionIDs})
				continue
			}
			nodes[column.OccurrenceID].fields = append(nodes[column.OccurrenceID].fields, recipe.Field{Name: leaf, FieldRef: sourcePath, Expr: recipe.Expression{Select: alias + "." + path}, ValueMode: projectionValueMode(projectionMode)})
		case authoringv2.SourceProjectID:
			literal, _ := json.Marshal(project)
			nodes[column.OccurrenceID].fields = append(nodes[column.OccurrenceID].fields, recipe.Field{Name: leaf, FieldRef: "project.id", Expr: recipe.Expression{Literal: literal}, ValueMode: recipe.ValueModeFirst})
		case authoringv2.SourceObservationComponentByCode:
			pivot, pivotErr := semanticObservationPivot(column, alias, leaf)
			if pivotErr != nil {
				return Result{}, fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), pivotErr.Error(), nil, pivotErr)
			}
			nodes[column.OccurrenceID].pivots = appendSemanticPivot(nodes[column.OccurrenceID].pivots, pivot)
		case authoringv2.SourceCodingBySystem:
			if column.Source.Lookup == nil || column.Source.Lookup.Binding == nil {
				// Legacy coding lookups remain readable for old immutable recipes,
				// but new writable correlated coding sources must use the typed
				// binding path below.
				dynamic, dynamicErr := semanticFixedLookup(column, alias, leaf, logicalType)
				if dynamicErr != nil {
					return Result{}, fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), dynamicErr.Error(), nil, dynamicErr)
				}
				nodes[column.OccurrenceID].dynamics = append(nodes[column.OccurrenceID].dynamics, dynamic)
				break
			}
			pivot, pivotErr := semanticCorrelatedCodingPivot(column, alias, leaf)
			if pivotErr != nil {
				return Result{}, fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), pivotErr.Error(), nil, pivotErr)
			}
			nodes[column.OccurrenceID].pivots = appendSemanticPivot(nodes[column.OccurrenceID].pivots, pivot)
		case authoringv2.SourceExtensionByURL:
			if column.Source.Lookup == nil || column.Source.Lookup.Extension == nil {
				// Legacy extension lookups remain readable for immutable recipes;
				// writable authoring commands reject this shape before compilation.
				dynamic, dynamicErr := semanticFixedLookup(column, alias, leaf, logicalType)
				if dynamicErr != nil {
					return Result{}, fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), dynamicErr.Error(), nil, dynamicErr)
				}
				nodes[column.OccurrenceID].dynamics = append(nodes[column.OccurrenceID].dynamics, dynamic)
				break
			}
			pivot, pivotErr := semanticExtensionPivot(column, leaf)
			if pivotErr != nil {
				return Result{}, fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), pivotErr.Error(), nil, pivotErr)
			}
			nodes[column.OccurrenceID].pivots = appendSemanticPivot(nodes[column.OccurrenceID].pivots, pivot)
		case authoringv2.SourceAggregate:
			var contributorWhere *recipe.Filter
			if column.Contributor != nil {
				catalog := catalogFromCapability(snapshot, explorerID)
				if err := authoringv2.ValidateContributorForCatalog(document, catalog, column.OccurrenceID, column.Source, *column.Contributor); err != nil {
					return Result{}, fail("intent", "INVALID_CONTRIBUTOR_PREDICATE", fmt.Sprintf("$.columns[%d].contributor", index), err.Error(), map[string]any{"candidateId": column.Contributor.CandidateID}, err)
				}
				candidate, found := capabilityCandidate(snapshot, occurrence.graph.ID, column.Contributor.CandidateID)
				if !found {
					return Result{}, fail("intent", "STALE_CONTRIBUTOR_CANDIDATE", fmt.Sprintf("$.columns[%d].contributor.candidateId", index), "contributor candidate is not present on the resolved capability node", map[string]any{"candidateId": column.Contributor.CandidateID}, nil)
				}
				where, whereErr := contributorRecipeFilter(occurrence.graph.ResourceType, alias, candidate, *column.Contributor)
				if whereErr != nil {
					return Result{}, fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].contributor", index), whereErr.Error(), nil, whereErr)
				}
				contributorWhere = &where
			}
			if column.Source.Aggregate != nil {
				if path := strings.TrimPrefix(strings.TrimSpace(column.Source.Aggregate.Path), "root."); path != "" {
					if _, found := semanticFieldCandidate(snapshot, occurrence.graph.ID, path); !found {
						return Result{}, fail("intent", "STALE_FIELD", fmt.Sprintf("$.columns[%d].source.aggregate.path", index), "aggregate path is not present on the resolved capability node", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": path}, nil)
					}
				}
				if column.Source.Aggregate.Where != nil {
					wherePath := strings.TrimPrefix(strings.TrimSpace(column.Source.Aggregate.Where.Path), "root.")
					if _, found := semanticFieldCandidate(snapshot, occurrence.graph.ID, wherePath); !found {
						return Result{}, fail("intent", "STALE_FIELD", fmt.Sprintf("$.columns[%d].source.aggregate.where.path", index), "aggregate predicate path is not present on the resolved capability node", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": wherePath}, nil)
					}
				}
			}
			aggregate, aggregateType, aggregateErr := semanticAggregate(column, alias, occurrence.graph.ResourceType, contributorWhere)
			if aggregateErr != nil {
				return Result{}, fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), aggregateErr.Error(), nil, aggregateErr)
			}
			nodes[column.OccurrenceID].aggregates = append(nodes[column.OccurrenceID].aggregates, aggregate)
			logicalType = aggregateType
			projectionMode = strings.ToUpper(strings.TrimSpace(column.Source.Aggregate.Operation))
			if sourcePath == "" {
				sourcePath = "$resource"
			}
		default:
			dynamic, dynamicErr := semanticFixedLookup(column, alias, leaf, logicalType)
			if dynamicErr != nil {
				return Result{}, fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), dynamicErr.Error(), nil, dynamicErr)
			}
			nodes[column.OccurrenceID].dynamics = append(nodes[column.OccurrenceID].dynamics, dynamic)
		}

		visible := true
		orderValue := presentationOrder
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
		if column.Source.Kind == authoringv2.SourceField && (projectionMode == "ALL" || projectionMode == "DISTINCT") {
			shape = "array"
			structuralSuitability = "array"
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
			if column.Source.Aggregate != nil && strings.EqualFold(column.Source.Aggregate.Operation, "DISTINCT_VALUES") {
				shape = "array"
				structuralSuitability = "array"
				lossReasons = append(lossReasons, "DISTINCT_VALUES_REDUCTION")
			}
		} else if column.Source.Kind != authoringv2.SourceProjectID {
			lossless = false
			lossReasons = append(lossReasons, "RELATED_LOOKUP_REDUCTION")
			structuralSuitability = "requires-review"
		}
		emission := explorer.EmittedColumn{EmissionID: emissionID, OutputID: document.Output.ID, NodeID: occurrence.graph.ID, SelectionID: candidateID, CandidateID: candidateID, OccurrenceID: column.OccurrenceID, ProjectionMode: projectionMode, PublicColumn: column.Column, Label: column.Label, LogicalType: logicalType, Nullable: true, Shape: shape, SourceResourceType: occurrence.graph.ResourceType, SourcePath: sourcePath, ChoiceArm: choiceArm, Lossless: lossless, MLReady: mlReady, StructuralSuitability: structuralSuitability, LossReasons: append([]string(nil), lossReasons...), Filterable: filterable, Chartable: chartable}
		emitted = append(emitted, emission)
		mergeContractQuality(&contract, emission)
		mappings = append(mappings, explorer.IdentityMapping{OutputID: document.Output.ID, CandidateID: candidateID, OccurrenceID: column.OccurrenceID, ProjectionMode: projectionMode, EmissionIDs: []string{emissionID}})
		presented := PresentationColumn{EmissionID: emissionID, PublicColumn: column.Column, Label: column.Label, Visible: visible, Order: orderValue, Pinned: pinned}
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
		presentation.Columns = append(presentation.Columns, presented)
		contract.Columns = append(contract.Columns, publicColumnContract(emission))
		presentationOrder++
	}

	output := recipe.Output{Name: document.Output.ID, RootResourceType: root.graph.ResourceType, RowGrain: string(rowGrain), RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact, Fields: nodes[authoringv2.RootOccurrenceID].fields, Pivots: nodes[authoringv2.RootOccurrenceID].pivots, Aggregates: nodes[authoringv2.RootOccurrenceID].aggregates, DynamicColumns: nodes[authoringv2.RootOccurrenceID].dynamics, CollisionPolicy: "error"}
	output.Traversals = semanticTraversals(document.Route, occurrences, nodes)
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "explorer_" + safeName(project) + "_" + safeName(explorerID), TranslationVersion: TranslationVersion, Outputs: []recipe.Output{output}}
	if err := bundle.Validate(); err != nil {
		return Result{}, fail("lower", "INVALID_RECIPE", "$.recipe", err.Error(), nil, err)
	}
	digest, err := bundle.Digest()
	if err != nil {
		return Result{}, fail("lower", "RECIPE_DIGEST_FAILED", "$.recipe", "recipe digest could not be calculated", nil, err)
	}
	_ = order
	return Result{Bundle: bundle, RecipeDigest: digest, EmittedColumns: emitted, IdentityMappings: mappings, Presentation: presentation, OutputContract: contract}, nil
}

func mergeContractQuality(contract *explorer.PublicOutputContract, emission explorer.EmittedColumn) {
	contract.Lossless = contract.Lossless && emission.Lossless
	contract.MLReady = contract.MLReady && emission.MLReady
	if emission.StructuralSuitability == "requires-review" {
		contract.StructuralSuitability = "requires-review"
	} else if contract.StructuralSuitability == "scalar" && emission.StructuralSuitability == "array" {
		contract.StructuralSuitability = "array"
	}
	for _, reason := range emission.LossReasons {
		if !containsSemanticString(contract.LossReasons, reason) {
			contract.LossReasons = append(contract.LossReasons, reason)
		}
	}
}

func containsSemanticString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func choiceArmForPath(path string) string {
	for _, raw := range strings.Split(strings.TrimPrefix(path, "root."), ".") {
		part := strings.TrimSuffix(raw, "[]")
		for _, family := range []string{"value", "effective", "deceased", "onset", "performed", "occurrence", "timing", "asNeeded", "medication"} {
			if len(part) > len(family) && strings.HasPrefix(part, family) {
				next := part[len(family)]
				if next >= 'A' && next <= 'Z' {
					return part
				}
			}
		}
	}
	return ""
}

func indexedLabel(label string, coordinates []capability.RepeatedCoordinate) string {
	parts := []string{label}
	for _, coordinate := range coordinates {
		parts = append(parts, fmt.Sprintf("[%d]", coordinate.Index))
	}
	return strings.Join(parts, " ")
}

func publicColumnContract(column explorer.EmittedColumn) explorer.PublicOutputColumn {
	return explorer.PublicOutputColumn{
		Column: column.PublicColumn, AuthoredColumns: append([]string(nil), column.AuthoredColumns...), Label: firstNonEmpty(column.Label, column.PublicColumn), LogicalType: column.LogicalType,
		Nullable: column.Nullable, Shape: column.Shape, SourceResourceType: column.SourceResourceType, SourcePath: column.SourcePath,
		ChoiceArm: column.ChoiceArm, Coordinates: append([]capability.RepeatedCoordinate(nil), column.Coordinates...),
		Lossless: column.Lossless, MLReady: column.MLReady, StructuralSuitability: column.StructuralSuitability, LossReasons: append([]string(nil), column.LossReasons...), Filterable: column.Filterable, Chartable: column.Chartable,
	}
}

func presentationColumn(source authoringv2.Column, sourceOrder, order int, emission explorer.EmittedColumn) PresentationColumn {
	visible := true
	pinned := false
	if source.Table != nil {
		if source.Table.Visible != nil {
			visible = *source.Table.Visible
		}
		pinned = source.Table.Pinned
	} else {
		visible = false
	}
	result := PresentationColumn{EmissionID: emission.EmissionID, PublicColumn: emission.PublicColumn, Label: emission.Label, Visible: visible, Order: order, Pinned: pinned}
	if source.Filter != nil && emission.Filterable {
		result.FilterLabel = firstNonEmpty(source.Filter.Label, emission.Label)
		result.FilterOrder = sourceOrder
	}
	if source.Chart != nil && emission.Chartable {
		result.ChartType = source.Chart.Type
		result.ChartTitle = source.Chart.Title
		result.ChartOrder = sourceOrder
	}
	return result
}

func resolveSemanticRoute(document authoringv2.Document, snapshot capability.Snapshot) (map[string]semanticOccurrence, []string, error) {
	byType := map[string][]capability.Node{}
	for _, node := range snapshot.Nodes {
		byType[node.ResourceType] = append(byType[node.ResourceType], node)
	}
	result := map[string]semanticOccurrence{}
	order := []string{}
	var walk func(authoringv2.RouteNode, *semanticOccurrence, string, int, map[string]bool) error
	walk = func(route authoringv2.RouteNode, parent *semanticOccurrence, path string, depth int, usedEdges map[string]bool) error {
		if snapshot.Policy.Route.MaxHops > 0 && depth > snapshot.Policy.Route.MaxHops {
			return fail("route", "ROUTE_TOO_LONG", path, "route exceeds capability route policy", map[string]any{"maxHops": snapshot.Policy.Route.MaxHops, "hops": depth}, nil)
		}
		var graph capability.Node
		var edge *capability.Edge
		if parent == nil {
			matches := byType[route.ResourceType]
			eligible := matches[:0]
			for _, match := range matches {
				if match.RowRootEligible {
					eligible = append(eligible, match)
				}
			}
			if len(eligible) != 1 {
				return fail("route", "AMBIGUOUS_ROOT_RESOURCE", path+".resourceType", "root resource type must resolve to exactly one eligible capability node", map[string]any{"resourceType": route.ResourceType, "matches": len(eligible)}, nil)
			}
			graph = eligible[0]
		} else {
			matches := []capability.Edge{}
			for _, candidate := range snapshot.Edges {
				target, ok := snapshot.Node(candidate.ToNodeID)
				if candidate.FromNodeID == parent.graph.ID && ok && target.ResourceType == route.ResourceType && candidate.Label == route.Relationship {
					matches = append(matches, candidate)
				}
			}
			if len(matches) != 1 {
				return fail("route", "AMBIGUOUS_RELATIONSHIP", path+".relationship", "relationship must resolve to exactly one capability edge", map[string]any{"fromResourceType": parent.graph.ResourceType, "relationship": route.Relationship, "toResourceType": route.ResourceType, "matches": len(matches)}, nil)
			}
			selected := matches[0]
			if usedEdges[selected.ID] && !snapshot.Policy.Route.AllowsRepeatedEdges {
				return fail("route", "REPEATED_EDGE_NOT_ALLOWED", path+".relationship", "route policy does not allow repeated edges", nil, nil)
			}
			if selected.FromNodeID == selected.ToNodeID && !snapshot.Policy.Route.AllowsSelfLoops {
				return fail("route", "SELF_LOOP_NOT_ALLOWED", path+".relationship", "route policy does not allow self loops", nil, nil)
			}
			nextUsedEdges := make(map[string]bool, len(usedEdges)+1)
			for id, used := range usedEdges {
				nextUsedEdges[id] = used
			}
			nextUsedEdges[selected.ID] = true
			edge = &selected
			graph, _ = snapshot.Node(selected.ToNodeID)
			usedEdges = nextUsedEdges
		}
		current := semanticOccurrence{node: route, graph: graph, edge: edge}
		result[route.OccurrenceID] = current
		order = append(order, route.OccurrenceID)
		children := append([]authoringv2.RouteNode(nil), route.Children...)
		sort.SliceStable(children, func(i, j int) bool { return children[i].OccurrenceID < children[j].OccurrenceID })
		for i := range children {
			if err := walk(children[i], &current, fmt.Sprintf("%s.children[%d]", path, i), depth+1, usedEdges); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(document.Route, nil, "$.route", 0, map[string]bool{}); err != nil {
		return nil, nil, err
	}
	return result, order, nil
}

func semanticTraversals(route authoringv2.RouteNode, occurrences map[string]semanticOccurrence, nodes map[string]*semanticRecipeNode) []recipe.Traversal {
	children := append([]authoringv2.RouteNode(nil), route.Children...)
	sort.SliceStable(children, func(i, j int) bool { return children[i].OccurrenceID < children[j].OccurrenceID })
	result := make([]recipe.Traversal, 0, len(children))
	for _, child := range children {
		occurrence := occurrences[child.OccurrenceID]
		node := nodes[child.OccurrenceID]
		matchMode := recipe.MatchOptional
		if child.MatchMode.Normalized() == authoringv2.RouteMatchRequired {
			matchMode = recipe.MatchRequired
		}
		result = append(result, recipe.Traversal{Name: recipeName(occurrence.edge.Label, occurrence.edge.ID), Alias: semanticAlias(child.OccurrenceID), ToResourceType: occurrence.graph.ResourceType, MatchMode: matchMode, Fields: node.fields, Pivots: node.pivots, Aggregates: node.aggregates, DynamicColumns: node.dynamics, Traversals: semanticTraversals(child, occurrences, nodes)})
	}
	return result
}

func semanticAlias(occurrenceID string) string {
	if occurrenceID == authoringv2.RootOccurrenceID {
		return "root"
	}
	// Semantic Builder occurrence IDs are globally unique output namespaces.
	// Keep the complete identifier so ALIAS traversal naming reproduces the
	// authored public-column prefix even when an older client encoded route
	// ancestry into the occurrence ID itself.
	return safeName(occurrenceID)
}

func semanticFieldCandidate(snapshot capability.Snapshot, nodeID, fieldPath string) (capability.Candidate, bool) {
	want := strings.TrimPrefix(strings.TrimSpace(fieldPath), "root.")
	for _, candidate := range snapshot.Candidates {
		actual := strings.TrimPrefix(strings.TrimSpace(candidate.FieldPath), "root.")
		if candidate.NodeID == nodeID && actual == want {
			return candidate, true
		}
	}
	return capability.Candidate{}, false
}

func capabilityProjectionMode(mode string) (capability.ProjectionMode, bool) {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case "VALUE":
		return capability.ProjectionScalar, true
	case "INDEXED":
		return capability.ProjectionIndexed, true
	case "FIRST":
		return capability.ProjectionFirst, true
	case "ALL":
		return capability.ProjectionArray, true
	case "DISTINCT":
		return capability.ProjectionDistinctArray, true
	default:
		return "", false
	}
}

func containsProjectionMode(values []capability.ProjectionMode, want capability.ProjectionMode) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func semanticFixedLookup(column authoringv2.Column, alias, leaf, logicalType string) (recipe.DynamicColumn, error) {
	empty := ""
	fieldPath := strings.Trim(strings.TrimSpace(column.Source.FieldPath()), ".")
	sourcePath, keyPath := "", ""
	var value recipe.Expression
	switch column.Source.Kind {
	case authoringv2.SourceIdentifierBySystem:
		sourcePath, keyPath = firstNonEmpty(fieldPath, "identifier[]"), "item.system"
		value = recipe.Expression{Select: "item.value"}
	case authoringv2.SourceExtensionByURL:
		sourcePath, keyPath = firstNonEmpty(fieldPath, "extension[]"), "item.url"
		value = coalesceString("item.valueString", "item.valueCode", "item.valueInteger", "item.valueDecimal", "item.valueBoolean", "item.valueDate", "item.valueDateTime", "item.valueUri")
	case authoringv2.SourceCodingBySystem:
		sourcePath, keyPath = firstNonEmpty(fieldPath, "code.coding[]"), "item.system"
		value = coalesceString("item.display", "item.code")
	default:
		return recipe.DynamicColumn{}, fmt.Errorf("unsupported source kind %q", column.Source.Kind)
	}
	key := recipe.Expression{Select: keyPath}
	return recipe.DynamicColumn{Name: "fixed_" + shortHash(column.Column), ColumnPrefix: &empty, Source: recipe.Expression{Select: alias + "." + sourcePath}, Key: &key, Value: &value, Columns: []string{leaf}, MaxColumns: 1, ColumnTypes: map[string]string{leaf: logicalType}, ColumnSourceKeys: map[string]string{leaf: column.Source.LookupMatch()}}, nil
}

func semanticAggregate(column authoringv2.Column, alias, resourceType string, contributorWhere *recipe.Filter) (recipe.Aggregate, string, error) {
	if column.Source.Aggregate == nil {
		return recipe.Aggregate{}, "", fmt.Errorf("aggregate payload is required")
	}
	source := column.Source.Aggregate
	op := strings.ToUpper(strings.TrimSpace(source.Operation))
	operation := recipe.AggregateOperation(op)
	aggregate := recipe.Aggregate{
		Name: "aggregate_" + shortHash(column.OccurrenceID+"\x00"+column.Column+"\x00"+op), OutputName: column.Column,
		Operation: operation, FieldRef: column.Column, ValueMode: recipe.ValueModeAuto,
		RequiredValues: append([]string(nil), source.RequiredValues...),
	}
	if strings.TrimSpace(source.Path) != "" {
		path := strings.Trim(strings.TrimSpace(source.Path), ".")
		aggregate.Expr = &recipe.Expression{Select: alias + "." + path}
	}
	if contributorWhere != nil {
		aggregate.Where = contributorWhere
	} else if source.Where != nil && strings.TrimSpace(source.Where.Path) != "" {
		wherePath := strings.Trim(strings.TrimSpace(source.Where.Path), ".")
		where := &recipe.Filter{Select: alias + "." + wherePath, FieldRef: column.Column}
		if strings.TrimSpace(source.Where.Equals) == "" {
			where.Operator = recipe.FilterExists
		} else {
			where.Operator = recipe.FilterEquals
			value := source.Where.Equals
			metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, wherePath)
			if !ok || metadata.Primitive != fhirschema.PrimitiveString {
				return recipe.Aggregate{}, "", fmt.Errorf("aggregate predicate selector %q must resolve to a string or code", source.Where.Path)
			}
			if metadata.Repeated {
				where.Quantifier = recipe.QuantifierAny
			}
			if wherePath == "code" || strings.HasSuffix(wherePath, ".code") {
				where.Values = []recipe.FilterValue{{Kind: recipe.FilterCode, Code: &recipe.CodeValue{Code: value}}}
			} else {
				where.Values = []recipe.FilterValue{{Kind: recipe.FilterString, String: &value}}
			}
		}
		aggregate.Where = where
	}

	logicalType := "string"
	switch operation {
	case recipe.AggregateCount, recipe.AggregateCountDistinct:
		logicalType = "integer"
	case recipe.AggregateExists, recipe.AggregateContainsAll:
		logicalType = "boolean"
	case recipe.AggregateDistinctValues, recipe.AggregateMin, recipe.AggregateMax:
		metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, strings.Trim(strings.TrimSpace(source.Path), "."))
		if !ok || metadata.Primitive == fhirschema.PrimitiveUnknown {
			return recipe.Aggregate{}, "", fmt.Errorf("aggregate selector %q is not represented by generated resource type %q", source.Path, resourceType)
		}
		switch metadata.Primitive {
		case fhirschema.PrimitiveInteger:
			logicalType = "integer"
		case fhirschema.PrimitiveDecimal:
			logicalType = "decimal"
		case fhirschema.PrimitiveBoolean:
			logicalType = "boolean"
		case fhirschema.PrimitiveDate:
			logicalType = "date"
		case fhirschema.PrimitiveDateTime:
			logicalType = "date_time"
		default:
			logicalType = "string"
		}
	default:
		return recipe.Aggregate{}, "", fmt.Errorf("unsupported aggregate operation %q", source.Operation)
	}
	return aggregate, logicalType, nil
}

func capabilityCandidate(snapshot capability.Snapshot, nodeID, candidateID string) (capability.Candidate, bool) {
	for _, candidate := range snapshot.Candidates {
		if candidate.ID == candidateID && candidate.NodeID == nodeID {
			return candidate, true
		}
	}
	return capability.Candidate{}, false
}

func contributorRecipeFilter(resourceType, alias string, candidate capability.Candidate, predicate authoringv2.ContributorPredicate) (recipe.Filter, error) {
	path := strings.TrimPrefix(strings.Trim(strings.TrimSpace(candidate.FieldPath), "."), "root.")
	selector, err := spec.ParseSelector(path)
	if err != nil {
		return recipe.Filter{}, fmt.Errorf("contributor candidate %q selector: %w", candidate.ID, err)
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, selector.CanonicalPath())
	if !ok || metadata.Primitive == fhirschema.PrimitiveUnknown {
		return recipe.Filter{}, fmt.Errorf("contributor candidate %q selector %q is not a supported scalar", candidate.ID, candidate.FieldPath)
	}
	where := recipe.Filter{Select: alias + "." + selector.CanonicalPath(), FieldRef: candidate.ID}
	where.Operator = recipe.FilterOperator(predicate.Operator)
	where.Quantifier = recipe.ArrayQuantifier(predicate.Quantifier)
	if predicate.Value != nil {
		value := recipe.FilterValue{Kind: recipe.FilterValueKind(predicate.Value.Kind)}
		switch predicate.Value.Kind {
		case authoringv2.ContributorString:
			stringValue := *predicate.Value.String
			value.String = &stringValue
		case authoringv2.ContributorValueCode:
			value.Code = &recipe.CodeValue{Code: predicate.Value.Code.Code}
		default:
			return recipe.Filter{}, fmt.Errorf("unsupported contributor value kind %q", predicate.Value.Kind)
		}
		where.Values = []recipe.FilterValue{value}
	}
	if err := where.Validate(); err != nil {
		return recipe.Filter{}, fmt.Errorf("contributor candidate %q: %w", candidate.ID, err)
	}
	return where, nil
}

func semanticObservationPivot(column authoringv2.Column, alias, leaf string) (recipe.Pivot, error) {
	if column.Source.Lookup == nil {
		return recipe.Pivot{}, fmt.Errorf("observation component lookup payload is required")
	}
	lookup := column.Source.Lookup
	if lookup.Binding != nil {
		if lookup.Key == nil {
			return recipe.Pivot{}, fmt.Errorf("correlated observation component lookup key is required")
		}
		if strings.TrimSpace(column.Column) == "" {
			return recipe.Pivot{}, fmt.Errorf("correlated observation component output column is required")
		}
		// Correlated pivots carry no legacy selector expressions. The checked
		// binding is the sole provenance for key/value extraction; ColumnAliases
		// preserves the authored public name independently of the terminology
		// code (including codes containing punctuation).
		return recipe.Pivot{
			Name:              "correlated_" + shortHash(column.Column+"\x00"+lookup.Key.System+"\x00"+lookup.Key.Code),
			Columns:           []string{lookup.Key.Code},
			ColumnAliases:     map[string]string{lookup.Key.Code: leaf},
			ProjectionMode:    recipe.NormalizedPivotProjectionMode(lookup.ProjectionMode),
			Correlation:       lookup.Binding,
			CorrelationSystem: lookup.Key.System,
			CorrelationCode:   lookup.Key.Code,
		}, nil
	}
	match := lookup.Match
	separator := "__" + match
	if !strings.HasSuffix(leaf, separator) || strings.TrimSuffix(leaf, separator) == "" {
		return recipe.Pivot{}, fmt.Errorf("observation component column %q must end with %q", column.Column, separator)
	}
	name := strings.TrimSuffix(leaf, separator)
	sourcePath := firstNonEmpty(strings.Trim(strings.TrimSpace(lookup.Path), "."), "component[]")
	prefix := alias + "." + sourcePath
	pivot := recipe.Pivot{
		Name:             name,
		ColumnExpr:       recipe.Expression{Select: prefix + ".code.coding[].code"},
		ValueExpr:        recipe.Expression{Select: prefix + ".valueString"},
		ValueFallbacks:   []recipe.Expression{{Select: prefix + ".valueCodeableConcept.text"}, {Select: prefix + ".valueQuantity.value"}, {Select: prefix + ".valueInteger"}},
		ItemSource:       recipe.Expression{Select: alias + "." + sourcePath},
		ItemResourceType: "ObservationComponent",
		Columns:          []string{match},
	}
	return pivot, nil
}

func semanticExtensionPivot(column authoringv2.Column, leaf string) (recipe.Pivot, error) {
	lookup := column.Source.Lookup
	if lookup == nil || lookup.Extension == nil {
		return recipe.Pivot{}, fmt.Errorf("ancestor-aware extension lookup requires extension binding")
	}
	if strings.TrimSpace(column.Column) == "" {
		return recipe.Pivot{}, fmt.Errorf("extension output column is required")
	}
	return recipe.Pivot{
		Name:    "extension_correlated_" + shortHash(column.Column+"\x00"+strings.Join(lookup.Extension.URLPath, "\x00")),
		Columns: []string{leaf}, ColumnAliases: map[string]string{leaf: leaf},
		ProjectionMode:       recipe.NormalizedPivotProjectionMode(lookup.ProjectionMode),
		ExtensionCorrelation: lookup.Extension,
	}, nil
}

func semanticCorrelatedCodingPivot(column authoringv2.Column, alias, leaf string) (recipe.Pivot, error) {
	lookup := column.Source.Lookup
	if lookup == nil || lookup.Binding == nil || lookup.Key == nil {
		return recipe.Pivot{}, fmt.Errorf("correlated coding lookup requires binding and key")
	}
	if strings.TrimSpace(column.Column) == "" {
		return recipe.Pivot{}, fmt.Errorf("correlated coding output column is required")
	}
	pivot := recipe.Pivot{
		Name:    "correlated_" + shortHash(column.Column+"\x00"+lookup.Key.System+"\x00"+lookup.Key.Code),
		Columns: []string{lookup.Key.Code}, ColumnAliases: map[string]string{lookup.Key.Code: leaf},
		ProjectionMode: recipe.NormalizedPivotProjectionMode(lookup.ProjectionMode), Correlation: lookup.Binding,
		CorrelationSystem: lookup.Key.System, CorrelationCode: lookup.Key.Code,
	}
	return pivot, nil
}

func appendSemanticPivot(pivots []recipe.Pivot, pivot recipe.Pivot) []recipe.Pivot {
	for i := range pivots {
		if pivots[i].Name == pivot.Name {
			pivots[i].Columns = append(pivots[i].Columns, pivot.Columns...)
			return pivots
		}
	}
	return append(pivots, pivot)
}

func coalesceString(paths ...string) recipe.Expression {
	args := make([]recipe.Expression, 0, len(paths))
	for _, path := range paths {
		args = append(args, recipe.Expression{Select: path})
	}
	return recipe.Expression{Call: "coalesce_string", Args: args}
}
