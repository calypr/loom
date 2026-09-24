package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

type availabilityWitnessStoreClient struct {
	docs                    map[string]map[string]json.RawMessage
	events                  []string
	partialWitnessWriteOnce bool
}

func newAvailabilityWitnessStoreClient() *availabilityWitnessStoreClient {
	return &availabilityWitnessStoreClient{docs: map[string]map[string]json.RawMessage{
		catalog.AvailableColumnWitnessCollection:      {},
		catalog.AvailableColumnWitnessBuildCollection: {},
	}}
}

func (*availabilityWitnessStoreClient) CollectionExists(context.Context, string) (bool, error) {
	return true, nil
}

func (c *availabilityWitnessStoreClient) QueryRows(_ context.Context, query string, _ int, vars map[string]any, visit store.RowVisitor) error {
	switch query {
	case availableColumnWitnessBuildByKeyAQL:
		doc, found := c.docs[catalog.AvailableColumnWitnessBuildCollection][stringValue(vars["key"])]
		if !found {
			return nil
		}
		row, err := jsonDocument(doc)
		if err != nil {
			return err
		}
		return visit(row)
	case availableColumnWitnessesByScopeAQL:
		keys := make([]string, 0, len(c.docs[catalog.AvailableColumnWitnessCollection]))
		for key := range c.docs[catalog.AvailableColumnWitnessCollection] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			doc := c.docs[catalog.AvailableColumnWitnessCollection][key]
			var witness catalog.AvailableColumnWitness
			if err := json.Unmarshal(doc, &witness); err != nil {
				return err
			}
			if witness.Project != stringValue(vars["project"]) || witness.DatasetGeneration != stringValue(vars["generation"]) ||
				witness.RootResourceType != stringValue(vars["root_type"]) || witness.InputDigest != stringValue(vars["input_digest"]) ||
				witness.SchemaVersion != intValue(vars["schema_version"]) {
				continue
			}
			if sourceKind := stringValue(vars["source_kind"]); sourceKind != "" &&
				(string(witness.Source.Kind) != sourceKind || witness.Source.FieldPath != stringValue(vars["source_field_path"]) ||
					witness.Source.ConceptID != stringValue(vars["source_concept_id"]) || witness.Source.BindingID != stringValue(vars["source_binding_id"])) {
				continue
			}
			row, err := jsonDocument(doc)
			if err != nil {
				return err
			}
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unexpected query %q", query)
	}
}

func (c *availabilityWitnessStoreClient) InsertBatchRaw(_ context.Context, collection string, docs []json.RawMessage, _ bool, _ string) error {
	if c.docs[collection] == nil {
		c.docs[collection] = map[string]json.RawMessage{}
	}
	if collection == catalog.AvailableColumnWitnessBuildCollection {
		var build catalog.AvailableColumnWitnessBuild
		if len(docs) != 1 {
			return fmt.Errorf("build write has %d documents, want 1", len(docs))
		}
		if err := json.Unmarshal(docs[0], &build); err != nil {
			return err
		}
		c.events = append(c.events, "marker:"+string(build.State))
	}
	if collection == catalog.AvailableColumnWitnessCollection {
		c.events = append(c.events, fmt.Sprintf("witnesses:%d", len(docs)))
		if c.partialWitnessWriteOnce && len(docs) > 0 {
			c.partialWitnessWriteOnce = false
			var first map[string]any
			if err := json.Unmarshal(docs[0], &first); err != nil {
				return err
			}
			c.docs[collection][stringValue(first["_key"])] = append(json.RawMessage(nil), docs[0]...)
			return errors.New("simulated partial batch failure")
		}
	}
	for _, doc := range docs {
		var identity struct {
			Key string `json:"_key"`
		}
		if err := json.Unmarshal(doc, &identity); err != nil {
			return err
		}
		if identity.Key == "" {
			return errors.New("document key is empty")
		}
		c.docs[collection][identity.Key] = append(json.RawMessage(nil), doc...)
	}
	return nil
}

func (c *availabilityWitnessStoreClient) ExecuteAQL(_ context.Context, query string, vars map[string]any) error {
	if query != availableColumnWitnessBuildUnlessCompleteAQL {
		return fmt.Errorf("unexpected AQL %q", query)
	}
	build, ok := vars["build"].(catalog.AvailableColumnWitnessBuild)
	if !ok {
		return fmt.Errorf("build bind variable has type %T", vars["build"])
	}
	if current, found := c.docs[catalog.AvailableColumnWitnessBuildCollection][stringValue(vars["key"])]; found {
		var stored catalog.AvailableColumnWitnessBuild
		if err := json.Unmarshal(current, &stored); err != nil {
			return err
		}
		if stored.State == catalog.AvailableColumnWitnessComplete {
			c.events = append(c.events, "marker:ignored")
			return nil
		}
	}
	c.events = append(c.events, "marker:"+string(build.State))
	encoded, err := json.Marshal(build)
	if err != nil {
		return err
	}
	c.docs[catalog.AvailableColumnWitnessBuildCollection][build.Key] = encoded
	return nil
}

func (*availabilityWitnessStoreClient) Bootstrap(context.Context, store.BootstrapSpec) error {
	return nil
}

func TestPersistAvailableColumnWitnessesPublishesOnlyAfterAllWritesAndScopesReads(t *testing.T) {
	ctx := context.Background()
	client := newAvailabilityWitnessStoreClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	build := completeAvailabilityWitnessBuild("project-a", "generation-7", "snapshot-a")
	witnesses := []catalog.AvailabilityWitness{
		fieldWitness("Patient/root-1", "Observation/source-1", "status", []catalog.AvailabilityRelation{{
			FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "HAS_OBSERVATION", StorageDirection: "OUTBOUND",
		}}),
		fieldWitness("Patient/root-1", "Observation/source-1", "status", []catalog.AvailabilityRelation{
			{FromResourceType: "Patient", ToResourceType: "Encounter", Relationship: "HAS_ENCOUNTER", StorageDirection: "OUTBOUND"},
			{FromResourceType: "Encounter", ToResourceType: "Observation", Relationship: "HAS_OBSERVATION", StorageDirection: "OUTBOUND"},
		}),
		semanticWitness("Patient/root-1", "Observation/source-2"),
	}
	if err := adapter.PersistAvailableColumnWitnesses(ctx, build, witnesses); err != nil {
		t.Fatal(err)
	}
	if got, want := client.events, []string{"marker:BUILDING", "witnesses:3", "marker:COMPLETE"}; !equalStrings(got, want) {
		t.Fatalf("persistence order = %v, want %v", got, want)
	}

	marker, rows, found, err := adapter.ReadAvailableColumnWitnesses(ctx, build.Project, build.DatasetGeneration, build.RootResourceType, build.InputDigest, nil)
	if err != nil || !found || marker.State != catalog.AvailableColumnWitnessComplete || len(rows) != 3 {
		t.Fatalf("complete scoped read marker=%#v rows=%d found=%v err=%v", marker, len(rows), found, err)
	}
	field := catalog.RouteCoverageSource{Kind: catalog.RouteCoverageField, FieldPath: "status"}
	_, rows, found, err = adapter.ReadAvailableColumnWitnesses(ctx, build.Project, build.DatasetGeneration, build.RootResourceType, build.InputDigest, &field)
	if err != nil || !found || len(rows) != 2 {
		t.Fatalf("field source read rows=%d found=%v err=%v, want both routes", len(rows), found, err)
	}
	semantic := catalog.RouteCoverageSource{Kind: catalog.RouteCoverageSemantic, ConceptID: "condition", BindingID: "condition-code"}
	_, rows, found, err = adapter.ReadAvailableColumnWitnesses(ctx, build.Project, build.DatasetGeneration, build.RootResourceType, build.InputDigest, &semantic)
	if err != nil || !found || len(rows) != 1 || rows[0].Source.BindingID != semantic.BindingID {
		t.Fatalf("semantic source read rows=%#v found=%v err=%v", rows, found, err)
	}
	_, rows, found, err = adapter.ReadAvailableColumnWitnesses(ctx, "project-b", build.DatasetGeneration, build.RootResourceType, build.InputDigest, nil)
	if err != nil || found || len(rows) != 0 {
		t.Fatalf("cross-project read rows=%d found=%v err=%v", len(rows), found, err)
	}
}

func TestPersistAvailableColumnWitnessesRetriesPartialFailureWithoutLeakingRows(t *testing.T) {
	ctx := context.Background()
	client := newAvailabilityWitnessStoreClient()
	client.partialWitnessWriteOnce = true
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	build := completeAvailabilityWitnessBuild("project-a", "generation-7", "snapshot-a")
	witnesses := []catalog.AvailabilityWitness{fieldWitness("Patient/root-1", "Observation/source-1", "status", []catalog.AvailabilityRelation{{
		FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "HAS_OBSERVATION", StorageDirection: "OUTBOUND",
	}})}
	if err := adapter.PersistAvailableColumnWitnesses(ctx, build, witnesses); err == nil {
		t.Fatal("partial witness write unexpectedly succeeded")
	}
	marker, rows, found, err := adapter.ReadAvailableColumnWitnesses(ctx, build.Project, build.DatasetGeneration, build.RootResourceType, build.InputDigest, nil)
	if err != nil || !found || marker.State != catalog.AvailableColumnWitnessFailed || len(rows) != 0 {
		t.Fatalf("failed build read marker=%#v rows=%d found=%v err=%v", marker, len(rows), found, err)
	}

	if err := adapter.PersistAvailableColumnWitnesses(ctx, build, witnesses); err != nil {
		t.Fatalf("retry after partial write: %v", err)
	}
	marker, rows, found, err = adapter.ReadAvailableColumnWitnesses(ctx, build.Project, build.DatasetGeneration, build.RootResourceType, build.InputDigest, nil)
	if err != nil || !found || marker.State != catalog.AvailableColumnWitnessComplete || len(rows) != 1 {
		t.Fatalf("retried build read marker=%#v rows=%d found=%v err=%v", marker, len(rows), found, err)
	}
	if got := len(client.docs[catalog.AvailableColumnWitnessCollection]); got != 1 {
		t.Fatalf("stored witness count after retry = %d, want 1", got)
	}
}

func TestReadAvailableColumnWitnessesWaitsForCompleteAndSeparatesInputDigests(t *testing.T) {
	ctx := context.Background()
	client := newAvailabilityWitnessStoreClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	witnesses := []catalog.AvailabilityWitness{
		fieldWitness("Patient/root-1", "Observation/source-1", "status", []catalog.AvailabilityRelation{{
			FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "HAS_OBSERVATION", StorageDirection: "OUTBOUND",
		}}),
	}
	building := catalog.NewAvailableColumnWitnessBuild("project-a", "generation-7", "Patient", "snapshot-a")
	if err := adapter.PersistAvailableColumnWitnesses(ctx, building, witnesses); err != nil {
		t.Fatal(err)
	}
	marker, rows, found, err := adapter.ReadAvailableColumnWitnesses(ctx, building.Project, building.DatasetGeneration, building.RootResourceType, building.InputDigest, nil)
	if err != nil || !found || marker.State != catalog.AvailableColumnWitnessBuilding || len(rows) != 0 {
		t.Fatalf("building read marker=%#v rows=%d found=%v err=%v", marker, len(rows), found, err)
	}

	complete := building
	complete.State = catalog.AvailableColumnWitnessComplete
	complete.AllRoots = true
	complete.Unrestricted = true
	complete.Exhaustive = true
	if err := adapter.PersistAvailableColumnWitnesses(ctx, complete, witnesses); err != nil {
		t.Fatal(err)
	}
	marker, rows, found, err = adapter.ReadAvailableColumnWitnesses(ctx, complete.Project, complete.DatasetGeneration, complete.RootResourceType, complete.InputDigest, nil)
	if err != nil || !found || marker.State != catalog.AvailableColumnWitnessComplete || len(rows) != 1 || rows[0].Source.FieldPath != "status" {
		t.Fatalf("complete read marker=%#v rows=%#v found=%v err=%v", marker, rows, found, err)
	}

	otherDigest := completeAvailabilityWitnessBuild("project-a", "generation-7", "snapshot-b")
	otherWitnesses := []catalog.AvailabilityWitness{
		fieldWitness("Patient/root-1", "Observation/source-2", "code", []catalog.AvailabilityRelation{{
			FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "HAS_OBSERVATION", StorageDirection: "OUTBOUND",
		}}),
	}
	if err := adapter.PersistAvailableColumnWitnesses(ctx, otherDigest, otherWitnesses); err != nil {
		t.Fatal(err)
	}
	marker, rows, found, err = adapter.ReadAvailableColumnWitnesses(ctx, complete.Project, complete.DatasetGeneration, complete.RootResourceType, complete.InputDigest, nil)
	if err != nil || !found || marker.InputDigest != "snapshot-a" || len(rows) != 1 || rows[0].Source.FieldPath != "status" {
		t.Fatalf("original digest read marker=%#v rows=%#v found=%v err=%v", marker, rows, found, err)
	}
	marker, rows, found, err = adapter.ReadAvailableColumnWitnesses(ctx, otherDigest.Project, otherDigest.DatasetGeneration, otherDigest.RootResourceType, otherDigest.InputDigest, nil)
	if err != nil || !found || marker.InputDigest != "snapshot-b" || len(rows) != 1 || rows[0].Source.FieldPath != "code" {
		t.Fatalf("other digest read marker=%#v rows=%#v found=%v err=%v", marker, rows, found, err)
	}
}

func TestPersistAvailableColumnWitnessesRequiresExhaustiveWorkAndMatchingDigest(t *testing.T) {
	ctx := context.Background()
	client := newAvailabilityWitnessStoreClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	build := catalog.NewAvailableColumnWitnessBuild("project-a", "generation-7", "Patient", "snapshot-a")
	build.State = catalog.AvailableColumnWitnessComplete
	if err := adapter.PersistAvailableColumnWitnesses(ctx, build, nil); err == nil {
		t.Fatal("non-exhaustive work unexpectedly completed")
	}
	if len(client.docs[catalog.AvailableColumnWitnessBuildCollection]) != 0 {
		t.Fatal("invalid complete build wrote a marker")
	}

	build.Exhaustive = true
	build.AllRoots = true
	build.Unrestricted = true
	if err := adapter.PersistAvailableColumnWitnesses(ctx, build, nil); err != nil {
		t.Fatal(err)
	}
	_, rows, found, err := adapter.ReadAvailableColumnWitnesses(ctx, build.Project, build.DatasetGeneration, build.RootResourceType, "snapshot-b", nil)
	if err != nil || found || len(rows) != 0 {
		t.Fatalf("different input digest reused old build: rows=%d found=%v err=%v", len(rows), found, err)
	}
	changed := build
	changed.InputDigest = "snapshot-b"
	changed.Key = catalog.NewAvailableColumnWitnessBuild(changed.Project, changed.DatasetGeneration, changed.RootResourceType, changed.InputDigest).Key
	if err := adapter.PersistAvailableColumnWitnesses(ctx, changed, nil); err != nil {
		t.Fatal(err)
	}
	marker, rows, found, err := adapter.ReadAvailableColumnWitnesses(ctx, changed.Project, changed.DatasetGeneration, changed.RootResourceType, changed.InputDigest, nil)
	if err != nil || !found || marker.InputDigest != "snapshot-b" || marker.State != catalog.AvailableColumnWitnessComplete || len(rows) != 0 {
		t.Fatalf("new digest read marker=%#v rows=%d found=%v err=%v", marker, len(rows), found, err)
	}
	oldMarker, oldRows, found, err := adapter.ReadAvailableColumnWitnesses(ctx, build.Project, build.DatasetGeneration, build.RootResourceType, build.InputDigest, nil)
	if err != nil || !found || oldMarker.State != catalog.AvailableColumnWitnessComplete || len(oldRows) != 0 {
		t.Fatalf("previous digest read marker=%#v rows=%d found=%v err=%v", oldMarker, len(oldRows), found, err)
	}
	if got := len(client.docs[catalog.AvailableColumnWitnessBuildCollection]); got != 2 {
		t.Fatalf("build marker count after digest change = %d, want one marker per digest", got)
	}
}

func completeAvailabilityWitnessBuild(project, generation, inputDigest string) catalog.AvailableColumnWitnessBuild {
	build := catalog.NewAvailableColumnWitnessBuild(project, generation, "Patient", inputDigest)
	build.State = catalog.AvailableColumnWitnessComplete
	build.Exhaustive = true
	build.AllRoots = true
	build.Unrestricted = true
	return build
}

func fieldWitness(rootID, sourceID, path string, route []catalog.AvailabilityRelation) catalog.AvailabilityWitness {
	return catalog.AvailabilityWitness{
		Feature:  catalog.AvailabilityFeature{Kind: "FIELD", ResourceType: "Observation", FieldPath: path},
		RootID:   rootID,
		SourceID: sourceID,
		Route:    route,
	}
}

func semanticWitness(rootID, sourceID string) catalog.AvailabilityWitness {
	return catalog.AvailabilityWitness{
		Feature: catalog.AvailabilityFeature{
			Kind: "SEMANTIC", ResourceType: "Observation", ConceptID: "condition", BindingID: "condition-code",
		},
		RootID:   rootID,
		SourceID: sourceID,
		Route: []catalog.AvailabilityRelation{{
			FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "HAS_OBSERVATION", StorageDirection: "OUTBOUND",
		}},
	}
}

func jsonDocument(doc json.RawMessage) (map[string]any, error) {
	var row map[string]any
	if err := json.Unmarshal(doc, &row); err != nil {
		return nil, err
	}
	return row, nil
}

func intValue(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case float64:
		return int(number)
	}
	return 0
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
