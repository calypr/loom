package arango

import (
	"context"
	"strings"
	"time"

	"github.com/calypr/loom/internal/explorer"
)

func (s *Store) SaveDraft(ctx context.Context, e explorer.Explorer, expected int64, expectedDigest ...string) (*explorer.Explorer, error) {
	e.UpdatedAt = time.Now().UTC()
	raw, err := document(e, explorerKey(e.Project, e.ExplorerID))
	if err != nil {
		return nil, err
	}
	digest := ""
	if len(expectedDigest) > 0 {
		digest = strings.TrimSpace(expectedDigest[0])
	}
	var out *explorer.Explorer
	err = s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key AND (d.draftVersion == @expected OR (!HAS(d, "draftVersion") AND @expected == 0)) AND (@expectedDigest == "" OR d.draftDigest == @expectedDigest) UPDATE d WITH MERGE(@doc, { draftVersion: NOT_NULL(d.draftVersion, 0) + 1 }) IN @@c RETURN NEW`, 1, map[string]any{"@c": ExplorersCollection, "key": explorerKey(e.Project, e.ExplorerID), "expected": expected, "expectedDigest": digest, "doc": raw}, func(row map[string]any) error { v, err := decode[explorer.Explorer](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrDraftConflict
	}
	return out, nil
}
