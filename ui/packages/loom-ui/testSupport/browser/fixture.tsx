import React, { useState } from 'react';
import { RouteFixture } from './routeFixture';
import { createRoot } from 'react-dom/client';
import { MantineProvider } from '@mantine/core';
import '@mantine/core/styles.css';
import '../../src/styles.css';
import { PreviewTable } from '../../src/features/ExplorerBuilder/components/PreviewTable';
import { OutputTable } from '../../src/features/ExplorerViewer/components';
import { createViewerReducerState } from '../../src/features/ExplorerViewer/reducer';
import type { DraftTable } from '../../src/features/ExplorerBuilder/model/authoring';
import type { ExplorerBuilderColumn, ExplorerBuilderPreviewResult, ExplorerRuntimeV1 } from '../../src/contracts/types';

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
  const [limit, setLimit] = useState<25 | 50 | 100 | 500 | 1000>(25);
  const [limitChanges, setLimitChanges] = useState<number[]>([]);
  const table: DraftTable = { outputId: 'rows', tabId: 'rows', title: 'Rows', document: {
    kind: 'ExplorerBuilderDocument', output: { id: 'rows', title: 'Rows' }, rootResourceType: 'Patient',
    route: { occurrenceId: 'base', resourceType: 'Patient' }, rows: { kind: 'RECORDS', records: {} }, columns,
  } };
  return <MantineProvider><main className="loom-ui-root" style={{ width: 1100, margin: 24 }}>
    <h1>Production component browser fixture</h1>
    {new URLSearchParams(location.search).get('feature') === 'route' ? <RouteFixture /> : new URLSearchParams(location.search).get('feature') === 'download'
      ? <OutputTable runtime={runtime} output={runtime.outputs[0]}
          result={{ columns: ['file'], rows: [{ file: fileId, file_name: 'fixture.txt' }], rowIds: ['file-row-A'], totalCount: 1, pageInfo: { hasNextPage: false }, facets: [] }}
          state={createViewerReducerState(runtime)} dispatch={() => undefined} />
      : <><output data-testid="preview-limit-state">{JSON.stringify({ limit, changes: limitChanges })}</output><PreviewTable preview={preview} table={table} limit={limit} onLimitChange={(next) => { setLimit(next); setLimitChanges((previous) => [...previous, next]); }} /></>}
  </main></MantineProvider>;
}
createRoot(document.getElementById('root')!).render(<App />);
