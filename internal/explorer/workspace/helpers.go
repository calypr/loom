package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/calypr/loom/internal/explorer/capability"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func CloneWorkspace(value Workspace) (Workspace, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return Workspace{}, err
	}
	var result Workspace
	if err := json.Unmarshal(raw, &result); err != nil {
		return Workspace{}, err
	}
	return result, nil
}

func CommandGeneratedID(prefix string, values ...any) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprint(value)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + hex.EncodeToString(sum[:12])
}

func CatalogNodeByID(catalog CatalogSnapshot, id string) (CatalogNode, bool) {
	for _, value := range catalog.Nodes {
		if value.ID == id {
			return value, true
		}
	}
	return CatalogNode{}, false
}

func CatalogEdgeByID(catalog CatalogSnapshot, id string) (CatalogEdge, bool) {
	for _, value := range catalog.Edges {
		if value.ID == id {
			return value, true
		}
	}
	return CatalogEdge{}, false
}

func RoutePath(route RouteNode, occurrenceID string) ([]RouteNode, bool) {
	if route.OccurrenceID == occurrenceID {
		return []RouteNode{route}, true
	}
	for _, child := range route.Children {
		path, found := RoutePath(child, occurrenceID)
		if found {
			return append([]RouteNode{route}, path...), true
		}
	}
	return nil, false
}

func CatalogNodeForConstructionRoot(catalog CatalogSnapshot, resourceType string) (CatalogNode, bool) {
	var result CatalogNode
	for _, node := range catalog.Nodes {
		if !node.RowRootEligible || node.ResourceType != resourceType {
			continue
		}
		if result.ID != "" {
			return CatalogNode{}, false
		}
		result = node
	}
	return result, result.ID != ""
}

func FindRoute(route *RouteNode, occurrenceID string) *RouteNode {
	if route.OccurrenceID == occurrenceID {
		return route
	}
	for i := range route.Children {
		if found := FindRoute(&route.Children[i], occurrenceID); found != nil {
			return found
		}
	}
	return nil
}

func QualifySemanticPath(ownerPath, relativePath string) string {
	ownerPath = strings.Trim(strings.TrimSpace(ownerPath), ".")
	relativePath = strings.Trim(strings.TrimSpace(relativePath), ".")
	if ownerPath == "" {
		return relativePath
	}
	if relativePath == "" {
		return ownerPath
	}
	return ownerPath + "." + relativePath
}

func InferredSourceLogicalType(document Document, catalog CatalogSnapshot, occurrenceID string, source ColumnSource, fallback string) string {
	if source.Kind == SourceProjectID {
		return "string"
	}
	if source.Lookup != nil && source.Lookup.Binding != nil && strings.TrimSpace(source.Lookup.Binding.LogicalType) != "" {
		return strings.TrimSpace(source.Lookup.Binding.LogicalType)
	}
	if source.Lookup != nil && source.Lookup.Extension != nil && strings.TrimSpace(source.Lookup.Extension.LogicalType) != "" {
		return strings.TrimSpace(source.Lookup.Extension.LogicalType)
	}
	if source.Lookup != nil && source.Lookup.Identifier != nil && strings.TrimSpace(source.Lookup.Identifier.LogicalType) != "" {
		return strings.TrimSpace(source.Lookup.Identifier.LogicalType)
	}
	if source.Kind == SourceAggregate && source.Aggregate != nil {
		switch strings.ToUpper(strings.TrimSpace(source.Aggregate.Operation)) {
		case "COUNT", "COUNT_DISTINCT":
			return "integer"
		case "SUM", "MEAN":
			return "decimal"
		case "EXISTS", "CONTAINS_ALL":
			return "boolean"
		}
	}
	occurrence := FindRoute(&document.Route, occurrenceID)
	if occurrence != nil {
		path := strings.TrimPrefix(strings.TrimSpace(source.fieldPath()), "root.")
		for _, candidate := range catalog.Candidates {
			node, ok := CatalogNodeByID(catalog, candidate.NodeID)
			if ok && node.ResourceType == occurrence.ResourceType && strings.TrimPrefix(strings.TrimSpace(candidate.FieldPath), "root.") == path && strings.TrimSpace(candidate.LogicalType) != "" {
				return candidate.LogicalType
			}
		}
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return "string"
}

func ValidateEditableSource(document Document, catalog CatalogSnapshot, occurrenceID string, source ColumnSource) error {
	return ValidateEditableSourcePolicy(document, catalog, occurrenceID, source, false)
}

func ValidateEditableSourcePolicy(document Document, catalog CatalogSnapshot, occurrenceID string, source ColumnSource, allowImportedRecipe bool) error {
	if source.Kind == SourceImported || (!allowImportedRecipe && document.ImportedRecipe != nil) {
		return fmt.Errorf("imported recipe source columns are immutable")
	}
	occurrence := FindRoute(&document.Route, occurrenceID)
	if occurrence == nil {
		return fmt.Errorf("occurrence %q was not found", occurrenceID)
	}
	path := strings.TrimPrefix(strings.TrimSpace(source.FieldPath()), "root.")
	if source.Kind == SourceProjectID {
		return nil
	}
	if source.Lookup != nil && source.Lookup.Extension != nil && source.Kind == SourceExtensionByURL {
		if _, err := fhirbinding.ValidateExtensionBinding(occurrence.ResourceType, *source.Lookup.Extension); err != nil {
			return fmt.Errorf("source extension: %w", err)
		}
		return nil
	}
	if source.Lookup != nil && source.Lookup.Identifier != nil && source.Kind == SourceIdentifierBySystem {
		if _, err := fhirbinding.ValidateIdentifierBinding(occurrence.ResourceType, *source.Lookup.Identifier); err != nil {
			return fmt.Errorf("source identifier: %w", err)
		}
		return nil
	}
	if source.Lookup != nil && source.Lookup.Binding != nil && source.Kind == SourceCodedValue {
		if _, err := fhirbinding.ValidateCorrelatedBinding(occurrence.ResourceType, *source.Lookup.Binding); err != nil {
			return fmt.Errorf("source binding: %w", err)
		}

		return nil
	}
	if source.OwnerRecords != nil && source.Kind == SourceOwnerRecords {
		if _, err := fhirbinding.ValidateCorrelatedBinding(occurrence.ResourceType, source.OwnerRecords.Binding); err != nil {
			return fmt.Errorf("source owner records binding: %w", err)
		}
		return nil
	}
	if source.Kind == SourceExtensionByURL || source.Kind == SourceCodedValue || source.Kind == SourceOwnerRecords {
		return fmt.Errorf("new %s lookup requires an explicit typed binding", source.Kind)
	}
	if path == "" && source.Kind != SourceAggregate {
		return fmt.Errorf("source path is required for %s", source.Kind)
	}
	if source.Kind == SourceAggregate {
		return validateAggregateSourcePolicy(document, catalog, occurrenceID, occurrence, path, source)
	}
	candidate, ok := catalogCandidateForOccurrencePath(catalog, occurrence.ResourceType, path)
	if !ok {
		return fmt.Errorf("source path %q is not present for occurrence %q", path, occurrenceID)
	}
	mode := strings.ToUpper(strings.TrimSpace(source.ProjectionMode()))
	if mode == "" {
		mode = "FIRST"
	}
	if !slices.Contains(candidate.ProjectionModes, mode) {
		return fmt.Errorf("projection mode %q is not advertised for source path %q", mode, path)
	}
	return nil
}

func catalogCandidateForOccurrencePath(catalog CatalogSnapshot, resourceType, candidatePath string) (CatalogCandidate, bool) {
	candidatePath = strings.TrimPrefix(strings.TrimSpace(candidatePath), "root.")
	for _, candidate := range catalog.Candidates {
		node, ok := CatalogNodeByID(catalog, candidate.NodeID)
		if !ok || node.ResourceType != resourceType {
			continue
		}
		if candidatePath == strings.TrimPrefix(strings.TrimSpace(candidate.FieldPath), "root.") {
			return candidate, true
		}
	}
	return CatalogCandidate{}, false
}

func validateAggregateSourcePolicy(document Document, catalog CatalogSnapshot, occurrenceID string, occurrence *RouteNode, path string, source ColumnSource) error {
	selected, hasSelected, err := validateAggregateOperationPolicy(document, catalog, occurrenceID, occurrence, path, source)
	if err != nil {
		return err
	}
	if source.Aggregate == nil {
		return nil
	}
	if source.Aggregate.ContributorWindow != nil {
		if err := validateAggregateContributorWindow(document, catalog, occurrenceID, occurrence, selected, hasSelected, source.Aggregate.ContributorWindow, source); err != nil {
			return err
		}
	}
	if source.Aggregate.UnitNormalization != nil {
		return validateAggregateUnitNormalization(catalog, selected, hasSelected, source.Aggregate.UnitNormalization)
	}
	return nil
}

func validateAggregateOperationPolicy(document Document, catalog CatalogSnapshot, occurrenceID string, occurrence *RouteNode, path string, source ColumnSource) (CatalogCandidate, bool, error) {
	var selected CatalogCandidate
	hasSelected := false
	if path != "" {
		selected, hasSelected = catalogCandidateForOccurrencePath(catalog, occurrence.ResourceType, path)
		if !hasSelected {
			return CatalogCandidate{}, false, fmt.Errorf("source path %q is not present for occurrence %q", path, occurrenceID)
		}
	}
	if source.Aggregate != nil {
		related := occurrenceID != RootOccurrenceID
		input := capability.AggregateInput{
			HasField:                    path != "" && hasSelected,
			RelatedResource:             related,
			ContributorWindowConfigured: source.Aggregate.ContributorWindow != nil,
			OrderingConfigured:          source.Aggregate.Ordering != nil,
			RequiredValuesConfigured:    len(source.Aggregate.RequiredValues) > 0,
		}
		if hasSelected {
			input.LogicalType, input.Cardinality = selected.LogicalType, selected.Cardinality
		}
		choices := capability.DeriveAggregateOperationCapabilities(input, capability.AggregateRowContext(document.Rows.Kind))
		operation := capability.AggregateOperation(strings.ToUpper(strings.TrimSpace(source.Aggregate.Operation)))
		for _, choice := range choices {
			if choice.Operation != operation {
				continue
			}
			if !choice.Supported && (operation == capability.AggregateSum || operation == capability.AggregateMean) {
				return CatalogCandidate{}, false, fmt.Errorf("aggregate operation %s unavailable (%s): %s", operation, choice.ReasonCode, choice.Reason)
			}
			break
		}
	}
	return selected, hasSelected, nil
}

func validateAggregateContributorWindow(document Document, catalog CatalogSnapshot, occurrenceID string, occurrence *RouteNode, selected CatalogCandidate, hasSelected bool, window *ContributorWindowSource, source ColumnSource) error {
	if occurrenceID == RootOccurrenceID {
		return fmt.Errorf("contributor window requires a related resource occurrence")
	}
	timestampPath := canonicalTransformPath(window.TimestampPath)
	timestampCandidate, hasTimestamp := catalogCandidateForOccurrencePath(catalog, occurrence.ResourceType, timestampPath)
	if !hasTimestamp {
		return fmt.Errorf("contributor window timestamp path %q is not advertised for occurrence %q", window.TimestampPath, occurrenceID)
	}
	transformCandidate := timestampCandidate
	if hasSelected {
		transformCandidate = selected
	} else if operation := strings.ToUpper(strings.TrimSpace(source.Aggregate.Operation)); operation != "COUNT" && operation != "EXISTS" {
		return fmt.Errorf("contributor window requires an advertised aggregate candidate")
	}
	transformations := AggregateTransformationCapabilitiesForCatalog(catalog, transformCandidate.ID)
	if !transformations.Temporal.Available {
		return fmt.Errorf("contributor window unavailable (%s): %s", transformations.Temporal.ReasonCode, transformations.Temporal.Reason)
	}
	if !transformations.Temporal.SupportsTimestamp(occurrence.ResourceType, timestampPath) {
		return fmt.Errorf("contributor window timestamp path %q is not advertised for occurrence %q", window.TimestampPath, occurrenceID)
	}
	anchorPath := canonicalTransformPath(window.AnchorPath)
	root := FindRoute(&document.Route, RootOccurrenceID)
	if root == nil || !transformations.Temporal.SupportsAnchor(root.ResourceType, anchorPath) {
		return fmt.Errorf("contributor window anchor path %q is not advertised for the root resource", window.AnchorPath)
	}
	if ordering := source.Aggregate.Ordering; ordering != nil {
		orderingPath := canonicalTransformPath(ordering.TimestampPath)
		if !transformations.Temporal.SupportsTimestamp(occurrence.ResourceType, orderingPath) {
			return fmt.Errorf("ordering timestamp path %q is not advertised for occurrence %q", ordering.TimestampPath, occurrenceID)
		}
	}
	return nil
}

func validateAggregateUnitNormalization(catalog CatalogSnapshot, selected CatalogCandidate, hasSelected bool, policy *UnitNormalizationPolicy) error {
	if !hasSelected {
		return fmt.Errorf("unit normalization requires an advertised aggregate candidate")
	}
	transformations := AggregateTransformationCapabilitiesForCatalog(catalog, selected.ID)
	for _, preset := range transformations.UnitNormalization.Presets {
		if preset.PolicyID != policy.PolicyID || preset.Version != policy.Version {
			continue
		}
		if !preset.Available {
			return fmt.Errorf("unit normalization policy %s@%s unavailable (%s): %s", preset.PolicyID, preset.Version, preset.ReasonCode, preset.Reason)
		}
		return nil
	}
	return fmt.Errorf("unit normalization policy %s@%s is not advertised for aggregate candidate %q", policy.PolicyID, policy.Version, selected.ID)
}

func ValidateContributorForCatalog(document Document, catalog CatalogSnapshot, occurrenceID string, source ColumnSource, predicate ContributorPredicate) error {
	if source.Kind != SourceAggregate || source.Aggregate == nil {
		return fmt.Errorf("contributor is only supported for aggregate sources")
	}
	if err := predicate.Validate(); err != nil {
		return err
	}
	occurrence := FindRoute(&document.Route, occurrenceID)
	if occurrence == nil {
		return fmt.Errorf("occurrence %q was not found", occurrenceID)
	}
	candidate, ok := CatalogCandidateByID(catalog, predicate.CandidateID)
	if !ok {
		return fmt.Errorf("contributor candidate %q was not found", predicate.CandidateID)
	}
	node, ok := CatalogNodeByID(catalog, candidate.NodeID)
	if !ok || node.ResourceType != occurrence.ResourceType {
		return fmt.Errorf("contributor candidate %q does not belong to occurrence %q", predicate.CandidateID, occurrenceID)
	}
	path := strings.TrimPrefix(strings.Trim(strings.TrimSpace(candidate.FieldPath), "."), "root.")
	canonical := fhirschema.CanonicalizePath(path)
	field, ok := fhirschema.LookupField(occurrence.ResourceType, canonical)
	if !ok {
		return fmt.Errorf("contributor candidate %q selector %q is not present in generated FHIR schema", predicate.CandidateID, candidate.FieldPath)
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(occurrence.ResourceType, canonical)
	if !ok || metadata.Primitive == fhirschema.PrimitiveUnknown {
		return fmt.Errorf("contributor candidate %q selector %q is not a supported scalar", predicate.CandidateID, candidate.FieldPath)
	}
	repeated := metadata.Repeated || strings.Contains(field.Path, "[]")
	if repeated {
		if predicate.Quantifier != ContributorAny {
			return fmt.Errorf("repeated contributor candidate %q requires explicit ANY quantifier", predicate.CandidateID)
		}
	} else if predicate.Quantifier != "" {
		return fmt.Errorf("scalar contributor candidate %q forbids quantifier", predicate.CandidateID)
	}
	if predicate.Operator != ContributorEquals {
		return nil
	}
	wantKind := ContributorString
	if strings.EqualFold(strings.TrimSpace(candidate.LogicalType), "code") || canonical == "code" || strings.HasSuffix(canonical, ".code") {
		wantKind = ContributorValueCode
	}
	if predicate.Value == nil || predicate.Value.Kind != wantKind {
		return fmt.Errorf("contributor candidate %q requires %s value", predicate.CandidateID, wantKind)
	}
	return nil
}

func CatalogCandidateByID(catalog CatalogSnapshot, id string) (CatalogCandidate, bool) {
	for _, value := range catalog.Candidates {
		if value.ID == id {
			return value, true
		}
	}
	return CatalogCandidate{}, false
}

func (s ColumnSource) ValidateSource(path string) error { return s.validate(path) }
