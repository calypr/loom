import React from 'react';
import type { ExplorerBuilderPreviewResult } from '../../../types';
import type { DraftTable } from '../authoring/model';
import { formatPreviewCell, previewCellTitle, sortByPresentationOrder } from '../components/PreviewTable';

export const ConstructionProposalPreview = ({
  preview,
  presentationTable,
  partialValidationMessage = 'Only displayed groups were checked in this preview. Publishing runs Pivot rules over the full source.',
}: {
  readonly preview: ExplorerBuilderPreviewResult;
  readonly presentationTable?: DraftTable;
  readonly partialValidationMessage?: string;
}) => {
  const outputs = presentationTable?.document.construction?.steps.at(-1)?.outputs ?? [];
  const outputByName = new Map(outputs.map((output) => [output.name, output]));
  const outputOrder = new Map(outputs.map((output, index) => [
    output.name,
    output.table?.order ?? index,
  ]));
  const columns = outputs.length === 0
    ? preview.columns
    : sortByPresentationOrder(
        preview.columns,
        (column) => outputOrder.get(column.column) ?? Number.MAX_SAFE_INTEGER,
      )
        .filter((column) => outputByName.get(column.column)?.table?.visible !== false)
        .map((column) => ({
          ...column,
          label: outputByName.get(column.column)?.label ?? column.label,
        }));

  return <div
    data-testid="construction-proposal-preview"
    data-preview-status="ready"
    data-preview-receipt-id={preview.receiptId}
    data-preview-output-id={preview.outputId}
    className="overflow-auto"
  >
    {preview.rows === null ? (
      <p role="status" className="p-4 text-sm text-amber-900">
        Loom did not return preview rows for this proposal.
      </p>
    ) : columns.length === 0 ? (
      <p role="status" className="p-4 text-sm text-slate-600">
        This table has no visible columns.
      </p>
    ) : (
      <table className="min-w-full border-collapse text-sm">
        <thead className="sticky top-0 bg-slate-50 text-left text-xs text-slate-600">
          <tr>
            {columns.map((column) => (
              <th key={column.column} scope="col" className="border-b border-slate-200 px-3 py-2 font-semibold">
                <span className="block">{column.label}</span>
                {column.logicalType ? (
                  <span className="mt-0.5 block font-normal text-slate-400">{column.logicalType}</span>
                ) : null}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {preview.rows.length === 0 ? (
            <tr>
              <td colSpan={columns.length} className="px-3 py-6 text-center text-sm text-slate-500">
                No rows matched this proposal.
              </td>
            </tr>
          ) : preview.rows.map((row, rowIndex) => (
            <tr key={rowIndex} data-testid="construction-proposal-preview-row" className="border-b border-slate-100 even:bg-slate-50/60">
              {columns.map((column) => {
                const value = row[column.column];
                return (
                  <td key={column.column} title={previewCellTitle(value)} className="max-w-72 px-3 py-2 align-top text-slate-800">
                    {formatPreviewCell(value)}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    )}
    <p className="border-t border-slate-200 px-3 py-2 text-xs text-slate-500">
      {preview.partialValidation
        ? partialValidationMessage
        : preview.sampled === false
        ? `Showing all ${preview.rowCount} ${preview.rowCount === 1 ? 'row' : 'rows'} in this proposal.`
        : `Showing ${preview.rows?.length ?? 0} preview rows. Full-output coverage is unavailable before publication.`}
    </p>
  </div>;
};
