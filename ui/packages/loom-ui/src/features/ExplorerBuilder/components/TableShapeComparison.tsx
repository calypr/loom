import React from 'react';

export type ComparisonSamplingStatus =
  | { readonly kind: 'sampled'; readonly label: string; readonly explanation?: string }
  | { readonly kind: 'complete'; readonly label: string; readonly explanation?: string };

export interface ComparisonRowCount {
  readonly rowCount: number;
  readonly sampling: ComparisonSamplingStatus;
}

export type ComparisonCellValue =
  | { readonly kind: 'value'; readonly displayText: string }
  | { readonly kind: 'absent'; readonly displayText: string };

export interface ComparedCell {
  readonly columnId: string;
  readonly columnLabel: string;
  readonly before: ComparisonCellValue;
  readonly after: ComparisonCellValue;
}

export interface TableShapeComparisonExample {
  readonly exampleId: string;
  readonly label: string;
  readonly cells: ReadonlyArray<ComparedCell>;
  readonly contributors: ReadonlyArray<string>;
  readonly exclusions: ReadonlyArray<string>;
}

export interface DeclaredInformationLoss {
  readonly lossId: string;
  readonly label: string;
  readonly explanation: string;
}

export interface TableShapeComparisonEvidence {
  readonly base: ComparisonRowCount;
  readonly candidate: ComparisonRowCount;
  readonly changedColumns: ReadonlyArray<{ readonly columnId: string; readonly label: string }>;
  readonly examples: ReadonlyArray<TableShapeComparisonExample>;
  readonly contributors: ReadonlyArray<string>;
  readonly exclusions: ReadonlyArray<string>;
  readonly informationLoss: ReadonlyArray<DeclaredInformationLoss>;
}

export interface TableShapeComparisonProps {
  readonly evidence: TableShapeComparisonEvidence;
}

const MAX_VISIBLE_EXAMPLES = 5;
const MAX_VISIBLE_CHANGED_COLUMNS = 8;
const MAX_VISIBLE_CELLS_PER_EXAMPLE = 6;
const MAX_VISIBLE_EVIDENCE_ITEMS = 5;

const BoundedTextList = ({
  label,
  testId,
  items,
}: {
  readonly label: string;
  readonly testId: string;
  readonly items: ReadonlyArray<string>;
}) => {
  const visibleItems = items.slice(0, MAX_VISIBLE_EVIDENCE_ITEMS);
  return (
    <section aria-label={label} data-testid={testId} className="grid gap-1">
      <h4 className="text-sm font-medium text-slate-800">{label}</h4>
      {visibleItems.length > 0 ? (
        <ul className="list-inside list-disc text-sm text-slate-700">
          {visibleItems.map((item, index) => <li key={`${index}:${item}`}>{item}</li>)}
        </ul>
      ) : <p className="text-sm text-slate-600">None listed.</p>}
      {items.length > visibleItems.length ? (
        <p className="text-xs text-slate-600">Showing the first {visibleItems.length} of {items.length} returned items.</p>
      ) : null}
    </section>
  );
};

const renderedCellValue = (value: ComparisonCellValue): string => {
  switch (value.kind) {
    case 'value':
      return value.displayText;
    case 'absent':
      return value.displayText;
  }
};

const SamplingLabel = ({
  side,
  status,
}: {
  readonly side: 'base' | 'candidate';
  readonly status: ComparisonSamplingStatus;
}) => (
  <span
    data-testid={`ui04-comparison-${side}-sampling`}
    data-sampled={status.kind === 'sampled' ? 'true' : 'false'}
    className={status.kind === 'sampled'
      ? 'rounded bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-900'
      : 'rounded bg-emerald-100 px-2 py-0.5 text-xs font-medium text-emerald-900'}
  >
    {status.label}
  </span>
);

export const TableShapeComparison = ({ evidence }: TableShapeComparisonProps) => {
  const visibleExamples = evidence.examples.slice(0, MAX_VISIBLE_EXAMPLES);
  const visibleChangedColumns = evidence.changedColumns.slice(0, MAX_VISIBLE_CHANGED_COLUMNS);

  return (
    <section
      aria-label="Before and after table shape comparison"
      data-testid="ui04-table-shape-comparison"
      className="grid gap-4 rounded-lg border border-slate-200 bg-slate-50 p-4 text-slate-800"
    >
      <div>
        <h2 className="font-semibold text-slate-900">Before and after</h2>
        <p className="mt-1 text-xs text-slate-600">Comparison evidence supplied by the server.</p>
      </div>

      <dl className="grid gap-3 sm:grid-cols-2">
        <div className="rounded border border-slate-200 bg-white p-3" data-testid="ui04-comparison-base-rows">
          <dt className="text-sm font-medium text-slate-700">Base rows</dt>
          <dd className="mt-1 flex flex-wrap items-center gap-2 text-lg font-semibold text-slate-900">
            <span>{evidence.base.rowCount}</span>
            <SamplingLabel side="base" status={evidence.base.sampling} />
          </dd>
          {evidence.base.sampling.explanation ? (
            <p className="mt-1 text-xs text-slate-600">{evidence.base.sampling.explanation}</p>
          ) : null}
        </div>
        <div className="rounded border border-slate-200 bg-white p-3" data-testid="ui04-comparison-candidate-rows">
          <dt className="text-sm font-medium text-slate-700">Candidate rows</dt>
          <dd className="mt-1 flex flex-wrap items-center gap-2 text-lg font-semibold text-slate-900">
            <span>{evidence.candidate.rowCount}</span>
            <SamplingLabel side="candidate" status={evidence.candidate.sampling} />
          </dd>
          {evidence.candidate.sampling.explanation ? (
            <p className="mt-1 text-xs text-slate-600">{evidence.candidate.sampling.explanation}</p>
          ) : null}
        </div>
      </dl>

      <section aria-label="Changed columns" data-testid="ui04-comparison-changed-columns" className="grid gap-1">
        <h3 className="text-sm font-medium text-slate-800">Changed columns</h3>
        {evidence.changedColumns.length > 0 ? (
          <ul className="list-inside list-disc text-sm text-slate-700">
            {visibleChangedColumns.map((column) => (
              <li key={column.columnId}>{column.label}</li>
            ))}
          </ul>
        ) : <p className="text-sm text-slate-600">No changed columns were reported.</p>}
        {evidence.changedColumns.length > visibleChangedColumns.length ? (
          <p className="text-xs text-slate-600">
            Showing the first {visibleChangedColumns.length} of {evidence.changedColumns.length} returned changed columns.
          </p>
        ) : null}
      </section>

      <section aria-label="Example values" data-testid="ui04-comparison-examples" className="grid gap-2">
        <div>
          <h3 className="text-sm font-medium text-slate-800">Example values</h3>
          {evidence.examples.length > visibleExamples.length ? (
            <p className="mt-1 text-xs text-slate-600">
              Showing the first {visibleExamples.length} of {evidence.examples.length} returned examples.
            </p>
          ) : null}
        </div>
        {visibleExamples.length > 0 ? (
          <ol className="grid gap-2">
            {visibleExamples.map((example, index) => (
              <li key={example.exampleId} data-testid={`ui04-comparison-example-${index + 1}`} className="rounded border border-slate-200 bg-white p-3">
                <h4 className="text-sm font-medium text-slate-800">{example.label}</h4>
                {example.cells.length > 0 ? (
                  <div className="mt-2 overflow-x-auto" data-testid={`ui04-comparison-example-${index + 1}-cells`}>
                    {example.cells.length > MAX_VISIBLE_CELLS_PER_EXAMPLE ? (
                      <p className="mb-1 text-xs text-slate-600">
                        Showing the first {MAX_VISIBLE_CELLS_PER_EXAMPLE} of {example.cells.length} returned cells.
                      </p>
                    ) : null}
                    <table className="w-full text-left text-sm">
                      <thead>
                        <tr className="border-b border-slate-200 text-xs text-slate-600">
                          <th scope="col" className="py-1 pr-3">Column</th>
                          <th scope="col" className="py-1 pr-3">Before</th>
                          <th scope="col" className="py-1">After</th>
                        </tr>
                      </thead>
                      <tbody>
                        {example.cells.slice(0, MAX_VISIBLE_CELLS_PER_EXAMPLE).map((cell) => (
                          <tr key={cell.columnId} className="border-b border-slate-100 last:border-0">
                            <th scope="row" className="py-1 pr-3 font-medium">{cell.columnLabel}</th>
                            <td className="py-1 pr-3">{renderedCellValue(cell.before)}</td>
                            <td className="py-1">{renderedCellValue(cell.after)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : null}
                <div className="mt-3 grid gap-3 sm:grid-cols-2">
                  <BoundedTextList
                    label="Contributors"
                    testId={`ui04-comparison-example-${index + 1}-contributors`}
                    items={example.contributors}
                  />
                  <BoundedTextList
                    label="Exclusions"
                    testId={`ui04-comparison-example-${index + 1}-exclusions`}
                    items={example.exclusions}
                  />
                </div>
              </li>
            ))}
          </ol>
        ) : <p className="text-sm text-slate-600">No example values were returned.</p>}
      </section>

      <div className="grid gap-3 sm:grid-cols-2">
        <BoundedTextList
          label="Contributors"
          testId="ui04-comparison-contributors"
          items={evidence.contributors}
        />
        <BoundedTextList
          label="Exclusions"
          testId="ui04-comparison-exclusions"
          items={evidence.exclusions}
        />
      </div>

      <section aria-label="Declared information loss" data-testid="ui04-comparison-information-loss" className="grid gap-1">
        <h3 className="text-sm font-medium text-slate-800">Declared information loss</h3>
        {evidence.informationLoss.length > 0 ? (
          <ul className="grid gap-2">
            {evidence.informationLoss.slice(0, MAX_VISIBLE_EVIDENCE_ITEMS).map((loss) => (
              <li key={loss.lossId} className="rounded border border-amber-200 bg-amber-50 p-2">
                <p className="text-sm font-medium text-amber-950">{loss.label}</p>
                <p className="text-xs text-amber-900">{loss.explanation}</p>
              </li>
            ))}
          </ul>
        ) : <p className="text-sm text-slate-600">No information loss was declared.</p>}
        {evidence.informationLoss.length > MAX_VISIBLE_EVIDENCE_ITEMS ? (
          <p className="text-xs text-slate-600">
            Showing the first {MAX_VISIBLE_EVIDENCE_ITEMS} of {evidence.informationLoss.length} declared items.
          </p>
        ) : null}
      </section>
    </section>
  );
};
