// @vitest-environment jsdom
import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createLoomClient, type Artifact, type LoomOutputRequest } from '../../api';
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
function harness(options: { runtime?: ExplorerRuntimeV1; respond?: (index: number) => Promise<Artifact>; customActions?: Readonly<Record<string, LoomViewerActionHandler>> } = {}) {
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
  const view = render(<LoomExplorerViewer project="org/project" explorerId="explorer-main" client={client} customActions={options.customActions} />);
  return { ...view, client, requests, query, prepare };
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
