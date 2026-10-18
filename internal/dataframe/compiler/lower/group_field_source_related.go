package lower

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// BindGroupRelatedOccurrenceSource resolves occurrence identities and the
// canonical source-shape digest for one related Group field source. Lifecycle
// choices pass graph route facts here; they do not synthesize occurrence IDs
// or source-shape digests themselves.
func BindGroupRelatedOccurrenceSource(
	context *CompiledGroupFieldSourceContext,
	source recipe.ConstructionGroupFieldSource,
) (recipe.ConstructionGroupFieldSource, error) {
	if context == nil || source.Kind != "BINDING" || source.ChoiceID != "" || context.SourceStageID == "" || context.SourceShapeID == "" {
		return recipe.ConstructionGroupFieldSource{}, fmt.Errorf("related Group binding requires the exact compiler source context and a resolved BINDING")
	}
	input, err := recipeGroupFieldSourceInput(source)
	if err != nil || input.Kind != "RELATED_OCCURRENCE" || input.RelatedOccurrence == nil {
		return recipe.ConstructionGroupFieldSource{}, fmt.Errorf("related Group binding requires a RELATED_OCCURRENCE input")
	}
	if source.ValueKind != "SCALAR" && source.ValueKind != "CODING_IDENTITY" {
		return recipe.ConstructionGroupFieldSource{}, fmt.Errorf("related Group binding has unsupported valueKind %q", source.ValueKind)
	}
	related, shapeID, err := ResolveGroupRelatedOccurrenceSource(context, source.ValueKind, *input.RelatedOccurrence)
	if err != nil {
		return recipe.ConstructionGroupFieldSource{}, err
	}
	if source.SourceStageID != "" && source.SourceStageID != context.SourceStageID {
		return recipe.ConstructionGroupFieldSource{}, fmt.Errorf("related Group binding names a different source stage")
	}
	if source.SourceShapeID != "" && source.SourceShapeID != shapeID {
		return recipe.ConstructionGroupFieldSource{}, fmt.Errorf("related Group binding source shape does not match the compiler-derived route shape")
	}
	source.SourceStageID, source.SourceShapeID = context.SourceStageID, shapeID
	source.Input = &recipe.ConstructionGroupFieldSourceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: &related}
	source.SourceRole, source.OccurrenceID, source.OwnerResourceType = "", "", ""
	source.ResourceType, source.CandidateID, source.CanonicalPath, source.BoundarySelector = "", "", "", ""
	return source, nil
}

// ResolveGroupRelatedOccurrenceSource is shared by capability issuance and
// durable binding validation. It returns route-prefix occurrence IDs, the
// route ID, and the selected-value source-shape digest from compiler-owned
// source context and exact graph route facts.
func ResolveGroupRelatedOccurrenceSource(
	context *CompiledGroupFieldSourceContext,
	valueKind string,
	source recipe.ConstructionGroupRelatedOccurrenceFieldSource,
) (recipe.ConstructionGroupRelatedOccurrenceFieldSource, string, error) {
	if context == nil || context.SourceStageID == "" || context.SourceShapeID == "" {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route requires an exact compiler source context")
	}
	source.Route = append([]recipe.ConstructionGroupRelatedRouteStep(nil), source.Route...)
	anchorOccurrence, anchorResource := context.RootOccurrenceID, context.RootResourceType
	if context.SourceExpansion != nil {
		anchorOccurrence, anchorResource = context.SourceExpansion.OwnerOccurrenceID, context.SourceExpansion.OwnerResourceType
	}
	if anchorOccurrence == "" || anchorResource == "" {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route anchor is missing its compiler-issued occurrence/resource identity")
	}
	if source.AnchorOccurrenceID == "" {
		source.AnchorOccurrenceID = anchorOccurrence
	}
	if source.AnchorOccurrenceID != anchorOccurrence || source.AnchorNodeID == "" || source.AnchorResourceType != anchorResource {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route anchor is not the exact current source occurrence")
	}
	if len(source.Route) == 0 {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route is empty")
	}
	fromOccurrence, fromNode, fromResource := source.AnchorOccurrenceID, source.AnchorNodeID, source.AnchorResourceType
	for index := range source.Route {
		hop := &source.Route[index]
		if hop.StepIndex != index || hop.FromNodeID != fromNode || hop.FromResourceType != fromResource ||
			!relatedGroupRouteStepComplete(*hop) {
			return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route step %d is incomplete or discontinuous", index)
		}
		if hop.FromOccurrenceID == "" {
			hop.FromOccurrenceID = fromOccurrence
		}
		if hop.FromOccurrenceID != fromOccurrence {
			return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route step %d has a different source occurrence", index)
		}
		wantOccurrence := relatedGroupOccurrenceID(context.SourceStageID, context.SourceShapeID, index, fromOccurrence, *hop)
		if hop.ToOccurrenceID != "" && hop.ToOccurrenceID != wantOccurrence {
			return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route step %d target occurrence does not match compiler derivation", index)
		}
		hop.ToOccurrenceID = wantOccurrence
		fromOccurrence, fromNode, fromResource = hop.ToOccurrenceID, hop.ToNodeID, hop.ToResourceType
	}
	if source.TargetOccurrenceID != "" && source.TargetOccurrenceID != fromOccurrence {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group target occurrence does not match compiler derivation")
	}
	if source.TargetNodeID != "" && source.TargetNodeID != fromNode {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group target node does not match terminal route node")
	}
	if source.TargetResourceType != "" && source.TargetResourceType != fromResource {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group target resource does not match terminal route resource")
	}
	source.TargetOccurrenceID, source.TargetNodeID, source.TargetResourceType = fromOccurrence, fromNode, fromResource
	wantValueResourceType, err := relatedGroupValueResourceType(valueKind, source)
	if err != nil {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", err
	}
	if source.ResourceType != "" && source.ResourceType != wantValueResourceType {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group field datatype %q does not match generated selected value type %q", source.ResourceType, wantValueResourceType)
	}
	source.ResourceType = wantValueResourceType
	if source.EmptyPolicy != recipe.ExpansionPreserveParent && source.EmptyPolicy != recipe.ExpansionExclude && source.EmptyPolicy != recipe.ExpansionError {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route has unsupported empty policy")
	}
	if source.Multiplicity != recipe.ConstructionGroupRelatedMultiplicityDistinctTargetPerInput {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group route has unsupported multiplicity")
	}
	routeID, err := relatedGroupRouteID(context, source)
	if err != nil {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", err
	}
	if source.RouteID != "" && source.RouteID != routeID {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", fmt.Errorf("related Group routeId does not match compiler derivation")
	}
	source.RouteID = routeID
	shapeID, err := relatedGroupFieldSourceShapeID(context, valueKind, source)
	if err != nil {
		return recipe.ConstructionGroupRelatedOccurrenceFieldSource{}, "", err
	}
	return source, shapeID, nil
}

func relatedGroupValueResourceType(
	valueKind string,
	source recipe.ConstructionGroupRelatedOccurrenceFieldSource,
) (string, error) {
	if valueKind == "CODING_IDENTITY" {
		return "Coding", nil
	}
	if source.CanonicalPath == "" {
		return "", fmt.Errorf("related Group scalar source has no selected canonical path")
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return "", fmt.Errorf("load generated FHIR schema: %w", err)
	}
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName(source.TargetResourceType), source.CanonicalPath)
	if err != nil || facts.CanonicalPath != source.CanonicalPath || facts.Shape != fhirschema.RowPathScalar || facts.FHIRType == "" {
		return "", fmt.Errorf("related Group path %q is not a generated scalar on %s", source.CanonicalPath, source.TargetResourceType)
	}
	return facts.FHIRType, nil
}

func recipeGroupFieldSourceInput(source recipe.ConstructionGroupFieldSource) (recipe.ConstructionGroupFieldSourceInput, error) {
	if source.Input != nil {
		return *source.Input, nil
	}
	return recipe.ConstructionGroupFieldSourceInput{}, fmt.Errorf("related Group binding must use the nested source union")
}

func relatedGroupRouteStepComplete(step recipe.ConstructionGroupRelatedRouteStep) bool {
	return step.FromNodeID != "" && step.FromResourceType != "" && step.EdgeID != "" && step.Relationship != "" &&
		(step.StorageDirection == "INBOUND" || step.StorageDirection == "OUTBOUND") &&
		(step.MatchMode == "OPTIONAL" || step.MatchMode == "REQUIRED") && step.ToNodeID != "" && step.ToResourceType != ""
}

func relatedGroupOccurrenceID(sourceStage, sourceShape string, index int, fromOccurrence string, step recipe.ConstructionGroupRelatedRouteStep) string {
	identity := struct {
		Version          int
		SourceStage      string
		SourceShape      string
		StepIndex        int
		FromOccurrenceID string
		FromNodeID       string
		FromResourceType string
		EdgeID           string
		Relationship     string
		StorageDirection string
		MatchMode        string
		ToNodeID         string
		ToResourceType   string
	}{1, sourceStage, sourceShape, index, fromOccurrence, step.FromNodeID, step.FromResourceType, step.EdgeID,
		step.Relationship, step.StorageDirection, step.MatchMode, step.ToNodeID, step.ToResourceType}
	encoded, _ := json.Marshal(identity)
	digest := sha256.Sum256(encoded)
	return "related-occurrence-v1:" + hex.EncodeToString(digest[:16])
}

func relatedGroupRouteID(context *CompiledGroupFieldSourceContext, related recipe.ConstructionGroupRelatedOccurrenceFieldSource) (string, error) {
	identity := struct {
		Version          int
		SourceStageID    string
		SourceShapeID    string
		AnchorOccurrence string
		Route            []recipe.ConstructionGroupRelatedRouteStep
		TargetOccurrence string
	}{1, context.SourceStageID, context.SourceShapeID, related.AnchorOccurrenceID, related.Route, related.TargetOccurrenceID}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "related-route-v1:" + hex.EncodeToString(digest[:]), nil
}

func relatedGroupFieldSourceShapeID(
	context *CompiledGroupFieldSourceContext,
	valueKind string,
	related recipe.ConstructionGroupRelatedOccurrenceFieldSource,
) (string, error) {
	canonical := struct {
		Version           int
		SourceStageID     string
		SourceShapeID     string
		ValueKind         string
		AnchorOccurrence  string
		AnchorNode        string
		AnchorResource    string
		Route             []recipe.ConstructionGroupRelatedRouteStep
		TargetOccurrence  string
		TargetNode        string
		TargetResource    string
		ResourceType      string
		CandidateID       string
		CanonicalPath     string
		RouteEmptyPolicy  recipe.ExpansionEmptyPolicy
		Multiplicity      string
		CodingSelector    string
		CodingEmptyPolicy recipe.ConstructionGroupCodingEmptyPolicy
		OrdinalSemantics  string
	}{
		Version: 1, SourceStageID: context.SourceStageID, SourceShapeID: context.SourceShapeID, ValueKind: valueKind,
		AnchorOccurrence: related.AnchorOccurrenceID, AnchorNode: related.AnchorNodeID, AnchorResource: related.AnchorResourceType,
		Route: related.Route, TargetOccurrence: related.TargetOccurrenceID, TargetNode: related.TargetNodeID,
		TargetResource: related.TargetResourceType, ResourceType: related.ResourceType, CandidateID: related.CandidateID,
		CanonicalPath: related.CanonicalPath, RouteEmptyPolicy: related.EmptyPolicy, Multiplicity: related.Multiplicity,
		CodingSelector: related.CodingSelector, CodingEmptyPolicy: related.CodingEmptyPolicy,
		OrdinalSemantics: related.OrdinalSemantics,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "group-related-source-shape-v1:" + hex.EncodeToString(digest[:]), nil
}
