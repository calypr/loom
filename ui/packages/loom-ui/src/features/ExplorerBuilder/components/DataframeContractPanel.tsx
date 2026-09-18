import React, { useMemo } from 'react';
import type { ExplorerBuilderCompileResult } from '../../../types';
import { useVirtualViewport, virtualRange } from './virtualization';

const ROW_HEIGHT = 42;
const lossLabels: Readonly<Record<string, string>> = {
  RELATED_RESOURCE_FIRST_LOSSY: 'Only the first related record is kept. Other records are omitted.',
  RELATED_RESOURCE_ALL_LOSSY: 'Values from related records are flattened. Their association with each record is not retained.',
  FIELD_FIRST_REDUCTION: 'Only the first value in a repeated field is kept. Other values are omitted.',
  DISTINCT_VALUES_REDUCTION: 'Repeated values are deduplicated. Their frequency and order are not retained.',
  AGGREGATE_REDUCTION: 'Records are summarized. Individual input values are not retained.',
  RELATED_LOOKUP_REDUCTION: 'The lookup does not preserve every related record.',
};
const lossLabel = (reason: string): string => lossLabels[reason] ?? reason;
const structureLabels = {
  scalar: 'Scalar columns',
  array: 'Includes arrays',
  'requires-review': 'Needs review',
};

const shortDigest = (value: string | undefined) =>
  value ? `${value.slice(0, 18)}…${value.slice(-8)}` : 'unavailable';

export const DataframeContractPanel = ({
  receipt,
  outputId,
}: {
  readonly receipt: ExplorerBuilderCompileResult;
  readonly outputId: string;
}) => {
  const output = receipt.outputs.find((candidate) => candidate.outputId === outputId);
  const { viewport, ref: viewportRef } =
    useVirtualViewport<HTMLDivElement>(1200, 252);
  const range = virtualRange({
    count: output?.columns.length ?? 0,
    offset: viewport.scrollTop,
    viewport: viewport.height,
    itemSize: ROW_HEIGHT,
    overscan: 4,
  });
  const manifest = useMemo(
    () => JSON.stringify(receipt, null, 2),
    [receipt],
  );
  if (!output) return null;

  const downloadManifest = () => {
    const url = URL.createObjectURL(
      new Blob([manifest], { type: 'application/json' }),
    );
    const link = document.createElement('a');
    link.href = url;
    link.download = `${output.outputId}-${receipt.receiptId}-contract.json`;
    link.click();
    URL.revokeObjectURL(url);
  };

  return (
    <section className="rounded-xl border border-emerald-200 bg-white p-3 shadow-sm">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold text-slate-900">
            Dataframe contract
          </h2>
          <p className="mt-1 text-xs text-slate-600">
            One row per {output.rowGrain || output.rootResourceType || 'root resource'} · Row multiplication:{' '}
            {output.rowMultiplication ?? 'none'} · {output.columns.length} physical columns
          </p>
        </div>
        <button
          type="button"
          className="rounded border border-emerald-300 px-2.5 py-1 text-xs font-semibold text-emerald-800 hover:bg-emerald-50"
          onClick={downloadManifest}
        >
          Download manifest
        </button>
      </div>
      <div className="mt-3 grid gap-2 text-xs sm:grid-cols-2 xl:grid-cols-4">
        <div><span className="font-semibold">Lossless:</span> {output.lossless === undefined ? 'Not assessed' : output.lossless ? 'Yes' : 'No'}</div>
        <div><span className="font-semibold">Structure:</span> {output.structuralSuitability ? structureLabels[output.structuralSuitability] : 'Not assessed'}</div>
        <div title={receipt.generation}><span className="font-semibold">Generation:</span> {receipt.generation}</div>
        <div title={receipt.receiptId}><span className="font-semibold">Receipt:</span> {shortDigest(receipt.receiptId)}</div>
        <div title={receipt.shapeDigest}><span className="font-semibold">Shape:</span> {shortDigest(receipt.shapeDigest)}</div>
        <div title={receipt.recipeDigest}><span className="font-semibold">Recipe:</span> {shortDigest(receipt.recipeDigest)}</div>
        <div title={receipt.resolvedSchemaDigest}><span className="font-semibold">Schema:</span> {shortDigest(receipt.resolvedSchemaDigest)}</div>
        <div title={receipt.outputContractDigest}><span className="font-semibold">Contract:</span> {shortDigest(receipt.outputContractDigest)}</div>
      </div>
      <p className="mt-2 text-xs text-slate-600">
        Scalar columns alone do not establish suitability for machine learning. Data quality and feature meaning still need review.
      </p>
      {output.lossReasons?.length ? (
        <ul className="mt-2 list-inside list-disc text-xs text-amber-800">
          {output.lossReasons.map((reason) => <li key={reason}>{lossLabel(reason)}</li>)}
        </ul>
      ) : null}
      <details className="mt-3">
        <summary className="cursor-pointer text-xs font-semibold text-slate-700">
          Column lineage and nullability
        </summary>
        <div ref={viewportRef} className="mt-2 h-64 overflow-auto rounded border border-slate-200">
          <div className="relative min-w-[60rem]" style={{ height: output.columns.length * ROW_HEIGHT }}>
            {output.columns.slice(range.start, range.end).map((column, offset) => {
              const index = range.start + offset;
              const coordinates = column.coordinates?.map((coordinate) => `${coordinate.boundaryPath}[${coordinate.index}/${coordinate.width}]`).join(', ');
              return (
                <div
                  key={column.column}
                  className="absolute grid w-full grid-cols-[14rem_8rem_7rem_1fr] items-center gap-2 border-b border-slate-100 px-2 text-xs"
                  style={{ height: ROW_HEIGHT, transform: `translateY(${index * ROW_HEIGHT}px)` }}
                >
                  <div className="truncate font-mono" title={column.column}>{column.column}</div>
                  <div>{column.logicalType}{column.nullable ? ' · nullable' : ''}</div>
                  <div title={column.lossReasons?.map(lossLabel).join('; ')}>{column.shape ?? 'Not assessed'}{column.lossless === false ? ' · lossy' : ''}</div>
                  <div className="truncate" title={[column.sourceResourceType, column.sourcePath, column.choiceArm, coordinates].filter(Boolean).join(' · ')}>
                    {[column.sourceResourceType, column.sourcePath, column.choiceArm && `choice ${column.choiceArm}`, coordinates].filter(Boolean).join(' · ') || 'compiler-generated'}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </details>
    </section>
  );
};
