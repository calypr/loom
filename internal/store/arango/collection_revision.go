package arango

import "context"

// CollectionRevision changes after any write. Derived graph readers use it to
// reject a graph built while its inputs changed, including edge-only updates.
func (c *Client) CollectionRevision(ctx context.Context, name string) (string, error) {
	collection, err := c.db.GetCollection(ctx, name, nil)
	if err != nil {
		return "", err
	}
	properties, err := collection.Revision(ctx)
	return properties.Revision, err
}
