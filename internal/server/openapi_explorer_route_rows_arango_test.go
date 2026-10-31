package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

// Uses real HTTP routes/compiler/executor, literal graph data, and the actual
// Arango Explorer/history repository in a fresh owned database. It proves stored
// read-back across repository instances, not a database-process restart.
func TestSavedRelatedRouteRowsAgainstArango(t *testing.T) {
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to the owned disposable Arango service")
	}
	database := "loom_test_route_roundtrip_" + uuid.NewString()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	client, err := storearango.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		request, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
		if err != nil {
			t.Error(err)
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Error(err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("remove owned disposable database status=%d", response.StatusCode)
		}
	})
	if err := client.Bootstrap(ctx, storearango.BootstrapSpec{Collections: []storearango.CollectionSpec{{Name: "Patient"}, {Name: "Observation"}, {Name: "Group"}, {Name: "Specimen"}, {Name: "fhir_edge", Edge: true}}}); err != nil {
		t.Fatal(err)
	}
	insert := func(collection string, documents []map[string]any) {
		t.Helper()
		raw := make([]json.RawMessage, 0, len(documents))
		for _, document := range documents {
			value, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, value)
		}
		if err := client.InsertBatchRaw(ctx, collection, raw, false, "document"); err != nil {
			t.Fatal(err)
		}
	}
	for collection, ids := range map[string][]string{"Patient": {"p1", "p2"}, "Observation": {"o1", "o2"}, "Group": {"g1"}, "Specimen": {"s1"}} {
		var documents []map[string]any
		for _, id := range ids {
			documents = append(documents, map[string]any{"_key": id, "id": id, "project": "project-a", "project_id": "project-a", "dataset_generation": "generation-a", "resourceType": collection, "payload": map[string]any{"id": id, "resourceType": collection}})
		}
		insert(collection, documents)
	}
	var edges []map[string]any
	for i, edge := range []struct{ from, to, label, fromType, toType string }{
		{"Observation/o1", "Patient/p1", "subject_Patient", "Observation", "Patient"},
		{"Observation/o2", "Patient/p2", "subject_Patient", "Observation", "Patient"},
		{"Group/g1", "Patient/p1", "member_entity_Patient", "Group", "Patient"},
		{"Specimen/s1", "Group/g1", "subject_Group", "Specimen", "Group"},
		{"Observation/o2", "Specimen/s1", "specimen_Specimen", "Observation", "Specimen"},
	} {
		edges = append(edges, map[string]any{"_key": fmt.Sprintf("edge_%d", i), "_from": edge.from, "_to": edge.to, "label": edge.label, "from_type": edge.fromType, "to_type": edge.toType, "project": "project-a", "project_id": "project-a", "dataset_generation": "generation-a"})
	}
	insert("fhir_edge", edges)
	savedRelatedRouteRoundtrip(t, client)
}
