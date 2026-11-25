import React from 'react';
import type { ExplorerBuilderPreviewResult } from '../../../../contracts/types';
import { previewResultMultiplicity, previewValueCoverage } from '../../model/previewCoverage';

export const PreviewValueCoverage = ({
  preview,
  columnIds,
  resultForm,
}: {
  readonly preview: ExplorerBuilderPreviewResult;
  readonly columnIds: ReadonlyArray<string>;
  readonly resultForm?: 'COUNT' | 'PRESENCE' | 'ALL';
}) => {
  const coverage = previewValueCoverage(preview, columnIds);
  if (coverage.length === 0) return null;
  return (
    <div data-testid="construction-preview-value-coverage" className="mt-3 rounded-lg border border-slate-200 bg-slate-50 p-3 text-sm text-slate-700">
      <h4 className="font-semibold text-slate-900">Values in the preview</h4>
      <ul className="mt-2 space-y-2">
        {coverage.map((column) => {
          const multiplicity = resultForm ? previewResultMultiplicity(preview, column.columnId, resultForm) : undefined;
          return (
            <li key={column.columnId} data-column-id={column.columnId}>
              <span className="font-medium">{column.label}</span>
              {' · '}{column.populatedRows} of {column.displayedRows} {column.fullOutput ? 'rows' : 'displayed rows'} contain a value.
              {multiplicity?.kind === 'EXACT_COUNT' ? (
                <span className="block text-xs text-slate-700">Matching records in {column.fullOutput ? 'all rows' : 'displayed rows'}: 0 for {multiplicity.zero}, 1 for {multiplicity.one}, 2 or more for {multiplicity.many}.</span>
              ) : multiplicity?.kind === 'PRESENCE' ? (
                <span className="block text-xs text-slate-700">Matching records in {column.fullOutput ? 'all rows' : 'displayed rows'}: none for {multiplicity.zero}, at least one for {multiplicity.oneOrMore}.</span>
              ) : multiplicity?.kind === 'VALUE_LIST' ? (
                <span className="block text-xs text-slate-700">Returned values in {column.fullOutput ? 'all rows' : 'displayed rows'}: 0 for {multiplicity.zero}, 1 for {multiplicity.one}, 2 or more for {multiplicity.many}.</span>
              ) : null}
              {column.examples.length > 0 ? <span className="block text-xs text-slate-600">Examples: {column.examples.join(', ')}</span> : null}
            </li>
          );
        })}
      </ul>
      {!coverage.every((column) => column.fullOutput) ? (
        <p className="mt-2 text-xs text-slate-600">These counts describe the preview rows. Coverage across the full table has not been measured.</p>
      ) : null}
    </div>
  );
};
