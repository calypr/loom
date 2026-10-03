package arango

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	storepkg "github.com/calypr/loom/internal/store/arango"
)

type interpretationClient struct {
	libraries map[string]map[string]any
	revisions map[string]map[string]any
	queries   []queryCall
}

func (c *interpretationClient) WithTransaction(ctx context.Context, _ storepkg.TransactionCollections, fn storepkg.TransactionFunc) error {
	return fn(ctx, c)
}

func (c *interpretationClient) QueryRows(_ context.Context, query string, _ int, binds map[string]any, visit storepkg.RowVisitor) error {
	c.queries = append(c.queries, queryCall{query: query, binds: binds})
	if strings.Contains(query, "SORT d.id ASC") {
		for _, value := range c.libraries {
			if err := visit(value); err != nil {
				return err
			}
		}
		return nil
	}
	if strings.Contains(query, "d._key == @key AND d.id == @id") {
		if value := c.revisions[binds["key"].(string)]; value != nil && value["project"] == binds["project"] {
			return visit(value)
		}
		return nil
	}
	if strings.Contains(query, "LET library = FIRST(") {
		if c.libraries == nil {
			c.libraries = map[string]map[string]any{}
		}
		if c.revisions == nil {
			c.revisions = map[string]map[string]any{}
		}
		doc := binds["revision"].(map[string]any)
		key := binds["revisionKey"].(string)
		existing := c.revisions[key]
		status := "create"
		if existing != nil {
			if reflect.DeepEqual(existing["identity"], binds["identity"]) {
				status = "idempotent"
			} else {
				status = "collision"
			}
		} else {
			library := c.libraries[binds["libraryKey"].(string)]
			expected := binds["expectedParent"].(string)
			current := ""
			if library != nil {
				current, _ = library["headRevisionId"].(string)
			}
			parentMatches := true
			if parentID := binds["parentId"].(string); parentID != "" {
				parent := c.revisions[binds["parentKey"].(string)]
				parentMatches = parent != nil && parent["libraryId"] == binds["libraryId"] && parent["contentDigest"] == binds["parentDigest"]
			}
			if (library == nil && expected != "") || (library != nil && current != expected) || !parentMatches {
				status = "parent_conflict"
			}
		}
		response := map[string]any{"status": status}
		switch status {
		case "create":
			c.revisions[key] = doc
			library := binds["library"].(map[string]any)
			if old := c.libraries[binds["libraryKey"].(string)]; old != nil {
				library = mergeMap(old, binds["libraryPatch"].(map[string]any))
			}
			c.libraries[binds["libraryKey"].(string)] = library
			response["revision"] = doc
		case "idempotent":
			response["existing"] = existing
		}
		return visit(response)
	}
	return nil
}

func mergeMap(old, patch map[string]any) map[string]any {
	merged := map[string]any{}
	for key, value := range old {
		merged[key] = value
	}
	for key, value := range patch {
		merged[key] = value
	}
	return merged
}

func interpretationTestRevision(project string) explorer.InterpretationRevision {
	return explorer.InterpretationRevision{
		Project: project, LibraryID: "library-a", Author: "author", Explanation: "reason",
		Rules: []explorer.InterpretationRule{{ID: "rule-a", Match: explorer.InterpretationStructuralMatch{ResourceType: "Observation"}, Definition: explorer.InterpretationFeatureDefinition{Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "status", ProjectionMode: "VALUE"}}}}},
	}
}

func TestInterpretationArangoCreateRetryCollisionParentConflictAndIsolation(t *testing.T) {
	client := &interpretationClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := adapter.CreateInterpretationRevision(ctx, interpretationTestRevision("project-a"), nil)
	if err != nil {
		t.Fatal(err)
	}
	retried, err := adapter.CreateInterpretationRevision(ctx, interpretationTestRevision("project-a"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != retried.ID || first.ContentDigest != retried.ContentDigest {
		t.Fatalf("retry changed revision: %#v %#v", first, retried)
	}
	stale := interpretationTestRevision("project-a")
	stale.Explanation = "a different proposal"
	if _, err := adapter.CreateInterpretationRevision(ctx, stale, nil); !errors.Is(err, explorer.ErrInterpretationParentConflict) {
		t.Fatalf("stale root error=%v", err)
	}
	// A persisted key collision with different immutable identity fails closed.
	prepared, err := explorer.PrepareInterpretationRevision(interpretationTestRevision("project-a"))
	if err != nil {
		t.Fatal(err)
	}
	client.revisions[string(prepared.ID)] = map[string]any{"_key": string(prepared.ID), "id": string(prepared.ID), "project": prepared.Project, "identity": map[string]any{"different": true}}
	if _, err := adapter.CreateInterpretationRevision(ctx, interpretationTestRevision("project-a"), nil); !errors.Is(err, explorer.ErrImmutableInterpretation) {
		t.Fatalf("immutable collision error=%v", err)
	}
	if _, err := adapter.GetInterpretationRevision(ctx, "project-b", first.ID); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("cross-project get=%v", err)
	}
	libs, err := adapter.ListInterpretationLibraries(ctx, "project-a")
	if err != nil || len(libs) != 1 || libs[0].Project != "project-a" {
		t.Fatalf("libraries=%#v err=%v", libs, err)
	}
	for _, call := range client.queries {
		if strings.Contains(call.query, "Interpretation") {
			continue
		}
		if strings.Contains(call.query, "@c") && strings.Contains(call.query, "d.project") {
			if _, ok := call.binds["project"]; !ok {
				t.Fatalf("project scope missing from query %q", call.query)
			}
		}
	}
}

func TestInterpretationArangoAdvancesOnlyFromExactParent(t *testing.T) {
	client := &interpretationClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	parent, err := adapter.CreateInterpretationRevision(ctx, interpretationTestRevision("project-a"), nil)
	if err != nil {
		t.Fatal(err)
	}
	childInput := interpretationTestRevision("project-a")
	childInput.ParentRevisionID = &parent.ID
	childInput.ParentDigest = &parent.ContentDigest
	childInput.Explanation = "approved child mapping"
	child, err := adapter.CreateInterpretationRevision(ctx, childInput, &parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if child.ID == parent.ID || child.ParentRevisionID == nil || *child.ParentRevisionID != parent.ID {
		t.Fatalf("child=%#v parent=%#v", child, parent)
	}
	library := client.libraries[interpretationLibraryKey("project-a", parent.LibraryID)]
	if got := library["headRevisionId"]; got != string(child.ID) {
		t.Fatalf("headRevisionId=%v, want %s", got, child.ID)
	}

	wrongDigest := explorer.InterpretationContentDigest("sha256:" + strings.Repeat("0", 64))
	grandchild := interpretationTestRevision("project-a")
	grandchild.ParentRevisionID = &child.ID
	grandchild.ParentDigest = &wrongDigest
	grandchild.Explanation = "must not be accepted"
	if _, err := adapter.CreateInterpretationRevision(ctx, grandchild, &child.ID); !errors.Is(err, explorer.ErrInterpretationParentConflict) {
		t.Fatalf("wrong parent digest error=%v", err)
	}
	if got := library["headRevisionId"]; got != string(child.ID) {
		t.Fatalf("failed child changed headRevisionId=%v", got)
	}
}

func TestInterpretationArangoQueriesAreExactProjectScoped(t *testing.T) {
	client := &interpretationClient{libraries: map[string]map[string]any{}, revisions: map[string]map[string]any{}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ListInterpretationLibraries(context.Background(), "program/project"); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.GetInterpretationRevision(context.Background(), "program/project", "revision-a"); err != nil && !errors.Is(err, explorer.ErrNotFound) {
		t.Fatal(err)
	}
	if len(client.queries) != 2 {
		t.Fatalf("queries=%d", len(client.queries))
	}
	if !strings.Contains(client.queries[0].query, "d.project == @project") || client.queries[0].binds["project"] != "program/project" {
		t.Fatalf("list query=%#v", client.queries[0])
	}
	if !strings.Contains(client.queries[1].query, "d._key == @key AND d.id == @id AND d.project == @project") {
		t.Fatalf("get query=%q", client.queries[1].query)
	}
}
