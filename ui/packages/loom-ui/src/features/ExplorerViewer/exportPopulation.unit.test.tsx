// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createLoomClient, type Artifact, type LoomClient, type LoomOutputRequest } from '../../api';
import { LoomExplorerViewer, type LoomViewerActionHandler } from '../../Viewer';
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
function harness(options: { runtime?: ExplorerRuntimeV1; respond?: (index: number) => Promise<Artifact>; activeOutputId?: string; customActionsFor?: (client: LoomClient) => Readonly<Record<string, LoomViewerActionHandler>> } = {}) {
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
  const customActions = options.customActionsFor?.(client);
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
    // No fileName: the download name can only come from the resolved target's own identity.
    { type: 'export-target-id-no-filename', title: 'Export patients unfiled', output: 'pat-9c21' },
    // An empty fileName is not a usable download name, so it must not download the active table.
    { type: 'export-target-id-empty-filename', title: 'Export patients blank file', output: 'pat-9c21', fileName: '' },
    // Only a case-exact lookup may resolve these; a lenient lookup would export patients.
    { type: 'export-wrong-case-id', title: 'Export patients by wrong-case ID', output: 'PAT-9C21' },
    { type: 'export-wrong-case-name', title: 'Export patients by wrong-case name', output: 'Patients' },
    // A configured column/heading override is part of the resolved target's export request.
    { type: 'export-target-id-columns', title: 'Export patients gender', output: 'pat-9c21', columns: ['gender'], exportHeaders: { gender: 'Sex' } },
    // '' is a contract-valid string in runtimeActionSchema, so an action may carry it. It is
    // an explicit target that resolves to nothing, not a request for whatever is on screen.
    { type: 'export-empty-string', title: 'Export empty target', output: '' },
    // A published output literally named ' patients ' must not be reachable by the trimmed
    // name, and ' patients ' must never silently resolve to the unpadded patients table.
    { type: 'export-padded-name', title: 'Export padded table', output: ' patients ', fileName: 'padded-by-name.csv' },
    // Only an exact name may resolve; a prefix is a different (missing) table.
    { type: 'export-prefix-name', title: 'Export prefix table', output: 'patient' },
    { type: 'custom-export-patients', title: 'Custom export patients', output: 'pat-9c21' },
    { type: 'custom-export-stale', title: 'Custom export stale table', output: 'visits-42' },
  ],
};
// A published output whose name carries the padding the padded-name action asks for. An
// implementation that trims the target answers this request with the patients table.
const paddedNamedOutput: ExplorerRuntimeV1['outputs'][number] = {
  ...encountersOutput,
  outputId: 'pad-6c11', name: ' patients ', title: 'Padded named table', actions: undefined,
  selector: { recipe: 'recipe-pinned', translationVersion: 'v1', output: 'collisions' },
};
// A published output that borrows the active output's name. Only its own ID may select it;
// an implementation that shortcuts a name match to the active output exports the wrong table.
const shadowOutput: ExplorerRuntimeV1['outputs'][number] = {
  ...encountersOutput,
  outputId: 'shd-3a90', name: 'encounters', title: 'Shadow table', actions: undefined,
  selector: { recipe: 'recipe-pinned', translationVersion: 'v1', output: 'collisions' },
};
const twinEncountersOutput: ExplorerRuntimeV1['outputs'][number] = {
  ...encountersOutput, outputId: 'enc-8b02', title: 'Encounters copy', actions: undefined,
};
// A published output whose only name is whitespace. An implementation that matches a
// whitespace-only action target verbatim would export this table instead of failing closed.
const blankNamedOutput: ExplorerRuntimeV1['outputs'][number] = {
  ...encountersOutput,
  outputId: 'blk-2e50', name: '   ', title: 'Blank named table', actions: undefined,
  selector: { recipe: 'recipe-pinned', translationVersion: 'v1', output: 'collisions' },
};
// A published output whose only name is the empty string. An implementation that trims a
// whitespace-only target and then looks it up would export this table instead of failing closed.
const emptyNamedOutput: ExplorerRuntimeV1['outputs'][number] = {
  ...encountersOutput,
  outputId: 'emp-4b08', name: '', title: 'Empty named table', actions: undefined,
  selector: { recipe: 'recipe-pinned', translationVersion: 'v1', output: 'collisions' },
};
// A published output named exactly like the encounters outputId exercises ID-over-name
// precedence: a name-first lookup would export this table instead of the ID owner.
const collidingOutput: ExplorerRuntimeV1['outputs'][number] = {
  ...encountersOutput,
  outputId: 'col-1d4e', name: 'enc-7f3a', title: 'Colliding table', actions: undefined,
  selector: { recipe: 'recipe-pinned', translationVersion: 'v1', output: 'collisions' },
};
const dualRuntime: ExplorerRuntimeV1 = { ...runtime, outputs: [patientsOutput, encountersOutput] };
const ambiguousRuntime: ExplorerRuntimeV1 = { ...runtime, outputs: [patientsOutput, encountersOutput, twinEncountersOutput] };
// The padded-name table is published so an exact match exists for the padded target; a
// trimmed lookup would answer that request with the unpadded patients table.
const paddedRuntime: ExplorerRuntimeV1 = { ...runtime, outputs: [patientsOutput, encountersOutput, paddedNamedOutput] };
// The shadow output is published first and shares the active output's name, so an
// implementation that resolves a name to the active output exports the wrong table.
const shadowRuntime: ExplorerRuntimeV1 = {
  ...runtime,
  outputs: [
    shadowOutput,
    patientsOutput,
    { ...encountersOutput, actions: [...(encountersOutput.actions ?? []), { type: 'export-shadow', title: 'Export shadow table', output: 'shd-3a90', fileName: 'shadow-by-id.csv' }] },
  ],
};
// The whitespace-named outputs are published first, so an implementation that resolves a
// blank target by name lands on them rather than failing closed.
const blankNamedRuntime: ExplorerRuntimeV1 = { ...runtime, outputs: [blankNamedOutput, emptyNamedOutput, patientsOutput, encountersOutput] };
// The colliding output is listed first so a name-first lookup would win over the real ID owner.
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
    // By default the embedder pins the Encounters tab; an uncontrolled session lets a
    // test select outputs the way a reader does.
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

  // The action runs asynchronously; flush its pending promise chain first so a silent-fallback
  // implementation has already issued the export it is not allowed to issue. Asserting the spy
  // before the error text keeps the failure evidence behavioral, not just a missing message.
  const unresolvedAlert = async (exportOutput: { mock: { calls: unknown[] } }, actionTitle: string, target?: string) => {
    for (let round = 0; round < 5; round += 1) await act(async () => { await Promise.resolve(); });
    expect(exportOutput.mock.calls, `action "${actionTitle}" must not issue an export request`).toHaveLength(0);
    expect(await screen.findByText('Action failed')).toBeInTheDocument();
    // The visible error names the action and the unresolved target; the wording is not asserted,
    // so a rephrased message still satisfies the contract.
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
    // The action config carries no fileName, so the only correct name is the target's own.
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
    // An action-declared (even unusable) file name must not be replaced by the active
    // table's name: that would label a patients export as the encounters download.
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
    // 'enc-7f3a' is the encounters outputId and simultaneously the collisions output name.
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
    // The action asks for ' patients ' and a table is published under exactly that name, so
    // only that table may answer. A trimmed lookup would export the unpadded patients table.
    expect(exportOutput.mock.calls[0][0].selector).toEqual({ recipe: 'recipe-pinned', translationVersion: 'v1', output: 'collisions' });
    expect(created).toHaveBeenCalledTimes(1);
    expect(downloads[0].fileName).toBe('padded-by-name.csv');
    expect(await downloads[0].blob?.text()).toContain('collision-1');
    expect(screen.queryByText('Action failed')).toBeNull();
  });

  it('shows an action error and makes no export request when only the trimmed name is published', async () => {
    // No output is named ' patients ' here; the closest is 'patients'. A lookup that trims
    // the target before matching would download the patients table for this action.
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
    // 'shd-3a90' is the shadow table, whose name duplicates the active output's name; an
    // implementation that treats a name match as the active output exports encounters here.
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

  // runtimeActionSchema accepts any string, so '' is a published shape. It names no table, so
  // it must not be read as "no target" and quietly export whatever tab the reader is on.
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

  // Published blank/empty output names are the decoys: a lookup that resolves a
  // whitespace-only target by name would export one of those tables.
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
    // The handler exports through context.request, so a context bound to the active output
    // would download the encounters table for an action that names patients.
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
    // Read the patients table (the default output), then move to encounters so the action
    // runs with a different table active.
    await screen.findByText('patient-male');
    fireEvent.click(screen.getByRole('tab', { name: /Encounters/ }));
    await screen.findByText('encounter-confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Custom export patients' }));
    await waitFor(() => expect(contexts).toHaveLength(1));
    // The handler reads context.result for the rows it exports, so it must receive the
    // target's rows rather than the active table's rows or nothing at all.
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
