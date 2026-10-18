package lower

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const (
	groupSourceOrdinalSemantics  = "ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY"
	groupSourceIdentitySemantics = "ROOT_KEY_ROUTE_EDGES_OWNER_KEY_ITEM_PRESENT_ORDINAL"
)

type canonicalGroupSourceShape struct {
	Version           int                                 `json:"version"`
	RootOccurrenceID  string                              `json:"rootOccurrenceId"`
	RootResourceType  string                              `json:"rootResourceType"`
	IdentitySemantics string                              `json:"identitySemantics"`
	Expansion         *canonicalGroupSourceExpansionShape `json:"expansion,omitempty"`
}

type canonicalGroupSourceExpansionShape struct {
	OwnerOccurrenceID string                              `json:"ownerOccurrenceId"`
	OwnerResourceType string                              `json:"ownerResourceType"`
	Selector          string                              `json:"selector"`
	ItemResourceType  string                              `json:"itemResourceType"`
	Route             []CompiledGroupFieldSourceRouteStep `json:"route"`
	Ancestors         []CompiledGroupFieldSourceAncestor  `json:"ancestors"`
	OrdinalSemantics  string                              `json:"ordinalSemantics"`
	EmptyPolicy       string                              `json:"emptyPolicy"`
}

func describeGroupFieldSourceContext(sourceStageID, rootResourceType, sourceIdentity string, plan *ir.PhysicalPlan) (*CompiledGroupFieldSourceContext, error) {
	if plan == nil || sourceStageID == "" || rootResourceType == "" {
		return nil, nil
	}
	var root *ir.PhysicalRootScan
	var unnests []ir.PhysicalUnnest
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if operation.RootScan == nil || root != nil {
				return nil, nil
			}
			root = operation.RootScan
		case ir.PhysicalUnnestOp:
			if operation.Unnest == nil {
				return nil, nil
			}
			unnests = append(unnests, *operation.Unnest)
		}
	}
	if root == nil || root.Variable == "" || len(unnests) > 1 {
		return nil, nil
	}
	if len(unnests) == 0 && sourceIdentity != "_key" || len(unnests) == 1 && sourceIdentity != "__loom_expansion_identity" {
		return nil, nil
	}
	if !groupFieldSourcePrefixIsRowPreserving(plan, root, len(unnests)) || !groupFieldSourcePopulationIsTyped(root.Population, root.Variable) {
		return nil, nil
	}
	rootOccurrenceID := plan.RootOccurrenceID
	if rootOccurrenceID == "" {
		rootOccurrenceID = "base"
	}
	context := &CompiledGroupFieldSourceContext{
		SourceStageID: sourceStageID, SourceIdentityColumn: sourceIdentity,
		FieldOwners: []CompiledGroupFieldOwner{{Role: "ROOT", OccurrenceID: rootOccurrenceID, OwnerResourceType: rootResourceType, ResourceType: rootResourceType}},
	}
	shape := canonicalGroupSourceShape{
		Version: 1, RootOccurrenceID: rootOccurrenceID, RootResourceType: rootResourceType,
		IdentitySemantics: "ROOT_KEY",
	}
	if len(unnests) == 1 {
		unnest := unnests[0]
		if unnest.Expression.Extract == nil || unnest.Expression.Extract.Selector.Filter != nil || len(unnest.Expression.Extract.Selector.Steps) == 0 {
			return nil, nil
		}
		selector := unnest.Expression.Extract.Selector.CanonicalPath()
		index, err := fhirschema.GeneratedIndex()
		if err != nil {
			return nil, err
		}
		itemFacts, err := index.ResolveRowPath(fhirschema.DefinitionName(unnest.Owner.ResourceType), selector)
		if err != nil || itemFacts.CanonicalPath != selector {
			return nil, nil
		}
		route := make([]CompiledGroupFieldSourceRouteStep, 0, len(unnest.Owner.Route))
		fromType := rootResourceType
		for _, step := range unnest.Owner.Route {
			toType, _ := plan.BindVars[step.Traversal.TargetTypeBindKey].(string)
			edgeLabel, _ := plan.BindVars[step.Traversal.EdgeLabelBindKey].(string)
			if toType == "" || edgeLabel == "" {
				return nil, nil
			}
			if boundFromType, ok := plan.BindVars[step.Traversal.SourceTypeBindKey].(string); ok && boundFromType != "" {
				fromType = boundFromType
			}
			route = append(route, CompiledGroupFieldSourceRouteStep{
				OccurrenceID: step.OccurrenceID, FromType: fromType, EdgeLabel: edgeLabel,
				ToType: toType, Direction: string(step.Traversal.Direction),
			})
			fromType = toType
		}
		ancestors := make([]CompiledGroupFieldSourceAncestor, 0, len(unnest.Ancestors))
		owners := append([]CompiledGroupFieldOwner(nil), context.FieldOwners...)
		if unnest.Owner.OccurrenceID != rootOccurrenceID || unnest.Owner.ResourceType != rootResourceType {
			owners = append(owners, CompiledGroupFieldOwner{Role: "OWNER", OccurrenceID: unnest.Owner.OccurrenceID, OwnerResourceType: unnest.Owner.ResourceType, ResourceType: unnest.Owner.ResourceType})
		}
		for _, ancestor := range unnest.Ancestors {
			if ancestor.StepIndex < 0 || ancestor.StepIndex >= len(unnest.Expression.Extract.Selector.Steps)-1 {
				return nil, nil
			}
			boundary := selectorPrefix(unnest.Expression.Extract.Selector, ancestor.StepIndex)
			facts, resolveErr := index.ResolveRowPath(fhirschema.DefinitionName(unnest.Owner.ResourceType), boundary)
			if resolveErr != nil || facts.CanonicalPath != boundary || facts.FHIRType == "" {
				return nil, nil
			}
			entry := CompiledGroupFieldSourceAncestor{StepIndex: ancestor.StepIndex, BoundarySelector: boundary, ResourceType: facts.FHIRType}
			ancestors = append(ancestors, entry)
			owners = append(owners, CompiledGroupFieldOwner{
				Role: "ANCESTOR", OccurrenceID: unnest.Owner.OccurrenceID, OwnerResourceType: unnest.Owner.ResourceType,
				ResourceType: facts.FHIRType, BoundarySelector: boundary, StepIndex: ancestor.StepIndex,
			})
		}
		owners = append(owners, CompiledGroupFieldOwner{
			Role: "CURRENT_ITEM", OccurrenceID: unnest.Owner.OccurrenceID, OwnerResourceType: unnest.Owner.ResourceType,
			ResourceType: itemFacts.FHIRType, BoundarySelector: selector,
		})
		context.FieldOwners = owners
		context.SourceExpansion = &CompiledGroupFieldSourceExpansion{
			OwnerOccurrenceID: unnest.Owner.OccurrenceID, OwnerResourceType: unnest.Owner.ResourceType,
			Selector: selector, Route: route, Ancestors: ancestors,
			OrdinalSemantics: groupSourceOrdinalSemantics, IdentitySemantics: groupSourceIdentitySemantics,
			EmptyPolicy: string(unnest.EmptyPolicy),
		}
		shape.IdentitySemantics = groupSourceIdentitySemantics
		shape.Expansion = &canonicalGroupSourceExpansionShape{
			OwnerOccurrenceID: unnest.Owner.OccurrenceID, OwnerResourceType: unnest.Owner.ResourceType,
			Selector: selector, ItemResourceType: itemFacts.FHIRType, Route: route, Ancestors: ancestors,
			OrdinalSemantics: groupSourceOrdinalSemantics, EmptyPolicy: string(unnest.EmptyPolicy),
		}
	}
	canonical, err := json.Marshal(shape)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	context.SourceShapeID = "group-source-shape-v1:" + hex.EncodeToString(digest[:])
	return context, nil
}

func groupFieldSourcePrefixIsRowPreserving(plan *ir.PhysicalPlan, root *ir.PhysicalRootScan, unnestCount int) bool {
	if plan == nil || root == nil || len(plan.Operations) < 2 {
		return false
	}
	rootCount, unnestSeen, returns := 0, 0, 0
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			rootCount++
			if index != 0 || operation.RootScan != root {
				return false
			}
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp:
		case ir.PhysicalSetOp:
			if !groupFieldSourceRowPreservingSet(operation, root.Variable) {
				return false
			}
		case ir.PhysicalUnnestOp:
			unnestSeen++
			if unnestSeen > 1 || operation.Unnest == nil || operation.Unnest.Owner.RootVariable != root.Variable {
				return false
			}
		case ir.PhysicalReturnOp:
			returns++
			if operation.Return == nil || index != len(plan.Operations)-1 {
				return false
			}
		default:
			// Sets, traversals, windows, table reshapes and prior row changing
			// stages cannot be silently treated as source occurrence context.
			return false
		}
	}
	return rootCount == 1 && unnestSeen == unnestCount && returns == 1
}

// Population roots are row-preserving because the typed root renderer groups
// the selected root key before loading the root document. The context checks
// only the source and identity structure; PhysicalPlan validation owns filter,
// resource-scope, and authorization validation.
func groupFieldSourcePopulationIsTyped(population *ir.PhysicalPopulationRootSource, rootVariable string) bool {
	if population == nil {
		return true
	}
	if rootVariable == "" || population.CollectMembersVariable != "" ||
		population.MemberScan.Variable == "" || population.MemberScan.CollectionBindKey == "" ||
		population.MemberID.Variable != population.MemberScan.Variable || !physicalPathEquals(population.MemberID.Path, "id") ||
		len(population.ResourceOperations) == 0 || population.ResourceOperations[0].Kind != ir.PhysicalCollectionScanOp ||
		population.ResourceOperations[0].CollectionScan == nil {
		return false
	}
	current := population.ResourceOperations[0].CollectionScan.Variable
	if current == "" || population.ResourceOperations[0].CollectionScan.CollectionBindKey == "" {
		return false
	}
	for _, operation := range population.ResourceOperations[1:] {
		switch operation.Kind {
		case ir.PhysicalFilterOp:
			if operation.Filter == nil {
				return false
			}
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet == nil {
				return false
			}
		case ir.PhysicalTraversalOp:
			if operation.Traversal == nil || operation.Traversal.SourceVariable != current || operation.Traversal.TargetVariable == "" {
				return false
			}
			current = operation.Traversal.TargetVariable
		default:
			return false
		}
	}
	return population.RootKey.Variable == current && physicalPathEquals(population.RootKey.Path, "_key")
}

// A PhysicalSet is an array-valued LET, so a validated direct related-field
// set preserves each outer row. Require its correlated traversal to remain
// anchored at the physical root; sets from other source shapes are not proof
// that root/active occurrence identity survived.
func groupFieldSourceRowPreservingSet(operation ir.PhysicalOperation, rootVariable string) bool {
	set := operation.Set
	if operation.Kind != ir.PhysicalSetOp || set == nil || set.Variable == "" || rootVariable == "" ||
		operation.Source.SemanticNode == "" || operation.Source.ResourceType == "" || operation.Source.Relationship == "" ||
		len(set.Subplan.Captures) != 1 || set.Subplan.Captures[0] != rootVariable || len(set.Subplan.Operations) == 0 {
		return false
	}
	first := set.Subplan.Operations[0]
	if first.Kind != ir.PhysicalTraversalOp || first.Traversal == nil || first.Traversal.SourceVariable != rootVariable {
		return false
	}
	for _, suboperation := range set.Subplan.Operations[1:] {
		switch suboperation.Kind {
		case ir.PhysicalFilterOp:
			if suboperation.Filter == nil {
				return false
			}
		case ir.PhysicalDerivedLetOp:
			if suboperation.DerivedLet == nil {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func physicalPathEquals(path []string, field string) bool {
	return len(path) == 1 && path[0] == field
}

func selectorPrefix(selector spec.Selector, lastStep int) string {
	parts := make([]string, 0, lastStep+1)
	for index, step := range selector.Steps {
		if index > lastStep {
			break
		}
		part := step.Field
		if step.Iterate {
			part += "[]"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ".")
}
