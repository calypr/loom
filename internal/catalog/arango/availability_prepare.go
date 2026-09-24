package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

// availabilityInputDigest binds compact witnesses to the exact traversal
// contract used by Builder. Generation data is immutable; schema and route
// changes produce a new digest and cannot reuse an older proof.
func availabilityInputDigest(project, generation string, relations []catalog.AvailabilityRelation) string {
	canonical := append([]catalog.AvailabilityRelation(nil), relations...)
	sort.Slice(canonical, func(i, j int) bool {
		a, b := canonical[i], canonical[j]
		if a.FromResourceType != b.FromResourceType {
			return a.FromResourceType < b.FromResourceType
		}
		if a.ToResourceType != b.ToResourceType {
			return a.ToResourceType < b.ToResourceType
		}
		if a.Relationship != b.Relationship {
			return a.Relationship < b.Relationship
		}
		return a.StorageDirection < b.StorageDirection
	})
	canonical = compactAvailabilityRelations(canonical)
	encoded, _ := json.Marshal(struct {
		Version                string                         `json:"version"`
		Project                string                         `json:"project"`
		Generation             string                         `json:"generation"`
		FieldMembershipVersion int                            `json:"fieldMembershipVersion"`
		SemanticRuleVersion    int                            `json:"semanticRuleVersion"`
		Relations              []catalog.AvailabilityRelation `json:"relations"`
	}{"available-column-input/v1", project, generation, catalog.FieldSourceMembershipSchemaVersion, catalog.SemanticObservationRuleVersion, canonical})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func compactAvailabilityRelations(relations []catalog.AvailabilityRelation) []catalog.AvailabilityRelation {
	if len(relations) < 2 {
		return relations
	}
	unique := relations[:1]
	for _, relation := range relations[1:] {
		if relation != unique[len(unique)-1] {
			unique = append(unique, relation)
		}
	}
	return unique
}

// AvailableColumnWitnessCollectionSpecs defines the same persistent indexes
// for generation ingestion and opt-in preparation of an older generation.
func AvailableColumnWitnessCollectionSpecs() []store.CollectionSpec {
	return []store.CollectionSpec{
		{Name: catalog.AvailableColumnWitnessCollection, Indexes: [][]string{
			{"project", "dataset_generation", "root_resource_type", "input_digest", "schema_version"},
			{"project", "dataset_generation", "root_resource_type", "input_digest", "schema_version", "source.Kind", "source.FieldPath"},
			{"project", "dataset_generation", "root_resource_type", "input_digest", "schema_version", "source.Kind", "source.ConceptID", "source.BindingID"},
		}},
		{Name: catalog.AvailableColumnWitnessBuildCollection, Indexes: [][]string{{"project", "dataset_generation", "root_resource_type", "input_digest", "schema_version"}}},
	}
}

// PrepareAvailableColumns builds all unrestricted default-row witnesses before
// a generation becomes active. No request owns or retains the source graph.
func (s *Store) PrepareAvailableColumns(ctx context.Context, project, generation string, rootTypes []string, relations []catalog.AvailabilityRelation) error {
	if len(rootTypes) == 0 {
		return fmt.Errorf("column availability requires at least one populated root type")
	}
	if err := s.client.Bootstrap(ctx, store.BootstrapSpec{Collections: AvailableColumnWitnessCollectionSpecs()}); err != nil {
		return fmt.Errorf("prepare available-column witness storage: %w", err)
	}
	stamp, err := s.availabilityStamp(ctx, project, generation)
	if err != nil {
		return err
	}
	if stamp.key == "" {
		return fmt.Errorf("column availability sources are incomplete for %s/%s", project, generation)
	}
	digest := availabilityInputDigest(project, generation, relations)
	slog.Info("preparing generation column witnesses", "project", project, "generation", generation, "input_digest", digest, "relations", len(relations), "roots", len(rootTypes))
	reader := s.client.(availabilityReader)
	started := time.Now()
	graph, err := s.readAvailabilityGraph(ctx, reader, project, generation, stamp)
	if err != nil {
		return err
	}
	current, err := s.availabilityStamp(ctx, project, generation)
	if err != nil {
		return err
	}
	if current.key != stamp.key {
		return fmt.Errorf("column availability inputs changed during preparation")
	}
	defer func() {
		slog.Info("generation column availability prepared", "project", project, "generation", generation, "elapsed", time.Since(started), "graph_stats", graph.Stats())
	}()
	seen := make(map[string]struct{}, len(rootTypes))
	for _, root := range rootTypes {
		if root == "" {
			return fmt.Errorf("column availability root type is empty")
		}
		seen[root] = struct{}{}
	}
	ordered := make([]string, 0, len(seen))
	for root := range seen {
		ordered = append(ordered, root)
	}
	sort.Strings(ordered)
	for _, root := range ordered {
		witnesses, err := graph.Find(ctx, catalog.AvailabilityQuery{RootResourceType: root, AllRoots: true, Unrestricted: true, Relations: relations})
		if err != nil {
			return fmt.Errorf("prepare %s column witnesses: %w", root, err)
		}
		build := catalog.NewAvailableColumnWitnessBuild(project, generation, root, digest)
		build.AllRoots, build.Unrestricted, build.Exhaustive, build.State = true, true, true, catalog.AvailableColumnWitnessComplete
		if err := s.PersistAvailableColumnWitnesses(ctx, build, witnesses); err != nil {
			return fmt.Errorf("persist %s column witnesses: %w", root, err)
		}
	}
	return nil
}
