package arango

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

// A registered recipe name must stay re-registerable with correct parent
// chaining. The currentDigest AQL projection returned a bare scalar
// (RETURN doc.digest); the arango driver cursor decoder requires object rows,
// so EVERY second and later registration of a name died on a raw driver
// decode error and surfaced as INTERNAL_ERROR 500 (retryable=false) at the
// GraphQL boundary — recipe authoring save loops could never complete.
// The fake below reproduces the driver contract (only object rows decode);
// the legacy fake in revisions_test.go hand-synthesized object rows for any
// projection, which is why this defect shipped green.

type driverFaithfulClient struct {
	docs []map[string]any
}

func (c *driverFaithfulClient) EnsureDatabase(context.Context) error { return nil }
func (c *driverFaithfulClient) EnsureCollections(context.Context, arangostore.BootstrapSpec) error {
	return nil
}
func (c *driverFaithfulClient) InsertBatchRaw(_ context.Context, collection string, docs []json.RawMessage, _ bool, _ string) error {
	if collection != RevisionCollection {
		return errors.New("unexpected collection " + collection)
	}
	for _, raw := range docs {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		c.docs = append(c.docs, doc)
	}
	return nil
}

func (c *driverFaithfulClient) QueryRows(_ context.Context, query string, _ int, vars map[string]any, visit arangostore.RowVisitor) error {
	if got, _ := vars["@collection"].(string); got != RevisionCollection {
		return nil
	}
	project, name := stringVar(vars, "project"), stringVar(vars, "name")
	digest, hasDigest := vars["digest"].(string)
	scalar := strings.Contains(query, "RETURN doc.digest")
	for _, doc := range c.docs {
		if doc["project"] != project || doc["name"] != name {
			continue
		}
		if hasDigest && doc["digest"] != digest {
			continue
		}
		if scalar {
			// Real driver behavior: scalar rows fail to decode into map[string]any.
			return errors.New("read arango query cursor: json: cannot unmarshal string into Go value of type map[string]interface {}")
		}
		row := map[string]any{"digest": doc["digest"], "project": doc["project"], "name": doc["name"]}
		if err := visit(row); err != nil {
			return err
		}
		return nil
	}
	return nil
}

func stringVar(vars map[string]any, key string) string {
	if v, ok := vars[key].(string); ok {
		return v
	}
	return ""
}

func bundleForRevTest(name, translationVersion string) recipe.Bundle {
	return recipe.Bundle{RecipeSchemaVersion: 1, Name: name, TranslationVersion: translationVersion, Outputs: []recipe.Output{{Name: "rows", RootResourceType: "Patient", RowGrain: "patient"}}}
}

func registerForTest(t *testing.T, reg *RevisionRegistry, name, tv, parent string) (recipe.RecipeRevision, error) {
	t.Helper()
	return reg.Register(context.Background(), "HTAN_INT", bundleForRevTest(name, tv), parent)
}

func TestSecondRegisterOfExistingNameIsClassifiedNotInternal(t *testing.T) {
	reg := &RevisionRegistry{client: &driverFaithfulClient{}, batchSize: 32}
	first, err := registerForTest(t, reg, "rows-a", "1", "")
	if err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	// Exact duplicate without parent: the current-parent contract must be
	// reported as a user fault (STALE_CURSOR), never as an untyped internal.
	_, err = registerForTest(t, reg, "rows-a", "1", "")
	userErr, ok := classifiedUserFault(err)
	if !ok {
		t.Fatalf("duplicate registration escaped untyped (boundary serves INTERNAL_ERROR 500): %v", err)
	}
	if userErr.Code() != "STALE_CURSOR" {
		t.Fatalf("duplicate registration code = %s, want STALE_CURSOR", userErr.Code())
	}
	// Stale parent likewise stays a typed user fault.
	if _, err := registerForTest(t, reg, "rows-a", "2", strings.Repeat("f", 64)); !isStaleCursor(err) {
		t.Fatalf("stale parent must be STALE_CURSOR, got %v", err)
	}
	// Correct parent chains normally.
	child, err := registerForTest(t, reg, "rows-a", "2", first.Digest)
	if err != nil {
		t.Fatalf("correct-parent child registration failed: %v", err)
	}
	if child.Parent != first.Digest {
		t.Fatalf("child parent = %s, want %s", child.Parent, first.Digest)
	}
}

func classifiedUserFault(err error) (interface {
	Code() string
	Retryable() bool
}, bool) {
	if err == nil {
		return nil, false
	}
	return dataframeerrors.AsUserError(err)
}

func isStaleCursor(err error) bool {
	userErr, ok := classifiedUserFault(err)
	return ok && userErr.Code() == "STALE_CURSOR"
}
