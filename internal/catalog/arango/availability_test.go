package arango

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

type availabilityTestClient struct {
	fieldSourceMembershipTestClient
	fields            catalog.FieldSourceMembershipBuild
	semantics         catalog.SemanticInventoryBuild
	changeDuringBuild bool
	transactions      int
}

func (c *availabilityTestClient) WithStreamingReadTransaction(ctx context.Context, collections []string, fn store.TransactionFunc) error {
	if !slices.Equal(collections, availabilityCollections) {
		return fmt.Errorf("graph inputs must use one read-only snapshot")
	}
	c.transactions++
	err := fn(ctx, c)
	if c.changeDuringBuild {
		c.fields.ScannedResources++
	}
	return err
}

func (c *availabilityTestClient) QueryRows(_ context.Context, query string, _ int, vars map[string]any, visit store.RowVisitor) error {
	emit := func(v any) error {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var row map[string]any
		if err := json.Unmarshal(raw, &row); err != nil {
			return err
		}
		return visit(row)
	}
	switch query {
	case fieldSourceMembershipBuildByKeyAQL:
		return emit(c.fields)
	case semanticInventoryBuildByKeyAQL:
		return emit(c.semantics)
	case availabilityVerticesAQL, availabilityEdgesAQL:
		if vars["project"] != "p" || vars["generation"] != "g" {
			return fmt.Errorf("missing graph scope")
		}
	default:
		return fmt.Errorf("unexpected availability query: %s", query)
	}
	switch query {
	case availabilityVerticesAQL:
		for _, row := range []catalog.FieldSourceMembership{
			{VertexID: "Patient/p", ResourceType: "Patient", ScalarPaths: []string{"id"}, SemanticFeatures: []catalog.SemanticInventoryReference{}},
			{VertexID: "Observation/o", ResourceType: "Observation", ScalarPaths: []string{"valueInteger"}, SemanticFeatures: []catalog.SemanticInventoryReference{{ConceptID: "height", BindingID: "value"}}},
		} {
			if err := emit(row); err != nil {
				return err
			}
		}
	case availabilityEdgesAQL:
		return emit(map[string]any{"from": "Observation/o", "to": "Patient/p", "label": "subject_Patient", "auth": ""})
	}
	return nil
}

func newAvailabilityTestClient() *availabilityTestClient {
	fields := catalog.NewFieldSourceMembershipBuild("p", "g")
	fields.State, fields.ScannedResources = catalog.FieldSourceMembershipComplete, 2
	return &availabilityTestClient{fields: fields, semantics: catalog.SemanticInventoryBuild{
		Key: catalog.SemanticInventoryBuildKey("p", "g"), Project: "p", DatasetGeneration: "g",
		BuildID: catalog.SemanticInventoryBuildID("p", "g"), State: catalog.SemanticInventoryComplete,
		SourceKind: catalog.SemanticInventorySourceRetained, SourceAvailability: catalog.SemanticInventorySourceAvailabilityVerified,
	}}
}

func TestAvailabilityLoadsOnceAndRejectsChangingInputs(t *testing.T) {
	for _, changing := range []bool{false, true} {
		t.Run(fmt.Sprint(changing), func(t *testing.T) {
			client := newAvailabilityTestClient()
			client.changeDuringBuild = changing
			adapter, _ := New(client)
			stamp, err := adapter.availabilityStamp(context.Background(), "p", "g")
			if err != nil {
				t.Fatal(err)
			}
			build := &availabilityBuild{key: stamp.key, buildID: stamp.buildID, done: make(chan struct{})}
			adapter.availability = build
			adapter.buildAvailabilityGraph(context.Background(), build, "p", "g", stamp)
			if changing {
				if build.err == nil || build.graph != nil {
					t.Fatalf("mixed graph published: %+v", build)
				}
				return
			}
			if build.err != nil {
				t.Fatal(build.err)
			}
			result, err := adapter.AvailableColumns(context.Background(), catalog.AvailabilityOptions{Project: "p", DatasetGeneration: "g", Query: catalog.AvailabilityQuery{
				RootResourceType: "Patient", AllRoots: true, Unrestricted: true, MaxHops: 5,
				Relations: []catalog.AvailabilityRelation{{FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND"}},
			}})
			if err != nil || result.State != catalog.SemanticInventoryComplete || len(result.Witnesses) != 3 || client.transactions != 1 {
				t.Fatalf("cached graph result=%+v error=%v transactions=%d", result, err, client.transactions)
			}
		})
	}
}

func TestAvailabilityIncompleteEvidenceDoesNotStartGraphBuild(t *testing.T) {
	client := newAvailabilityTestClient()
	client.fields.State = catalog.FieldSourceMembershipBuilding
	adapter, _ := New(client)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := adapter.AvailableColumns(ctx, catalog.AvailabilityOptions{Project: "p", DatasetGeneration: "g"})
	if err != nil || result.State != catalog.SemanticInventoryRunning || len(result.Witnesses) != 0 || adapter.availability != nil {
		t.Fatalf("incomplete evidence leaked: %+v, %v", result, err)
	}
}
