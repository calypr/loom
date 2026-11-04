// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createLoomClient, type Artifact, type LoomOutputRequest } from '../../api';
import { LoomExplorerViewer } from '../../Viewer';
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
