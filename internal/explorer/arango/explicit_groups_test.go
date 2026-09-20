package arango

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

func TestPutExplicitGroupDefinitionsUsesObjectRows(t *testing.T) {
	ctx := context.Background()
	revision := explorer.ExplicitGroupRevision{
		ID: explorer.ExplicitGroupRevisionIDFor("project-a", "groups-a"), Project: "project-a",
		Generation: "generation-a", ScopeDigest: "scope-a", ResourceType: "Patient",
		SourceSelectionRevisionID: "selection-a", SourceMembershipDigest: "source-membership-a",
		State: explorer.ExplicitGroupRevisionStaging, IdempotencyKey: "groups-a", CreatedAt: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
	header, err := document(revision, string(revision.ID))
	if err != nil {
		t.Fatal(err)
	}
	header["writerToken"] = "writer-a"
	client := &explicitGroupDefinitionClient{header: header}
	repository, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	want := []explorer.ExplicitGroupDefinition{{ID: "group-empty", Label: "Empty group", Ordinal: 0}}
	if err := repository.PutExplicitGroupDefinitions(ctx, revision.ID, "writer-a", want); err != nil {
		t.Fatalf("put definitions: %v", err)
	}
	if !client.definitionWriteReturnedObject {
		t.Fatal("definition UPSERT did not return a map-shaped row")
	}
	definitionDigest, err := explorer.ExplicitGroupDefinitionDigest(want)
	if err != nil {
		t.Fatal(err)
	}
	membershipDigest, err := explorer.ExplicitGroupMembershipDigest(revision, want, nil)
	if err != nil {
		t.Fatal(err)
	}
	completed := revision
	completed.State = explorer.ExplicitGroupRevisionComplete
	completed.DefinitionDigest = definitionDigest
	completed.MembershipDigest = membershipDigest
	completed.GroupCount = 1
	completedAt := time.Date(2026, time.January, 1, 0, 1, 0, 0, time.UTC)
	completed.CompletedAt = &completedAt
	client.header, err = document(completed, string(completed.ID))
	if err != nil {
		t.Fatal(err)
	}
	got, err := repository.ListExplicitGroupDefinitions(ctx, revision.Project, revision.ID)
	if err != nil {
		t.Fatalf("list definitions: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("definitions = %#v, want %#v", got, want)
	}
}

type explicitGroupDefinitionClient struct {
	header                        map[string]any
	definitions                   []map[string]any
	definitionWriteReturnedObject bool
}

func (c *explicitGroupDefinitionClient) WithTransaction(ctx context.Context, _ store.TransactionCollections, fn store.TransactionFunc) error {
	return fn(ctx, c)
}

func (c *explicitGroupDefinitionClient) QueryRows(_ context.Context, query string, _ int, binds map[string]any, visit store.RowVisitor) error {
	switch {
	case strings.Contains(query, "UPDATE d WITH {writerExpiresAt: @expiresAt}"):
		return visit(map[string]any{"renewed": true})
	case strings.Contains(query, "SORT g.ordinal ASC, g.groupId ASC"):
		for _, definition := range c.definitions {
			if err := visit(definition); err != nil {
				return err
			}
		}
		return nil
	case strings.Contains(query, "FOR doc IN @docs"):
		if !strings.Contains(query, "RETURN {key: NEW._key}") {
			return fmt.Errorf("definition UPSERT must return an object row for QueryRows")
		}
		c.definitionWriteReturnedObject = true
		docs, ok := binds["docs"].([]map[string]any)
		if !ok {
			return fmt.Errorf("definition UPSERT docs have type %T", binds["docs"])
		}
		for _, definition := range docs {
			c.definitions = append(c.definitions, definition)
			if err := visit(map[string]any{"key": definition["_key"]}); err != nil {
				return err
			}
		}
		return nil
	case strings.Contains(query, "RETURN d"):
		return visit(c.header)
	default:
		return fmt.Errorf("unexpected explicit-group query: %s", query)
	}
}
