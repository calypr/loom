package arango

import (
	"context"
	"errors"
	"testing"

	driver "github.com/arangodb/go-driver/v2/arangodb"
)

type revisionTestDatabase struct {
	driver.Database
	collection driver.Collection
	name       string
	err        error
}

func (d *revisionTestDatabase) GetCollection(_ context.Context, name string, _ *driver.GetCollectionOptions) (driver.Collection, error) {
	d.name = name
	return d.collection, d.err
}

type revisionTestCollection struct {
	driver.Collection
	properties driver.CollectionProperties
	err        error
}

func (c revisionTestCollection) Revision(context.Context) (driver.CollectionProperties, error) {
	return c.properties, c.err
}

func TestClientCollectionRevisionReadsCollectionRevision(t *testing.T) {
	collection := revisionTestCollection{properties: driver.CollectionProperties{Revision: "revision-7"}}
	database := &revisionTestDatabase{collection: collection}
	client := &Client{db: database}

	revision, err := client.CollectionRevision(context.Background(), "Observation")
	if err != nil {
		t.Fatal(err)
	}
	if revision != "revision-7" || database.name != "Observation" {
		t.Fatalf("revision=%q collection=%q, want revision-7/Observation", revision, database.name)
	}
}

func TestClientCollectionRevisionReturnsWrappedLookupErrors(t *testing.T) {
	want := errors.New("collection unavailable")
	client := &Client{db: &revisionTestDatabase{err: want}}
	if _, err := client.CollectionRevision(context.Background(), "Observation"); !errors.Is(err, want) {
		t.Fatalf("error=%v, want wrapped collection lookup error", err)
	}
}
