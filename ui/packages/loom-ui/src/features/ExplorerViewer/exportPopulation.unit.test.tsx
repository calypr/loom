// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createLoomClient, type Artifact, type LoomClient, type LoomOutputRequest } from '../../api';
import { LoomExplorerViewer, type LoomViewerActionHandler } from '../../Viewer';
import type { CellTraceResponse } from '../../cellTrace';
import type { ExplorerRuntimeV1 } from '../../types';

const runtime: ExplorerRuntimeV1 = {
  publication: { state: 'READY', revisionId: 'revision-published-7' },
  outputs: [{ outputId: 'patients', name: 'patients', title: 'Patients', rowLabel: 'patient',
    selector: { recipe: 'recipe-pinned', translationVersion: 'v1', output: 'patients' },
    columns: [
      { column: 'id', label: 'ID', logicalType: 'string', visible: true, order: 0, filterable: false, chartable: false },
      { column: 'gender', label: 'Gender', logicalType: 'string', visible: true, order: 1, filterable: true, chartable: false },
    ],
    table: { columns: [{ column: 'id', label: 'ID', visible: true }, { column: 'gender', label: 'Gender', visible: true }] },
    filters: [{ column: 'gender', label: 'Gender', type: 'enum' }], charts: [], fixedFilters: {},
  }], sharedFilters: {}, diagnostics: [],
};
const population = [{ id: 'patient-female', gender: 'female' }, { id: 'patient-male', gender: 'male' }];
const encounterRows = [['encounter-confirmed', 'confirmed']];
const tableData: Record<string, { readonly columns: ReadonlyArray<string>; readonly rows: ReadonlyArray<ReadonlyArray<string>>; readonly rowIds: ReadonlyArray<string> }> = {
  patients: { columns: ['id', 'gender'], rows: [['patient-female', 'female'], ['patient-male', 'male']], rowIds: ['opaque-female', 'opaque-male'] },
  encounters: { columns: ['id', 'status'], rows: encounterRows, rowIds: ['opaque-encounter'] },
  collisions: { columns: ['id', 'status'], rows: [['collision-1', 'conflict']], rowIds: ['opaque-collision'] },
};
const artifact = (index: number): Artifact => ({
  id: `artifact_${String(index).repeat(64)}`, project: 'org/project', explorerId: 'explorer-main',
  revisionId: 'revision-published-7', outputId: 'patients', receiptId: 'receipt-published-7', executionId: 'execution-7',
  datasetGeneration: 'generation-authorized-7', schemaDigest: 'sha256:schema-7', state: 'COMPLETE', format: 'CSV',
  filename: 'complete-patients.zip', mediaType: 'application/zip', archiveSha256: 'a'.repeat(64), bytes: 1024, rows: 2, features: 2,
  createdAt: '2026-10-31T00:00:00Z', completedAt: '2026-10-31T00:00:01Z', expiresAt: '2026-11-01T00:00:00Z',
});
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
};
const gate = () => {
  let resolve!: () => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<void>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
};
function harness(options: { runtime?: ExplorerRuntimeV1; respond?: (index: number) => Promise<Artifact>; activeOutputId?: string; customActions?: Readonly<Record<string, LoomViewerActionHandler>>; customActionsFor?: (client: LoomClient) => Readonly<Record<string, LoomViewerActionHandler>> } = {}) {
  const requests: Array<{ url: string; method: string; body: Record<string, unknown> }> = [];
  const graphql: Array<Record<string, unknown>> = [];
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/graphql/graph')) {
      const payload = JSON.parse(String(init?.body)) as { variables: { input: { selector: { output: string }; filters?: Array<{ column: string; op: string; value: unknown }> } } };
      const { input: queryInput } = payload.variables;
      graphql.push({ ...queryInput });
      const table = tableData[queryInput.selector.output];
      if (!table) throw new Error(`Unknown output ${queryInput.selector.output}`);
      const filtered = queryInput.filters?.some(filter => filter.column === 'gender' && filter.op === 'EQ' && filter.value === 'female');
      const rows = filtered ? table.rows.slice(0, 1) : [...table.rows];
      const rowIds = filtered ? table.rowIds.slice(0, 1) : [...table.rowIds];
      return new Response(JSON.stringify({
        data: {
          dataframeRows: {
            columns: table.columns, rows, rowIds, totalCount: rows.length,
            pageInfo: { hasNextPage: false, endCursor: null },
          },
        },
      }), { status: 200, headers: { 'content-type': 'application/json' } });
    }
    if (!url.endsWith('/artifacts')) throw new Error(`Unexpected transport: ${url}`);
    requests.push({ url, method: init?.method ?? 'GET', body: JSON.parse(String(init?.body)) });
    const value = await (options.respond?.(requests.length) ?? Promise.resolve(artifact(requests.length)));
    return new Response(JSON.stringify(value), { status: 200, headers: { 'content-type': 'application/json' } });
  });
  const client = createLoomClient({ baseUrl: 'http://loom.test', fetch: fetcher as typeof fetch });
  vi.spyOn(client, 'getExplorer').mockResolvedValue(options.runtime ?? runtime);
  const query = vi.spyOn(client, 'queryOutput').mockImplementation(async (request: LoomOutputRequest) => {
    if (request.selector.output !== 'patients') {
      const table = tableData[request.selector.output];
      return {
        columns: table.columns,
        rows: table.rows.map((row) => Object.fromEntries(table.columns.map((column, index) => [column, row[index]]))),
        rowIds: [...table.rowIds], totalCount: table.rows.length,
        pageInfo: { hasNextPage: false }, facets: [],
      };
    }
    const filtered = request.filters?.some(filter => filter.column === 'gender' && filter.op === 'EQ' && filter.value === 'female');
    return { columns: ['id', 'gender'], rows: filtered ? [population[0]] : population,
      rowIds: filtered ? ['opaque-female'] : ['opaque-female', 'opaque-male'], totalCount: filtered ? 1 : 2,
      pageInfo: { hasNextPage: false },
      facets: [{ name: 'loom:patients:gender', kind: 'TERMS', columns: ['key', 'doc_count'], rows: [{ key: 'female', doc_count: 1 }, { key: 'male', doc_count: 1 }] }],
    };
  });
  const prepare = vi.spyOn(client, 'prepareArtifact');
  const exportOutput = vi.spyOn(client, 'exportOutput');
  const customActions = options.customActionsFor?.(client) ?? options.customActions;
  const view = render(<LoomExplorerViewer project="org/project" explorerId="explorer-main" client={client} {...(options.activeOutputId ? { activeOutputId: options.activeOutputId } : {})} {...(customActions ? { customActions } : {})} />);
  return { ...view, requests, graphql, query, prepare, client, exportOutput };
}
type CapturedDownload = { readonly fileName: string; readonly blob: Blob | undefined };
const downloadsOn = () => {
  const downloads: CapturedDownload[] = [];
  let pendingBlob: Blob | undefined;
  const created = vi.spyOn(URL, 'createObjectURL').mockImplementation((blob) => {
    if (blob instanceof Blob) pendingBlob = blob;
    return 'blob:loom-export';
  });
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => undefined);
  vi.spyOn(HTMLElement.prototype, 'click').mockImplementation(function (this: HTMLElement) {
    downloads.push({ fileName: this.getAttribute('download') ?? '', blob: pendingBlob });
    pendingBlob = undefined;
  });
  return { downloads, created };
};
const patientsOutput: ExplorerRuntimeV1['outputs'][number] = {
  ...runtime.outputs[0],
  outputId: 'pat-9c21',
  name: 'patients',
};
const encountersOutput: ExplorerRuntimeV1['outputs'][number] = {
  outputId: 'enc-7f3a', name: 'encounters', title: 'Encounters', rowLabel: 'encounter',
  selector: { recipe: 'recipe-pinned', translationVersion: 'v1', output: 'encounters' },
  columns: [
    { column: 'id', label: 'ID', logicalType: 'string', visible: true, order: 0, filterable: false, chartable: false },
    { column: 'status', label: 'Status', logicalType: 'string', visible: true, order: 1, filterable: true, chartable: false },
  ],
  table: { columns: [{ column: 'id', label: 'ID', visible: true }, { column: 'status', label: 'Status', visible: true }] },
  filters: [], charts: [], fixedFilters: {},
  actions: [
    { type: 'export-target-id', title: 'Export patients by ID', output: 'pat-9c21', fileName: 'patients-by-id.csv' },
    { type: 'export-target-name', title: 'Export patients by name', output: 'patients', fileName: 'patients-by-name.csv' },
    { type: 'export-active', title: 'Export active table' },
    { type: 'export-stale', title: 'Export stale table', output: 'visits-42' },
    { type: 'export-blank', title: 'Export blank target', output: '   ' },
    { type: 'export-ambiguous', title: 'Export duplicated table', output: 'encounters' },
    { type: 'export-target-id-no-filename', title: 'Export patients unfiled', output: 'pat-9c21' },
    { type: 'export-target-id-empty-filename', title: 'Export patients blank file', output: 'pat-9c21', fileName: '' },
    { type: 'export-wrong-case-id', title: 'Export patients by wrong-case ID', output: 'PAT-9C21' },
    { type: 'export-wrong-case-name', title: 'Export patients by wrong-case name', output: 'Patients' },
    { type: 'export-target-id-columns', title: 'Export patients gender', output: 'pat-9c21', columns: ['gender'], exportHeaders: { gender: 'Sex' } },
    { type: 'export-empty-string', title: 'Export empty target', output: '' },
    { type: 'export-padded-name', title: 'Export padded table', output: ' patients ', fileName: 'padded-by-name.csv' },
    { type: 'export-prefix-name', title: 'Export prefix table', output: 'patient' },
    { type: 'custom-export-patients', title: 'Custom export patients', output: 'pat-9c21' },
    { type: 'custom-export-stale', title: 'Custom export stale table', output: 'visits-42' },
  ],
};
const paddedNamedOutput: ExplorerRuntimeV1['outputs'][number] = {
  ...encountersOutput,
  outputId: 'pad-6c11', name: ' patients ', title: 'Padded named table', actions: undefined,
  selector: { recipe: 'recipe-pinned', translationVersion: 'v1', output: 'collisions' },
};
const shadowOutput: ExplorerRuntimeV1['outputs'][number] = {
  ...encountersOutput,
  outputId: 'shd-3a90', name: 'encounters', title: 'Shadow table', actions: undefined,
  selector: { recipe: 'recipe-pinned', translationVersion: 'v1', output: 'collisions' },
};
const twinEncountersOutput: ExplorerRuntimeV1['outputs'][number] = {
  ...encountersOutput, outputId: 'enc-8b02', title: 'Encounters copy', actions: undefined,
};
const blankNamedOutput: ExplorerRuntimeV1['outputs'][number] = {
  ...encountersOutput,
  outputId: 'blk-2e50', name: '   ', title: 'Blank named table', actions: undefined,
  selector: { recipe: 'recipe-pinned', translationVersion: 'v1', output: 'collisions' },
};
const emptyNamedOutput: ExplorerRuntimeV1['outputs'][number] = {
  ...encountersOutput,
  outputId: 'emp-4b08', name: '', title: 'Empty named table', actions: undefined,
  selector: { recipe: 'recipe-pinned', translationVersion: 'v1', output: 'collisions' },
};
const collidingOutput: ExplorerRuntimeV1['outputs'][number] = {
  ...encountersOutput,
  outputId: 'col-1d4e', name: 'enc-7f3a', title: 'Colliding table', actions: undefined,
  selector: { recipe: 'recipe-pinned', translationVersion: 'v1', output: 'collisions' },
};
const dualRuntime: ExplorerRuntimeV1 = { ...runtime, outputs: [patientsOutput, encountersOutput] };
const ambiguousRuntime: ExplorerRuntimeV1 = { ...runtime, outputs: [patientsOutput, encountersOutput, twinEncountersOutput] };
const paddedRuntime: ExplorerRuntimeV1 = { ...runtime, outputs: [patientsOutput, encountersOutput, paddedNamedOutput] };
const shadowRuntime: ExplorerRuntimeV1 = {
  ...runtime,
  outputs: [
    shadowOutput,
    patientsOutput,
    { ...encountersOutput, actions: [...(encountersOutput.actions ?? []), { type: 'export-shadow', title: 'Export shadow table', output: 'shd-3a90', fileName: 'shadow-by-id.csv' }] },
  ],
};
const blankNamedRuntime: ExplorerRuntimeV1 = { ...runtime, outputs: [blankNamedOutput, emptyNamedOutput, patientsOutput, encountersOutput] };
const collisionRuntime: ExplorerRuntimeV1 = {
  ...runtime,
  outputs: [
    collidingOutput,
    patientsOutput,
    { ...encountersOutput, actions: [...(encountersOutput.actions ?? []), { type: 'export-colliding', title: 'Export colliding target', output: 'enc-7f3a', fileName: 'colliding-by-id.csv' }] },
  ],
};
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
async function chooseFemale() {
  await screen.findByText('patient-male');
  fireEvent.click(await screen.findByRole('checkbox', { name: /female/ }));
  await waitFor(() => expect(screen.queryByText('patient-male')).toBeNull());
  expect(screen.getByText('patient-female')).toBeInTheDocument();
  expect(screen.getByText('1 matching rows')).toBeInTheDocument();
}
describe('Viewer complete-population dataset download', () => {
  it('keeps a transient facet in the visible query but sends only pinned export identity through the real API client', async () => {
    const { requests, query } = harness();
    await chooseFemale();
    expect(query.mock.calls.at(-1)?.[0].filters).toEqual([{ column: 'gender', op: 'EQ', value: 'female' }]);
    fireEvent.click(screen.getByRole('button', { name: 'Download dataset' }));
    const modal = await screen.findByRole('dialog', { name: 'Download dataset' });
    expect(within(modal).getByText(/Loom prepared 2 rows from the complete authorized population/)).toBeInTheDocument();
    expect(requests).toHaveLength(1);
    expect(requests[0].url).toBe('http://loom.test/api/v1/projects/org%252Fproject/explorers/explorer-main/authoring/v2/artifacts');
    expect(requests[0].method).toBe('POST');
    expect(requests[0].body).toEqual({ revisionId: 'revision-published-7', outputId: 'patients', idempotencyKey: expect.any(String) });
    const download = within(modal).getByRole('link', { name: 'Download ZIP' });
    expect(download).toHaveAttribute('download', 'complete-patients.zip');
    expect(download).toHaveAttribute('href', `http://loom.test/api/v1/projects/org%252Fproject/explorers/explorer-main/authoring/v2/artifacts/artifact_${'1'.repeat(64)}`);
  });
  it('Cancel and reopen retain the facet but create a new pinned artifact request and download identity', async () => {
    const { requests } = harness();
    await chooseFemale();
    fireEvent.click(screen.getByRole('button', { name: 'Download dataset' }));
    const first = await screen.findByRole('dialog', { name: 'Download dataset' });
    fireEvent.click(within(first).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Download dataset' })).toBeNull());
    expect(screen.queryByText('patient-male')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Download dataset' }));
    const second = await screen.findByRole('dialog', { name: 'Download dataset' });
    expect(requests).toHaveLength(2);
    expect(requests[1].body).toEqual({ revisionId: 'revision-published-7', outputId: 'patients', idempotencyKey: expect.any(String) });
    expect(requests[1].body.idempotencyKey).not.toBe(requests[0].body.idempotencyKey);
    expect(within(second).getByRole('link', { name: 'Download ZIP' }).getAttribute('href')).toContain(`artifact_${'2'.repeat(64)}`);
  });
  it('accepts the same project identity in its encoded transport form', async () => {
    harness({ respond: async () => ({ ...artifact(1), project: 'org%2Fproject' }) });
    await screen.findByText('patient-male');
    fireEvent.click(screen.getByRole('button', { name: 'Download dataset' }));
    const modal = await screen.findByRole('dialog', { name: 'Download dataset' });
    expect(within(modal).getByRole('link', { name: 'Download ZIP' })).toHaveAttribute('download', 'complete-patients.zip');
  });
  it.each([
    ['project', 'other/project'], ['explorerId', 'other-explorer'],
    ['revisionId', 'other-revision'], ['outputId', 'other-output'],
  ] as const)('rejects an artifact belonging to another %s', async (field, foreign) => {
    harness({ respond: async () => ({ ...artifact(1), [field]: foreign }) });
    await screen.findByText('patient-male');
    fireEvent.click(screen.getByRole('button', { name: 'Download dataset' }));
    await screen.findByText('Dataset download failed');
    expect(screen.queryByRole('dialog', { name: 'Download dataset' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'Download ZIP' })).toBeNull();
  });
  it('does not expose a completed artifact from an output left while preparation was pending', async () => {
    const pending = deferred<Artifact>();
    const other = { ...runtime.outputs[0], outputId: 'encounters', name: 'encounters', title: 'Encounters', selector: { ...runtime.outputs[0].selector, output: 'encounters' } };
    const { requests, prepare } = harness({ runtime: { ...runtime, outputs: [...runtime.outputs, other] }, respond: () => pending.promise });
    await screen.findByText('patient-male');
    fireEvent.click(screen.getByRole('button', { name: 'Download dataset' }));
    await waitFor(() => expect(requests).toHaveLength(1));
    fireEvent.click(screen.getByRole('tab', { name: /Encounters/ }));
    await act(async () => {
      pending.resolve(artifact(1));
      await prepare.mock.results[0].value;
    });
    await waitFor(() => expect(screen.getByRole('tab', { name: /Encounters/ })).toHaveAttribute('aria-selected', 'true'));
    expect(screen.queryByRole('dialog', { name: 'Download dataset' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'Download ZIP' })).toBeNull();
  });
});

describe('Viewer export action output targeting', () => {
  const actionHarness = (options: {
    runtime?: ExplorerRuntimeV1;
    customActionsFor?: (client: LoomClient) => Readonly<Record<string, LoomViewerActionHandler>>;
    uncontrolled?: boolean;
  } = {}) => {
    const { downloads, created } = downloadsOn();
    const view = harness({
      runtime: options.runtime ?? dualRuntime,
      ...(options.uncontrolled ? {} : { activeOutputId: encountersOutput.outputId }),
      ...('customActionsFor' in options ? { customActionsFor: options.customActionsFor } : {}),
    });
    return { ...view, downloads, created };
  };

  const unresolvedAlert = async (exportOutput: { mock: { calls: unknown[] } }, actionTitle: string, target?: string) => {
    for (let round = 0; round < 5; round += 1) await act(async () => { await Promise.resolve(); });
    expect(exportOutput.mock.calls, `action "${actionTitle}" must not issue an export request`).toHaveLength(0);
    expect(await screen.findByText('Action failed')).toBeInTheDocument();
    if (target) await screen.findByText((content) => content.includes(actionTitle) && content.includes(target));
  };

  it('exports the output named by an action output ID while a different output is active', async () => {
    const { exportOutput, downloads, created } = actionHarness();
    await screen.findByText('encounter-confirmed');
    expect(screen.getByRole('tab', { name: /Encounters/ })).toHaveAttribute('aria-selected', 'true');
    fireEvent.click(screen.getByRole('button', { name: 'Export patients by ID' }));
    await waitFor(() => expect(downloads).toHaveLength(1));
    expect(exportOutput).toHaveBeenCalledTimes(1);
    expect(exportOutput.mock.calls[0][0].selector).toEqual({ recipe: 'recipe-pinned', translationVersion: 'v1', output: 'patients' });
    expect(exportOutput.mock.calls[0][0].project).toBe('org/project');
    expect(created).toHaveBeenCalledTimes(1);
    expect(downloads[0].fileName).toBe('patients-by-id.csv');
    expect(await downloads[0].blob?.text()).toContain('patient-male');
    expect(screen.getByRole('tab', { name: /Encounters/ })).toHaveAttribute('aria-selected', 'true');
    expect(screen.queryByText('Action failed')).toBeNull();
  });

  it('resolves an action output by name to that same output', async () => {
    const { exportOutput, downloads, created } = actionHarness();
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Export patients by name' }));
    await waitFor(() => expect(downloads).toHaveLength(1));
    expect(exportOutput).toHaveBeenCalledTimes(1);
    expect(exportOutput.mock.calls[0][0].selector).toEqual({ recipe: 'recipe-pinned', translationVersion: 'v1', output: 'patients' });
    expect(created).toHaveBeenCalledTimes(1);
    expect(downloads[0].fileName).toBe('patients-by-name.csv');
    expect(await downloads[0].blob?.text()).toContain('patient-male');
    expect(screen.queryByText('Action failed')).toBeNull();
  });

  it('names an unfiled target export after the resolved output, not the active one', async () => {
    const { exportOutput, downloads, created } = actionHarness();
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Export patients unfiled' }));
    await waitFor(() => expect(downloads).toHaveLength(1));
    expect(exportOutput).toHaveBeenCalledTimes(1);
    expect(exportOutput.mock.calls[0][0].selector).toEqual({ recipe: 'recipe-pinned', translationVersion: 'v1', output: 'patients' });
    expect(created).toHaveBeenCalledTimes(1);
    expect(downloads[0].fileName).toBe('patients.csv');
    expect(await downloads[0].blob?.text()).toContain('patient-male');
    expect(screen.queryByText('Action failed')).toBeNull();
  });

  it('never renames a configured target download after the active output', async () => {
    const { exportOutput, downloads, created } = actionHarness();
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Export patients blank file' }));
    await waitFor(() => expect(downloads).toHaveLength(1));
    expect(exportOutput).toHaveBeenCalledTimes(1);
    expect(exportOutput.mock.calls[0][0].selector).toEqual({ recipe: 'recipe-pinned', translationVersion: 'v1', output: 'patients' });
    expect(created).toHaveBeenCalledTimes(1);
    expect(downloads[0].fileName).not.toBe('encounters.csv');
    expect(await downloads[0].blob?.text()).toContain('patient-male');
    expect(screen.queryByText('Action failed')).toBeNull();
  });

  it('applies the configured columns and headings to a non-active target export', async () => {
    const { exportOutput, downloads, created } = actionHarness();
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Export patients gender' }));
    await waitFor(() => expect(downloads).toHaveLength(1));
    expect(exportOutput).toHaveBeenCalledTimes(1);
    const exportRequest = exportOutput.mock.calls[0][0];
    expect(exportRequest.selector).toEqual({ recipe: 'recipe-pinned', translationVersion: 'v1', output: 'patients' });
    expect(exportRequest.columns).toEqual(['gender']);
    expect(exportRequest.exportHeaders).toEqual({ gender: 'Sex' });
    expect(created).toHaveBeenCalledTimes(1);
    expect(downloads[0].fileName).toBe('patients.csv');
    expect(await downloads[0].blob?.text()).toBe('Sex\nfemale\nmale\n');
    expect(screen.queryByText('Action failed')).toBeNull();
  });

  it('prefers an exact output ID over another output that uses the ID as its name', async () => {
    const { exportOutput, downloads, created } = actionHarness({ runtime: collisionRuntime });
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Export colliding target' }));
    await waitFor(() => expect(downloads).toHaveLength(1));
    expect(exportOutput).toHaveBeenCalledTimes(1);
    expect(exportOutput.mock.calls[0][0].selector).toEqual({ recipe: 'recipe-pinned', translationVersion: 'v1', output: 'encounters' });
    expect(created).toHaveBeenCalledTimes(1);
    expect(downloads[0].fileName).toBe('colliding-by-id.csv');
    expect(await downloads[0].blob?.text()).toContain('encounter-confirmed');
    expect(screen.queryByText('Action failed')).toBeNull();
  });

  it('resolves a padded target name to the output published with exactly that name', async () => {
    const { exportOutput, downloads, created } = actionHarness({ runtime: paddedRuntime });
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Export padded table' }));
    await waitFor(() => expect(downloads).toHaveLength(1));
    expect(exportOutput).toHaveBeenCalledTimes(1);
    expect(exportOutput.mock.calls[0][0].selector).toEqual({ recipe: 'recipe-pinned', translationVersion: 'v1', output: 'collisions' });
    expect(created).toHaveBeenCalledTimes(1);
    expect(downloads[0].fileName).toBe('padded-by-name.csv');
    expect(await downloads[0].blob?.text()).toContain('collision-1');
    expect(screen.queryByText('Action failed')).toBeNull();
  });

  it('shows an action error and makes no export request when only the trimmed name is published', async () => {
    const { exportOutput, requests, downloads, created } = actionHarness();
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Export padded table' }));
    await unresolvedAlert(exportOutput, 'Export padded table', 'patients');
    expect(exportOutput).not.toHaveBeenCalled();
    expect(requests).toHaveLength(0);
    expect(created).not.toHaveBeenCalled();
    expect(downloads).toHaveLength(0);
  });

  it('shows an action error and makes no export request for a name that only prefixes a published output', async () => {
    const { exportOutput, requests, downloads, created } = actionHarness();
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Export prefix table' }));
    await unresolvedAlert(exportOutput, 'Export prefix table', 'patient');
    expect(exportOutput).not.toHaveBeenCalled();
    expect(requests).toHaveLength(0);
    expect(created).not.toHaveBeenCalled();
    expect(downloads).toHaveLength(0);
  });

  it('exports the output that owns an ID even when another output shares the active name', async () => {
    const { exportOutput, downloads, created } = actionHarness({ runtime: shadowRuntime });
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Export shadow table' }));
    await waitFor(() => expect(downloads).toHaveLength(1));
    expect(exportOutput).toHaveBeenCalledTimes(1);
    expect(exportOutput.mock.calls[0][0].selector).toEqual({ recipe: 'recipe-pinned', translationVersion: 'v1', output: 'collisions' });
    expect(created).toHaveBeenCalledTimes(1);
    expect(downloads[0].fileName).toBe('shadow-by-id.csv');
    expect(await downloads[0].blob?.text()).toContain('collision-1');
    expect(screen.queryByText('Action failed')).toBeNull();
  });

  it('shows an action error and makes no export request or download for an output that is not published', async () => {
    const { exportOutput, graphql, requests, downloads, created } = actionHarness();
    await screen.findByText('encounter-confirmed');
    const graphqlBefore = graphql.length;
    fireEvent.click(screen.getByRole('button', { name: 'Export stale table' }));
    await unresolvedAlert(exportOutput, 'Export stale table', 'visits-42');
    expect(exportOutput).not.toHaveBeenCalled();
    expect(graphql).toHaveLength(graphqlBefore);
    expect(requests).toHaveLength(0);
    expect(created).not.toHaveBeenCalled();
    expect(downloads).toHaveLength(0);
  });

  it('shows an action error and makes no export request for an empty-string output', async () => {
    const { exportOutput, graphql, requests, downloads, created } = actionHarness();
    await screen.findByText('encounter-confirmed');
    const graphqlBefore = graphql.length;
    fireEvent.click(screen.getByRole('button', { name: 'Export empty target' }));
    await unresolvedAlert(exportOutput, 'Export empty target');
    expect(exportOutput).not.toHaveBeenCalled();
    expect(graphql).toHaveLength(graphqlBefore);
    expect(requests).toHaveLength(0);
    expect(created).not.toHaveBeenCalled();
    expect(downloads).toHaveLength(0);
  });

  it('exports the active output for an action without an explicit output', async () => {
    const { exportOutput, downloads, created } = actionHarness();
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Export active table' }));
    await waitFor(() => expect(downloads).toHaveLength(1));
    expect(exportOutput).toHaveBeenCalledTimes(1);
    expect(exportOutput.mock.calls[0][0].selector).toEqual({ recipe: 'recipe-pinned', translationVersion: 'v1', output: 'encounters' });
    expect(created).toHaveBeenCalledTimes(1);
    expect(downloads[0].fileName).toBe('encounters.csv');
    expect(await downloads[0].blob?.text()).toContain('encounter-confirmed');
    expect(screen.queryByText('Action failed')).toBeNull();
  });

  it.each([
    ['a blank explicit output', 'Export blank target', {}],
    ['an empty explicit output', 'Export blank target', { runtime: blankNamedRuntime }],
  ] as const)('shows an action error and makes no export request for %s', async (_label, actionTitle, options) => {
    const { exportOutput, requests, downloads, created } = actionHarness(options);
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: actionTitle }));
    await unresolvedAlert(exportOutput, actionTitle);
    expect(exportOutput).not.toHaveBeenCalled();
    expect(requests).toHaveLength(0);
    expect(created).not.toHaveBeenCalled();
    expect(downloads).toHaveLength(0);
  });

  it.each([
    ['an output ID whose case does not match', 'Export patients by wrong-case ID', 'PAT-9C21'],
    ['an output name whose case does not match', 'Export patients by wrong-case name', 'Patients'],
  ] as const)('shows an action error and makes no export request for %s', async (_label, actionTitle, target) => {
    const { exportOutput, requests, downloads, created } = actionHarness();
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: actionTitle }));
    await unresolvedAlert(exportOutput, actionTitle, target);
    expect(exportOutput).not.toHaveBeenCalled();
    expect(requests).toHaveLength(0);
    expect(created).not.toHaveBeenCalled();
    expect(downloads).toHaveLength(0);
  });

  it('shows an action error and makes no export request when an output name matches two published outputs', async () => {
    const { exportOutput, requests, downloads, created } = actionHarness({ runtime: ambiguousRuntime });
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Export duplicated table' }));
    await unresolvedAlert(exportOutput, 'Export duplicated table', 'encounters');
    expect(exportOutput).not.toHaveBeenCalled();
    expect(requests).toHaveLength(0);
    expect(created).not.toHaveBeenCalled();
    expect(downloads).toHaveLength(0);
  });

  type CapturedContext = {
    readonly actionOutput: string | undefined;
    readonly contextOutputId: string;
    readonly requestOutput: string;
    readonly requestProject: string;
    readonly requestColumns: ReadonlyArray<string>;
    readonly resultRowIds: ReadonlyArray<string> | undefined;
  };
  const recordingHandler = (contexts: CapturedContext[], loom: LoomClient): LoomViewerActionHandler => async (context, signal) => {
    contexts.push({
      actionOutput: context.action.output,
      contextOutputId: context.output.outputId,
      requestOutput: context.request.selector.output,
      requestProject: context.request.project,
      requestColumns: context.request.columns ?? [],
      resultRowIds: context.result?.rowIds,
    });
    await loom.exportOutput(context.request, signal);
  };

  it('gives a custom handler the resolved target output, request and result', async () => {
    const contexts: CapturedContext[] = [];
    const { exportOutput } = actionHarness({ customActionsFor: (client) => ({ 'custom-export-patients': recordingHandler(contexts, client) }) });
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Custom export patients' }));
    await waitFor(() => expect(contexts).toHaveLength(1));
    expect(contexts[0]).toEqual({
      actionOutput: 'pat-9c21',
      contextOutputId: 'pat-9c21',
      requestOutput: 'patients',
      requestProject: 'org/project',
      requestColumns: ['id', 'gender'],
      resultRowIds: undefined,
    });
    expect(exportOutput).toHaveBeenCalledTimes(1);
    expect(exportOutput.mock.calls[0][0].selector).toEqual({ recipe: 'recipe-pinned', translationVersion: 'v1', output: 'patients' });
    expect(screen.queryByText('Action failed')).toBeNull();
    expect(screen.getByRole('tab', { name: /Encounters/ })).toHaveAttribute('aria-selected', 'true');
  });

  it('gives a custom handler the target rows once the target output has been loaded', async () => {
    const contexts: CapturedContext[] = [];
    const { exportOutput } = actionHarness({ uncontrolled: true, customActionsFor: (client) => ({ 'custom-export-patients': recordingHandler(contexts, client) }) });
    await screen.findByText('patient-male');
    fireEvent.click(screen.getByRole('tab', { name: /Encounters/ }));
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Custom export patients' }));
    await waitFor(() => expect(contexts).toHaveLength(1));
    expect(contexts[0]).toEqual({
      actionOutput: 'pat-9c21',
      contextOutputId: 'pat-9c21',
      requestOutput: 'patients',
      requestProject: 'org/project',
      requestColumns: ['id', 'gender'],
      resultRowIds: ['opaque-female', 'opaque-male'],
    });
    expect(exportOutput).toHaveBeenCalledTimes(1);
    expect(exportOutput.mock.calls[0][0].selector).toEqual({ recipe: 'recipe-pinned', translationVersion: 'v1', output: 'patients' });
    expect(screen.queryByText('Action failed')).toBeNull();
    expect(screen.getByRole('tab', { name: /Encounters/ })).toHaveAttribute('aria-selected', 'true');
  });

  it('keeps the active output context for a custom handler whose action has no explicit output', async () => {
    const contexts: CapturedContext[] = [];
    const { exportOutput } = actionHarness({ customActionsFor: (client) => ({ 'export-active': recordingHandler(contexts, client) }) });
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Export active table' }));
    await waitFor(() => expect(contexts).toHaveLength(1));
    expect(contexts[0]).toEqual({
      actionOutput: undefined,
      contextOutputId: encountersOutput.outputId,
      requestOutput: 'encounters',
      requestProject: 'org/project',
      requestColumns: ['id', 'status'],
      resultRowIds: ['opaque-encounter'],
    });
    expect(exportOutput).toHaveBeenCalledTimes(1);
    expect(exportOutput.mock.calls[0][0].selector).toEqual({ recipe: 'recipe-pinned', translationVersion: 'v1', output: 'encounters' });
    expect(screen.queryByText('Action failed')).toBeNull();
  });

  it('does not run a custom handler for an action whose explicit output is not published', async () => {
    const contexts: CapturedContext[] = [];
    const { exportOutput, requests, downloads, created } = actionHarness({ customActionsFor: (client) => ({ 'custom-export-stale': recordingHandler(contexts, client) }) });
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Custom export stale table' }));
    await unresolvedAlert(exportOutput, 'Custom export stale table', 'visits-42');
    expect(contexts).toHaveLength(0);
    expect(exportOutput).not.toHaveBeenCalled();
    expect(requests).toHaveLength(0);
    expect(created).not.toHaveBeenCalled();
    expect(downloads).toHaveLength(0);
  });
});

const runtimeWithActions: ExplorerRuntimeV1 = {
  ...runtime,
  outputs: [{
    ...runtime.outputs[0],
    actions: [
      { type: 'flag-review', title: 'Flag for review' },
      { type: 'share-cohort', title: 'Share cohort' },
    ],
  }],
};
const actionHarness = (customActions: Readonly<Record<string, LoomViewerActionHandler>>) =>
  harness({ runtime: runtimeWithActions, customActions });
const actionHarnessFor = (
  actions: ReadonlyArray<{ readonly type: string; readonly title: string }>,
  handler: LoomViewerActionHandler,
) => harness({
  runtime: { ...runtimeWithActions, outputs: [{ ...runtimeWithActions.outputs[0], actions }] },
  customActions: Object.fromEntries(actions.map((action) => [action.type, handler])),
});
const expectPending = (button: HTMLElement) => {
  expect(button).toBeDisabled();
  expect(button).toHaveAttribute('data-loading', 'true');
};
const expectSettled = (button: HTMLElement) => {
  expect(button).toBeEnabled();
  expect(button).not.toHaveAttribute('data-loading');
};

describe('Viewer concurrent configured custom actions', () => {
  const actionButtons = () => ({
    runFirst: screen.getByRole('button', { name: 'Flag for review' }),
    runSecond: screen.getByRole('button', { name: 'Share cohort' }),
  });
  const deferredActions = (gates: ReturnType<typeof gate>[]) => {
    const handlers: Record<string, LoomViewerActionHandler> = {};
    const start = () => { const next = gate(); gates.push(next); return next.promise; };
    handlers['flag-review'] = start;
    handlers['share-cohort'] = start;
    return handlers;
  };
  it('keeps both buttons pending while both run and keeps the slower one pending after the first settles', async () => {
    const first = gate();
    const second = gate();
    actionHarness({ 'flag-review': () => first.promise, 'share-cohort': () => second.promise });
    await screen.findByText('patient-male');
    const { runFirst, runSecond } = actionButtons();
    fireEvent.click(runFirst);
    fireEvent.click(runSecond);
    await waitFor(() => {
      expectPending(runFirst);
      expectPending(runSecond);
    });

    first.resolve();
    await waitFor(() => expectSettled(runFirst));
    expectPending(runSecond);
    expect(screen.queryByRole('alert', { name: 'Action failed' })).toBeNull();

    second.resolve();
    await waitFor(() => expectSettled(runSecond));
    expect(screen.queryByRole('alert', { name: 'Action failed' })).toBeNull();
  });
  it('keeps the earlier-started action pending when the later action settles first', async () => {
    const first = gate();
    const second = gate();
    actionHarness({ 'flag-review': () => first.promise, 'share-cohort': () => second.promise });
    await screen.findByText('patient-male');
    const { runFirst, runSecond } = actionButtons();
    fireEvent.click(runFirst);
    fireEvent.click(runSecond);
    await waitFor(() => {
      expectPending(runFirst);
      expectPending(runSecond);
    });

    second.resolve();
    await waitFor(() => expectSettled(runSecond));
    expectPending(runFirst);

    first.resolve();
    await waitFor(() => expectSettled(runFirst));
  });
  it('shows a failing action error without clearing the other pending action, and settles both', async () => {
    const first = gate();
    const second = gate();
    actionHarness({ 'flag-review': () => first.promise, 'share-cohort': () => second.promise });
    await screen.findByText('patient-male');
    const { runFirst, runSecond } = actionButtons();
    fireEvent.click(runFirst);
    fireEvent.click(runSecond);
    await waitFor(() => {
      expectPending(runFirst);
      expectPending(runSecond);
    });

    first.reject(new Error('The cohort could not be flagged.'));
    const alert = await screen.findByRole('alert', { name: 'Action failed' });
    expect(alert).toHaveTextContent('The cohort could not be flagged.');
    expectSettled(runFirst);
    expectPending(runSecond);

    second.resolve();
    await waitFor(() => expectSettled(runSecond));
    expect(screen.getByRole('alert', { name: 'Action failed' })).toBeInTheDocument();
  });
  it('shows each failed action separately and dismisses only the clicked failure', async () => {
    const gates: ReturnType<typeof gate>[] = [];
    actionHarness(deferredActions(gates));
    await screen.findByText('patient-male');
    const { runFirst, runSecond } = actionButtons();
    fireEvent.click(runFirst);
    fireEvent.click(runSecond);
    await waitFor(() => {
      expectPending(runFirst);
      expectPending(runSecond);
    });

    gates[0].reject(new Error('The cohort could not be flagged.'));
    gates[1].reject(new Error('The cohort could not be shared.'));
    await waitFor(() => expect(screen.getAllByRole('alert', { name: 'Action failed' })).toHaveLength(2));
    expectSettled(runFirst);
    expectSettled(runSecond);
    expect(screen.getByText('The cohort could not be flagged.')).toBeInTheDocument();
    expect(screen.getByText('The cohort could not be shared.')).toBeInTheDocument();

    fireEvent.click(within(screen.getByText('The cohort could not be flagged.').closest('[role="alert"]') as HTMLElement)
      .getByRole('button', { name: 'Dismiss action failure' }));
    await waitFor(() => expect(screen.queryByText('The cohort could not be flagged.')).toBeNull());
    expect(screen.getByText('The cohort could not be shared.')).toBeInTheDocument();
  });
  it('keeps one failed action error visible while another action starts and runs', async () => {
    const gates: ReturnType<typeof gate>[] = [];
    actionHarness(deferredActions(gates));
    await screen.findByText('patient-male');
    const { runFirst, runSecond } = actionButtons();
    fireEvent.click(runFirst);
    await waitFor(() => expect(gates).toHaveLength(1));
    expectPending(runFirst);

    gates[0].reject(new Error('The cohort could not be flagged.'));
    await waitFor(() => expect(screen.getByText('The cohort could not be flagged.')).toBeInTheDocument());
    expectSettled(runFirst);

    fireEvent.click(runSecond);
    await waitFor(() => expect(gates).toHaveLength(2));
    expectPending(runSecond);
    expectSettled(runFirst);
    expect(screen.getByText('The cohort could not be flagged.')).toBeInTheDocument();

    gates[1].resolve();
    await waitFor(() => {
      expectSettled(runFirst);
      expectSettled(runSecond);
    });
    expect(screen.getByText('The cohort could not be flagged.')).toBeInTheDocument();
  });
  it('scopes pending state to the action run and not to the output currently shown', async () => {
    const flag = gate();
    const share = gate();
    const encounters = {
      ...runtimeWithActions.outputs[0],
      outputId: 'encounters',
      name: 'encounters',
      title: 'Encounters',
      selector: { ...runtimeWithActions.outputs[0].selector, output: 'encounters' },
    };
    harness({
      runtime: { ...runtimeWithActions, outputs: [runtimeWithActions.outputs[0], encounters] },
      customActions: { 'flag-review': () => flag.promise, 'share-cohort': () => share.promise },
    });
    await screen.findByText('patient-male');
    fireEvent.click(screen.getByRole('button', { name: 'Flag for review' }));
    await waitFor(() => expectPending(screen.getByRole('button', { name: 'Flag for review' })));

    fireEvent.click(screen.getByRole('tab', { name: /Encounters/ }));
    await waitFor(() => expect(screen.getByRole('tab', { name: /Encounters/ })).toHaveAttribute('aria-selected', 'true'));
    expectSettled(screen.getByRole('button', { name: 'Flag for review' }));

    fireEvent.click(screen.getByRole('tab', { name: /Patients/ }));
    await waitFor(() => expectPending(screen.getByRole('button', { name: 'Flag for review' })));
    flag.resolve();
    await waitFor(() => expectSettled(screen.getByRole('button', { name: 'Flag for review' })));

    fireEvent.click(screen.getByRole('button', { name: 'Share cohort' }));
    await waitFor(() => expectPending(screen.getByRole('button', { name: 'Share cohort' })));
    share.resolve();
    await waitFor(() => expectSettled(screen.getByRole('button', { name: 'Share cohort' })));
  });
  it('keeps a single action pending only while its own promise runs, on every run', async () => {
    const invocations: ReturnType<typeof gate>[] = [];
    actionHarness({ 'flag-review': () => { const next = gate(); invocations.push(next); return next.promise; } });
    await screen.findByText('patient-male');
    const { runFirst, runSecond } = actionButtons();
    expectSettled(runFirst);
    expectSettled(runSecond);

    fireEvent.click(runFirst);
    await waitFor(() => expect(invocations).toHaveLength(1));
    expectPending(runFirst);
    expectSettled(runSecond);

    await act(async () => { invocations[0].resolve(); });
    await waitFor(() => {
      expectSettled(runFirst);
      expectSettled(runSecond);
    });
    expect(screen.queryByRole('alert', { name: 'Action failed' })).toBeNull();

    fireEvent.click(runFirst);
    await waitFor(() => expect(invocations).toHaveLength(2));
    expectPending(runFirst);
    expectSettled(runSecond);

    await act(async () => { invocations[1].resolve(); });
    await waitFor(() => {
      expectSettled(runFirst);
      expectSettled(runSecond);
    });
    expect(screen.queryByRole('alert', { name: 'Action failed' })).toBeNull();
  });
  it('hides an action failure belonging to an output left behind and retains it for the return visit', async () => {
    const encounters = {
      ...runtimeWithActions.outputs[0],
      outputId: 'encounters',
      name: 'encounters',
      title: 'Encounters',
      selector: { ...runtimeWithActions.outputs[0].selector, output: 'encounters' },
      actions: [],
    };
    const flag = gate();
    harness({
      runtime: { ...runtimeWithActions, outputs: [runtimeWithActions.outputs[0], encounters] },
      customActions: { 'flag-review': () => flag.promise },
    });
    await screen.findByText('patient-male');
    fireEvent.click(screen.getByRole('button', { name: 'Flag for review' }));
    await waitFor(() => expectPending(screen.getByRole('button', { name: 'Flag for review' })));

    flag.reject(new Error('The cohort could not be flagged.'));
    await waitFor(() => expect(screen.getByText('The cohort could not be flagged.')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('tab', { name: /Encounters/ }));
    await waitFor(() => expect(screen.getByRole('tab', { name: /Encounters/ })).toHaveAttribute('aria-selected', 'true'));
    expect(screen.queryByRole('alert', { name: 'Action failed' })).toBeNull();
    expect(screen.queryByText('The cohort could not be flagged.')).toBeNull();

    fireEvent.click(screen.getByRole('tab', { name: /Patients/ }));
    await waitFor(() => expect(screen.getByRole('tab', { name: /Patients/ })).toHaveAttribute('aria-selected', 'true'));
    expect(screen.getByRole('alert', { name: 'Action failed' })).toHaveTextContent('The cohort could not be flagged.');
  });
  it('does not show an action failure that settles after its output was left behind', async () => {
    const encounters = {
      ...runtimeWithActions.outputs[0],
      outputId: 'encounters',
      name: 'encounters',
      title: 'Encounters',
      selector: { ...runtimeWithActions.outputs[0].selector, output: 'encounters' },
      actions: [],
    };
    const flag = gate();
    harness({
      runtime: { ...runtimeWithActions, outputs: [runtimeWithActions.outputs[0], encounters] },
      customActions: { 'flag-review': () => flag.promise },
    });
    await screen.findByText('patient-male');
    fireEvent.click(screen.getByRole('button', { name: 'Flag for review' }));
    await waitFor(() => expectPending(screen.getByRole('button', { name: 'Flag for review' })));

    fireEvent.click(screen.getByRole('tab', { name: /Encounters/ }));
    await waitFor(() => expect(screen.getByRole('tab', { name: /Encounters/ })).toHaveAttribute('aria-selected', 'true'));

    flag.reject(new Error('The cohort could not be flagged while away.'));
    await waitFor(() => expect(screen.getByRole('tab', { name: /Patients/ })).toBeInTheDocument());
    expect(screen.queryByRole('alert', { name: 'Action failed' })).toBeNull();
    expect(screen.queryByText('The cohort could not be flagged while away.')).toBeNull();

    fireEvent.click(screen.getByRole('tab', { name: /Patients/ }));
    await waitFor(() => expect(screen.getByRole('alert', { name: 'Action failed' })).toBeInTheDocument());
    expect(screen.getByRole('alert', { name: 'Action failed' })).toHaveTextContent('The cohort could not be flagged while away.');
  });
  it('keeps a running action loading when the configured action list changes order', async () => {
    const actions: Array<{ readonly type: string; readonly title: string }> = [
      { type: 'flag-review', title: 'Flag for review' },
      { type: 'flag-review', title: 'Flag for audit' },
    ];
    const gates: ReturnType<typeof gate>[] = [];
    const customActions: Record<string, LoomViewerActionHandler> = {
      'flag-review': () => { const next = gate(); gates.push(next); return next.promise; },
    };
    const { client, rerender } = harness({
      runtime: { ...runtimeWithActions, outputs: [{ ...runtimeWithActions.outputs[0], actions }] },
      customActions,
    });
    await screen.findByText('patient-male');
    const first = screen.getByRole('button', { name: 'Flag for review' });
    const second = screen.getByRole('button', { name: 'Flag for audit' });
    fireEvent.click(first);
    await waitFor(() => expectPending(first));
    expectSettled(second);

    actions.reverse();
    rerender(<LoomExplorerViewer project="org/project" explorerId="explorer-main" client={client} customActions={customActions} />);

    expectPending(screen.getByRole('button', { name: 'Flag for review' }));
    expectSettled(screen.getByRole('button', { name: 'Flag for audit' }));

    gates[0].resolve();
    await waitFor(() => {
      expectSettled(screen.getByRole('button', { name: 'Flag for review' }));
      expectSettled(screen.getByRole('button', { name: 'Flag for audit' }));
    });
    expect(screen.queryByRole('alert', { name: 'Action failed' })).toBeNull();
  });
  it('keeps identically configured actions pending independently', async () => {
    const actions = [
      { type: 'flag-review', title: 'Flag for review' },
      { type: 'flag-review', title: 'Flag for review' },
    ];
    const gates: ReturnType<typeof gate>[] = [];
    actionHarnessFor(actions, () => { const next = gate(); gates.push(next); return next.promise; });
    await screen.findByText('patient-male');
    const buttons = screen.getAllByRole('button', { name: 'Flag for review' });
    expect(buttons).toHaveLength(2);
    fireEvent.click(buttons[0]);
    fireEvent.click(buttons[1]);
    await waitFor(() => {
      expectPending(buttons[0]);
      expectPending(buttons[1]);
    });

    gates[0].reject(new Error('The first flag failed.'));
    await waitFor(() => expect(screen.getByText('The first flag failed.')).toBeInTheDocument());
    expectSettled(buttons[0]);
    expectPending(buttons[1]);

    gates[1].resolve();
    await waitFor(() => expectSettled(buttons[1]));
    expect(screen.getByRole('alert', { name: 'Action failed' })).toHaveTextContent('The first flag failed.');
  });
});

const traceBinding = {
  receiptId: 'receipt-published-7', outputId: 'patients', project: 'org/project', explorerId: 'explorer-main',
  generation: 'generation-authorized-7', scopeDigest: 'sha256:scope-7',
};
const traceFeature = {
  outputId: 'patients', column: 'gender', authoredColumn: 'gender', occurrenceId: 'occurrence-gender-1', label: 'Gender',
  logicalType: 'string', sourceResourceType: 'Patient', sourcePath: 'gender', projectionMode: 'DIRECT', lossless: true, lossReasons: [],
};
const contribution = (resourceId: string) => ({ resourceType: 'Patient', resourceId, value: 'female' });
const tracePage = (resourceIds: ReadonlyArray<string>, hasMore: boolean, nextOffset: number, rowId = 'opaque-female'): CellTraceResponse => ({
  binding: traceBinding,
  feature: traceFeature,
  trace: {
    status: 'VALUE', rowId, column: 'gender', value: rowId === 'opaque-male' ? 'male' : 'female',
    contributions: resourceIds.map(contribution), hasMore, nextOffset, complete: true,
  },
});
const explainRuntime: ExplorerRuntimeV1 = {
  ...runtime,
  qualityReports: [{
    id: 'quality-7', receiptId: 'receipt-published-7', project: 'org/project', datasetGeneration: 'generation-authorized-7',
    scopeDigest: 'sha256:scope-7', output: 'patients', policyVersion: 'v1', completeness: 'COMPLETE', verdict: 'PASSED', rowCount: 2,
    columns: [{ column: 'gender', present: 2, missing: 0, recordedNull: 0, emptyArray: 0 }],
    keyIntegrity: { distinct: 2, missing: 0, duplicate: 0 }, limits: { maxRows: 100, maxDistinctKeys: 100 },
    issues: { ambiguous: 0, invalidType: 0, incompatibleUnit: 0 },
  }],
};
type TraceOutcome = CellTraceResponse | { readonly message: string } | { readonly gate: ReturnType<typeof deferred<CellTraceResponse>> };
function explainHarness(outcomes: ReadonlyArray<TraceOutcome>, runtimeValue: ExplorerRuntimeV1 = explainRuntime) {
  const requests: Array<Record<string, unknown>> = [];
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (!url.endsWith('/cell-trace')) throw new Error(`Unexpected transport: ${url}`);
    requests.push(JSON.parse(String(init?.body)));
    const outcome = outcomes[requests.length - 1];
    if (!outcome) throw new Error(`Unexpected cell-trace request number ${requests.length}`);
    if ('message' in outcome) {
      return new Response(JSON.stringify({ message: outcome.message }), { status: 503, headers: { 'content-type': 'application/json' } });
    }
    const value = 'gate' in outcome ? await outcome.gate.promise : outcome;
    return new Response(JSON.stringify(value), { status: 200, headers: { 'content-type': 'application/json' } });
  });
  const client = createLoomClient({ baseUrl: 'http://loom.test', fetch: fetcher as typeof fetch });
  vi.spyOn(client, 'getExplorer').mockResolvedValue(runtimeValue);
  vi.spyOn(client, 'queryOutput').mockResolvedValue({
    columns: ['id', 'gender'], rows: population, rowIds: ['opaque-female', 'opaque-male'], totalCount: 2,
    pageInfo: { hasNextPage: false }, facets: [],
  });
  const view = render(<LoomExplorerViewer project="org/project" explorerId="explorer-main" client={client} />);
  return { ...view, requests };
}
async function openExplanationFor(outcomes: ReadonlyArray<TraceOutcome>) {
  const { requests } = explainHarness(outcomes);
  await screen.findByText('patient-male');
  fireEvent.click(screen.getByRole('button', { name: 'Explain Gender for row 1' }));
  return { dialog: await screen.findByRole('dialog', { name: 'Why is Gender female?' }), requests };
}
const sourceRows = (dialog: HTMLElement, resourceId: string) =>
  within(dialog).queryAllByText(`Patient / ${resourceId}`);
const loadMoreButton = (dialog: HTMLElement) =>
  within(dialog).getByRole('button', { name: 'Load more source details' });
const retryButton = (dialog: HTMLElement) =>
  within(dialog).findByRole('button', { name: 'Retry loading more source details' });
const anyPagingButton = (dialog: HTMLElement) =>
  within(dialog).queryByRole('button', { name: /more source details/i });
const laterPageAlert = (dialog: HTMLElement) =>
  within(dialog).findByRole('alert', { name: /Some source details are unavailable/ });
const doubledNegation = /\bnot\s+not\b/i;
describe('Viewer cell explanation later-page failure', () => {
  it('keeps loaded page-one evidence visible with an error when a later page fails', async () => {
    const { dialog, requests } = await openExplanationFor([
      tracePage(['patient-gender-1'], true, 1),
      { message: 'The cell trace could not be loaded.' },
    ]);
    await within(dialog).findByText('Patient / patient-gender-1');
    fireEvent.click(loadMoreButton(dialog));
    expect(await within(dialog).findByText('The cell trace could not be loaded.')).toBeInTheDocument();
    expect(within(dialog).queryByText('Explanation unavailable')).toBeNull();
    expect(within(dialog).getByText(/already loaded are still shown/)).toBeInTheDocument();
    expect(within(dialog).getByText('Source details (1)')).toBeInTheDocument();
    fireEvent.click(within(dialog).getByText('Source details (1)'));
    expect(sourceRows(dialog, 'patient-gender-1')).toHaveLength(1);
    expect(within(dialog).queryByText('Patient / patient-gender-2')).toBeNull();
    await retryButton(dialog);
    expect(requests).toHaveLength(2);
  });
  it('keeps page one visible and alerts through a role-scoped later-page alert while retry is in flight', async () => {
    const gate = deferred<CellTraceResponse>();
    const { dialog, requests } = await openExplanationFor([
      tracePage(['patient-gender-1'], true, 1),
      { message: 'The cell trace could not be loaded.' },
      { gate },
    ]);
    await within(dialog).findByText('Patient / patient-gender-1');
    fireEvent.click(loadMoreButton(dialog));
    const alert = await laterPageAlert(dialog);
    expect(alert).toHaveTextContent('The cell trace could not be loaded.');
    expect(within(alert).getByText(/already loaded are still shown/)).toBeInTheDocument();
    fireEvent.click(await retryButton(dialog));
    expect(await retryButton(dialog)).toBeDisabled();
    expect(within(dialog).getByText('Patient / patient-gender-1')).toBeInTheDocument();
    expect(within(dialog).getByText('Source details (1)')).toBeInTheDocument();
    expect(await laterPageAlert(dialog)).toHaveTextContent('The cell trace could not be loaded.');
    expect(requests).toHaveLength(3);
    await act(async () => {
      gate.resolve(tracePage(['patient-gender-2'], false, 2));
      await gate.promise;
    });
    await within(dialog).findByText('Patient / patient-gender-2');
    expect(within(dialog).queryByRole('alert')).toBeNull();
    expect(within(dialog).getByText('Source details (2)')).toBeInTheDocument();
    expect(sourceRows(dialog, 'patient-gender-1')).toHaveLength(1);
  });
  it('renders a later-page failure sentence without a doubled negation', async () => {
    const { dialog } = await openExplanationFor([
      tracePage(['patient-gender-1'], true, 1),
      { message: 'The cell trace could not not be loaded.' },
    ]);
    await within(dialog).findByText('Patient / patient-gender-1');
    fireEvent.click(loadMoreButton(dialog));
    const alert = await laterPageAlert(dialog);
    expect(alert).toHaveTextContent('The cell trace could not be loaded.');
    expect(alert.textContent).not.toMatch(doubledNegation);
    expect(dialog.textContent).not.toMatch(doubledNegation);
  });
  it('renders a first-page failure sentence without a doubled negation', async () => {
    const { dialog } = await openExplanationFor([{ message: 'Loom could not not explain this cell.' }]);
    const alert = await within(dialog).findByRole('alert', { name: /Explanation unavailable/ });
    expect(alert).toHaveTextContent('Loom could not explain this cell.');
    expect(dialog.textContent).not.toMatch(doubledNegation);
  });
  it('surfaces a later-page error when the upstream failure carries no message', async () => {
    const { dialog } = await openExplanationFor([
      tracePage(['patient-gender-1'], true, 1),
      { message: '' },
    ]);
    await within(dialog).findByText('Patient / patient-gender-1');
    fireEvent.click(loadMoreButton(dialog));
    const alert = await laterPageAlert(dialog);
    expect(alert).toHaveTextContent('Loom could not load more source details.');
    expect(within(dialog).getByText('Patient / patient-gender-1')).toBeInTheDocument();
    await retryButton(dialog);
  });
  it('retries a repeated later-page failure from the same offset without duplicating page one', async () => {
    const { dialog, requests } = await openExplanationFor([
      tracePage(['patient-gender-1'], true, 1),
      { message: 'The cell trace could not be loaded.' },
      { message: 'The trace service is still unavailable.' },
      tracePage(['patient-gender-2'], false, 2),
    ]);
    await within(dialog).findByText('Patient / patient-gender-1');
    fireEvent.click(loadMoreButton(dialog));
    fireEvent.click(await retryButton(dialog));
    const alert = await laterPageAlert(dialog);
    expect(alert).toHaveTextContent('The trace service is still unavailable.');
    expect(within(dialog).getByText('Patient / patient-gender-1')).toBeInTheDocument();
    expect(sourceRows(dialog, 'patient-gender-1')).toHaveLength(1);
    fireEvent.click(await retryButton(dialog));
    await within(dialog).findByText('Patient / patient-gender-2');
    expect(sourceRows(dialog, 'patient-gender-1')).toHaveLength(1);
    expect(within(dialog).getByText('Source details (2)')).toBeInTheDocument();
    expect(requests.slice(1)).toEqual([
      { receiptId: 'receipt-published-7', outputId: 'patients', rowId: 'opaque-female', column: 'gender', offset: 1, limit: 25 },
      { receiptId: 'receipt-published-7', outputId: 'patients', rowId: 'opaque-female', column: 'gender', offset: 1, limit: 25 },
      { receiptId: 'receipt-published-7', outputId: 'patients', rowId: 'opaque-female', column: 'gender', offset: 1, limit: 25 },
    ]);
  });
  it('retries the same failed offset without refetching or duplicating page one', async () => {
    const { dialog, requests } = await openExplanationFor([
      tracePage(['patient-gender-1'], true, 1),
      { message: 'The cell trace could not be loaded.' },
      tracePage(['patient-gender-2'], false, 2),
    ]);
    await within(dialog).findByText('Patient / patient-gender-1');
    fireEvent.click(loadMoreButton(dialog));
    fireEvent.click(await retryButton(dialog));
    await within(dialog).findByText('Patient / patient-gender-2');
    expect(within(dialog).queryByText('The cell trace could not be loaded.')).toBeNull();
    expect(within(dialog).queryByText(/already loaded are still shown/)).toBeNull();
    expect(sourceRows(dialog, 'patient-gender-1')).toHaveLength(1);
    expect(sourceRows(dialog, 'patient-gender-2')).toHaveLength(1);
    expect(within(dialog).getByText('Source details (2)')).toBeInTheDocument();
    expect(requests[0]).toEqual({ receiptId: 'receipt-published-7', outputId: 'patients', rowId: 'opaque-female', column: 'gender', limit: 25 });
    expect(requests.slice(1)).toEqual([
      { receiptId: 'receipt-published-7', outputId: 'patients', rowId: 'opaque-female', column: 'gender', offset: 1, limit: 25 },
      { receiptId: 'receipt-published-7', outputId: 'patients', rowId: 'opaque-female', column: 'gender', offset: 1, limit: 25 },
    ]);
  });
  it('appends a retried page once and reflects its returned paging', async () => {
    const { dialog, requests } = await openExplanationFor([
      tracePage(['patient-gender-1'], true, 1),
      { message: 'The cell trace could not be loaded.' },
      tracePage(['patient-gender-2', 'patient-gender-3'], true, 3),
      tracePage(['patient-gender-4'], false, 4),
    ]);
    await within(dialog).findByText('Patient / patient-gender-1');
    fireEvent.click(loadMoreButton(dialog));
    fireEvent.click(await retryButton(dialog));
    await within(dialog).findByText('Patient / patient-gender-3');
    expect(within(dialog).getByText('Source details (3)')).toBeInTheDocument();
    expect(sourceRows(dialog, 'patient-gender-1')).toHaveLength(1);
    expect(sourceRows(dialog, 'patient-gender-2')).toHaveLength(1);
    expect(sourceRows(dialog, 'patient-gender-3')).toHaveLength(1);
    expect(loadMoreButton(dialog)).toBeInTheDocument();
    fireEvent.click(loadMoreButton(dialog));
    await within(dialog).findByText('Patient / patient-gender-4');
    expect(sourceRows(dialog, 'patient-gender-4')).toHaveLength(1);
    expect(within(dialog).getByText('Source details (4)')).toBeInTheDocument();
    expect(requests[3]).toEqual({ receiptId: 'receipt-published-7', outputId: 'patients', rowId: 'opaque-female', column: 'gender', offset: 3, limit: 25 });
    await waitFor(() => expect(anyPagingButton(dialog)).toBeNull());
    expect(requests).toHaveLength(4);
  });
  it('hides the load-more control when a retried later page reports no more evidence', async () => {
    const { dialog, requests } = await openExplanationFor([
      tracePage(['patient-gender-1'], true, 1),
      { message: 'The cell trace could not be loaded.' },
      tracePage(['patient-gender-2'], false, 2),
    ]);
    await within(dialog).findByText('Patient / patient-gender-1');
    fireEvent.click(loadMoreButton(dialog));
    fireEvent.click(await retryButton(dialog));
    await within(dialog).findByText('Patient / patient-gender-2');
    await waitFor(() => expect(anyPagingButton(dialog)).toBeNull());
    expect(requests).toHaveLength(3);
  });
  it('hides the load-more control when the first page already reports no more evidence', async () => {
    const { dialog, requests } = await openExplanationFor([tracePage(['patient-gender-1'], false, 1)]);
    await within(dialog).findByText('Patient / patient-gender-1');
    expect(anyPagingButton(dialog)).toBeNull();
    expect(requests).toHaveLength(1);
  });
  it('still reports an unavailable explanation when the first page fails', async () => {
    const { dialog, requests } = await openExplanationFor([{ message: 'The cell trace could not be loaded.' }]);
    expect(await within(dialog).findByText('Explanation unavailable')).toBeInTheDocument();
    expect(within(dialog).queryByText(/already loaded are still shown/)).toBeNull();
    expect(anyPagingButton(dialog)).toBeNull();
    expect(requests).toHaveLength(1);
  });
  it('retains a newer missing-evidence explanation when the previous trace arrives', async () => {
    const pending = deferred<CellTraceResponse>();
    const otherOutput = {
      ...explainRuntime.outputs[0], outputId: 'other', name: 'other', title: 'Other',
      selector: { ...explainRuntime.outputs[0].selector, output: 'other' },
    };
    const { requests } = explainHarness([{ gate: pending }], {
      ...explainRuntime, outputs: [...explainRuntime.outputs, otherOutput],
    });
    await screen.findByText('patient-male');
    fireEvent.click(screen.getByRole('button', { name: 'Explain Gender for row 1' }));
    await screen.findByRole('dialog', { name: 'Why is Gender female?' });
    fireEvent.click(screen.getByRole('tab', { name: /Other/ }));
    await waitFor(() => expect(screen.getByRole('tab', { name: /Other/ })).toHaveAttribute('aria-selected', 'true'));
    fireEvent.click(screen.getByRole('button', { name: 'Explain Gender for row 2' }));
    const current = await screen.findByRole('dialog', { name: 'Why is Gender male?' });
    expect(within(current).getByText('This publication does not include receipt-bound quality evidence.')).toBeInTheDocument();
    await act(async () => {
      pending.resolve(tracePage(['stale-patient-source'], false, 1));
      await pending.promise;
    });
    expect(screen.getByRole('dialog', { name: 'Why is Gender male?' })).toBe(current);
    expect(within(current).getByText('This publication does not include receipt-bound quality evidence.')).toBeInTheDocument();
    expect(within(current).queryByText('Patient / stale-patient-source')).toBeNull();
    expect(requests).toHaveLength(1);
  });
  it('drops an in-flight later page when the researcher explains another cell first', async () => {
    const gate = deferred<CellTraceResponse>();
    const { dialog, requests } = await openExplanationFor([
      tracePage(['patient-gender-1'], true, 1),
      { gate },
      tracePage(['patient-gender-9'], false, 1, 'opaque-male'),
    ]);
    await within(dialog).findByText('Patient / patient-gender-1');
    fireEvent.click(loadMoreButton(dialog));
    fireEvent.click(screen.getByRole('button', { name: 'Explain Gender for row 2' }));
    const other = await screen.findByRole('dialog', { name: 'Why is Gender male?' });
    await within(other).findByText('Patient / patient-gender-9');
    await act(async () => {
      gate.resolve(tracePage(['patient-gender-stale'], true, 2));
      await gate.promise;
    });
    expect(within(other).queryByText('Patient / patient-gender-stale')).toBeNull();
    expect(within(other).getByText('Source details (1)')).toBeInTheDocument();
    expect(anyPagingButton(other)).toBeNull();
    expect(requests).toHaveLength(3);
  });
});
