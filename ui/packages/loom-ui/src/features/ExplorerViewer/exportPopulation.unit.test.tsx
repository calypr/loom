// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createLoomClient, type Artifact, type LoomOutputRequest } from '../../api';
import { LoomExplorerViewer } from '../../Viewer';
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
function harness(options: { runtime?: ExplorerRuntimeV1; respond?: (index: number) => Promise<Artifact> } = {}) {
  const requests: Array<{ url: string; method: string; body: Record<string, unknown> }> = [];
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (!url.endsWith('/artifacts')) throw new Error(`Unexpected transport: ${url}`);
    requests.push({ url, method: init?.method ?? 'GET', body: JSON.parse(String(init?.body)) });
    const value = await (options.respond?.(requests.length) ?? Promise.resolve(artifact(requests.length)));
    return new Response(JSON.stringify(value), { status: 200, headers: { 'content-type': 'application/json' } });
  });
  const client = createLoomClient({ baseUrl: 'http://loom.test', fetch: fetcher as typeof fetch });
  vi.spyOn(client, 'getExplorer').mockResolvedValue(options.runtime ?? runtime);
  const query = vi.spyOn(client, 'queryOutput').mockImplementation(async (request: LoomOutputRequest) => {
    const filtered = request.filters?.some(filter => filter.column === 'gender' && filter.op === 'EQ' && filter.value === 'female');
    return { columns: ['id', 'gender'], rows: filtered ? [population[0]] : population,
      rowIds: filtered ? ['opaque-female'] : ['opaque-female', 'opaque-male'], totalCount: filtered ? 1 : 2,
      pageInfo: { hasNextPage: false },
      facets: [{ name: 'loom:patients:gender', kind: 'TERMS', columns: ['key', 'doc_count'], rows: [{ key: 'female', doc_count: 1 }, { key: 'male', doc_count: 1 }] }],
    };
  });
  const prepare = vi.spyOn(client, 'prepareArtifact');
  const view = render(<LoomExplorerViewer project="org/project" explorerId="explorer-main" client={client} />);
  return { ...view, requests, query, prepare };
}
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
function explainHarness(outcomes: ReadonlyArray<TraceOutcome>) {
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
  vi.spyOn(client, 'getExplorer').mockResolvedValue(explainRuntime);
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
    // The retry request is pending: the earlier evidence and its alert stay on screen.
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
