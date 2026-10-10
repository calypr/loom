package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
	"github.com/gofiber/fiber/v3"
)

func TestConstructionInputsGeneratedHTTPRouteReturnsExactCatalog(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	service, reader, bundleCatalog, snapshot, digest := newConstructionInputsFixture(t, scope)
	selector := dataset.DataframeSelector{Recipe: "lab_summary", TranslationVersion: "v1", Output: "observations"}
	execution := constructionInputsExecution("revision-current", selector, "project-a", "generation-a", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	bundleCatalog.executions = []publication.BundleExecution{execution}
	bundleCatalog.pointers[execution.PointerName()] = publication.BundlePointer{Name: execution.PointerName(), ExecutionID: execution.ID}
	reader.values[execution.ID+"/"+selector.Output] = constructionInputsMaterialization(execution, selector, scope)

	app := fiber.New()
	routes := &HTTPRoutes{explorer: &explorerHTTPHandlers{
		authorizeRead:      func(context.Context, *authscope.Principal, string) error { return nil },
		constructionInputs: service,
	}}
	loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(routes, nil), loomapi.FiberServerOptions{})
	response := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/builder-a/authoring/v2/construction-inputs", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q}`, snapshot.Token, digest,
	))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("construction inputs status=%d body=%s", response.StatusCode, response.Body)
	}
	var catalog loomapi.ConstructionInputsResponse
	if err := json.Unmarshal([]byte(response.Body), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.SnapshotToken != snapshot.Token || catalog.DatasetGeneration != "generation-a" || catalog.DraftVersion != 1 || catalog.DraftDigest != digest || len(catalog.Entries) != 1 {
		t.Fatalf("catalog snapshot/draft binding = %#v", catalog)
	}
	entry := catalog.Entries[0]
	if entry.Kind != loomapi.ConstructionInputRevisionKindTABLEREVISION || entry.TableId != selector.Key() || entry.RevisionId != execution.ID || entry.OutputId != selector.Output || !entry.IsCurrent {
		t.Fatalf("catalog exact revision identity = %#v", entry)
	}
	if len(entry.Columns) != 1 || entry.Columns[0].Id != "stable-lab-value" || entry.Columns[0].ClickhouseType != "Nullable(Float64)" || !entry.Columns[0].Nullable {
		t.Fatalf("catalog stable public columns = %#v", entry.Columns)
	}
}
