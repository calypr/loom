import React, { useMemo, useState } from 'react';
import type { ExplorerRuntimeOutputV1 } from './types';
import { useLoomRows, useLoomRuntime, LoomProvider } from './react';
import { createLoomClient, type LoomClient } from './api';

export interface LoomExplorerViewerProps {
  readonly project: string;
  readonly explorerId?: string;
  readonly client?: LoomClient;
  readonly className?: string;
}

const textFor = (value: unknown): string => {
  if (value === undefined || value === null) return '—';
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') return String(value);
  if (Array.isArray(value)) return value.map(textFor).filter((value) => value !== '—').join('; ') || '—';
  if (typeof value === 'object') {
    const record = value as Record<string, unknown>;
    for (const key of ['text', 'display', 'value', 'code', 'reference']) {
      if (record[key] !== undefined) return textFor(record[key]);
    }
    try { return JSON.stringify(value); } catch { return String(value); }
  }
  return String(value);
};

const outputColumns = (output: ExplorerRuntimeOutputV1): ReadonlyArray<string> => {
  const allowed = new Set(output.columns.filter((column) => column.visible).map((column) => column.column));
  return [...output.table.columns]
    .filter((binding) => binding.visible && allowed.has(binding.column))
    .map((binding) => binding.column);
};

const ViewerContent = ({ project, explorerId }: Required<Pick<LoomExplorerViewerProps, 'project'>> & Pick<LoomExplorerViewerProps, 'explorerId'>) => {
  const runtime = useLoomRuntime({ project, explorerId: explorerId ?? 'default' });
  const [selectedOutputId, setSelectedOutputId] = useState<string>();
  const [filterText, setFilterText] = useState('');
  const output = useMemo(() => {
    if (!runtime.data) return undefined;
    return runtime.data.outputs.find((candidate) => candidate.outputId === selectedOutputId) ?? runtime.data.outputs[0];
  }, [runtime.data, selectedOutputId]);
  const columns = useMemo(() => output ? outputColumns(output) : [], [output]);
  const rows = useLoomRows(project, output?.selector, columns, { enabled: Boolean(output), first: 100 });
  const visibleRows = useMemo(() => {
    const needle = filterText.trim().toLowerCase();
    if (!needle) return rows.data?.rows ?? [];
    return (rows.data?.rows ?? []).filter((row) => columns.some((column) => textFor(row[column]).toLowerCase().includes(needle)));
  }, [columns, filterText, rows.data]);

  if (runtime.isLoading) return <main className="loom-loading" role="status">Loading Explorer…</main>;
  if (runtime.error || !runtime.data) return <main className="loom-error" role="alert">The published Explorer could not be loaded.</main>;
  if (runtime.data.outputs.length === 0) return <main className="loom-empty">This Explorer has no published tables.</main>;

  return (
    <main className="loom-viewer">
      <header className="loom-viewer-header">
        <div>
          <p className="loom-eyebrow">Loom Explorer</p>
          <h1>{runtime.data.outputs[0]?.title ?? 'Explorer'}</h1>
        </div>
        <label className="loom-search">
          <span>Search rows</span>
          <input value={filterText} onChange={(event) => setFilterText(event.currentTarget.value)} placeholder="Search visible values" />
        </label>
      </header>
      <nav className="loom-tabs" aria-label="Explorer tables">
        {runtime.data.outputs.map((candidate) => (
          <button
            type="button"
            key={candidate.outputId}
            className={candidate.outputId === output?.outputId ? 'active' : ''}
            onClick={() => { setSelectedOutputId(candidate.outputId); setFilterText(''); }}
          >
            {candidate.title}
          </button>
        ))}
      </nav>
      {output ? (
        <section className="loom-table-card" aria-label={output.title}>
          <div className="loom-table-heading">
            <div><strong>{output.title}</strong><span>{rows.data?.totalCount ?? '—'} rows</span></div>
            {rows.isLoading ? <span role="status">Loading rows…</span> : null}
            {rows.error ? <span className="loom-error-text" role="alert">Rows could not be loaded.</span> : null}
          </div>
          <div className="loom-table-scroll">
            <table>
              <thead><tr>{columns.map((column) => <th key={column}>{output.columns.find((candidate) => candidate.column === column)?.label ?? column}</th>)}</tr></thead>
              <tbody>
                {visibleRows.map((row, index) => <tr key={`${index}-${textFor(row[columns[0] ?? ''])}`}>{columns.map((column) => <td key={column} title={textFor(row[column])}>{textFor(row[column])}</td>)}</tr>)}
              </tbody>
            </table>
            {!rows.isLoading && visibleRows.length === 0 ? <p className="loom-empty">No rows match the current search.</p> : null}
          </div>
        </section>
      ) : null}
    </main>
  );
};

export const LoomExplorerViewer = ({ project, explorerId, client, className }: LoomExplorerViewerProps) => {
  const ownedClient = useMemo(() => client ?? createLoomClient(), [client]);
  return <LoomProvider client={ownedClient}><div className={['loom-ui-root', className].filter(Boolean).join(' ')}><ViewerContent project={project} explorerId={explorerId} /></div></LoomProvider>;
};

export default LoomExplorerViewer;
