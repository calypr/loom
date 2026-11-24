package ir

import (
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

type legacyPhysicalOperation struct {
	Kind                      PhysicalOperationKind
	Source                    PhysicalSource
	RootScan                  *PhysicalRootScan
	Traversal                 *PhysicalTraversal
	Filter                    *PhysicalFilter
	DerivedLet                *PhysicalDerivedLet
	ExpressionLet             *PhysicalExpressionLet
	Set                       *PhysicalSet
	Unnest                    *PhysicalUnnest
	Sort                      *PhysicalSort
	Limit                     *PhysicalLimit
	Return                    *PhysicalReturn
	PathSeed                  *PhysicalPathSeed
	PathExtend                *PhysicalPathExtend
	GraphReturn               *PhysicalGraphReturn
	CollectionScan            *PhysicalCollectionScan
	KeySetLookup              *PhysicalKeySetLookup
	DocumentLookup            *PhysicalDocumentLookup
	PopulationMappingReturn   *PhysicalPopulationMappingReturn
	CellTraceReturn           *PhysicalCellTraceReturn
	TableShapeExclusionReturn any
	GroupRows                 *PhysicalGroupRows
	GroupedPivot              *PhysicalGroupedPivot
	Unpivot                   *PhysicalUnpivot
}

var legacyPhysicalOperations = jsonv2.WithMarshalers(jsonv2.MarshalToFunc(
	func(enc *jsontext.Encoder, operation PhysicalOperation) error {
		return jsonv2.MarshalEncode(enc, legacyPhysicalOperation{
			Kind:                    operation.Kind,
			Source:                  operation.Source,
			RootScan:                operation.RootScan,
			Traversal:               operation.Traversal,
			Filter:                  operation.Filter,
			DerivedLet:              operation.DerivedLet,
			ExpressionLet:           operation.ExpressionLet,
			Set:                     operation.Set,
			Unnest:                  operation.Unnest,
			Sort:                    operation.Sort,
			Limit:                   operation.Limit,
			Return:                  operation.Return,
			PathSeed:                operation.PathSeed,
			PathExtend:              operation.PathExtend,
			GraphReturn:             operation.GraphReturn,
			CollectionScan:          operation.CollectionScan,
			KeySetLookup:            operation.KeySetLookup,
			DocumentLookup:          operation.DocumentLookup,
			PopulationMappingReturn: operation.PopulationMappingReturn,
			CellTraceReturn:         operation.CellTraceReturn,
			GroupRows:               operation.GroupRows,
			GroupedPivot:            operation.GroupedPivot,
			Unpivot:                 operation.Unpivot,
		})
	},
))

func MarshalLegacyPhysical(value any) ([]byte, error) {
	return jsonv2.Marshal(value, json.DefaultOptionsV1(), legacyPhysicalOperations)
}
