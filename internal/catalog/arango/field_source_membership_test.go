package arango

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

type fieldSourceMembershipTestClient struct {
	specs   []store.BootstrapSpec
	inserts []fieldSourceMembershipInsert
	docs    map[string]map[string]json.RawMessage
}

type fieldSourceMembershipInsert struct {
	collection string
	docs       []json.RawMessage
	overwrite  bool
}

func (f *fieldSourceMembershipTestClient) CollectionExists(context.Context, string) (bool, error) {
	return true, nil
}

func (f *fieldSourceMembershipTestClient) Bootstrap(_ context.Context, spec store.BootstrapSpec) error {
	f.specs = append(f.specs, spec)
	return nil
}

func (f *fieldSourceMembershipTestClient) QueryRows(_ context.Context, query string, _ int, vars map[string]any, visit store.RowVisitor) error {
	if query != fieldSourceMembershipBuildByKeyAQL {
		return nil
	}
	key, _ := vars["key"].(string)
	if raw := f.docs[catalog.FieldSourceMembershipBuildCollection][key]; len(raw) > 0 {
		var row map[string]any
		if err := json.Unmarshal(raw, &row); err != nil {
			return err
		}
		return visit(row)
	}
	return nil
}

func (f *fieldSourceMembershipTestClient) InsertBatchRaw(_ context.Context, collection string, docs []json.RawMessage, overwrite bool, _ string) error {
	copyDocs := make([]json.RawMessage, len(docs))
	for index, doc := range docs {
		copyDocs[index] = append(json.RawMessage(nil), doc...)
	}
	f.inserts = append(f.inserts, fieldSourceMembershipInsert{collection: collection, docs: copyDocs, overwrite: overwrite})
	if f.docs == nil {
		f.docs = map[string]map[string]json.RawMessage{}
	}
	if f.docs[collection] == nil {
		f.docs[collection] = map[string]json.RawMessage{}
	}
	for _, doc := range docs {
		var decoded struct {
			Key string `json:"_key"`
		}
		if err := json.Unmarshal(doc, &decoded); err != nil {
			return err
		}
		f.docs[collection][decoded.Key] = append(json.RawMessage(nil), doc...)
	}
	return nil
}

func (*fieldSourceMembershipTestClient) ExecuteAQL(context.Context, string, map[string]any) error {
	return nil
}

func TestPrepareFieldSourceMembershipUsesOneExpandedArrayIndex(t *testing.T) {
	client := &fieldSourceMembershipTestClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.PrepareFieldSourceMembership(context.Background()); err != nil {
		t.Fatal(err)
	}
	var membershipFound, buildFound bool
	for _, collection := range client.specs[0].Collections {
		switch collection.Name {
		case catalog.FieldSourceMembershipCollection:
			membershipFound = true
			if !reflect.DeepEqual(collection.Indexes, [][]string{{"project", "dataset_generation", "resource_type", "scalar_paths[*]"}}) {
				t.Fatalf("membership indexes=%v, want one scoped expanded-array index", collection.Indexes)
			}
		case catalog.FieldSourceMembershipBuildCollection:
			buildFound = true
			if collection.Truncate {
				t.Fatal("field-source completeness marker collection must persist across loads")
			}
		}
	}
	if !membershipFound || !buildFound {
		t.Fatalf("bootstrap collections: membership=%v build=%v", membershipFound, buildFound)
	}
}

func TestWriteFieldSourceMembershipsReplacesDeterministicKeysOnReplay(t *testing.T) {
	client := &fieldSourceMembershipTestClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	membership, err := catalog.NewFieldSourceMembership("project", "generation", "scope", "Observation", "Observation/g_key", map[string]any{"valueString": "sparse"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := adapter.WriteFieldSourceMemberships(context.Background(), []catalog.FieldSourceMembership{membership}, 10); err != nil {
			t.Fatal(err)
		}
	}
	if len(client.inserts) != 2 {
		t.Fatalf("sidecar writes=%d, want replayed write twice", len(client.inserts))
	}
	for _, insert := range client.inserts {
		if insert.collection != catalog.FieldSourceMembershipCollection || !insert.overwrite || len(insert.docs) != 1 {
			t.Fatalf("sidecar insert=%+v, want one replace-mode deterministic write", insert)
		}
	}
	if len(client.docs[catalog.FieldSourceMembershipCollection]) != 1 {
		t.Fatalf("replayed sidecars persisted %d keys, want one", len(client.docs[catalog.FieldSourceMembershipCollection]))
	}
}

func TestFieldSourceMembershipBuildRoundTripsIndependentCompleteness(t *testing.T) {
	client := &fieldSourceMembershipTestClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	build := catalog.NewFieldSourceMembershipBuild("project", "generation")
	build.State = catalog.FieldSourceMembershipComplete
	build.ScannedResources = 42
	build.Checkpoint = catalog.FieldSourceMembershipCheckpoint{}
	if err := adapter.WriteFieldSourceMembershipBuild(context.Background(), build); err != nil {
		t.Fatal(err)
	}
	got, found, err := adapter.ReadFieldSourceMembershipBuild(context.Background(), "project", "generation")
	if err != nil || !found {
		t.Fatalf("read marker found=%v err=%v", found, err)
	}
	if got.SchemaVersion != catalog.FieldSourceMembershipSchemaVersion || got.State != catalog.FieldSourceMembershipComplete || got.ScannedResources != 42 {
		t.Fatalf("field-source marker=%+v", got)
	}
}
