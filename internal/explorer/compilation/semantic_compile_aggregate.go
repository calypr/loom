package compilation

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"strings"
)

func (columns *semanticColumnCompiler) compileAggregate(index int, column authoringv2.Column, occurrence semanticOccurrence, alias string) (string, error) {
	document, snapshot := columns.document, columns.snapshot
	root := columns.occurrences[authoringv2.RootOccurrenceID]
	nodes, explorerID := columns.nodes, columns.explorerID
	if source := column.Source.Aggregate; source != nil {
		if source.ContributorWindow != nil && column.OccurrenceID == authoringv2.RootOccurrenceID {
			return "", fail("intent", "CONTRIBUTOR_WINDOW_REQUIRES_RELATED_RESOURCE", fmt.Sprintf("$.columns[%d].source.aggregate.contributorWindow", index), "contributor windows require a related resource occurrence", nil, nil)
		}
		operation := capability.AggregateOperation(strings.ToUpper(strings.TrimSpace(source.Operation)))
		path := strings.TrimPrefix(strings.TrimSpace(source.Path), "root.")
		candidate, found := semanticFieldCandidate(snapshot, occurrence.graph.ID, path)
		if found {
			input := capability.AggregateInput{
				LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality,
				HasField: path != "", RelatedResource: column.OccurrenceID != authoringv2.RootOccurrenceID,
				ContributorWindowConfigured: source.ContributorWindow != nil,
				OrderingConfigured:          source.Ordering != nil,
				RequiredValuesConfigured:    len(source.RequiredValues) > 0,
			}
			choices := capability.DeriveAggregateOperationCapabilities(input, capability.AggregateRowContext(document.Rows.Kind))
			for _, choice := range choices {
				if choice.Operation == operation && !choice.Supported && (operation == capability.AggregateSum || operation == capability.AggregateMean) {
					return "", fail("capability", "AGGREGATE_OPERATION_UNAVAILABLE", fmt.Sprintf("$.columns[%d].source.aggregate.operation", index), choice.Reason, map[string]any{"operation": operation, "reasonCode": choice.ReasonCode, "rowContext": choice.RowContext}, nil)
				}
			}
		}
	}
	var contributorWhere *recipe.Filter
	if column.Contributor != nil {
		catalog := authoringv2.CatalogFromCapability(snapshot, explorerID)
		if err := authoringv2.ValidateContributorForCatalog(document, catalog, column.OccurrenceID, column.Source, *column.Contributor); err != nil {
			return "", fail("intent", "INVALID_CONTRIBUTOR_PREDICATE", fmt.Sprintf("$.columns[%d].contributor", index), err.Error(), map[string]any{"candidateId": column.Contributor.CandidateID}, err)
		}
		candidate, found := capabilityCandidate(snapshot, occurrence.graph.ID, column.Contributor.CandidateID)
		if !found {
			return "", fail("intent", "STALE_CONTRIBUTOR_CANDIDATE", fmt.Sprintf("$.columns[%d].contributor.candidateId", index), "contributor candidate is not present on the resolved capability node", map[string]any{"candidateId": column.Contributor.CandidateID}, nil)
		}
		where, whereErr := contributorRecipeFilter(occurrence.graph.ResourceType, alias, candidate, *column.Contributor)
		if whereErr != nil {
			return "", fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].contributor", index), whereErr.Error(), nil, whereErr)
		}
		contributorWhere = &where
	}
	if column.Source.Aggregate != nil {
		if path := strings.TrimPrefix(strings.TrimSpace(column.Source.Aggregate.Path), "root."); path != "" {
			if _, found := semanticFieldCandidate(snapshot, occurrence.graph.ID, path); !found {
				return "", fail("intent", "STALE_FIELD", fmt.Sprintf("$.columns[%d].source.aggregate.path", index), "aggregate path is not present on the resolved capability node", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": path}, nil)
			}
		}
		if window := column.Source.Aggregate.ContributorWindow; window != nil {
			timestampPath := strings.TrimPrefix(strings.TrimSpace(window.TimestampPath), "root.")
			timestampCandidate, found := semanticFieldCandidate(snapshot, occurrence.graph.ID, timestampPath)
			if !found || !strings.EqualFold(timestampCandidate.LogicalType, "date_time") {
				return "", fail("intent", "INVALID_CONTRIBUTOR_WINDOW_TIMESTAMP", fmt.Sprintf("$.columns[%d].source.aggregate.contributorWindow.timestampPath", index), "contributor window timestamp must be a date_time field on the contributing resource", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": timestampPath}, nil)
			}
			transformCandidate := timestampCandidate
			aggregatePath := strings.TrimPrefix(strings.TrimSpace(column.Source.Aggregate.Path), "root.")
			if aggregatePath != "" {
				if aggregateCandidate, hasAggregate := semanticFieldCandidate(snapshot, occurrence.graph.ID, aggregatePath); hasAggregate {
					transformCandidate = aggregateCandidate
				}
			} else {
				operation := strings.ToUpper(strings.TrimSpace(column.Source.Aggregate.Operation))
				if operation != "COUNT" && operation != "EXISTS" {
					return "", fail("intent", "STALE_FIELD", fmt.Sprintf("$.columns[%d].source.aggregate.path", index), "aggregate path is required for this operation", map[string]any{"operation": operation}, nil)
				}
			}
			transformations := authoringv2.AggregateTransformationCapabilitiesForCapability(snapshot, transformCandidate.ID)
			if !transformations.Temporal.SupportsTimestamp(occurrence.graph.ResourceType, timestampPath) {
				return "", fail("capability", "UNADVERTISED_CONTRIBUTOR_WINDOW_TIMESTAMP", fmt.Sprintf("$.columns[%d].source.aggregate.contributorWindow.timestampPath", index), "contributor window timestamp is not an advertised scalar date_time choice for the contributing resource", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": timestampPath}, nil)
			}
			anchorPath := strings.TrimPrefix(strings.TrimSpace(window.AnchorPath), "root.")
			anchorCandidate, found := semanticFieldCandidate(snapshot, root.graph.ID, anchorPath)
			if !found || !strings.EqualFold(anchorCandidate.LogicalType, "date_time") {
				return "", fail("intent", "INVALID_CONTRIBUTOR_WINDOW_ANCHOR", fmt.Sprintf("$.columns[%d].source.aggregate.contributorWindow.anchorPath", index), "contributor window anchor must be a date_time field on the root row", map[string]any{"resourceType": root.graph.ResourceType, "fieldPath": anchorPath}, nil)
			}
			if !transformations.Temporal.SupportsAnchor(root.graph.ResourceType, anchorPath) {
				return "", fail("capability", "UNADVERTISED_CONTRIBUTOR_WINDOW_ANCHOR", fmt.Sprintf("$.columns[%d].source.aggregate.contributorWindow.anchorPath", index), "contributor window anchor is not an advertised scalar date_time choice for the root row", map[string]any{"resourceType": root.graph.ResourceType, "fieldPath": anchorPath}, nil)
			}
			if ordering := column.Source.Aggregate.Ordering; ordering != nil {
				orderingPath := strings.TrimPrefix(strings.TrimSpace(ordering.TimestampPath), "root.")
				orderingCandidate, found := semanticFieldCandidate(snapshot, occurrence.graph.ID, orderingPath)
				if !found || !strings.EqualFold(orderingCandidate.LogicalType, "date_time") {
					return "", fail("intent", "INVALID_ORDERING_TIMESTAMP", fmt.Sprintf("$.columns[%d].source.aggregate.ordering.timestampPath", index), "ordering timestamp must be a date_time field on the contributing resource", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": orderingPath}, nil)
				}
				if !transformations.Temporal.SupportsTimestamp(occurrence.graph.ResourceType, orderingPath) {
					return "", fail("capability", "UNADVERTISED_ORDERING_TIMESTAMP", fmt.Sprintf("$.columns[%d].source.aggregate.ordering.timestampPath", index), "ordering timestamp is not an advertised scalar date_time choice for the contributing resource", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": orderingPath}, nil)
				}
			}
		}
		if policy := column.Source.Aggregate.UnitNormalization; policy != nil {
			aggregatePath := strings.TrimPrefix(strings.TrimSpace(column.Source.Aggregate.Path), "root.")
			aggregateCandidate, found := semanticFieldCandidate(snapshot, occurrence.graph.ID, aggregatePath)
			if !found {
				return "", fail("intent", "STALE_FIELD", fmt.Sprintf("$.columns[%d].source.aggregate.path", index), "aggregate path is not present on the resolved capability node", map[string]any{"resourceType": occurrence.graph.ResourceType, "fieldPath": aggregatePath}, nil)
			}
			transformations := authoringv2.AggregateTransformationCapabilitiesForCapability(snapshot, aggregateCandidate.ID)
			advertised := false
			for _, preset := range transformations.UnitNormalization.Presets {
				if preset.PolicyID != policy.PolicyID || preset.Version != policy.Version {
					continue
				}
				if !preset.Available {
					return "", fail("capability", "UNIT_NORMALIZATION_UNAVAILABLE", fmt.Sprintf("$.columns[%d].source.aggregate.unitNormalization", index), preset.Reason, map[string]any{"policyId": policy.PolicyID, "version": policy.Version, "reasonCode": preset.ReasonCode}, nil)
				}
				advertised = true
				break
			}
			if !advertised {
				return "", fail("capability", "UNADVERTISED_UNIT_NORMALIZATION", fmt.Sprintf("$.columns[%d].source.aggregate.unitNormalization", index), "unit normalization policy is not advertised for the aggregate candidate", map[string]any{"policyId": policy.PolicyID, "version": policy.Version}, nil)
			}
		}
	}
	aggregate, aggregateType, aggregateErr := semanticAggregate(column, alias, occurrence.graph.ResourceType, contributorWhere)
	if aggregateErr != nil {
		return "", fail("lower", "INVALID_TYPED_SOURCE", fmt.Sprintf("$.columns[%d].source", index), aggregateErr.Error(), nil, aggregateErr)
	}
	nodes[column.OccurrenceID].aggregates = append(nodes[column.OccurrenceID].aggregates, aggregate)
	return aggregateType, nil
}
