package authoringv2

import (
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type aggregateTransformCandidate struct {
	id           string
	nodeID       string
	resourceType string
	fieldPath    string
	label        string
	logicalType  string
	cardinality  string
	repeated     bool
	concepts     []aggregateUnitEvidence
}

type aggregateUnitEvidence struct {
	valuePath string
	units     []string
	truncated bool
}

type aggregateTransformNode struct {
	id              string
	resourceType    string
	rowRootEligible bool
}

// AggregateTransformationCapabilitiesForCatalog derives choices from the
// resolved catalog candidates and generated FHIR schema.
func AggregateTransformationCapabilitiesForCatalog(catalog CatalogSnapshot, candidateID string) AggregateTransformationCapabilities {
	nodes := make([]aggregateTransformNode, 0, len(catalog.Nodes))
	for _, node := range catalog.Nodes {
		nodes = append(nodes, aggregateTransformNode{id: node.ID, resourceType: node.ResourceType, rowRootEligible: node.RowRootEligible})
	}
	candidates := make([]aggregateTransformCandidate, 0, len(catalog.Candidates))
	for _, candidate := range catalog.Candidates {
		mapped := aggregateTransformCandidate{
			id: candidate.ID, nodeID: candidate.NodeID, fieldPath: candidate.FieldPath,
			label: candidate.Label, logicalType: candidate.LogicalType,
			cardinality: candidate.Cardinality, repeated: candidate.Repeated,
		}
		if node, ok := catalogNode(catalog, candidate.NodeID); ok {
			mapped.resourceType = node.ResourceType
		}
		for _, concept := range candidate.ConceptCandidates {
			mapped.concepts = append(mapped.concepts, aggregateUnitEvidence{
				valuePath: concept.ValueSelector, units: append([]string(nil), concept.ObservedUnits...),
				truncated: concept.ObservedUnitsTruncated,
			})
		}
		candidates = append(candidates, mapped)
	}
	return aggregateTransformationCapabilities(nodes, candidates, candidateID)
}

// AggregateTransformationCapabilitiesForCapability derives choices from the
// capability snapshot used at compilation time.
func AggregateTransformationCapabilitiesForCapability(snapshot capability.Snapshot, candidateID string) AggregateTransformationCapabilities {
	nodes := make([]aggregateTransformNode, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodes = append(nodes, aggregateTransformNode{id: node.ID, resourceType: node.ResourceType, rowRootEligible: node.RowRootEligible})
	}
	candidates := make([]aggregateTransformCandidate, 0, len(snapshot.Candidates))
	for _, candidate := range snapshot.Candidates {
		mapped := aggregateTransformCandidate{
			id: candidate.ID, nodeID: candidate.NodeID, resourceType: candidate.ResourceType,
			fieldPath: candidate.FieldPath, label: candidate.Label, logicalType: candidate.LogicalType,
			cardinality: candidate.Cardinality, repeated: capability.IsRepeatedCardinality(candidate.Cardinality),
		}
		for _, concept := range candidate.ConceptCandidates {
			mapped.concepts = append(mapped.concepts, aggregateUnitEvidence{
				valuePath: concept.ValueSelector, units: append([]string(nil), concept.ObservedUnits...),
				truncated: concept.ObservedUnitsTruncated,
			})
		}
		candidates = append(candidates, mapped)
	}
	return aggregateTransformationCapabilities(nodes, candidates, candidateID)
}

func aggregateTransformationCapabilities(nodes []aggregateTransformNode, candidates []aggregateTransformCandidate, candidateID string) AggregateTransformationCapabilities {
	result := AggregateTransformationCapabilities{
		Temporal:          TemporalReductionCapabilities{TimestampFields: []TemporalFieldChoice{}, AnchorFields: []TemporalFieldChoice{}},
		UnitNormalization: UnitNormalizationCapabilities{Presets: []UnitNormalizationPresetCapability{}},
	}
	selected, found := aggregateTransformCandidateByID(candidates, candidateID)
	if !found {
		result.Temporal.ReasonCode = "CANDIDATE_NOT_ADVERTISED"
		result.Temporal.Reason = "the aggregate candidate is not present in the resolved capability snapshot"
		result.UnitNormalization.ReasonCode = result.Temporal.ReasonCode
		result.UnitNormalization.Reason = result.Temporal.Reason
		return result
	}
	resourceByNode := make(map[string]string, len(nodes))
	rootNode := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		resourceByNode[node.id] = node.resourceType
		rootNode[node.id] = node.rowRootEligible
	}
	for _, candidate := range candidates {
		resourceType, ok := resourceByNode[candidate.nodeID]
		if !ok || !isAdvertisedDateTime(candidate, resourceType) {
			continue
		}
		choice := TemporalFieldChoice{
			CandidateID: candidate.id, NodeID: candidate.nodeID, ResourceType: resourceType,
			FieldPath: canonicalTransformPath(candidate.fieldPath), Label: candidate.label,
		}
		if candidate.nodeID == selected.nodeID {
			result.Temporal.TimestampFields = append(result.Temporal.TimestampFields, choice)
		}
		if rootNode[candidate.nodeID] {
			result.Temporal.AnchorFields = append(result.Temporal.AnchorFields, choice)
		}
	}
	sort.Slice(result.Temporal.TimestampFields, func(i, j int) bool {
		return result.Temporal.TimestampFields[i].FieldPath < result.Temporal.TimestampFields[j].FieldPath
	})
	sort.Slice(result.Temporal.AnchorFields, func(i, j int) bool {
		left, right := result.Temporal.AnchorFields[i], result.Temporal.AnchorFields[j]
		return left.ResourceType+"\x00"+left.FieldPath < right.ResourceType+"\x00"+right.FieldPath
	})
	switch {
	case len(result.Temporal.TimestampFields) == 0:
		result.Temporal.ReasonCode = "NO_TIMESTAMP_FIELDS"
		result.Temporal.Reason = "the candidate resource has no advertised scalar date_time fields"
	case len(result.Temporal.AnchorFields) == 0:
		result.Temporal.ReasonCode = "NO_ROOT_ANCHOR_FIELDS"
		result.Temporal.Reason = "no row-root resource has an advertised scalar date_time field"
	default:
		result.Temporal.Available = true
	}
	result.UnitNormalization = unitNormalizationCapabilities(selected)
	return result
}

// SupportsTimestamp reports whether the exact scalar timestamp is advertised.
func (c TemporalReductionCapabilities) SupportsTimestamp(resourceType, fieldPath string) bool {
	return temporalFieldAvailable(c.TimestampFields, resourceType, fieldPath)
}

// SupportsAnchor reports whether the exact root-row anchor is advertised.
func (c TemporalReductionCapabilities) SupportsAnchor(resourceType, fieldPath string) bool {
	return temporalFieldAvailable(c.AnchorFields, resourceType, fieldPath)
}

func temporalFieldAvailable(fields []TemporalFieldChoice, resourceType, fieldPath string) bool {
	resourceType = strings.TrimSpace(resourceType)
	fieldPath = canonicalTransformPath(fieldPath)
	for _, field := range fields {
		if field.ResourceType == resourceType && canonicalTransformPath(field.FieldPath) == fieldPath {
			return true
		}
	}
	return false
}

func isAdvertisedDateTime(candidate aggregateTransformCandidate, resourceType string) bool {
	if candidate.repeated || capability.IsRepeatedCardinality(candidate.cardinality) || !strings.EqualFold(strings.TrimSpace(candidate.logicalType), string(fhirschema.PrimitiveDateTime)) {
		return false
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, candidate.fieldPath)
	return ok && metadata.Primitive == fhirschema.PrimitiveDateTime && !metadata.Repeated
}

func unitNormalizationCapabilities(candidate aggregateTransformCandidate) UnitNormalizationCapabilities {
	result := UnitNormalizationCapabilities{Presets: []UnitNormalizationPresetCapability{}}
	_, _, shapeErr := fhirschema.QuantityIdentityPaths(candidate.resourceType, canonicalTransformPath(candidate.fieldPath))
	if shapeErr != nil || !numericLogicalType(candidate.logicalType) || candidate.repeated || capability.IsRepeatedCardinality(candidate.cardinality) {
		result.ReasonCode = "QUANTITY_VALUE_REQUIRED"
		result.Reason = "unit normalization requires a scalar numeric value with Quantity system and code fields"
	} else {
		sources, code, reason := observedUnitSources(candidate)
		if reason != "" {
			result.ReasonCode, result.Reason = code, reason
		} else {
			for _, policy := range unit.ApprovedUnitPolicies() {
				choice := UnitNormalizationPresetCapability{PolicyID: policy.PolicyID, Version: policy.Version, Target: policy.Target}
				if policySupportsAnySource(policy, sources) {
					choice.Available = true
				} else {
					choice.ReasonCode = "UNIT_PRESET_INCOMPATIBLE"
					choice.Reason = "the approved preset does not cover any observed source unit with a matching dimension"
				}
				result.Presets = append(result.Presets, choice)
				result.Available = result.Available || choice.Available
			}
			if !result.Available {
				result.ReasonCode = "NO_COMPATIBLE_UNIT_PRESET"
				result.Reason = "no approved unit preset covers the observed source units"
			}
		}
	}
	if result.ReasonCode != "" && len(result.Presets) == 0 {
		for _, policy := range unit.ApprovedUnitPolicies() {
			result.Presets = append(result.Presets, UnitNormalizationPresetCapability{
				PolicyID: policy.PolicyID, Version: policy.Version, Target: policy.Target,
				ReasonCode: result.ReasonCode, Reason: result.Reason,
			})
		}
	}
	return result
}

func observedUnitSources(candidate aggregateTransformCandidate) ([]unit.UnitIdentity, string, string) {
	sources := map[string]unit.UnitIdentity{}
	matched := false
	for _, concept := range candidate.concepts {
		if canonicalTransformPath(concept.valuePath) != canonicalTransformPath(candidate.fieldPath) {
			continue
		}
		matched = true
		for _, value := range concept.units {
			value = strings.TrimSpace(value)
			if value == "" || strings.Contains(value, ":") {
				continue
			}
			identity := unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: value}
			if _, ok := unit.UnitDimensionFor(identity); !ok {
				continue
			}
			sources[identity.Code] = identity
		}
	}
	if !matched || len(sources) == 0 {
		return nil, "SOURCE_UNITS_UNOBSERVED", "no approved source unit codes were observed for this Quantity value"
	}
	result := make([]unit.UnitIdentity, 0, len(sources))
	for _, source := range sources {
		result = append(result, source)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Code < result[j].Code })
	return result, "", ""
}

func policySupportsAnySource(policy unit.ApprovedUnitPolicy, sources []unit.UnitIdentity) bool {
	for _, source := range sources {
		if policy.SupportsSources([]unit.UnitIdentity{source}) {
			return true
		}
	}
	return false
}

func aggregateTransformCandidateByID(candidates []aggregateTransformCandidate, candidateID string) (aggregateTransformCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.id == candidateID {
			return candidate, true
		}
	}
	return aggregateTransformCandidate{}, false
}

func canonicalTransformPath(value string) string {
	return strings.Trim(strings.TrimPrefix(strings.TrimSpace(value), "root."), ".")
}

func numericLogicalType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "integer", "decimal", "number":
		return true
	default:
		return false
	}
}
