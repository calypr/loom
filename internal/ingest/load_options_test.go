package ingest

import (
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestNormalizeLoadOptionsUsesBoundedConcurrencyDefaults(t *testing.T) {
	got := normalizeLoadOptions(LoadOptions{WriterCount: 3})
	if got.WorkerCount != 3 {
		t.Fatalf("worker count = %d, want writer count 3", got.WorkerCount)
	}
	if got.LineQueueSize <= 0 || got.WriteQueueSize <= 0 {
		t.Fatalf("queue sizes = line %d write %d, want positive bounded defaults", got.LineQueueSize, got.WriteQueueSize)
	}
	if got.CatalogLimits.MaxDistinctValuesPerField <= 0 || got.CatalogLimits.MaxShapePlans <= 0 {
		t.Fatalf("catalog limits = %#v, want finite defaults", got.CatalogLimits)
	}
}

func TestNormalizeLoadOptionsPreservesExplicitConcurrencyAndCatalogLimits(t *testing.T) {
	got := normalizeLoadOptions(LoadOptions{
		WriterCount:   1,
		WorkerCount:   1,
		LineQueueSize: 32,
		WriteQueueSize: 4,
		CatalogLimits: catalog.ProfileLimits{
			MaxFields:                 4,
			MaxDistinctValuesPerField: 2,
			MaxDistinctValueBytes:     8,
			MaxPivotColumnsPerField:   2,
			MaxExtensionValuesPerField: 2,
			MaxShapePlans:             1,
		},
	})
	if got.WorkerCount != 1 || got.LineQueueSize != 32 || got.WriteQueueSize != 4 {
		t.Fatalf("normalized concurrency = %#v, want explicit values", got)
	}
	if got.CatalogLimits.MaxDistinctValuesPerField != 2 {
		t.Fatalf("normalized catalog limits = %#v, want explicit value", got.CatalogLimits)
	}
}
