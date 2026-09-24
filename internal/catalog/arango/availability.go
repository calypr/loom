package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

// A Store retains one immutable graph, not one copy per user or table. A build
// never runs on an HTTP request's cancellation context and publishes atomically.
type availabilityBuild struct {
	key        string
	buildID    string
	done       chan struct{}
	graph      *catalog.AvailabilityGraph
	err        error
	cancel     context.CancelFunc
	finishedAt time.Time
}

type availabilityStamp struct {
	key      string
	buildID  string
	vertices int
}

type availabilityReader interface {
	WithStreamingReadTransaction(context.Context, []string, store.TransactionFunc) error
}

var availabilityCollections = []string{
	catalog.FieldSourceMembershipCollection,
	catalog.FieldSourceMembershipBuildCollection,
	catalog.SemanticInventoryBuildCollection,
	"fhir_edge",
}

func (s *Store) AvailableColumns(ctx context.Context, opts catalog.AvailabilityOptions) (catalog.AvailabilityResult, error) {
	if strings.TrimSpace(opts.Project) == "" || strings.TrimSpace(opts.DatasetGeneration) == "" {
		return catalog.AvailabilityResult{}, fmt.Errorf("availability requires a project and generation")
	}
	if opts.Query.AllRoots && opts.Query.Unrestricted && opts.Query.MaxHops == 0 {
		exists, err := s.client.CollectionExists(ctx, catalog.AvailableColumnWitnessBuildCollection)
		if err != nil {
			return catalog.AvailabilityResult{}, err
		}
		if !exists {
			return catalog.AvailabilityResult{State: catalog.SemanticInventoryRunning}, nil
		}
		digest := availabilityInputDigest(opts.Project, opts.DatasetGeneration, opts.Query.Relations)
		build, stored, found, err := s.ReadAvailableColumnWitnesses(ctx, opts.Project, opts.DatasetGeneration, opts.Query.RootResourceType, digest, nil)
		if err != nil {
			return catalog.AvailabilityResult{}, err
		}
		if !found || build.State != catalog.AvailableColumnWitnessComplete {
			slog.Debug("column witness lookup pending", "project", opts.Project, "generation", opts.DatasetGeneration, "root", opts.Query.RootResourceType, "digest", digest, "relations", len(opts.Query.Relations), "found", found, "state", build.State)
			return catalog.AvailabilityResult{State: catalog.SemanticInventoryRunning}, nil
		}
		witnesses := make([]catalog.AvailabilityWitness, 0, len(stored))
		for _, row := range stored {
			feature := catalog.AvailabilityFeature{Kind: string(row.Source.Kind), ResourceType: row.SourceResourceType, FieldPath: row.Source.FieldPath, ConceptID: row.Source.ConceptID, BindingID: row.Source.BindingID}
			route := make([]catalog.AvailabilityRelation, len(row.Route))
			for i, step := range row.Route {
				route[i] = catalog.AvailabilityRelation{FromResourceType: step.FromResourceType, ToResourceType: step.ToResourceType, Relationship: step.Relationship, StorageDirection: step.StorageDirection}
			}
			witnesses = append(witnesses, catalog.AvailabilityWitness{Feature: feature, RootID: row.RootWitnessID, SourceID: row.SourceWitnessID, Route: route})
		}
		return catalog.AvailabilityResult{State: catalog.SemanticInventoryComplete, BuildID: digest, Witnesses: witnesses}, nil
	}
	stamp, err := s.availabilityStamp(ctx, opts.Project, opts.DatasetGeneration)
	if err != nil {
		return catalog.AvailabilityResult{}, err
	}
	if stamp.key == "" {
		return catalog.AvailabilityResult{State: catalog.SemanticInventoryRunning}, nil
	}
	s.availabilityMu.Lock()
	if s.availabilityClosed {
		s.availabilityMu.Unlock()
		return catalog.AvailabilityResult{}, fmt.Errorf("column availability store is closed")
	}
	build := s.availability
	if build != nil {
		select {
		case <-build.done:
			if build.err != nil && time.Since(build.finishedAt) >= 30*time.Second {
				build = nil
			}
		default:
		}
	}
	if build != nil && build.key != stamp.key {
		select {
		case <-build.done:
			build = nil
		default:
			// Serialize large builds. The next poll starts this generation after
			// the current build finishes rather than allocating another graph.
			s.availabilityMu.Unlock()
			return catalog.AvailabilityResult{State: catalog.SemanticInventoryRunning, BuildID: stamp.buildID}, nil
		}
	}
	if build == nil {
		buildCtx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		build = &availabilityBuild{key: stamp.key, buildID: stamp.buildID, done: make(chan struct{}), cancel: cancel}
		s.availability = build
		go s.buildAvailabilityGraph(buildCtx, build, opts.Project, opts.DatasetGeneration, stamp)
	}
	s.availabilityMu.Unlock()
	result := catalog.AvailabilityResult{State: catalog.SemanticInventoryRunning, BuildID: build.buildID}
	select {
	case <-build.done:
		if build.err != nil {
			return result, build.err
		}
		result.Witnesses, err = build.graph.Find(ctx, opts.Query)
		if err != nil {
			return result, err
		}
		result.State = catalog.SemanticInventoryComplete
	default:
	}
	return result, nil
}

func (s *Store) availabilityStamp(ctx context.Context, project, generation string) (availabilityStamp, error) {
	if _, ok := s.client.(availabilityReader); !ok {
		return availabilityStamp{}, fmt.Errorf("availability requires consistent graph reads")
	}
	fields, found, err := s.ReadFieldSourceMembershipBuild(ctx, project, generation)
	if err != nil {
		return availabilityStamp{}, err
	}
	if found && fields.State == catalog.FieldSourceMembershipFailed {
		return availabilityStamp{}, fmt.Errorf("column availability field membership build failed: %s", fields.Diagnostic)
	}
	if !found || fields.State != catalog.FieldSourceMembershipComplete || fields.SchemaVersion != catalog.FieldSourceMembershipSchemaVersion {
		return availabilityStamp{}, nil
	}
	semantics, found, err := s.semanticInventoryBuildByKey(ctx, catalog.SemanticInventoryBuildKey(project, generation))
	if err != nil {
		return availabilityStamp{}, err
	}
	if found && semantics.State == catalog.SemanticInventoryFailed {
		return availabilityStamp{}, fmt.Errorf("column availability semantic inventory build failed")
	}
	if !found || semantics.State != catalog.SemanticInventoryComplete || semantics.BuildID != catalog.SemanticInventoryBuildID(project, generation) || semantics.SourceKind != catalog.SemanticInventorySourceRetained || semantics.SourceAvailability != catalog.SemanticInventorySourceAvailabilityVerified {
		return availabilityStamp{}, nil
	}
	identity := []string{project, generation, fields.Key, fmt.Sprint(fields.ScannedResources), semantics.BuildID}
	raw, _ := json.Marshal(identity)
	digest := sha256.Sum256(raw)
	return availabilityStamp{key: hex.EncodeToString(digest[:]), buildID: semantics.BuildID, vertices: int(fields.ScannedResources)}, nil
}

func (s *Store) CloseAvailability() {
	s.availabilityMu.Lock()
	defer s.availabilityMu.Unlock()
	s.availabilityClosed = true
	if s.availability != nil && s.availability.cancel != nil {
		s.availability.cancel()
	}
}

func (s *Store) buildAvailabilityGraph(ctx context.Context, build *availabilityBuild, project, generation string, stamp availabilityStamp) {
	defer func() {
		if build.cancel != nil {
			build.cancel()
		}
		build.finishedAt = time.Now()
		close(build.done)
	}()
	started := time.Now()
	slog.Info("preparing column availability", "project", project, "generation", generation, "vertices", stamp.vertices)
	reader := s.client.(availabilityReader)
	build.graph = s.readAvailabilityCache(stamp.key)
	loaded := build.graph != nil
	if !loaded {
		build.graph, build.err = s.readAvailabilityGraph(ctx, reader, project, generation, stamp)
	}
	if build.err == nil {
		var current availabilityStamp
		current, build.err = s.availabilityStamp(ctx, project, generation)
		if build.err == nil && current.key != stamp.key {
			build.err = fmt.Errorf("column availability inputs changed during construction; retry with the current generation")
			build.graph = nil
		}
	}
	if build.err != nil {
		slog.Error("column availability preparation failed", "project", project, "elapsed", time.Since(started), "error", build.err)
		return
	}
	if !loaded {
		if err := s.writeAvailabilityCache(stamp.key, build.graph); err != nil {
			slog.Warn("column availability cache could not be saved", "error", err)
		}
	}
	slog.Info("column availability prepared", "project", project, "elapsed", time.Since(started), "loaded_from_disk", loaded, "stats", build.graph.Stats())
}

func (s *Store) readAvailabilityGraph(ctx context.Context, reader availabilityReader, project, generation string, stamp availabilityStamp) (*catalog.AvailabilityGraph, error) {
	builder := catalog.NewAvailabilityGraphBuilder(stamp.vertices, 0)
	err := reader.WithStreamingReadTransaction(ctx, availabilityCollections, func(ctx context.Context, query store.RowQueryer) error {
		vars := map[string]any{"project": project, "generation": generation}
		vertices := 0
		if err := query.QueryRows(ctx, availabilityVerticesAQL, 10000, vars, func(row map[string]any) error {
			vertex, paths, features, err := availabilityVertexRow(row)
			if err != nil {
				return err
			}
			vertices++
			if vertices%250000 == 0 {
				slog.Info("column availability load progress", "project", project, "stage", "vertices", "rows", vertices)
			}
			if err := builder.AddVertex(vertex, paths); err != nil {
				return err
			}
			for _, feature := range features {
				if err := builder.AddFeature(vertex.ID, feature); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("read availability vertices: %w", err)
		}
		if vertices != stamp.vertices {
			return fmt.Errorf("availability membership is incomplete: read %d vertices, build declares %d", vertices, stamp.vertices)
		}
		slog.Info("column availability vertices loaded", "project", project, "vertices", vertices)
		dangling := 0
		edges := 0
		if err := query.QueryRows(ctx, availabilityEdgesAQL, 10000, vars, func(row map[string]any) error {
			edges++
			if edges%250000 == 0 {
				slog.Info("column availability load progress", "project", project, "stage", "edges", "rows", edges)
			}
			if !builder.HasVertex(stringValue(row["from"])) || !builder.HasVertex(stringValue(row["to"])) {
				dangling++
				return nil
			}
			return builder.AddEdge(stringValue(row["from"]), stringValue(row["to"]), stringValue(row["label"]), stringValue(row["auth"]))
		}); err != nil {
			return fmt.Errorf("read availability edges: %w", err)
		}
		slog.Info("column availability links loaded", "project", project, "edges", edges, "dangling_references_excluded", dangling)
		return nil
	})
	if err != nil {
		return nil, err
	}
	graph, err := builder.Finish(ctx)
	if err != nil {
		return nil, err
	}
	if graph.Stats().Bytes > availabilityGraphMaxBytes {
		return nil, fmt.Errorf("column availability graph exceeds the 1536 MiB retained-memory budget")
	}
	return graph, nil
}

const availabilityVerticesAQL = `
FOR d IN fhir_field_source_membership
  FILTER d.project == @project AND d.dataset_generation == @generation
  RETURN {vertex_id: d.vertex_id, resource_type: d.resource_type,
    auth_resource_path: NOT_NULL(d.auth_resource_path, ""), scalar_paths: d.scalar_paths,
    semantic_features: d.semantic_features}`

func availabilityVertexRow(row map[string]any) (catalog.AvailabilityVertex, []string, []catalog.AvailabilityFeature, error) {
	vertex := catalog.AvailabilityVertex{ID: stringValue(row["vertex_id"]), ResourceType: stringValue(row["resource_type"]), AuthResourcePath: stringValue(row["auth_resource_path"])}
	if vertex.ResourceType == "" || !strings.HasPrefix(vertex.ID, vertex.ResourceType+"/") || len(vertex.ID) == len(vertex.ResourceType)+1 {
		return vertex, nil, nil, fmt.Errorf("invalid availability vertex identity")
	}
	values, ok := row["scalar_paths"].([]any)
	if !ok {
		return vertex, nil, nil, fmt.Errorf("availability vertex %q has no scalar path array", vertex.ID)
	}
	paths := make([]string, len(values))
	for i, value := range values {
		path, ok := value.(string)
		if !ok || path == "" {
			return vertex, nil, nil, fmt.Errorf("availability vertex %q has an invalid scalar path", vertex.ID)
		}
		paths[i] = path
	}
	values, ok = row["semantic_features"].([]any)
	if !ok {
		return vertex, nil, nil, fmt.Errorf("availability vertex %q has no semantic feature array", vertex.ID)
	}
	features := make([]catalog.AvailabilityFeature, 0, len(values))
	for _, value := range values {
		reference, ok := value.(map[string]any)
		if !ok || stringValue(reference["concept_id"]) == "" || stringValue(reference["binding_id"]) == "" {
			return vertex, nil, nil, fmt.Errorf("availability vertex %q has an invalid semantic feature", vertex.ID)
		}
		features = append(features, catalog.AvailabilityFeature{Kind: "SEMANTIC", ResourceType: vertex.ResourceType, ConceptID: stringValue(reference["concept_id"]), BindingID: stringValue(reference["binding_id"])})
	}
	return vertex, paths, features, nil
}

const availabilityEdgesAQL = `
FOR e IN fhir_edge
  FILTER e.project == @project AND e.dataset_generation == @generation
  RETURN {from: e._from, to: e._to, label: e.label, auth: NOT_NULL(e.auth_resource_path, "")}`
