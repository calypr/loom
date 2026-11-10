package execution

import (
	"context"

	"github.com/calypr/loom/internal/dataframe/compiler"
)

// Neutral preview observation contracts (arch-backend-02).
//
// Shared preview orchestration consumes only the types in this file. Each
// backend adapter decodes its own vendor plan and index-listing formats
// (Arango explain results, driver index metadata, pgx plan JSON) into these
// shapes at the adapter boundary; execution never imports or interprets a
// concrete vendor plan or index type. Optional evidence is advisory:
// missing, stale, or failed evidence must never block a bounded preview,
// and only execution owns the policy that decides when an optimization is
// certified.

// PreviewCollectionIndex is one persistent covering index observed on a
// source collection, already narrowed by the owning backend adapter to the
// compiler-owned preview indexes it manages. Its JSON encoding feeds the
// in-process group-projection decision key, so the field set is
// identity-bearing: a definition whose fields or stored values differ must
// produce a different decision.
type PreviewCollectionIndex struct {
	Collection   string
	Name         string
	Fields       []string
	StoredValues []string
}

// PreviewIndexCoverage classifies what the backend reported about whether
// one index scan node covers the projected fields. The tri-state is
// deliberate: a backend that omits either coverage flag, or reports the two
// flags inconsistently, reports Unknown, which certifies neither the
// covering optimization nor the broad-scan decision.
type PreviewIndexCoverage uint8

const (
	// PreviewIndexCoverageUnknown means the backend did not report a
	// complete, consistent coverage pair.
	PreviewIndexCoverageUnknown PreviewIndexCoverage = iota
	// PreviewIndexCoverageCovering means the backend reported that the
	// node covers both the filtered and the projected fields.
	PreviewIndexCoverageCovering
	// PreviewIndexCoverageNotCovering means the backend reported that the
	// node covers neither the filtered nor the projected fields.
	PreviewIndexCoverageNotCovering
)

// PreviewIndexNodeEvidence is one decoded index scan node on the queried
// collection: the index names the backend attributed to that node for the
// collection, and the node's coverage classification.
type PreviewIndexNodeEvidence struct {
	IndexNames []string
	Coverage   PreviewIndexCoverage
}

// PreviewCollectionScanEvidence is the adapter-decoded classification of the
// scans one compiled preview command performs on one collection. Adapters
// filter nodes to the queried collection (a node reports for the collection
// when its collection matches, using the empty string where the backend
// omits one) and drop node kinds that carry no scan meaning for preview
// policy. A command whose plan an adapter cannot decode at all yields the
// zero value.
type PreviewCollectionScanEvidence struct {
	// FullCollectionEnumeration is set when the backend plans a full
	// enumeration of the queried collection for this command.
	FullCollectionEnumeration bool
	// IndexNodes lists the index scan nodes for the queried collection in
	// the backend's own plan order.
	IndexNodes []PreviewIndexNodeEvidence
}

// PreviewCollectionScans is the optional preview-inspection capability: it
// answers what scans the backend's optimizer plans for one rendered preview
// command on one collection. Execution hands the adapter the parent-tagged
// command it would run and never loose query text or bind maps; the adapter
// rejects a command whose tag does not name its backend before any
// inspection call, decodes the vendor plan, and returns only neutral
// evidence. A nil capability (or an error) leaves execution on the
// canonical bounded fallback.
type PreviewCollectionScans func(context.Context, compiler.CompiledCommand, string) (PreviewCollectionScanEvidence, error)
