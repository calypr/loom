package ingest

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	publication "github.com/calypr/loom/internal/dataset"
)

type fieldSourceMembershipBackfillFake struct {
	rows        map[string][]catalogarango.RetainedSemanticInventoryRow
	memberships map[string]catalog.FieldSourceMembership
	build       catalog.FieldSourceMembershipBuild
	prepared    bool
}

func (f *fieldSourceMembershipBackfillFake) PrepareFieldSourceMembership(context.Context) error {
	f.prepared = true
	return nil
}

func (f *fieldSourceMembershipBackfillFake) ReadFieldSourceMembershipBuild(_ context.Context, project, generation string) (catalog.FieldSourceMembershipBuild, bool, error) {
	return f.build, f.build.Key != "" && f.build.Project == project && f.build.DatasetGeneration == generation, nil
}

func (f *fieldSourceMembershipBackfillFake) WriteFieldSourceMembershipBuild(_ context.Context, build catalog.FieldSourceMembershipBuild) error {
	f.build = build
	return nil
}

func (f *fieldSourceMembershipBackfillFake) ReadRetainedSemanticInventoryPage(_ context.Context, project, generation, collection, afterKey string, limit int) (catalogarango.RetainedSemanticInventoryPage, error) {
	rows := append([]catalogarango.RetainedSemanticInventoryRow(nil), f.rows[collection]...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	page := catalogarango.RetainedSemanticInventoryPage{Rows: make([]catalogarango.RetainedSemanticInventoryRow, 0, limit), SourceState: catalogarango.RetainedSemanticSourcePresent}
	for _, row := range rows {
		if row.Key <= afterKey {
			continue
		}
		if len(page.Rows) == limit {
			page.HasMore = true
			break
		}
		page.Rows = append(page.Rows, row)
	}
	return page, nil
}

func (f *fieldSourceMembershipBackfillFake) WriteFieldSourceMemberships(_ context.Context, memberships []catalog.FieldSourceMembership, _ int) error {
	if f.memberships == nil {
		f.memberships = map[string]catalog.FieldSourceMembership{}
	}
	for _, membership := range memberships {
		f.memberships[membership.Key] = membership
	}
	return nil
}

func TestBackfillFieldSourceMembershipIsBoundedResumableAndCompleteOnlyAtEnd(t *testing.T) {
	backend := &fieldSourceMembershipBackfillFake{
		rows: map[string][]catalogarango.RetainedSemanticInventoryRow{
			"Observation": {
				backfillSourceRow(t, "source-a", "scope-a", map[string]any{"resourceType": "Observation", "valueString": "sparse"}),
				backfillSourceRow(t, "source-b", "scope-b", map[string]any{"resourceType": "Observation", "status": "final"}),
			},
		},
	}
	manifest := backfillManifest(t, publication.StateStaged)
	options := FieldSourceMembershipBackfillOptions{Project: "project", DatasetGeneration: "generation", MaxResources: 1, PageSize: 10, BatchSize: 10}

	first, err := BackfillFieldSourceMembership(context.Background(), backend, manifest, options)
	if err != nil {
		t.Fatal(err)
	}
	if !backend.prepared || first.State != catalog.FieldSourceMembershipBuilding || first.ScannedThisRun != 1 || first.ScannedTotal != 1 || first.Checkpoint != (catalog.FieldSourceMembershipCheckpoint{Collection: "Observation", Key: "source-a"}) {
		t.Fatalf("first bounded page report=%+v prepared=%v", first, backend.prepared)
	}
	if len(backend.memberships) != 1 {
		t.Fatalf("first page wrote %d memberships, want 1", len(backend.memberships))
	}
	for _, membership := range backend.memberships {
		if !reflect.DeepEqual(membership.ScalarPaths, []string{"resourceType", "valueString"}) {
			t.Fatalf("sparse source membership paths=%v, want resourceType and valueString", membership.ScalarPaths)
		}
	}

	second, err := BackfillFieldSourceMembership(context.Background(), backend, manifest, options)
	if err != nil {
		t.Fatal(err)
	}
	if second.State != catalog.FieldSourceMembershipComplete || second.ScannedThisRun != 1 || second.ScannedTotal != 2 || second.Checkpoint != (catalog.FieldSourceMembershipCheckpoint{}) {
		t.Fatalf("second bounded page report=%+v", second)
	}
	if len(backend.memberships) != 2 || backend.build.ScannedResources != 2 {
		t.Fatalf("complete index has %d memberships and marker count %d, want 2", len(backend.memberships), backend.build.ScannedResources)
	}
	var emptySourceFound bool
	for _, membership := range backend.memberships {
		if membership.VertexID == "Observation/source-b" {
			emptySourceFound = reflect.DeepEqual(membership.ScalarPaths, []string{"resourceType", "status"})
		}
	}
	if !emptySourceFound {
		t.Fatal("second sidecar does not preserve its source's actual scalar paths")
	}
	third, err := BackfillFieldSourceMembership(context.Background(), backend, manifest, options)
	if err != nil || !third.NoOp || third.State != catalog.FieldSourceMembershipComplete {
		t.Fatalf("completed backfill replay report=%+v err=%v, want no-op", third, err)
	}
}

func TestFieldSourceMembershipBackfillRejectsUnboundedOrUnmatchedRequests(t *testing.T) {
	backend := &fieldSourceMembershipBackfillFake{}
	manifest := backfillManifest(t, publication.StateStaged)
	if _, err := BackfillFieldSourceMembership(context.Background(), backend, manifest, FieldSourceMembershipBackfillOptions{
		Project: "project", DatasetGeneration: "generation",
	}); err == nil {
		t.Fatal("backfill without resource budget succeeded")
	}
	if backend.prepared {
		t.Fatal("invalid request prepared storage")
	}
	_, err := BackfillFieldSourceMembership(context.Background(), backend, manifest, FieldSourceMembershipBackfillOptions{
		Project: "other", DatasetGeneration: "generation", MaxResources: 1,
	})
	if err == nil {
		t.Fatal("backfill with mismatched manifest succeeded")
	}
}
