package generation

import (
	"testing"

	"github.com/calypr/loom/internal/catalog/fieldprofile"
)

func TestNormalizeLoadOptionsUsesBoundedConcurrencyDefaults(t *testing.T) {
	got := normalizeLoadOptions(LoadOptions{WriterCount: 3})
	if got.WorkerCount != 3 {
		t.Fatalf("worker count = %d, want writer count 3", got.WorkerCount)
	}
	if got.LineQueueSize <= 0 || got.WriteQueueSize != 2 {
		t.Fatalf("queue sizes = line %d write %d, want positive line limit and two write batches", got.LineQueueSize, got.WriteQueueSize)
	}
	if got.CatalogLimits.MaxDistinctValuesPerField <= 0 || got.CatalogLimits.MaxShapePlans <= 0 {
		t.Fatalf("catalog limits = %#v, want finite defaults", got.CatalogLimits)
	}
}

func TestNormalizeLoadOptionsPreservesExplicitConcurrencyAndCatalogLimits(t *testing.T) {
	got := normalizeLoadOptions(LoadOptions{
		WriterCount:    1,
		WorkerCount:    1,
		LineQueueSize:  32,
		WriteQueueSize: 4,
		CatalogLimits: fieldprofile.ProfileLimits{
			MaxFields:                  4,
			MaxDistinctValuesPerField:  2,
			MaxDistinctValueBytes:      8,
			MaxPivotColumnsPerField:    2,
			MaxExtensionValuesPerField: 2,
			MaxShapePlans:              1,
		},
	})
	if got.WorkerCount != 1 || got.LineQueueSize != 32 || got.WriteQueueSize != 4 {
		t.Fatalf("normalized concurrency = %#v, want explicit values", got)
	}
	if got.CatalogLimits.MaxDistinctValuesPerField != 2 {
		t.Fatalf("normalized catalog limits = %#v, want explicit value", got.CatalogLimits)
	}
}

func TestNormalizeLoadOptions(t *testing.T) {
	got := normalizeLoadOptions(LoadOptions{})
	if got.BatchSize != 5000 || got.ProgressEvery != 50000 || got.WriterCount != 8 || got.FileConcurrency != 2 || got.WriteAPI != "import" {
		t.Fatalf("normalizeLoadOptions defaults = %+v", got)
	}

	got = normalizeLoadOptions(LoadOptions{BatchSize: 13, ProgressEvery: 17, WriterCount: 3, FileConcurrency: 4, WriteAPI: "documents"})
	if got.BatchSize != 13 || got.ProgressEvery != 17 || got.WriterCount != 3 || got.FileConcurrency != 4 || got.WriteAPI != "documents" {
		t.Fatalf("normalizeLoadOptions overwrote explicit values: %+v", got)
	}
}
