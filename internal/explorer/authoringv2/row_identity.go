package authoringv2

import "fmt"

// RowIdentityPlan is a derived description of the authoritative source and
// ordered construction. It carries no query expressions or editable state.
// Counts deliberately remain unknown until a receipt-bound execution supplies
// evidence; a preview sample cannot establish universal cardinality.
type RowIdentityPlan struct {
	OutputID         string                  `json:"outputId"`
	SourceDefinition RowDefinition           `json:"sourceDefinition"`
	Stages           []RowIdentityStage      `json:"stages"`
	Relationships    []RelationshipRowEffect `json:"relationships"`
}

type RowIdentity struct {
	Kind                  string   `json:"kind"`
	OccurrenceID          string   `json:"occurrenceId,omitempty"`
	ResourceType          string   `json:"resourceType,omitempty"`
	InputStageID          string   `json:"inputStageId,omitempty"`
	AnchorColumnID        string   `json:"anchorColumnId,omitempty"`
	RelatedRecordColumnID string   `json:"relatedRecordColumnId,omitempty"`
	ScopePath             string   `json:"scopePath,omitempty"`
	KeyColumnIDs          []string `json:"keyColumnIds,omitempty"`
	GroupRevisionID       string   `json:"groupRevisionId,omitempty"`
	MissingPolicy         string   `json:"missingPolicy,omitempty"`
	// Global uniqueness is never implied by expansion: identical related
	// resources reached by different input rows retain separate row pairs.
	Scope string `json:"scope"`
}

type RowCountEvidence struct {
	Kind  string `json:"kind"`
	Value *int64 `json:"value,omitempty"`
}

type RowIdentityStage struct {
	StageID           string           `json:"stageId"`
	OperationID       string           `json:"operationId,omitempty"`
	Identity          RowIdentity      `json:"identity"`
	Meaning           string           `json:"meaning"`
	Effect            string           `json:"effect"`
	MayExcludeRows    bool             `json:"mayExcludeRows"`
	Multiplicity      string           `json:"multiplicity"`
	Count             RowCountEvidence `json:"count"`
	UnsupportedReason string           `json:"unsupportedReason,omitempty"`
}

type RelationshipRowEffect struct {
	OccurrenceID      string         `json:"occurrenceId"`
	CatalogEdgeID     string         `json:"catalogEdgeId,omitempty"`
	MatchMode         RouteMatchMode `json:"matchMode"`
	PreservesIdentity bool           `json:"preservesIdentity"`
	MayExcludeRows    bool           `json:"mayExcludeRows"`
	Multiplicity      string         `json:"multiplicity"`
}

// DescribeRowIdentity must receive the effective document resolved from the
// pinned recipe and its overlay for imported sources. It never modifies Rows
// to pretend a construction stage changed the source row definition.
func DescribeRowIdentity(document Document) RowIdentityPlan {
	plan := RowIdentityPlan{OutputID: document.Output.ID, SourceDefinition: document.Rows,
		Stages: []RowIdentityStage{}, Relationships: []RelationshipRowEffect{}}
	var walk func(RouteNode, bool)
	walk = func(node RouteNode, requiredPrefix bool) {
		for _, child := range node.Children {
			plan.Relationships = append(plan.Relationships, RelationshipRowEffect{
				OccurrenceID: child.OccurrenceID, CatalogEdgeID: child.CatalogEdgeID,
				MatchMode: child.MatchMode.Normalized(), PreservesIdentity: true,
				MayExcludeRows: requiredPrefix && child.MatchMode.Normalized() == RouteMatchRequired, Multiplicity: "UNKNOWN",
			})
			walk(child, requiredPrefix && child.MatchMode.Normalized() == RouteMatchRequired)
		}
	}
	walk(document.Route, true)
	identity := RowIdentity{Kind: "RECORD", OccurrenceID: document.Route.OccurrenceID,
		ResourceType: document.RootResourceType, Scope: "SOURCE_RECORD"}
	meaning := fmt.Sprintf("One row per %s record.", document.RootResourceType)
	unsupported := ""
	deferredGroups := document.Rows.Kind == RowDefinitionGroups && document.Rows.Groups != nil && document.Rows.Groups.AfterStepID != ""
	if !deferredGroups {
		identity, meaning, unsupported = describeSourceRowIdentity(document)
	}
	sourceExcludes := false
	if !deferredGroups {
		if document.Rows.Expanded != nil {
			sourceExcludes = document.Rows.Expanded.EmptyCollectionPolicy == EmptyCollectionExclude
		}
		if document.Rows.Groups != nil && document.Rows.Groups.Source.Explicit != nil {
			sourceExcludes = document.Rows.Groups.Source.Explicit.UnassignedMemberPolicy == UnassignedMemberExclude
		}
	}
	source := identityStage("source_projection", "", identity, meaning, "SOURCE", sourceExcludes)
	source.UnsupportedReason = unsupported
	for _, relationship := range plan.Relationships {
		source.MayExcludeRows = source.MayExcludeRows || relationship.MayExcludeRows
	}
	plan.Stages = append(plan.Stages, source)
	if document.Construction == nil {
		return plan
	}
	identities := map[string]RowIdentity{"source_projection": identity}
	if document.Rows.Kind == RowDefinitionGroups && !deferredGroups {
		identities["group_rows"] = identity
	}
	for _, step := range document.Construction.Steps {
		inputID := "source_projection"
		if len(step.Inputs) == 1 && step.Inputs[0].Kind == ConstructionInputStepOutput {
			inputID = step.Inputs[0].StepID
		}
		input, found := identities[inputID]
		if len(step.Inputs) != 1 || (step.Inputs[0].Kind != ConstructionInputStepOutput && step.Inputs[0].Kind != ConstructionInputSourceProjection) {
			found = false
		}
		stage := identityStage(step.ID, step.ID, input, "The input row identity is retained.", "PRESERVE", false)
		if !found {
			stage.UnsupportedReason = "The input stage row identity is unavailable."
		}
		switch step.Operation.Kind {
		case ConstructionOperationDerive, ConstructionOperationRelatedSource, ConstructionOperationRelatedField:
		case ConstructionOperationFilter, ConstructionOperationRelatedEligibility:
			stage.Effect, stage.MayExcludeRows = "FILTER", true
			stage.Meaning = "The input row identity is retained; rows failing this stage's match or filter are excluded."
		case ConstructionOperationRelatedExpand:
			if related := step.Operation.RelatedExpand; related != nil {
				stage.Identity = RowIdentity{Kind: "RELATED_RECORD_PAIR", InputStageID: inputID,
					AnchorColumnID: related.AnchorColumnID, RelatedRecordColumnID: related.RelatedRecordColumnID,
					ResourceType: related.TargetResourceType, Scope: "PER_INPUT_ROW", MissingPolicy: string(related.EmptyPolicy)}
				stage.Effect = "EXPAND_RELATED"
				stage.MayExcludeRows = related.EmptyPolicy == ConstructionExpandEmptyExclude
				stage.Meaning = fmt.Sprintf("One row per input row and distinct related %s record; upstream values repeat for each pair.", related.TargetResourceType)
			}
		case ConstructionOperationExpand:
			if expand := step.Operation.Expand; expand != nil {
				stage.Identity = RowIdentity{Kind: "COLUMN_ITEM", InputStageID: inputID, ScopePath: expand.InputColumnID,
					Scope: "PER_INPUT_ROW", MissingPolicy: string(expand.EmptyPolicy)}
				stage.Effect, stage.MayExcludeRows = "EXPAND_ITEMS", expand.EmptyPolicy == ConstructionExpandEmptyExclude
				stage.Meaning = "One row per list item within each input row; upstream values repeat."
			}
		case ConstructionOperationGroup:
			if group := step.Operation.Group; group != nil {
				keys := make([]string, 0, len(group.Keys))
				for _, key := range group.Keys {
					keys = append(keys, key.OutputColumnID)
				}
				stage.Identity = RowIdentity{Kind: "KEY_TUPLE", KeyColumnIDs: keys, InputStageID: inputID,
					Scope: "INPUT_STAGE", MissingPolicy: string(group.MissingKeyPolicy.Normalized())}
				stage.Effect, stage.MayExcludeRows = "GROUP", group.MissingKeyPolicy.Normalized() == ConstructionGroupMissingKeyExclude
				stage.Meaning = "One row per complete Group key tuple; summaries and retained contributor values do not change that tuple."
			}
		default:
			// Do not substitute a root name for row identity we cannot describe.
			stage.Identity = RowIdentity{Kind: "OPERATION_DEFINED", InputStageID: inputID, Scope: "INPUT_STAGE"}
			stage.Effect = "OPERATION_DEFINED"
			stage.Meaning = "This operation defines its own row identity."
			stage.UnsupportedReason = fmt.Sprintf("Row identity inspection for %s is unavailable; inspect the operation contract.", step.Operation.Kind)
		}
		identities[step.ID] = stage.Identity
		plan.Stages = append(plan.Stages, stage)
		if deferredGroups && document.Rows.Groups.AfterStepID == step.ID {
			groupIdentity, groupMeaning, reason := describeSourceRowIdentity(document)
			groupIdentity.InputStageID = step.ID
			groupStage := identityStage("group_rows", "", groupIdentity, groupMeaning, "GROUP_NAMED", document.Rows.Groups.Source.Explicit != nil && document.Rows.Groups.Source.Explicit.UnassignedMemberPolicy == UnassignedMemberExclude)
			groupStage.UnsupportedReason = reason
			identities["group_rows"] = groupIdentity
			plan.Stages = append(plan.Stages, groupStage)
		}
	}
	return plan
}

func identityStage(stageID, operationID string, identity RowIdentity, meaning, effect string, exclude bool) RowIdentityStage {
	return RowIdentityStage{StageID: stageID, OperationID: operationID, Identity: identity,
		Meaning: meaning, Effect: effect, MayExcludeRows: exclude, Multiplicity: "UNKNOWN", Count: RowCountEvidence{Kind: "UNKNOWN"}}
}

func describeSourceRowIdentity(document Document) (RowIdentity, string, string) {
	identity := RowIdentity{Kind: "RECORD", OccurrenceID: document.Route.OccurrenceID,
		ResourceType: document.RootResourceType, Scope: "SOURCE_RECORD"}
	switch document.Rows.Kind {
	case RowDefinitionRecords:
		return identity, fmt.Sprintf("One row per %s record.", document.RootResourceType), ""
	case RowDefinitionExpanded:
		if expanded := document.Rows.Expanded; expanded != nil {
			return RowIdentity{Kind: "SOURCE_ITEM", OccurrenceID: expanded.OccurrenceID, ScopePath: expanded.ScopePath,
				Scope: "PER_SOURCE_RECORD", MissingPolicy: string(expanded.EmptyCollectionPolicy)}, "One row per repeated-field item within its source record.", ""
		}
	case RowDefinitionGroups:
		if groups := document.Rows.Groups; groups != nil {
			if groups.Source.Kind == GroupSourceExplicit && groups.Source.Explicit != nil {
				return RowIdentity{Kind: "NAMED_GROUP", GroupRevisionID: groups.Source.Explicit.RevisionID,
					Scope: "GROUP_REVISION", MissingPolicy: string(groups.Source.Explicit.UnassignedMemberPolicy)}, "One row per saved named group, including empty groups.", ""
			}
			return RowIdentity{Kind: "UNSUPPORTED", Scope: "SOURCE"}, "Source field-key grouping is unavailable.", "Rows.GROUPS/FIELD is unsupported; use a construction GROUP operation with explicit keys."
		}
	}
	return RowIdentity{Kind: "UNSUPPORTED", Scope: "SOURCE"}, "Source row identity is unavailable.", "The source row definition is invalid or unsupported."
}
