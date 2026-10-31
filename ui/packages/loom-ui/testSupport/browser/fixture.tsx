import React, { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { MantineProvider } from '@mantine/core';
import '@mantine/core/styles.css';
import '../../src/styles.css';
import { PreviewTable, type PreviewTablePresentationChange } from '../../src/features/ExplorerBuilder/components/PreviewTable';
import { OutputTable } from '../../src/features/ExplorerViewer/components';
import { createViewerReducerState } from '../../src/features/ExplorerViewer/reducer';
import type { DraftTable } from '../../src/features/ExplorerBuilder/authoring/model';
import type { ExplorerBuilderColumn, ExplorerBuilderPreviewResult, ExplorerRuntimeV1 } from '../../src/types';

const columns: ExplorerBuilderColumn[] = ['First', 'Second'].map((label, order) => ({
  column: label.toLowerCase(), label, occurrenceId: 'base',
  source: { kind: 'field', field: { path: label.toLowerCase(), projectionMode: 'FIRST' } },
  table: { visible: true, order },
}));
const rows = [
  { first: 'patient-A', second: 'observation-A', __loom_row_id: 'canonical-row-A' },
  { first: 'patient-B', second: 'observation-B', __loom_row_id: 'canonical-row-B' },
];
const preview: ExplorerBuilderPreviewResult = {
  apiVersion: 'loom.calypr.org/explorer-authoring/v2', kind: 'ExplorerBuilderPreview',
  receiptId: 'browser-preview', outputId: 'rows',
  columns: columns.map(column => ({ column: column.column, label: column.label, logicalType: 'string', filterable: true, chartable: true })),
  rows, rowCount: 2, diagnostics: [], rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'BROWSER_FIXTURE' },
};
const fileId = '11111111-2222-4333-8444-555555555555';
const runtime: ExplorerRuntimeV1 = {
  outputs: [{ outputId: 'files', name: 'files', title: 'Files', rowLabel: 'file',
    selector: { recipe: 'fixture-recipe', translationVersion: 'v1', output: 'files' },
    columns: [{ column: 'file', label: 'File', logicalType: 'string', visible: true, order: 0, filterable: false, chartable: false }],
    table: { columns: [{ column: 'file', label: 'File', visible: true, cellRenderer: 'fileActions' }] },
    filters: [], charts: [], fixedFilters: {} }],
  sharedFilters: {}, diagnostics: [],
  fileActions: { extensions: { txt: ['file_download'] }, actions: { file_download: new URLSearchParams(location.search).get('downloadBase') === 'query' ? '/download?source=viewer&redirect=false#details' : '/download' } },
};
function App() {
  // This is a test-owned caller for production components, not BuilderWorkspace
  // or a claim about the real command/store path.
  const [authored, setAuthored] = useState<ExplorerBuilderColumn[]>(() => {
    const saved = sessionStorage.getItem('browser-fixture-columns');
    return saved ? JSON.parse(saved) : columns;
  });
  const table: DraftTable = { outputId: 'rows', tabId: 'rows', title: 'Rows', document: {
    kind: 'ExplorerBuilderDocument', output: { id: 'rows', title: 'Rows' }, rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' }, rows: { kind: 'RECORDS', records: {} }, columns: authored,
  } };
  const change = (changes: readonly PreviewTablePresentationChange[]) => setAuthored(current => {
    const next = current.map(column => changes.find(change => change.kind === 'AUTHORED_COLUMN' && change.column.column === column.column))
      .map((change, index) => change?.kind === 'AUTHORED_COLUMN' ? change.column : current[index]);
    sessionStorage.setItem('browser-fixture-columns', JSON.stringify(next));
    return next;
  });
  return <MantineProvider><main className="loom-ui-root" style={{ width: 1100, margin: 24 }}>
    <h1>Production component browser fixture</h1>
    {new URLSearchParams(location.search).get('feature') === 'download'
      ? <OutputTable runtime={runtime} output={runtime.outputs[0]}
          result={{ columns: ['file'], rows: [{ file: fileId, file_name: 'fixture.txt' }], rowIds: ['file-row-A'], totalCount: 1, pageInfo: { hasNextPage: false }, facets: [] }}
          state={createViewerReducerState(runtime)} dispatch={() => undefined} />
      : <PreviewTable preview={preview} table={table} limit={25} onLimitChange={() => undefined}
          onColumnChange={column => change([column])} onColumnsChange={change} />}
  </main></MantineProvider>;
}
createRoot(document.getElementById('root')!).render(<App />);
