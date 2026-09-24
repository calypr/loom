package arango

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

type availabilityPreparationTestClient struct {
	*availabilityTestClient
	witness *availabilityWitnessStoreClient
}

func (c *availabilityPreparationTestClient) QueryRows(ctx context.Context, query string, batch int, vars map[string]any, visit store.RowVisitor) error {
	switch query {
	case availableColumnWitnessBuildByKeyAQL, availableColumnWitnessesByScopeAQL:
		return c.witness.QueryRows(ctx, query, batch, vars, visit)
	default:
		return c.availabilityTestClient.QueryRows(ctx, query, batch, vars, visit)
	}
}

func (c *availabilityPreparationTestClient) InsertBatchRaw(ctx context.Context, collection string, docs []json.RawMessage, overwrite bool, writeAPI string) error {
	return c.witness.InsertBatchRaw(ctx, collection, docs, overwrite, writeAPI)
}

func (c *availabilityPreparationTestClient) ExecuteAQL(ctx context.Context, query string, vars map[string]any) error {
	return c.witness.ExecuteAQL(ctx, query, vars)
}

func (*availabilityPreparationTestClient) CollectionExists(context.Context, string) (bool, error) {
	return true, nil
}

func (*availabilityPreparationTestClient) Bootstrap(context.Context, store.BootstrapSpec) error {
	return nil
}

func TestAvailabilityInputDigestIgnoresRelationOrderAndDuplicates(t *testing.T) {
	a := catalog.AvailabilityRelation{FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND"}
	b := catalog.AvailabilityRelation{FromResourceType: "Observation", ToResourceType: "Specimen", Relationship: "specimen_Specimen", StorageDirection: "OUTBOUND"}
	first := availabilityInputDigest("project", "generation", []catalog.AvailabilityRelation{a, b})
	if got := availabilityInputDigest("project", "generation", []catalog.AvailabilityRelation{b, a, a}); got != first {
		t.Fatalf("same traversal contract produced different digests: %s != %s", got, first)
	}
	if got := availabilityInputDigest("project", "generation", []catalog.AvailabilityRelation{a}); got == first {
		t.Fatal("removing a compiler-allowed relation did not invalidate the witness build")
	}
	if got := availabilityInputDigest("project", "other-generation", []catalog.AvailabilityRelation{a, b}); got == first {
		t.Fatal("changing the generation did not invalidate the witness build")
	}
}

func TestDefaultAvailabilityReadsOnlyCompleteGenerationWitnesses(t *testing.T) {
	ctx := context.Background()
	client := newAvailabilityWitnessStoreClient()
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	relation := catalog.AvailabilityRelation{FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND"}
	query := catalog.AvailabilityQuery{RootResourceType: "Patient", AllRoots: true, Unrestricted: true, Relations: []catalog.AvailabilityRelation{relation}}
	opts := catalog.AvailabilityOptions{Project: "project", DatasetGeneration: "generation", Query: query}
	before, err := adapter.AvailableColumns(ctx, opts)
	if err != nil || before.State != catalog.SemanticInventoryRunning {
		t.Fatalf("unprepared read = %+v, %v", before, err)
	}
	build := catalog.NewAvailableColumnWitnessBuild("project", "generation", "Patient", availabilityInputDigest("project", "generation", query.Relations))
	build.AllRoots, build.Unrestricted, build.Exhaustive, build.State = true, true, true, catalog.AvailableColumnWitnessComplete
	if err := adapter.PersistAvailableColumnWitnesses(ctx, build, []catalog.AvailabilityWitness{{
		Feature: catalog.AvailabilityFeature{Kind: "FIELD", ResourceType: "Observation", FieldPath: "status"},
		RootID:  "Patient/p", SourceID: "Observation/o", Route: []catalog.AvailabilityRelation{relation},
	}}); err != nil {
		t.Fatal(err)
	}
	after, err := adapter.AvailableColumns(ctx, opts)
	if err != nil || after.State != catalog.SemanticInventoryComplete || len(after.Witnesses) != 1 || after.Witnesses[0].Feature.FieldPath != "status" || len(after.Witnesses[0].Route) != 1 {
		t.Fatalf("prepared read = %+v, %v", after, err)
	}
}

func TestPrepareAvailableColumnsPublishesGraphProofBeforeDefaultRead(t *testing.T) {
	ctx := context.Background()
	client := &availabilityPreparationTestClient{availabilityTestClient: newAvailabilityTestClient(), witness: newAvailabilityWitnessStoreClient()}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	relation := catalog.AvailabilityRelation{FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND"}
	if err := adapter.PrepareAvailableColumns(ctx, "p", "g", []string{"Patient"}, []catalog.AvailabilityRelation{relation}); err != nil {
		t.Fatal(err)
	}
	if client.transactions != 1 {
		t.Fatalf("graph scans = %d, want one preparation scan", client.transactions)
	}
	result, err := adapter.AvailableColumns(ctx, catalog.AvailabilityOptions{Project: "p", DatasetGeneration: "g", Query: catalog.AvailabilityQuery{
		RootResourceType: "Patient", AllRoots: true, Unrestricted: true, Relations: []catalog.AvailabilityRelation{relation},
	}})
	if err != nil || result.State != catalog.SemanticInventoryComplete || len(result.Witnesses) != 3 {
		t.Fatalf("prepared default read = %+v, %v", result, err)
	}
	if client.transactions != 1 {
		t.Fatalf("request rebuilt the graph: scans = %d", client.transactions)
	}
	wrongGeneration, err := adapter.AvailableColumns(ctx, catalog.AvailabilityOptions{Project: "p", DatasetGeneration: "other", Query: catalog.AvailabilityQuery{
		RootResourceType: "Patient", AllRoots: true, Unrestricted: true, Relations: []catalog.AvailabilityRelation{relation},
	}})
	if err != nil || wrongGeneration.State != catalog.SemanticInventoryRunning || len(wrongGeneration.Witnesses) != 0 {
		t.Fatalf("wrong generation borrowed witnesses: %+v, %v", wrongGeneration, err)
	}
}
