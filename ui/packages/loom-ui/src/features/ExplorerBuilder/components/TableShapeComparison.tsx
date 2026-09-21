import React from 'react';
import type { TableShapeComparison as TableShapeComparisonResult } from '../../../types';

const MAX_VISIBLE_ROWS = 5;
const MAX_VISIBLE_COLUMNS = 8;
const MAX_VISIBLE_CONTRIBUTORS = 8;

const jsonText = (value: unknown): string => {
  if (value === null) return 'null';
  const encoded = JSON.stringify(value);
  return encoded === undefined ? 'Unknown value' : encoded;
};

export interface TableShapeComparisonProps {
  readonly comparison: TableShapeComparisonResult;
  readonly columnLabels: Readonly<Record<string, string>>;
}

const EvidenceList = ({
  label,
  testId,
  items,
}: {
  readonly label: string;
  readonly testId: string;
  readonly items: ReadonlyArray<string>;
}) => (
  <section aria-label={label} data-testid={testId} className="grid gap-1">
    <h3 className="text-sm font-medium text-slate-800">{label}</h3>
    {items.length > 0 ? (
      <ul className="list-inside list-disc text-sm text-slate-700">
        {items.slice(0, MAX_VISIBLE_CONTRIBUTORS).map((item, index) => <li key={`${index}:${item}`}>{item}</li>)}
      </ul>
    ) : <p className="text-sm text-slate-600">None reported.</p>}
    {items.length > MAX_VISIBLE_CONTRIBUTORS ? (
      <p className="text-xs text-slate-600">Showing {MAX_VISIBLE_CONTRIBUTORS} of {items.length} items.</p>
    ) : null}
  </section>
);

const InformationList = ({
  label,
  testId,
  items,
}: {
  readonly label: string;
  readonly testId: string;
  readonly items: ReadonlyArray<string>;
}) => (
  <section aria-label={label} data-testid={testId} className="grid gap-1">
    <h3 className="text-sm font-medium text-slate-800">{label}</h3>
    {items.length > 0 ? (
      <ul className="list-inside list-disc text-sm text-slate-700">
        {items.map((item, index) => <li key={`${index}:${item}`}>{item}</li>)}
      </ul>
    ) : <p className="text-sm text-slate-600">None reported.</p>}
  </section>
);

export const TableShapeComparison = ({ comparison, columnLabels }: TableShapeComparisonProps) => {
  if (comparison.status === 'UNAVAILABLE') {
    return (
      <section
        aria-label="Before and after table shape comparison"
        data-testid="ui04-table-shape-comparison"
        className="grid gap-3 rounded-lg border border-amber-200 bg-amber-50 p-4 text-slate-800"
      >
        <h2 className="font-semibold text-amber-950">Comparison unavailable</h2>
        <p role="alert" data-testid="ui04-comparison-unavailable-reason">
          {comparison.reasonCode}: {comparison.reason}
        </p>
        <InformationList
          label="Evidence limitations"
          testId="ui04-comparison-limitations"
          items={comparison.evidenceLimitations}
        />
        <InformationList label="Notices" testId="ui04-comparison-notices" items={comparison.notices} />
      </section>
    );
  }

  const visibleRows = comparison.changedRows.slice(0, MAX_VISIBLE_ROWS);
  const visibleColumns = comparison.changedColumns.slice(0, MAX_VISIBLE_COLUMNS);
  const contributors = comparison.contributors.map(({ resourceType, resourceId }) => `${resourceType}/${resourceId}`);

  return (
    <section
      aria-label="Before and after table shape comparison"
      data-testid="ui04-table-shape-comparison"
      className="grid gap-4 rounded-lg border border-slate-200 bg-slate-50 p-4 text-slate-800"
    >
      <div>
        <h2 className="font-semibold text-slate-900">Before and after</h2>
        <p className="mt-1 text-xs text-slate-600">Values and source evidence returned by Loom.</p>
      </div>

      <dl className="grid gap-3 sm:grid-cols-2">
        <div className="rounded border border-slate-200 bg-white p-3" data-testid="ui04-comparison-base-rows">
          <dt className="text-sm font-medium text-slate-700">Base rows</dt>
          <dd className="mt-1 text-lg font-semibold text-slate-900">{comparison.base.rowCount}</dd>
          <p className="text-xs text-slate-600">{comparison.base.sampled ? 'Sampled' : 'Complete count'}</p>
        </div>
        <div className="rounded border border-slate-200 bg-white p-3" data-testid="ui04-comparison-candidate-rows">
          <dt className="text-sm font-medium text-slate-700">Candidate rows</dt>
          <dd className="mt-1 text-lg font-semibold text-slate-900">{comparison.candidate.rowCount}</dd>
          <p className="text-xs text-slate-600">{comparison.candidate.sampled ? 'Sampled' : 'Complete count'}</p>
        </div>
      </dl>

      <section aria-label="Changed columns" data-testid="ui04-comparison-changed-columns" className="grid gap-1">
        <h3 className="text-sm font-medium text-slate-800">Changed columns</h3>
        {visibleColumns.length > 0 ? (
          <ul className="list-inside list-disc text-sm text-slate-700">
            {visibleColumns.map((column) => <li key={column}>{columnLabels[column] ?? column}</li>)}
          </ul>
        ) : <p className="text-sm text-slate-600">No changed columns were reported.</p>}
        {comparison.changedColumns.length > visibleColumns.length ? (
          <p className="text-xs text-slate-600">Showing {visibleColumns.length} of {comparison.changedColumns.length} columns.</p>
        ) : null}
      </section>

      <section aria-label="Changed example rows" data-testid="ui04-comparison-examples" className="grid gap-2">
        <h3 className="text-sm font-medium text-slate-800">Changed example rows</h3>
        {visibleRows.length > 0 ? (
          <ol className="grid gap-2">
            {visibleRows.map((row, index) => (
              <li key={row.rowIdentity} data-testid={`ui04-comparison-example-${index + 1}`} className="grid gap-3 rounded border border-slate-200 bg-white p-3">
                <div>
                  <h4 className="text-sm font-medium text-slate-800">{row.rowIdentity}</h4>
                  <p className="text-xs text-slate-600">
                    Base row: {row.basePresent ? 'present' : 'missing'}. Candidate row: {row.candidatePresent ? 'present' : 'missing'}.
                  </p>
                </div>
                {row.changedCells.length > 0 ? (
                  <div className="overflow-x-auto" data-testid={`ui04-comparison-example-${index + 1}-cells`}>
                    <table className="w-full text-left text-sm">
                      <thead>
                        <tr className="border-b border-slate-200 text-xs text-slate-600">
                          <th scope="col" className="py-1 pr-3">Column</th>
                          <th scope="col" className="py-1 pr-3">Before</th>
                          <th scope="col" className="py-1">After</th>
                        </tr>
                      </thead>
                      <tbody>
                        {row.changedCells.map((cell) => {
                          const before = !row.basePresent
                            ? 'No row'
                            : !cell.before.present
                              ? 'Missing'
                              : jsonText(cell.before.value);
                          const after = !row.candidatePresent
                            ? 'No row'
                            : !cell.after.present
                              ? 'Missing'
                              : jsonText(cell.after.value);
                          const cellContributors = cell.trace.contributors.map((contributor) =>
                            `${contributor.resourceType}/${contributor.resourceId}: ${jsonText(contributor.value)}`,
                          );
                          return (
                            <React.Fragment key={cell.column}>
                              <tr className="border-b border-slate-100 last:border-0">
                                <th scope="row" className="py-1 pr-3 font-medium">{columnLabels[cell.column] ?? cell.column}</th>
                                <td className="py-1 pr-3">{before}</td>
                                <td className="py-1">{after}</td>
                              </tr>
                              <tr className="border-b border-slate-100 last:border-0">
                                <td colSpan={3} className="py-1 text-xs text-slate-600">
                                  <span>Trace {cell.trace.state.toLowerCase()}</span>
                                  {cell.trace.cellStatus ? <span> · {cell.trace.cellStatus}</span> : null}
                                  {!cell.trace.complete ? <span> · incomplete</span> : null}
                                  {cell.trace.sampled ? <span> · sampled</span> : null}
                                  {cell.trace.omissionCode ? <span> · omission {cell.trace.omissionCode}</span> : null}
                                  {cell.trace.failureCode ? <span> · failure {cell.trace.failureCode}</span> : null}
                                  {cellContributors.length > 0 ? (
                                    <ul className="mt-1 list-inside list-disc">
                                      {cellContributors.slice(0, MAX_VISIBLE_CONTRIBUTORS).map((contributor) => (
                                        <li key={contributor}>{contributor}</li>
                                      ))}
                                    </ul>
                                  ) : <span> · no cell contributors reported</span>}
                                </td>
                              </tr>
                            </React.Fragment>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                ) : <p className="text-sm text-slate-600">No changed cell values were returned for this row.</p>}
              </li>
            ))}
          </ol>
        ) : <p className="text-sm text-slate-600">No changed rows were returned.</p>}
        {comparison.changedRowsSampled ? (
          <p className="text-xs text-amber-800">The changed rows are a sample of {comparison.changedRowCount} rows.</p>
        ) : null}
      </section>

      <EvidenceList label="Contributing resources" testId="ui04-comparison-contributors" items={contributors} />
      {comparison.contributorsSampled ? <p className="text-xs text-amber-800">The contributing resource list is sampled.</p> : null}
      <InformationList label="Evidence limitations" testId="ui04-comparison-limitations" items={comparison.evidenceLimitations} />
      <InformationList label="Notices" testId="ui04-comparison-notices" items={comparison.notices} />
    </section>
  );
};
