package compilation

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func constructionColumnSourceKey(origin facade.CompiledSourceOrigin, semanticPath string) string {
	if origin.OccurrenceID == "" || origin.ResourceType == "" {
		return ""
	}
	path := strings.TrimPrefix(strings.TrimSpace(semanticPath), "root.")
	path = strings.TrimPrefix(path, origin.ResourceType+".")
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return ""
	}
	return origin.OccurrenceID + "|" + origin.ResourceType + "|" + path
}

func ReceiptConstructionStageFromDescriptor(descriptor facade.CompiledStageDescriptor) (receiptmodel.ReceiptConstructionStage, error) {
	stage := receiptmodel.ReceiptConstructionStage{
		ID: descriptor.ID, InputStageID: descriptor.InputStageID, Operation: descriptor.Operation,
		RowIdentityColumn: descriptor.RowIdentityColumn,
		Columns:           make([]receiptmodel.ReceiptConstructionStageColumn, 0, len(descriptor.Columns)),
		Capabilities:      make([]receiptmodel.ReceiptConstructionOperationChoice, 0, len(descriptor.Capabilities)),
	}
	if sourceContext := descriptor.GroupFieldSourceContext; sourceContext != nil {
		context := &receiptmodel.ReceiptConstructionGroupFieldSourceContext{
			SourceStageID: sourceContext.SourceStageID, SourceShapeID: sourceContext.SourceShapeID,
			SourceIdentityColumn: sourceContext.SourceIdentityColumn,
			FieldOwners:          make([]receiptmodel.ReceiptConstructionGroupFieldOwner, 0, len(sourceContext.FieldOwners)),
		}
		for _, owner := range sourceContext.FieldOwners {
			mappedOwner := receiptmodel.ReceiptConstructionGroupFieldOwner{
				Role: owner.Role, OccurrenceID: owner.OccurrenceID, OwnerResourceType: owner.OwnerResourceType,
				ResourceType: owner.ResourceType, BoundarySelector: owner.BoundarySelector, StepIndex: owner.StepIndex,
			}
			context.FieldOwners = append(context.FieldOwners, mappedOwner)
		}
		if expansion := sourceContext.SourceExpansion; expansion != nil {
			mapped := &receiptmodel.ReceiptConstructionGroupFieldSourceExpansion{
				OwnerOccurrenceID: expansion.OwnerOccurrenceID, OwnerResourceType: expansion.OwnerResourceType,
				Selector: expansion.Selector, OrdinalSemantics: expansion.OrdinalSemantics,
				IdentitySemantics: expansion.IdentitySemantics, EmptyPolicy: expansion.EmptyPolicy,
				Route:     make([]receiptmodel.ReceiptConstructionGroupFieldSourceRouteStep, 0, len(expansion.Route)),
				Ancestors: make([]receiptmodel.ReceiptConstructionGroupFieldSourceAncestor, 0, len(expansion.Ancestors)),
			}
			for _, hop := range expansion.Route {
				mapped.Route = append(mapped.Route, receiptmodel.ReceiptConstructionGroupFieldSourceRouteStep{
					OccurrenceID: hop.OccurrenceID, FromType: hop.FromType, EdgeLabel: hop.EdgeLabel,
					ToType: hop.ToType, Direction: hop.Direction,
				})
			}
			for _, ancestor := range expansion.Ancestors {
				mapped.Ancestors = append(mapped.Ancestors, receiptmodel.ReceiptConstructionGroupFieldSourceAncestor{
					StepIndex: ancestor.StepIndex, BoundarySelector: ancestor.BoundarySelector, ResourceType: ancestor.ResourceType,
				})
			}
			context.SourceExpansion = mapped
		}
		stage.GroupFieldSourceContext = context
	}
	for _, anchor := range descriptor.RelatedExpandAnchors {
		stage.RelatedExpandAnchors = append(stage.RelatedExpandAnchors, receiptmodel.ReceiptConstructionRelatedExpandAnchor{
			AnchorColumnID: anchor.AnchorColumnID, Kind: anchor.Kind, NodeID: anchor.NodeID,
			ResourceType: anchor.ResourceType, Label: anchor.Label,
		})
	}
	for _, column := range descriptor.Columns {
		if column.Internal || column.Identity {
			continue
		}
		cardinality := expression.Cardinality(column.Cardinality)
		if !cardinality.Valid() {
			return receiptmodel.ReceiptConstructionStage{}, fmt.Errorf("construction stage %q column %q has unsupported cardinality %q", descriptor.ID, column.ID, column.Cardinality)
		}
		sourceKey := ""
		if column.SourceOrigin != nil {
			sourceKey = constructionColumnSourceKey(*column.SourceOrigin, column.SemanticPath)
		}
		stage.Columns = append(stage.Columns, receiptmodel.ReceiptConstructionStageColumn{
			ID: column.ID, Name: column.Name, Label: column.Label, Type: column.Kind, Cardinality: cardinality, SourceKey: sourceKey,
		})
	}
	for _, capability := range descriptor.Capabilities {
		reasonCode, reason := capability.ReasonCode, capability.Reason
		if capability.Supported {
			reasonCode, reason = "", ""
		}
		stage.Capabilities = append(stage.Capabilities, receiptmodel.ReceiptConstructionOperationChoice{
			Kind: string(capability.Operation), Supported: capability.Supported,
			ReasonCode: reasonCode, Reason: reason,
		})
	}
	if descriptor.RelatedExpand != nil {
		related := descriptor.RelatedExpand
		stage.RelatedExpand = &receiptmodel.ReceiptConstructionRelatedExpand{
			AnchorColumnID: related.AnchorColumnID, AnchorColumn: related.AnchorColumn,
			AnchorKind: related.AnchorKind, AnchorNodeID: related.AnchorNodeID,
			AnchorResourceType:     related.AnchorResourceType,
			RelatedRecordColumnID:  related.RelatedRecordColumnID,
			ParentIdentityColumnID: related.ParentIdentityColumnID, ParentIdentityColumn: related.ParentIdentityColumn,
			TerminalIdentityColumn: related.TerminalIdentityColumn,
			TargetNodeID:           related.TargetNodeID, TargetResourceType: related.TargetResourceType,
			Route: append([]recipe.ConstructionRelatedRouteStep(nil), related.Route...),
		}
	}
	if descriptor.ActiveRelatedRecord != nil {
		active := descriptor.ActiveRelatedRecord
		stage.ActiveRelatedRecord = &receiptmodel.ReceiptConstructionActiveRelatedRecord{
			TargetNodeID: active.TargetNodeID, TargetResourceType: active.TargetResourceType,
			TerminalIdentityColumn: active.TerminalIdentityColumn,
		}
	}
	return stage, nil
}
