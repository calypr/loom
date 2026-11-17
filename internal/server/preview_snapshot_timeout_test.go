package server

import (
	"context"
	"testing"
	"time"
)

func TestSnapshotPreviewQueryHasRuntimeBudget(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want time.Duration
	}{
		{"ordinary", context.Background(), explorerPreviewTimeout},
		{"pivot", withPivotPreviewQueryBudget(context.Background()), pivotCategoryScanTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := withPreviewQueryTimeout(func(ctx context.Context, _ string, _ int, _ map[string]any, _ func(map[string]any) error) error {
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("snapshot query has no deadline")
				}
				remaining := time.Until(deadline)
				if remaining > tc.want || remaining < tc.want-time.Second {
					t.Fatalf("query budget = %s, want %s", remaining, tc.want)
				}
				return nil
			})
			if err := query(tc.ctx, "RETURN 1", 1, nil, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
