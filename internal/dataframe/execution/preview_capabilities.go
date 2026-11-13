package execution

import (
	"context"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
)

type PreviewCollectionIndex struct {
	Collection   string
	Name         string
	Fields       []string
	StoredValues []string
}

type PreviewIndexCoverage uint8

const (
	PreviewIndexCoverageUnknown PreviewIndexCoverage = iota
	PreviewIndexCoverageCovering
	PreviewIndexCoverageNotCovering
)

type PreviewIndexNodeEvidence struct {
	IndexNames []string
	Coverage   PreviewIndexCoverage
}

type PreviewCollectionScanEvidence struct {
	FullCollectionEnumeration bool
	IndexNodes                []PreviewIndexNodeEvidence
}

type PreviewCollectionScans func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error)
