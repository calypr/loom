package dataframe

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/fhirschema"
)

// lowerGenericGraphQLBuilder produces a correct, conservative plan for any
// root and populated traversal represented in the generated FHIR graph schema.
//
// The generic plan uses the compiler-owned fhir_edge storage route. Most
// populated relationships are generated builder routes that reach a referring
// child with an INBOUND traversal. The explicit ResearchSubject --study-->
// ResearchStudy contract is the currently proven forward exception. Generated
// traversal metadata validates FHIR semantics but does not by itself define a
// safe physical AQL direction, so every other forward/ANY route remains
// rejected until its edge-layout evidence is added to storage_route.go.
func lowerGenericGraphQLBuilder(builder Builder, request logicalRequest) (Builder, error) {
	if !fhirschema.HasResource(request.Root.ResourceType) {
		return Builder{}, unsupportedLoweringError(fmt.Sprintf("root resource type %q is not represented by the active generated FHIR schema", request.Root.ResourceType))
	}
	requiredMatches, err := requiredTraversalMatches(request.Root)
	if err != nil {
		return Builder{}, unsupportedLoweringError(err.Error())
	}

	ctx := &loweringContext{
		request: request,
		builder: Builder{
			Project:                  request.Project,
			DatasetGeneration:        request.DatasetGeneration,
			AuthResourcePaths:        request.AuthResourcePaths,
			AuthScopeMode:            request.AuthScopeMode,
			RootResourceType:         request.Root.ResourceType,
			Fields:                   append([]FieldSelect(nil), request.Root.Fields...),
			Filters:                  append([]TypedFilter(nil), request.Root.Filters...),
			Pivots:                   append([]PivotSelect(nil), request.Root.Pivots...),
			Aggregates:               append([]AggregateSelect(nil), request.Root.Aggregates...),
			Slices:                   append([]RepresentativeSlice(nil), request.Root.Slices...),
			Sets:                     []NamedSet{},
			DerivedFields:            []DerivedField{},
			RequiredTraversalMatches: requiredMatches,
		},
		setsByName:              map[string]struct{}{},
		modes:                   map[string]string{},
		genericSetsBySignature:  map[string]string{},
		genericFilterSetsBySig:  map[string]string{},
		genericAliasesBySetName: map[string]string{},
	}

	if err := ctx.lowerGenericChildren(request.Root, ""); err != nil {
		return Builder{}, err
	}
	appliedRules := make([]string, 0, 3)
	if requestHasTypedFilters(request.Root) {
		appliedRules = append(appliedRules, OptimizerRuleFilterPushdown)
	}
	if ctx.genericTraversalShareCount > 0 {
		appliedRules = append(appliedRules, OptimizerRuleTraversalSharing)
	}
	if len(requiredMatches) > 0 {
		appliedRules = append(appliedRules, OptimizerRuleRelationshipSemiJoin)
	}
	if len(appliedRules) == 0 {
		appliedRules = nil
	}
	ctx.builder.PlanHint = &PlanHint{
		Mode:          "lowered",
		Profile:       "generic_fhir_graph",
		NamedSetCount: len(ctx.builder.Sets),
		AppliedRules:  appliedRules,
	}
	return ctx.builder, nil
}

func (ctx *loweringContext) lowerGenericChildren(parent logicalNode, parentSet string) error {
	children, groups, groupOrder, err := ctx.prepareGenericChildren(parent, parentSet)
	if err != nil {
		return err
	}

	// Materialize every shareable prefix before lowering the individual child
	// selections. The shared set intentionally has no target-type predicate:
	// its per-resource-type subsets are built below. This is the same physical
	// shape as the Patient optimizer's root_patient_neighbor_set, but it is
	// now available at every generic parent node.
	for _, key := range groupOrder {
		group := groups[key]
		if !group.canSharePrefix() {
			continue
		}
		baseName := ctx.nextGenericSharedTraversalSetName(parentSet, group.label)
		group.baseSet = ctx.ensureSet(NamedSet{
			Name:      baseName,
			Kind:      SetKindTraverse,
			Source:    parentSet,
			Direction: group.route.namedSetDirection(),
			Label:     group.label,
			// ToResourceType remains a generated-route validation anchor. The
			// physical traversal deliberately includes every target type with
			// this label; each child gets a typed filter subset below.
			ToResourceType: children[group.indices[0]].node.ResourceType,
			AllTargetTypes: true,
			Unique:         true,
		}, "node")
		ctx.genericTraversalShareCount += len(group.indices) - 1
	}

	// Preserve request order for derived fields and nested traversals. Prefix
	// discovery may be grouped, but user-visible output ordering must not be.
	for _, child := range children {
		group := groups[child.groupKey]
		setName := child.setName
		if group.baseSet != "" {
			var reused bool
			setName, reused, err = ctx.ensureGenericFilteredSubset(group.baseSet, child.node)
			if err != nil {
				return err
			}
			if reused {
				ctx.genericTraversalShareCount++
			}
		} else {
			setName, err = ctx.ensureGenericTraversalSet(parentSet, child.node, child.route, setName)
			if err != nil {
				return err
			}
		}

		ctx.lowerNodeSelections(child.node, setName)
		if err := ctx.lowerGenericChildren(child.node, setName); err != nil {
			return err
		}
	}
	return nil
}

type genericTraversalChild struct {
	node     logicalNode
	route    storageRoute
	setName  string
	groupKey string
}

type genericTraversalGroup struct {
	label         string
	route         storageRoute
	indices       []int
	resourceTypes map[string]struct{}
	baseSet       string
}

func (group genericTraversalGroup) canSharePrefix() bool {
	return len(group.indices) > 1 && len(group.resourceTypes) > 1
}

func (ctx *loweringContext) prepareGenericChildren(parent logicalNode, parentSet string) ([]genericTraversalChild, map[string]*genericTraversalGroup, []string, error) {
	children := make([]genericTraversalChild, 0, len(parent.Children))
	groups := make(map[string]*genericTraversalGroup, len(parent.Children))
	groupOrder := make([]string, 0, len(parent.Children))
	for _, child := range parent.Children {
		setName, err := ctx.genericSetNameForAlias(child.Alias)
		if err != nil {
			return nil, nil, nil, err
		}
		route, err := resolveStorageRoute(parent.ResourceType, child.Label, child.ResourceType)
		if err != nil {
			return nil, nil, nil, unsupportedLoweringError(err.Error())
		}
		key := genericSiblingTraversalKey(parentSet, child.Label, route)
		group, exists := groups[key]
		if !exists {
			group = &genericTraversalGroup{
				label:         child.Label,
				route:         route,
				resourceTypes: map[string]struct{}{},
			}
			groups[key] = group
			groupOrder = append(groupOrder, key)
		}
		group.indices = append(group.indices, len(children))
		group.resourceTypes[child.ResourceType] = struct{}{}
		children = append(children, genericTraversalChild{
			node:     child,
			route:    route,
			setName:  setName,
			groupKey: key,
		})
	}
	return children, groups, groupOrder, nil
}

func (ctx *loweringContext) genericSetNameForAlias(alias string) (string, error) {
	if strings.TrimSpace(alias) == "" {
		return "", unsupportedLoweringError("generic lowering requires a traversal alias")
	}
	setName := "generic_" + sanitizeColumnName(alias) + "_set"
	if priorAlias, exists := ctx.genericAliasesBySetName[setName]; exists && priorAlias != alias {
		return "", unsupportedLoweringError(fmt.Sprintf("traversal aliases collide after identifier normalization: %q", alias))
	}
	if _, exists := ctx.setsByName[setName]; exists {
		return "", unsupportedLoweringError(fmt.Sprintf("traversal alias %q collides with an existing generic set after identifier normalization", alias))
	}
	ctx.genericAliasesBySetName[setName] = alias
	return setName, nil
}

func (ctx *loweringContext) nextGenericSharedTraversalSetName(parentSet, label string) string {
	parentName := "root"
	if strings.TrimSpace(parentSet) != "" {
		parentName = sanitizeColumnName(parentSet)
	}
	base := "generic_" + parentName + "_" + sanitizeColumnName(label) + "_neighbors_set"
	name := base
	for suffix := 2; ; suffix++ {
		_, usedBySet := ctx.setsByName[name]
		_, usedByAlias := ctx.genericAliasesBySetName[name]
		if !usedBySet && !usedByAlias {
			return name
		}
		name = fmt.Sprintf("%s_%d", base, suffix)
	}
}

func (ctx *loweringContext) ensureGenericTraversalSet(parentSet string, child logicalNode, route storageRoute, setName string) (string, error) {
	signature, err := genericTraversalSignature(parentSet, child, route)
	if err != nil {
		return "", err
	}
	if sharedSet, exists := ctx.genericSetsBySignature[signature]; exists {
		ctx.genericTraversalShareCount++
		return sharedSet, nil
	}
	ctx.ensureSet(NamedSet{
		Name:           setName,
		Kind:           SetKindTraverse,
		Source:         parentSet,
		Direction:      route.namedSetDirection(),
		Label:          child.Label,
		ToResourceType: child.ResourceType,
		Filters:        append([]TypedFilter(nil), child.Filters...),
		Unique:         true,
		SortField:      genericTraversalRequiresStableOrder(child),
	}, "node")
	ctx.genericSetsBySignature[signature] = setName
	return setName, nil
}

func (ctx *loweringContext) ensureGenericFilteredSubset(source string, child logicalNode) (string, bool, error) {
	signature, err := genericFilteredSubsetSignature(source, child)
	if err != nil {
		return "", false, err
	}
	if sharedSet, exists := ctx.genericFilterSetsBySig[signature]; exists {
		return sharedSet, true, nil
	}
	setName, err := ctx.genericSetNameForAlias(child.Alias)
	if err != nil {
		return "", false, err
	}
	// The shared base is deduplicated before this subset is evaluated. Sort the
	// subset after it is typed and filtered so FIRST/ALL projections are stable
	// and aliases observing the same subset see exactly the same order.
	ctx.ensureSet(NamedSet{
		Name:              setName,
		Kind:              SetKindFilter,
		Source:            source,
		MatchResourceType: child.ResourceType,
		Filters:           append([]TypedFilter(nil), child.Filters...),
		// The all-target base is already UNIQUE. Repeating it here would add a
		// second hash-deduplication pass without changing the typed subset.
		Unique:    false,
		SortField: "_key",
	}, "node")
	ctx.genericFilterSetsBySig[signature] = setName
	return setName, false, nil
}

func genericTraversalRequiresStableOrder(node logicalNode) string {
	for _, field := range node.Fields {
		switch normalizeValueMode(field.ValueMode) {
		case "FIRST", "ALL", "AUTO":
			return "_key"
		}
	}
	if len(node.Slices) > 0 {
		return "_key"
	}
	return ""
}

func genericTraversalSignature(parentSet string, child logicalNode, route storageRoute) (string, error) {
	filters, err := json.Marshal(child.Filters)
	if err != nil {
		return "", fmt.Errorf("serialize traversal filters for %s -> %s (%s): %w", child.ResourceType, child.Alias, child.Label, err)
	}
	return strings.Join([]string{parentSet, child.Label, child.ResourceType, route.namedSetDirection(), genericTraversalRequiresStableOrder(child), string(filters)}, "\x00"), nil
}

func genericSiblingTraversalKey(parentSet, label string, route storageRoute) string {
	return strings.Join([]string{parentSet, label, route.namedSetDirection()}, "\x00")
}

func genericFilteredSubsetSignature(source string, child logicalNode) (string, error) {
	filters, err := json.Marshal(child.Filters)
	if err != nil {
		return "", fmt.Errorf("serialize shared traversal filters for %s -> %s: %w", child.ResourceType, child.Alias, err)
	}
	// Shared generic subsets always sort by _key. This makes all selection
	// modes deterministic and lets aliases with identical type/filter semantics
	// reuse the same materialized subset.
	return strings.Join([]string{source, child.ResourceType, "_key", string(filters)}, "\x00"), nil
}
