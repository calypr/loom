package catalog

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unsafe"
)

// AvailabilityFeature identifies one populated field or semantic contribution.
type AvailabilityFeature struct {
	Kind         string // FIELD or SEMANTIC
	ResourceType string
	FieldPath    string
	ConceptID    string
	BindingID    string
}

// AvailabilityRelation is one compiler-allowed navigation step.
type AvailabilityRelation struct {
	FromResourceType string
	ToResourceType   string
	Relationship     string
	StorageDirection string // OUTBOUND or INBOUND
}

// RelationshipTraversalCandidates exposes both directions of one stored FHIR
// reference. The compiler decides which candidates are executable.
func RelationshipTraversalCandidates(observation RelationshipObservation) [2]AvailabilityRelation {
	return [2]AvailabilityRelation{
		{FromResourceType: observation.StorageFromType, ToResourceType: observation.StorageToType, Relationship: observation.Label, StorageDirection: "OUTBOUND"},
		{FromResourceType: observation.StorageToType, ToResourceType: observation.StorageFromType, Relationship: observation.Label, StorageDirection: "INBOUND"},
	}
}

// AvailabilityVertex is a retained resource record.
type AvailabilityVertex struct {
	ID               string
	ResourceType     string
	AuthResourcePath string
}

// AvailabilityGraphBuilder collects graph records before compacting them into
// immutable CSR arrays.
type AvailabilityGraphBuilder struct {
	vertices      []builderVertex
	vertexByID    map[string]uint32
	edges         []builderEdge
	features      []featureIDs
	featureByKey  map[featureIDs]uint32
	associations  []builderAssociation
	stringValues  []string
	stringByValue map[string]uint32
	finished      bool
}

type builderVertex struct {
	id, resourceType, authPath uint32
}

type builderEdge struct {
	from, to, relationship, authPath uint32
}

type featureIDs struct {
	kind, resourceType, fieldPath, conceptID, bindingID uint32
}

type builderAssociation struct {
	vertex, feature uint32
}

type graphVertex struct {
	id, resourceType, authPath uint32
	featureStart, featureEnd   uint32
}

type graphRelation struct {
	fromType, toType, relationship, direction uint32
}

type graphArc struct {
	target, relation, authPath uint32
}

type physicalEdge struct {
	from, to, relationship, authPath uint32
}

type buildArc struct {
	from, target, relation, authPath uint32
}

// AvailabilityQuery defines roots, authorization scope, and executable route
// relations for a complete reachability search.
type AvailabilityQuery struct {
	RootResourceType  string
	AllRoots          bool
	RootIDs           []string // complete explicit root set; empty means none when AllRoots=false
	Unrestricted      bool
	AuthResourcePaths []string               // restricted-empty means no vertices/edges
	Relations         []AvailabilityRelation // empty allows none
	MaxHops           int                    // zero unlimited
}

// AvailabilityWitness proves that a root can reach a populated source.
type AvailabilityWitness struct {
	Feature  AvailabilityFeature
	RootID   string
	SourceID string
	Route    []AvailabilityRelation
}

// AvailabilityGraphStats reports counts and an estimate of retained storage.
// Bytes includes graph array and interned-string backing, but is not RSS.
type AvailabilityGraphStats struct {
	Vertices     int
	Edges        int
	Features     int
	Associations int
	Bytes        uint64
}

// AvailabilityGraph contains immutable compact graph data.
type AvailabilityGraph struct {
	strings               []string
	vertices              []graphVertex
	edgeOffsets           []uint32
	arcs                  []graphArc
	relations             []graphRelation
	features              []featureIDs
	associationFeatureIDs []uint32
	stats                 AvailabilityGraphStats
}

const maxVertexOrdinal = ^uint32(0)

// NewAvailabilityGraphBuilder creates a builder with optional capacity hints.
func NewAvailabilityGraphBuilder(vertices, edges int) *AvailabilityGraphBuilder {
	if vertices < 0 {
		vertices = 0
	}
	if edges < 0 {
		edges = 0
	}
	return &AvailabilityGraphBuilder{
		vertices:      make([]builderVertex, 0, vertices),
		vertexByID:    make(map[string]uint32, vertices),
		edges:         make([]builderEdge, 0, edges),
		features:      make([]featureIDs, 0),
		featureByKey:  make(map[featureIDs]uint32),
		associations:  make([]builderAssociation, 0),
		stringValues:  make([]string, 0),
		stringByValue: make(map[string]uint32),
	}
}

// HasVertex reports whether the builder has already loaded a vertex ID.
// Loaders can use it to discard dangling physical references before AddEdge.
func (b *AvailabilityGraphBuilder) HasVertex(id string) bool {
	if b == nil || b.finished {
		return false
	}
	_, ok := b.vertexByID[id]
	return ok
}

func (b *AvailabilityGraphBuilder) initialize() {
	if b.vertexByID == nil {
		b.vertexByID = make(map[string]uint32)
	}
	if b.featureByKey == nil {
		b.featureByKey = make(map[featureIDs]uint32)
	}
	if b.stringByValue == nil {
		b.stringByValue = make(map[string]uint32)
	}
}

func (b *AvailabilityGraphBuilder) intern(value string) (uint32, error) {
	if id, ok := b.stringByValue[value]; ok {
		return id, nil
	}
	if uint64(len(b.stringValues)) >= uint64(maxVertexOrdinal) {
		return 0, errors.New("availability graph has too many interned strings")
	}
	id := uint32(len(b.stringValues))
	value = strings.Clone(value)
	b.stringValues = append(b.stringValues, value)
	b.stringByValue[value] = id
	return id, nil
}

func (b *AvailabilityGraphBuilder) internFeature(feature AvailabilityFeature) (uint32, error) {
	kind, err := b.intern(feature.Kind)
	if err != nil {
		return 0, err
	}
	resourceType, err := b.intern(feature.ResourceType)
	if err != nil {
		return 0, err
	}
	fieldPath, err := b.intern(feature.FieldPath)
	if err != nil {
		return 0, err
	}
	conceptID, err := b.intern(feature.ConceptID)
	if err != nil {
		return 0, err
	}
	bindingID, err := b.intern(feature.BindingID)
	if err != nil {
		return 0, err
	}
	key := featureIDs{kind: kind, resourceType: resourceType, fieldPath: fieldPath, conceptID: conceptID, bindingID: bindingID}
	if id, ok := b.featureByKey[key]; ok {
		return id, nil
	}
	if uint64(len(b.features)) >= uint64(maxVertexOrdinal) {
		return 0, errors.New("availability graph has too many features")
	}
	id := uint32(len(b.features))
	b.features = append(b.features, key)
	b.featureByKey[key] = id
	return id, nil
}

// AddVertex adds a resource and its actually-present scalar field paths.
func (b *AvailabilityGraphBuilder) AddVertex(v AvailabilityVertex, fields []string) error {
	if b == nil {
		return errors.New("availability graph builder is nil")
	}
	if b.finished {
		return errors.New("availability graph builder is already finished")
	}
	b.initialize()
	if _, exists := b.vertexByID[v.ID]; exists {
		return fmt.Errorf("duplicate availability vertex ID %q", v.ID)
	}
	id, err := b.intern(v.ID)
	if err != nil {
		return err
	}
	resourceType, err := b.intern(v.ResourceType)
	if err != nil {
		return err
	}
	authPath, err := b.intern(v.AuthResourcePath)
	if err != nil {
		return err
	}
	if uint64(len(b.vertices)) >= uint64(maxVertexOrdinal) {
		return errors.New("availability graph has too many vertices")
	}
	ordinal := uint32(len(b.vertices))
	b.vertices = append(b.vertices, builderVertex{id: id, resourceType: resourceType, authPath: authPath})
	b.vertexByID[b.stringValues[id]] = ordinal
	for _, fieldPath := range fields {
		featureID, internErr := b.internFeature(AvailabilityFeature{
			Kind:         "FIELD",
			ResourceType: v.ResourceType,
			FieldPath:    fieldPath,
		})
		if internErr != nil {
			return internErr
		}
		b.associations = append(b.associations, builderAssociation{vertex: ordinal, feature: featureID})
	}
	return nil
}

// AddFeature adds an already-recognized feature contribution to a vertex.
func (b *AvailabilityGraphBuilder) AddFeature(vertexID string, feature AvailabilityFeature) error {
	if b == nil {
		return errors.New("availability graph builder is nil")
	}
	if b.finished {
		return errors.New("availability graph builder is already finished")
	}
	b.initialize()
	ordinal, ok := b.vertexByID[vertexID]
	if !ok {
		return fmt.Errorf("availability feature references unknown vertex %q", vertexID)
	}
	if feature.ResourceType != b.stringValues[b.vertices[ordinal].resourceType] {
		return fmt.Errorf("availability feature resource type %q does not match vertex %q resource type %q", feature.ResourceType, vertexID, b.stringValues[b.vertices[ordinal].resourceType])
	}
	featureID, err := b.internFeature(feature)
	if err != nil {
		return err
	}
	b.associations = append(b.associations, builderAssociation{vertex: ordinal, feature: featureID})
	return nil
}

// AddEdge adds one physical edge. Finish creates its outbound and inbound
// navigation possibilities; unresolved endpoints are reported by Finish.
func (b *AvailabilityGraphBuilder) AddEdge(fromID, toID, relationship, authResourcePath string) error {
	if b == nil {
		return errors.New("availability graph builder is nil")
	}
	if b.finished {
		return errors.New("availability graph builder is already finished")
	}
	b.initialize()
	from, err := b.intern(fromID)
	if err != nil {
		return err
	}
	to, err := b.intern(toID)
	if err != nil {
		return err
	}
	label, err := b.intern(relationship)
	if err != nil {
		return err
	}
	authPath, err := b.intern(authResourcePath)
	if err != nil {
		return err
	}
	b.edges = append(b.edges, builderEdge{from: from, to: to, relationship: label, authPath: authPath})
	return nil
}

// Finish validates endpoints and freezes the data into compact adjacency and
// feature-association arrays. A failed or canceled build can be retried.
func (b *AvailabilityGraphBuilder) Finish(ctx context.Context) (*AvailabilityGraph, error) {
	if b == nil {
		return nil, errors.New("availability graph builder is nil")
	}
	if b.finished {
		return nil, errors.New("availability graph builder is already finished")
	}
	if ctx == nil {
		return nil, errors.New("availability graph finish context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.initialize()
	if uint64(len(b.vertices)) >= uint64(maxVertexOrdinal) {
		return nil, errors.New("availability graph has too many vertices")
	}
	if uint64(len(b.features)) >= uint64(maxVertexOrdinal) {
		return nil, errors.New("availability graph has too many features")
	}

	vertexOrder := make([]uint32, len(b.vertices))
	for i := range vertexOrder {
		vertexOrder[i] = uint32(i)
	}
	sort.Slice(vertexOrder, func(i, j int) bool {
		return b.stringValues[b.vertices[vertexOrder[i]].id] < b.stringValues[b.vertices[vertexOrder[j]].id]
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	oldToNew := make([]uint32, len(b.vertices))
	graph := &AvailabilityGraph{
		vertices:    make([]graphVertex, len(b.vertices)),
		edgeOffsets: make([]uint32, len(b.vertices)+1),
	}
	for newOrdinal, oldOrdinal := range vertexOrder {
		if newOrdinal&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		oldToNew[oldOrdinal] = uint32(newOrdinal)
		v := b.vertices[oldOrdinal]
		graph.vertices[newOrdinal] = graphVertex{id: v.id, resourceType: v.resourceType, authPath: v.authPath}
	}

	physicalEdges := make([]physicalEdge, 0, len(b.edges))
	for i, edge := range b.edges {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		fromID := b.stringValues[edge.from]
		toID := b.stringValues[edge.to]
		fromOld, fromOK := b.vertexByID[fromID]
		toOld, toOK := b.vertexByID[toID]
		if !fromOK || !toOK {
			if !fromOK {
				return nil, fmt.Errorf("availability edge has unresolved from endpoint %q", fromID)
			}
			return nil, fmt.Errorf("availability edge has unresolved to endpoint %q", toID)
		}
		physicalEdges = append(physicalEdges, physicalEdge{
			from: oldToNew[fromOld], to: oldToNew[toOld],
			relationship: edge.relationship, authPath: edge.authPath,
		})
	}
	sort.Slice(physicalEdges, func(i, j int) bool {
		a, c := physicalEdges[i], physicalEdges[j]
		if a.from != c.from {
			return a.from < c.from
		}
		if a.to != c.to {
			return a.to < c.to
		}
		if left, right := b.stringValues[a.relationship], b.stringValues[c.relationship]; left != right {
			return left < right
		}
		return b.stringValues[a.authPath] < b.stringValues[c.authPath]
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uniquePhysical := physicalEdges[:0]
	for i, edge := range physicalEdges {
		if i == 0 || edge != physicalEdges[i-1] {
			uniquePhysical = append(uniquePhysical, edge)
		}
	}
	physicalEdges = uniquePhysical
	if uint64(len(physicalEdges))*2 > uint64(maxVertexOrdinal) {
		return nil, errors.New("availability graph has too many navigation edges")
	}

	outbound, err := b.intern("OUTBOUND")
	if err != nil {
		return nil, err
	}
	inbound, err := b.intern("INBOUND")
	if err != nil {
		return nil, err
	}
	relationIndex := make(map[graphRelation]uint32)
	relations := make([]graphRelation, 0)
	getRelation := func(key graphRelation) uint32 {
		if id, ok := relationIndex[key]; ok {
			return id
		}
		id := uint32(len(relations))
		relations = append(relations, key)
		relationIndex[key] = id
		return id
	}
	buildArcs := make([]buildArc, 0, len(physicalEdges)*2)
	for i, edge := range physicalEdges {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		fromType := graph.vertices[edge.from].resourceType
		toType := graph.vertices[edge.to].resourceType
		outRelation := getRelation(graphRelation{fromType: fromType, toType: toType, relationship: edge.relationship, direction: outbound})
		inRelation := getRelation(graphRelation{fromType: toType, toType: fromType, relationship: edge.relationship, direction: inbound})
		buildArcs = append(buildArcs,
			buildArc{from: edge.from, target: edge.to, relation: outRelation, authPath: edge.authPath},
			buildArc{from: edge.to, target: edge.from, relation: inRelation, authPath: edge.authPath},
		)
	}

	relationOrder := append([]graphRelation(nil), relations...)
	sort.Slice(relationOrder, func(i, j int) bool { return relationLess(relationOrder[i], relationOrder[j], b.stringValues) })
	newRelationIndex := make(map[graphRelation]uint32, len(relationOrder))
	for i, relation := range relationOrder {
		newRelationIndex[relation] = uint32(i)
	}
	for i := range buildArcs {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		buildArcs[i].relation = newRelationIndex[relations[buildArcs[i].relation]]
	}
	sort.Slice(buildArcs, func(i, j int) bool {
		a, c := buildArcs[i], buildArcs[j]
		if a.from != c.from {
			return a.from < c.from
		}
		if a.target != c.target {
			return a.target < c.target
		}
		if a.relation != c.relation {
			return a.relation < c.relation
		}
		return b.stringValues[a.authPath] < b.stringValues[c.authPath]
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uniqueArcs := buildArcs[:0]
	for i, arc := range buildArcs {
		if i == 0 || arc != buildArcs[i-1] {
			uniqueArcs = append(uniqueArcs, arc)
		}
	}
	buildArcs = uniqueArcs
	if uint64(len(buildArcs)) > uint64(maxVertexOrdinal) {
		return nil, errors.New("availability graph has too many navigation edges")
	}
	for _, arc := range buildArcs {
		graph.edgeOffsets[arc.from+1]++
	}
	for i := 1; i < len(graph.edgeOffsets); i++ {
		graph.edgeOffsets[i] += graph.edgeOffsets[i-1]
	}
	graph.arcs = make([]graphArc, len(buildArcs))
	for i, arc := range buildArcs {
		graph.arcs[i] = graphArc{target: arc.target, relation: arc.relation, authPath: arc.authPath}
	}
	graph.relations = relationOrder

	featureOrder := make([]uint32, len(b.features))
	for i := range featureOrder {
		featureOrder[i] = uint32(i)
	}
	sort.Slice(featureOrder, func(i, j int) bool {
		return featureLess(b.features[featureOrder[i]], b.features[featureOrder[j]], b.stringValues)
	})
	featureRemap := make([]uint32, len(b.features))
	graph.features = make([]featureIDs, len(b.features))
	for newID, oldID := range featureOrder {
		featureRemap[oldID] = uint32(newID)
		graph.features[newID] = b.features[oldID]
	}
	associations := make([]builderAssociation, len(b.associations))
	for i, pair := range b.associations {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		associations[i] = builderAssociation{vertex: oldToNew[pair.vertex], feature: featureRemap[pair.feature]}
	}
	sort.Slice(associations, func(i, j int) bool {
		if associations[i].vertex != associations[j].vertex {
			return associations[i].vertex < associations[j].vertex
		}
		return associations[i].feature < associations[j].feature
	})
	uniqueAssociations := associations[:0]
	for i, pair := range associations {
		if i == 0 || pair != associations[i-1] {
			uniqueAssociations = append(uniqueAssociations, pair)
		}
	}
	if uint64(len(uniqueAssociations)) > uint64(maxVertexOrdinal) {
		return nil, errors.New("availability graph has too many feature associations")
	}
	graph.associationFeatureIDs = make([]uint32, len(uniqueAssociations))
	associationIndex := 0
	for vertexOrdinal := range graph.vertices {
		if vertexOrdinal&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		start := associationIndex
		for associationIndex < len(uniqueAssociations) && uniqueAssociations[associationIndex].vertex == uint32(vertexOrdinal) {
			graph.associationFeatureIDs[associationIndex] = uniqueAssociations[associationIndex].feature
			associationIndex++
		}
		graph.vertices[vertexOrdinal].featureStart = uint32(start)
		graph.vertices[vertexOrdinal].featureEnd = uint32(associationIndex)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	graph.strings = b.stringValues
	graph.stats = AvailabilityGraphStats{
		Vertices:     len(graph.vertices),
		Edges:        len(physicalEdges),
		Features:     len(graph.features),
		Associations: len(graph.associationFeatureIDs),
	}
	graph.stats.Bytes = graph.retainedBytes()
	b.finished = true
	b.vertices = nil
	b.vertexByID = nil
	b.edges = nil
	b.features = nil
	b.featureByKey = nil
	b.associations = nil
	b.stringValues = nil
	b.stringByValue = nil
	return graph, nil
}

func relationLess(a, b graphRelation, values []string) bool {
	left := [...]uint32{a.fromType, a.toType, a.relationship, a.direction}
	right := [...]uint32{b.fromType, b.toType, b.relationship, b.direction}
	for i := range left {
		leftValue, rightValue := values[left[i]], values[right[i]]
		if leftValue != rightValue {
			return leftValue < rightValue
		}
	}
	return false
}

func featureLess(a, b featureIDs, values []string) bool {
	left := [...]uint32{a.kind, a.resourceType, a.fieldPath, a.conceptID, a.bindingID}
	right := [...]uint32{b.kind, b.resourceType, b.fieldPath, b.conceptID, b.bindingID}
	for i := range left {
		leftValue, rightValue := values[left[i]], values[right[i]]
		if leftValue != rightValue {
			return leftValue < rightValue
		}
	}
	return false
}

func (r graphRelation) public(values []string) AvailabilityRelation {
	return AvailabilityRelation{
		FromResourceType: values[r.fromType],
		ToResourceType:   values[r.toType],
		Relationship:     values[r.relationship],
		StorageDirection: values[r.direction],
	}
}

func (f featureIDs) public(values []string) AvailabilityFeature {
	return AvailabilityFeature{
		Kind:         values[f.kind],
		ResourceType: values[f.resourceType],
		FieldPath:    values[f.fieldPath],
		ConceptID:    values[f.conceptID],
		BindingID:    values[f.bindingID],
	}
}

func (g *AvailabilityGraph) retainedBytes() uint64 {
	if g == nil {
		return 0
	}
	total := uint64(unsafe.Sizeof(*g))
	addBacking := func(capacity int, elementSize uintptr) {
		total += uint64(capacity) * uint64(elementSize)
	}
	addBacking(cap(g.strings), unsafe.Sizeof(""))
	for _, value := range g.strings {
		total += uint64(len(value))
	}
	addBacking(cap(g.vertices), unsafe.Sizeof(graphVertex{}))
	addBacking(cap(g.edgeOffsets), unsafe.Sizeof(uint32(0)))
	addBacking(cap(g.arcs), unsafe.Sizeof(graphArc{}))
	addBacking(cap(g.relations), unsafe.Sizeof(graphRelation{}))
	addBacking(cap(g.features), unsafe.Sizeof(featureIDs{}))
	addBacking(cap(g.associationFeatureIDs), unsafe.Sizeof(uint32(0)))
	return total
}

// Stats reports counts and the estimated retained graph storage.
func (g *AvailabilityGraph) Stats() AvailabilityGraphStats {
	if g == nil {
		return AvailabilityGraphStats{}
	}
	return g.stats
}

// Find returns one deterministic shortest witness per reachable feature.
// It uses a single multi-source breadth-first search and returns no partial
// result if the context is canceled.
func (g *AvailabilityGraph) Find(ctx context.Context, q AvailabilityQuery) ([]AvailabilityWitness, error) {
	if g == nil {
		return nil, errors.New("availability graph is nil")
	}
	if ctx == nil {
		return nil, errors.New("availability query context is nil")
	}
	if q.MaxHops < 0 {
		return nil, errors.New("availability query MaxHops cannot be negative")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	authorizedPaths := make(map[string]struct{}, len(q.AuthResourcePaths))
	if !q.Unrestricted {
		for _, path := range q.AuthResourcePaths {
			authorizedPaths[path] = struct{}{}
		}
	}
	allowedRelations := make(map[AvailabilityRelation]struct{}, len(q.Relations))
	for _, relation := range q.Relations {
		allowedRelations[relation] = struct{}{}
	}
	allowedRelationIDs := make([]bool, len(g.relations))
	for id, relation := range g.relations {
		if _, ok := allowedRelations[relation.public(g.strings)]; ok {
			allowedRelationIDs[id] = true
		}
	}

	vertexCount := len(g.vertices)
	distance := make([]uint32, vertexCount)
	for i := range distance {
		distance[i] = maxVertexOrdinal
	}
	parent := make([]uint32, vertexCount)
	parentRelation := make([]uint32, vertexCount)
	rootOf := make([]uint32, vertexCount)
	queue := make([]uint32, 0, vertexCount)
	vertexAuthorized := func(vertex graphVertex) bool {
		if q.Unrestricted {
			return true
		}
		_, ok := authorizedPaths[g.strings[vertex.authPath]]
		return ok
	}
	seed := func(ordinal uint32) {
		distance[ordinal] = 0
		rootOf[ordinal] = ordinal
		queue = append(queue, ordinal)
	}
	if q.AllRoots {
		for i, vertex := range g.vertices {
			if i&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if g.strings[vertex.resourceType] == q.RootResourceType && vertexAuthorized(vertex) {
				seed(uint32(i))
			}
		}
	} else if len(q.RootIDs) != 0 {
		for i, id := range q.RootIDs {
			if i&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			ordinal, ok := g.findVertex(id)
			if !ok {
				return nil, fmt.Errorf("availability query references unknown root ID %q", id)
			}
			vertex := g.vertices[ordinal]
			if g.strings[vertex.resourceType] != q.RootResourceType {
				return nil, fmt.Errorf("availability root %q has resource type %q, want %q", id, g.strings[vertex.resourceType], q.RootResourceType)
			}
			if vertexAuthorized(vertex) && distance[ordinal] == maxVertexOrdinal {
				seed(ordinal)
			}
		}
		sort.Slice(queue, func(i, j int) bool { return queue[i] < queue[j] })
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if len(queue) == 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return []AvailabilityWitness{}, nil
	}

	winningSource := make([]uint32, len(g.features))
	for i := range winningSource {
		winningSource[i] = maxVertexOrdinal
	}
	var checked uint64
	for head := 0; head < len(queue); head++ {
		if head&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		current := queue[head]
		vertex := g.vertices[current]
		for association := vertex.featureStart; association < vertex.featureEnd; association++ {
			if checked&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			checked++
			featureID := g.associationFeatureIDs[association]
			if winningSource[featureID] == maxVertexOrdinal {
				winningSource[featureID] = current
			}
		}
		if q.MaxHops > 0 && uint64(distance[current]) >= uint64(q.MaxHops) {
			continue
		}
		for arcID := g.edgeOffsets[current]; arcID < g.edgeOffsets[current+1]; arcID++ {
			if checked&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			checked++
			arc := g.arcs[arcID]
			if !allowedRelationIDs[arc.relation] {
				continue
			}
			if !q.Unrestricted {
				if _, ok := authorizedPaths[g.strings[arc.authPath]]; !ok {
					continue
				}
			}
			target := g.vertices[arc.target]
			if !vertexAuthorized(target) {
				continue
			}
			if distance[arc.target] != maxVertexOrdinal {
				continue
			}
			distance[arc.target] = distance[current] + 1
			parent[arc.target] = current
			parentRelation[arc.target] = arc.relation
			rootOf[arc.target] = rootOf[current]
			queue = append(queue, arc.target)
		}
	}

	result := make([]AvailabilityWitness, 0, len(g.features))
	for featureID, source := range winningSource {
		if featureID&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if source == maxVertexOrdinal {
			continue
		}
		hops := int(distance[source])
		route := make([]AvailabilityRelation, hops)
		cursor := source
		for routeIndex := hops - 1; routeIndex >= 0; routeIndex-- {
			if routeIndex&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if cursor >= uint32(vertexCount) || parent[cursor] >= uint32(vertexCount) {
				return nil, errors.New("availability graph contains an invalid witness parent chain")
			}
			route[routeIndex] = g.relations[parentRelation[cursor]].public(g.strings)
			cursor = parent[cursor]
		}
		root := rootOf[source]
		result = append(result, AvailabilityWitness{
			Feature:  g.features[featureID].public(g.strings),
			RootID:   g.strings[g.vertices[root].id],
			SourceID: g.strings[g.vertices[source].id],
			Route:    route,
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (g *AvailabilityGraph) findVertex(id string) (uint32, bool) {
	index := sort.Search(len(g.vertices), func(i int) bool {
		return g.strings[g.vertices[i].id] >= id
	})
	if index == len(g.vertices) || g.strings[g.vertices[index].id] != id {
		return 0, false
	}
	return uint32(index), true
}
