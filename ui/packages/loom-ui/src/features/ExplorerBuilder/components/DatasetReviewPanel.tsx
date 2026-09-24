import React from 'react';
import type {
  ExplorerAuthoringDiagnostic,
  ExplorerBuilderCatalog,
  ExplorerBuilderCompileResult,
  ExplorerBuilderPreviewResult,
} from '../../../types';
import {
  derivedOccurrences,
  type BuilderAuthoringState,
  type DraftTable,
} from '../authoring/model';
import { lossLabel } from './DataframeContractPanel';

type ResultShape =
  | { readonly kind: 'scalar' }
  | { readonly kind: 'record' }
  | {
      readonly kind: 'list';
      readonly itemKind: 'scalar' | 'record' | 'unknown';
    }
  | { readonly kind: 'unknown'; readonly reason: string };

export type DatasetReviewTarget =
  | {
      readonly kind: 'column';
      readonly outputId: string;
      readonly column: string;
      readonly label: string;
      readonly occurrenceId: string;
    }
  | {
      readonly kind: 'row';
      readonly outputId: string;
      readonly control: 'row-type' | 'row-definition';
    }
  | { readonly kind: 'new-table' }
  | { readonly kind: 'table'; readonly outputId: string };

type DatasetReviewItem =
  | {
      readonly kind: 'compile-assessment';
      readonly outputId: string;
      readonly status: 'current' | 'pending' | 'stale' | 'missing';
    }
  | {
      readonly kind: 'row-definition';
      readonly outputId: string;
      readonly summary: string;
    }
  | {
      readonly kind: 'requested-column';
      readonly outputId: string;
      readonly column: string;
      readonly label: string;
      readonly included: boolean;
      readonly shapes: ReadonlyArray<ResultShape>;
      readonly reductions: ReadonlyArray<string>;
    }
  | {
      readonly kind: 'table-reduction';
      readonly outputId: string;
      readonly summary: string;
    }
  | {
      readonly kind: 'blocking-issue';
      readonly outputId?: string;
      readonly id: string;
      readonly location: string;
      readonly message: string;
      readonly target?: DatasetReviewTarget;
    }
  | {
      readonly kind: 'preview-evidence';
      readonly outputId: string;
      readonly status: 'current-sample' | 'stale-sample' | 'missing';
      readonly sampleRows: number;
    };

type ReviewInputs = {
  readonly tables: ReadonlyArray<DraftTable>;
  readonly catalog: ExplorerBuilderCatalog;
  readonly receipt?: ExplorerBuilderCompileResult;
  readonly preview?: ExplorerBuilderPreviewResult;
  readonly diagnostics: ReadonlyArray<ExplorerAuthoringDiagnostic>;
  readonly reconciliation: BuilderAuthoringState['reconciliation'];
};

const visibleColumn = (column: DraftTable['document']['columns'][number]) =>
  column.table?.visible ?? Boolean(column.table);

const rowDescription = (
  table: DraftTable,
  catalog: ExplorerBuilderCatalog,
  rowGrain?: string,
): string => {
  const occurrences = derivedOccurrences(table, catalog);
  const occurrenceName = (occurrenceId: string) => {
    const occurrence = occurrences.find((candidate) => candidate.id === occurrenceId);
    return [occurrence?.resourceType, occurrence?.relationship && `via ${occurrence.relationship}`]
      .filter(Boolean)
      .join(' ');
  };
  const rows = table.document.rows;
  switch (rows.kind) {
    case 'RECORDS':
      {
        const label = table.document.output.rowLabel || rowGrain || table.document.rootResourceType || 'root resource';
        return `One row per ${label}${label.toLocaleLowerCase().endsWith('record') ? '' : ' record'}.`;
      }
    case 'GROUPS': {
      const source = rows.groups.source;
      switch (source.kind) {
        case 'FIELD': {
          const resource = occurrenceName(source.field.occurrenceId);
          const missing = {
            ERROR: 'missing keys stop the build',
            EXCLUDE: 'rows without a key are excluded',
            GROUP_AS_MISSING: 'missing keys share one group',
          }[source.field.missingKeyPolicy];
          return `One row per ${resource || 'selected resource'} group by ${source.field.fieldPath}; ${missing}.`;
        }
        case 'EXPLICIT': {
          const unassigned = {
            ERROR: 'unassigned members stop the build',
            EXCLUDE: 'unassigned members are excluded',
            GROUP_AS_UNASSIGNED: 'unassigned members share one group',
          }[source.explicit.unassignedMemberPolicy];
          return `One row per explicit group set at revision ${source.explicit.revisionId}; ${unassigned}.`;
        }
        default: {
          const exhaustive: never = source;
          return exhaustive;
        }
      }
    }
    case 'EXPANDED': {
      const resource = occurrenceName(rows.expanded.occurrenceId);
      const empty = {
        ERROR: 'empty collections stop the build',
        EXCLUDE: 'parents with empty collections are excluded',
        PRESERVE_PARENT: 'parents with empty collections remain',
      }[rows.expanded.emptyCollectionPolicy];
      return `Expand ${resource || 'the selected collection'} at ${rows.expanded.scopePath}; ${empty}.`;
    }
    default: {
      const exhaustive: never = rows;
      return exhaustive;
    }
  }
};

const compiledShape = (
  column: ExplorerBuilderCompileResult['outputs'][number]['columns'][number],
): ResultShape => {
  const shape = column.shape?.toLowerCase() ?? '';
  if (shape === 'record' || shape === 'object') return { kind: 'record' };
  if (shape.includes('record_list') || shape === 'list<record>') {
    return { kind: 'list', itemKind: 'record' };
  }
  if (shape === 'array' || shape === 'list' || shape.includes('[]')) {
    return { kind: 'list', itemKind: 'unknown' };
  }
  if (shape === 'scalar' || shape.endsWith('_scalar') || shape === 'repeated_count') {
    return { kind: 'scalar' };
  }
  if (column.structuralSuitability === 'array') {
    return { kind: 'list', itemKind: 'unknown' };
  }
  if (column.structuralSuitability === 'scalar') return { kind: 'scalar' };
  return {
    kind: 'unknown',
    reason: column.shape || 'The compiler did not report a concrete shape.',
  };
};

const shapeLabel = (shape: ResultShape): string => {
  switch (shape.kind) {
    case 'scalar':
      return 'Scalar';
    case 'record':
      return 'Record';
    case 'list':
      return shape.itemKind === 'record' ? 'List of records' : shape.itemKind === 'scalar' ? 'List of scalar values' : 'List';
    case 'unknown':
      return `Unresolved shape: ${shape.reason}`;
    default: {
      const exhaustive: never = shape;
      return exhaustive;
    }
  }
};

const authoredReductions = (
  column: DraftTable['document']['columns'][number],
): ReadonlyArray<string> => {
  const reductions: string[] = [];
  const source = column.source;
  if (source.kind === 'field' && source.field.relatedSelection?.kind === 'first-by-resource-key') {
    reductions.push('Keeps the first related record by resource key; this choice was acknowledged.');
  }
  const projection = (() => {
    switch (source.kind) {
      case 'field':
        return source.field.projectionMode;
      case 'identifierBySystem':
      case 'extensionByUrl':
      case 'codedValue':
        return source.lookup.projectionMode;
      case 'categoricalBySystem':
        return source.categorical.projectionMode;
      case 'ownerRecords':
      case 'aggregate':
      case 'projectId':
        return undefined;
      default: {
        const exhaustive: never = source;
        return exhaustive;
      }
    }
  })();
  if (projection === 'FIRST') reductions.push('Keeps only the first value.');
  if (projection === 'DISTINCT') reductions.push('Keeps distinct values and drops repeats.');
  if (source.kind === 'aggregate') {
    const path = source.aggregate.path ? ` at ${source.aggregate.path}` : '';
    const operation = source.aggregate.operation;
    const summary = (() => {
      switch (operation) {
        case 'COUNT': return 'Counts matching values';
        case 'COUNT_DISTINCT': return 'Counts distinct values';
        case 'DISTINCT_VALUES': return 'Keeps distinct values only';
        case 'MIN': return 'Keeps the minimum value';
        case 'MAX': return 'Keeps the maximum value';
        case 'SUM': return 'Adds matching values';
        case 'MEAN': return 'Calculates the mean value';
        case 'EXISTS': return 'Reports whether a value exists';
        case 'CONTAINS_ALL': return 'Reports whether all requested values exist';
        case 'REQUIRE_ONE': return 'Requires one matching value';
        case 'COLLECT': return 'Collects matching values into a list';
        case 'FIRST_ORDERED': return 'Keeps one value using the configured time ordering';
        default: {
          const exhaustive: never = operation;
          return exhaustive;
        }
      }
    })();
    reductions.push(`${summary}${path}.`);
  }
  return [...new Set(reductions)];
};

const sourcePaths = (column: DraftTable['document']['columns'][number]): ReadonlyArray<string> => {
  const source = column.source;
  switch (source.kind) {
    case 'field':
      return [source.field.path];
    case 'aggregate':
      return source.aggregate.path ? [source.aggregate.path] : [];
    case 'identifierBySystem':
    case 'extensionByUrl':
    case 'codedValue': {
      const lookup = source.lookup;
      if ('identifier' in lookup) {
        return [lookup.identifier.ownerPath, lookup.identifier.systemPath, lookup.identifier.valuePath];
      }
      if ('extension' in lookup) {
        return [lookup.extension.ownerPath, lookup.extension.valuePath, ...lookup.extension.urlPath];
      }
      if ('binding' in lookup) {
        return [lookup.binding.ownerPath ?? '', lookup.binding.valuePath, lookup.binding.keyPath];
      }
      return [lookup.match, lookup.path ?? ''];
    }
    case 'ownerRecords':
      return [source.ownerRecords.binding.ownerPath ?? '', source.ownerRecords.binding.valuePath];
    case 'categoricalBySystem':
      return [
        `${source.categorical.binding.keyPath}.${source.categorical.binding.valuePath}`,
        source.categorical.system,
      ];
    case 'projectId':
      return [];
    default: {
      const exhaustive: never = source;
      return exhaustive;
    }
  }
};

const diagnosticTarget = (
  diagnostic: ExplorerAuthoringDiagnostic,
  tables: ReadonlyArray<DraftTable>,
): DatasetReviewTarget | undefined => {
  const detailStrings = Object.values(diagnostic.details ?? {}).filter(
    (value): value is string => typeof value === 'string',
  );
  const values = [diagnostic.path, diagnostic.fieldPath, ...detailStrings].filter(
    (value): value is string => Boolean(value),
  );
  const tokens = new Set(values.flatMap((value) =>
    value.split(/[./\[\]]+/).map((token) => token.toLocaleLowerCase()).filter(Boolean),
  ));
  const outputId = typeof diagnostic.details?.outputId === 'string'
    ? diagnostic.details.outputId
    : undefined;
  const namedTables = tables.filter((table) => tokens.has(table.outputId.toLocaleLowerCase()));
  const scopedTables = outputId
    ? tables.filter((table) => table.outputId === outputId)
    : namedTables.length === 1
      ? namedTables
      : tables;
  const matches = scopedTables.flatMap((table) => table.document.columns
    .filter((column) => [column.column, column.label, ...sourcePaths(column)].some((identity) =>
      identity && (values.includes(identity) || tokens.has(identity.toLocaleLowerCase())),
    ))
    .map((column) => ({ table, column })),
  );
  if (matches.length === 1) {
    const { table, column } = matches[0];
    return {
      kind: 'column',
      outputId: table.outputId,
      column: column.column,
      label: column.label,
      occurrenceId: column.occurrenceId,
    };
  }
  const namesRowSetting = ['rows', 'rowdefinition', 'rootresourcetype'].some((token) => tokens.has(token));
  if (namesRowSetting && scopedTables.length === 1) {
    const table = scopedTables[0];
    return {
      kind: 'row',
      outputId: table.outputId,
      control: table.document.rootResourceType ? 'row-definition' : 'row-type',
    };
  }
  if (scopedTables.length === 1) return { kind: 'table', outputId: scopedTables[0].outputId };
  return undefined;
};

const reviewItems = (inputs: ReviewInputs): ReadonlyArray<DatasetReviewItem> => {
  const { tables, catalog, receipt, preview, diagnostics, reconciliation } = inputs;
  const currentReceipt = reconciliation === 'resolved' ? receipt : undefined;
  const items: DatasetReviewItem[] = [];
  if (!tables.length) {
    items.push({
      kind: 'blocking-issue',
      id: 'dataset:no-tables',
      location: 'Dataset',
      message: 'Create at least one output table before publishing.',
      target: { kind: 'new-table' },
    });
  }
  for (const table of tables) {
    const output = currentReceipt?.outputs.find((candidate) => candidate.outputId === table.outputId);
    const assessment = reconciliation === 'resolved' && output
      ? 'current'
      : reconciliation === 'pending'
        ? 'pending'
        : reconciliation === 'stale' || reconciliation === 'repair'
          ? 'stale'
          : 'missing';
    items.push({ kind: 'compile-assessment', outputId: table.outputId, status: assessment });
    items.push({
      kind: 'row-definition',
      outputId: table.outputId,
      summary: rowDescription(table, catalog, output?.rowGrain),
    });

    const columnLosses = new Set<string>();
    for (const column of table.document.columns) {
      const emitted = output?.columns.filter((candidate) =>
        candidate.authoredColumns?.includes(column.column) || candidate.column === column.column,
      ) ?? [];
      const shapes = emitted.length
        ? emitted.map(compiledShape)
        : [{ kind: 'unknown', reason: 'No current compiled output shape is available.' } as const];
      const reductions = authoredReductions(column);
      for (const contractColumn of emitted) {
        for (const reason of contractColumn.lossReasons ?? []) columnLosses.add(reason);
      }
      const compilerReductions = emitted.flatMap((candidate) => candidate.lossReasons ?? []).map(lossLabel);
      items.push({
        kind: 'requested-column',
        outputId: table.outputId,
        column: column.column,
        label: column.label,
        included: visibleColumn(column),
        shapes,
        reductions: [...new Set([...reductions, ...compilerReductions])],
      });
    }
    for (const reason of output?.lossReasons ?? []) {
      if (!columnLosses.has(reason)) {
        items.push({ kind: 'table-reduction', outputId: table.outputId, summary: lossLabel(reason) });
      }
    }
    if (table.document.rootResourceType === '') {
      items.push({
        kind: 'blocking-issue',
        outputId: table.outputId,
        id: `${table.outputId}:row-type`,
        location: 'Starting row type',
        message: `${table.title} needs a starting row type before it can be published.`,
        target: { kind: 'row', outputId: table.outputId, control: 'row-type' },
      });
    }
    if (table.document.columns.length === 0) {
      items.push({
        kind: 'blocking-issue',
        outputId: table.outputId,
        id: `${table.outputId}:columns`,
        location: 'Requested columns',
        message: `${table.title} needs at least one requested column before it can be published.`,
        target: { kind: 'table', outputId: table.outputId },
      });
    } else if (!table.document.columns.some(visibleColumn)) {
      const column = table.document.columns[0];
      items.push({
        kind: 'blocking-issue',
        outputId: table.outputId,
        id: `${table.outputId}:visible-columns`,
        location: `${column.label} (${column.column})`,
        message: `Make at least one configured column visible in ${table.title} before publishing.`,
        target: {
          kind: 'column',
          outputId: table.outputId,
          column: column.column,
          label: column.label,
          occurrenceId: column.occurrenceId,
        },
      });
    }
    if (output && output.columns.length === 0 && table.document.columns.some(visibleColumn)) {
      const column = table.document.columns.find(visibleColumn);
      items.push({
        kind: 'blocking-issue',
        outputId: table.outputId,
        id: `${table.outputId}:compiled-columns`,
        location: 'Compiled output',
        message: `Loom reported no output columns for ${table.title}.`,
        target: column ? {
          kind: 'column',
          outputId: table.outputId,
          column: column.column,
          label: column.label,
          occurrenceId: column.occurrenceId,
        } : { kind: 'table', outputId: table.outputId },
      });
    }
    const tablePreview = preview?.outputId === table.outputId ? preview : undefined;
    const previewIsCurrent = Boolean(
      tablePreview && currentReceipt && tablePreview.receiptId === currentReceipt.receiptId,
    );
    items.push({
      kind: 'preview-evidence',
      outputId: table.outputId,
      status: previewIsCurrent ? 'current-sample' : tablePreview ? 'stale-sample' : 'missing',
      sampleRows: tablePreview?.rows?.length ?? 0,
    });
  }

  const previewDiagnostics = currentReceipt && preview?.receiptId === currentReceipt.receiptId
    ? preview.diagnostics
    : [];
  const allDiagnostics = [
    ...diagnostics,
    ...(currentReceipt?.diagnostics ?? []),
    ...previewDiagnostics,
  ];
  const seenDiagnostics = new Set<string>();
  for (const diagnostic of allDiagnostics) {
    if (diagnostic.severity !== 'error') continue;
    const key = [diagnostic.code, diagnostic.path, diagnostic.fieldPath, diagnostic.message].join('|');
    if (seenDiagnostics.has(key)) continue;
    seenDiagnostics.add(key);
    const target = diagnosticTarget(diagnostic, tables);
    items.push({
      kind: 'blocking-issue',
      outputId: target && 'outputId' in target ? target.outputId : undefined,
      id: `diagnostic:${key}`,
      location: diagnostic.fieldPath || diagnostic.path || diagnostic.stage || 'Dataset',
      message: diagnostic.message || 'The Builder reported a blocking error.',
      target,
    });
  }
  return items;
};

const compileStatusLabel = (status: Extract<DatasetReviewItem, { kind: 'compile-assessment' }>['status']) => {
  switch (status) {
    case 'current': return 'Compiler result matches the saved draft.';
    case 'pending': return 'The saved draft is being compiled. Result shapes are not current yet.';
    case 'stale': return 'The compile result is stale. Result shapes are not confirmed for this draft.';
    case 'missing': return 'No compile result is available for this saved draft yet.';
    default: {
      const exhaustive: never = status;
      return exhaustive;
    }
  }
};

const targetAction = (target: DatasetReviewTarget) => {
  switch (target.kind) {
    case 'column': return `Review column ${target.label}`;
    case 'row': return target.control === 'row-type' ? 'Choose row type' : 'Review row definition';
    case 'new-table': return 'Create your first table';
    case 'table': return 'Open table builder';
    default: {
      const exhaustive: never = target;
      return exhaustive;
    }
  }
};

const ReviewItemView = ({
  item,
  onFocus,
}: {
  readonly item: DatasetReviewItem;
  readonly onFocus: (target: DatasetReviewTarget) => void;
}) => {
  switch (item.kind) {
    case 'compile-assessment':
      return <p className="text-xs text-slate-600">{compileStatusLabel(item.status)}</p>;
    case 'row-definition':
      return (
        <div>
          <h4 className="text-xs font-semibold uppercase tracking-wide text-slate-500">Saved row definition</h4>
          <p className="mt-1 text-sm text-slate-800">{item.summary}</p>
        </div>
      );
    case 'requested-column':
      return (
        <div className="rounded border border-slate-200 bg-white px-3 py-2">
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <h4 className="font-medium text-slate-900">{item.label} <span className="font-mono text-xs text-slate-500">({item.column})</span></h4>
            <span className={item.included ? 'text-xs text-emerald-800' : 'text-xs text-slate-500'}>
              {item.included ? 'Included in output' : 'Configured, hidden from output'}
            </span>
          </div>
          <p className="mt-1 text-xs text-slate-700">
            Result shape: {item.shapes.map(shapeLabel).join('; ')}
          </p>
          {item.reductions.length ? (
            <ul className="mt-1 list-inside list-disc text-xs text-amber-800">
              {item.reductions.map((reduction) => <li key={reduction}>{reduction}</li>)}
            </ul>
          ) : null}
        </div>
      );
    case 'table-reduction':
      return <p className="text-xs text-amber-800">Table-level reduction: {item.summary}</p>;
    case 'blocking-issue': {
      const target = item.target;
      return (
        <div role="alert" className="rounded border border-red-200 bg-red-50 px-3 py-2">
          <p className="text-sm font-medium text-red-900">{item.message}</p>
          <p className="mt-1 text-xs text-red-800">Responsible setting: {item.location}</p>
          {target ? (
            <button
              type="button"
              className="mt-2 rounded border border-red-300 bg-white px-2 py-1 text-xs font-semibold text-red-900 hover:bg-red-100"
              onClick={() => onFocus(target)}
            >
              {targetAction(target)}
            </button>
          ) : null}
        </div>
      );
    }
    case 'preview-evidence':
      return (
        <div className="rounded border border-slate-200 bg-slate-50 px-3 py-2">
          <h4 className="text-xs font-semibold text-slate-700">Preview evidence</h4>
          {item.status === 'current-sample' ? (
            <p className="mt-1 text-xs text-slate-600">
              Preview returned {item.sampleRows} sample rows. This sample does not validate the complete population.
            </p>
          ) : item.status === 'stale-sample' ? (
            <p className="mt-1 text-xs text-amber-800">
              The available preview is from another table or draft. Complete-population validation is not reported here.
            </p>
          ) : (
            <p className="mt-1 text-xs text-slate-600">
              No current preview sample is available. Complete-population validation is not reported here.
            </p>
          )}
        </div>
      );
    default: {
      const exhaustive: never = item;
      return exhaustive;
    }
  }
};

export const DatasetReviewPanel = ({
  tables,
  catalog,
  receipt,
  preview,
  diagnostics,
  reconciliation,
  onFocus,
  onClose,
}: ReviewInputs & {
  readonly onFocus: (target: DatasetReviewTarget) => void;
  readonly onClose: () => void;
}) => {
  const items = reviewItems({ tables, catalog, receipt, preview, diagnostics, reconciliation });
  const datasetBlockers = items.filter(
    (item): item is Extract<DatasetReviewItem, { kind: 'blocking-issue' }> =>
      item.kind === 'blocking-issue' && !item.outputId,
  );
  return (
    <section
      id="dataset-review-panel"
      aria-label="Dataset review"
      tabIndex={-1}
      className="mx-auto mb-3 max-w-[1920px] rounded-xl border border-blue-200 bg-blue-50/60 p-4 shadow-sm"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold text-slate-900">Review dataset</h2>
          <p className="mt-1 max-w-4xl text-sm text-slate-700">
            This review summarizes saved row and column definitions with compiler-reported shapes and reductions. It does not establish clinical correctness or machine-learning readiness.
          </p>
        </div>
        <button
          type="button"
          className="rounded border border-slate-300 bg-white px-2.5 py-1 text-sm text-slate-700 hover:bg-slate-100"
          onClick={onClose}
        >
          Close review
        </button>
      </div>
      {datasetBlockers.length ? (
        <section aria-label="Dataset blocking issues" className="mt-4 space-y-2">
          <h3 className="font-semibold text-red-900">Blocking issues</h3>
          {datasetBlockers.map((item) => <ReviewItemView key={item.id} item={item} onFocus={onFocus} />)}
        </section>
      ) : null}
      {tables.length ? (
        <div className="mt-4 space-y-3">
          {tables.map((table) => {
            const tableItems = items.filter((item) =>
              'outputId' in item && item.outputId === table.outputId,
            );
            return (
              <article key={table.outputId} aria-label={`${table.title} review`} className="rounded-lg border border-slate-200 bg-white p-3">
                <h3 className="text-base font-semibold text-slate-900">{table.title}</h3>
                <div className="mt-3 space-y-3">
                  {tableItems.map((item, index) => (
                    <ReviewItemView
                      key={item.kind === 'blocking-issue' ? item.id : `${item.kind}:${table.outputId}:${index}`}
                      item={item}
                      onFocus={onFocus}
                    />
                  ))}
                </div>
              </article>
            );
          })}
        </div>
      ) : (
        <p className="mt-4 text-sm text-slate-700">No saved tables are available to review.</p>
      )}
      {!items.some((item) => item.kind === 'blocking-issue') ? (
        <p className="mt-4 rounded border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-900">
          No blocking issues are currently reported for these saved tables.
        </p>
      ) : null}
    </section>
  );
};
