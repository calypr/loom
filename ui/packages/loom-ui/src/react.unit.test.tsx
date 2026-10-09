// @vitest-environment jsdom

import React, { StrictMode } from 'react';
import { act, render, renderHook, screen, waitFor } from '@testing-library/react';
import { createLoomClient } from './api';
import type { ResolveConfiguredColumnContextsArgs } from './api';
import {
  LoomProvider,
  resourceFor,
  useGetExplorerBuilderStateV2Query,
  useGetExplorerAuthoringExplorersQuery,
  usePreviewExplorerAuthoringV2Mutation,
  useResolveConfiguredColumnContextsQuery,
} from './react';

const ExplorerStatus = () => {
  const query = useGetExplorerAuthoringExplorersQuery({ project: 'NCPI_ACCEPTANCE' });
  if (query.isLoading) return <span>loading</span>;
  if (query.error) return <span>error</span>;
  return <span>{query.data?.length} explorers</span>;
};

const BuilderStatus = () => {
  const query = useGetExplorerBuilderStateV2Query({ project: 'NCPI_ACCEPTANCE', explorerId: 'default' });
  if (query.isLoading) return <span>loading</span>;
  if (query.error) return <span>error</span>;
  return <span>{query.data?.draftDigest}</span>;
};

describe('Loom React queries', () => {
  it('keeps configured-context queries disabled until their saved draft identity is ready', async () => {
    const requests: Array<{
      readonly url: string;
      readonly body: string;
      readonly resolve: (response: Response) => void;
    }> = [];
    const fetch = vi.fn<typeof globalThis.fetch>((input, init) => new Promise<Response>((resolve) => {
      requests.push({ url: String(input), body: String(init?.body), resolve });
    }));
    const client = createLoomClient({ fetch });
    const wrapper = ({ children }: { readonly children: React.ReactNode }) => (
      <LoomProvider client={client}>{children}</LoomProvider>
    );
    const args: ResolveConfiguredColumnContextsArgs = {
      project: 'HTAN_INT/BForePC',
      explorerId: 'cda-explorer',
      authResourcePath: '/programs/HTAN_INT/projects/BForePC',
      snapshotToken: 'snapshot-ready',
      expectedDraftVersion: 2,
      expectedDraftDigest: 'sha256:ready-draft',
    };
    type Props = { readonly args: ResolveConfiguredColumnContextsArgs | undefined; readonly key: string };
    const { result, rerender } = renderHook<ReturnType<typeof useResolveConfiguredColumnContextsQuery>, Props>(
      ({ args: requestArgs, key }: Props) => useResolveConfiguredColumnContextsQuery(requestArgs, key),
      { initialProps: { args: undefined, key: '' }, wrapper },
    );

    expect(result.current.isLoading).toBe(false);
    expect(requests).toHaveLength(0);

    rerender({ args, key: JSON.stringify(['owner-a', 'snapshot-ready', 2, 'sha256:ready-draft', 0]) });
    await waitFor(() => expect(requests).toHaveLength(1));
    expect(result.current.isLoading).toBe(true);
    const request = requests[0];
    if (!request) throw new Error('Configured context request did not start.');
    const requestURL = new URL(request.url, 'http://loom.test');
    expect(requestURL.pathname).toBe('/api/v1/projects/HTAN_INT%252FBForePC/explorers/cda-explorer/authoring/v2/configured-column-context');
    expect(requestURL.search).toBe('');
    expect(JSON.parse(request.body)).toEqual({
      snapshotToken: 'snapshot-ready',
      expectedDraftVersion: 2,
      expectedDraftDigest: 'sha256:ready-draft',
    });

    await act(async () => request.resolve(new Response(JSON.stringify({
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      libraries: [],
      pinnedRevisions: [],
      columns: [],
    }), { status: 200 })));
    await waitFor(() => expect(result.current.data?.draftDigest).toBe(args.expectedDraftDigest));
  });

  it('aborts and ignores a configured-context owner while disabled, then accepts the re-enabled owner', async () => {
    const requests: Array<{
      readonly url: string;
      readonly body: string;
      readonly signal?: AbortSignal;
      readonly resolve: (response: Response) => void;
    }> = [];
    const fetch = vi.fn<typeof globalThis.fetch>((input, init) => new Promise<Response>((resolve) => {
      requests.push({
        url: String(input),
        body: String(init?.body),
        signal: init?.signal ?? undefined,
        resolve,
      });
    }));
    const client = createLoomClient({ fetch });
    const wrapper = ({ children }: { readonly children: React.ReactNode }) => (
      <LoomProvider client={client}>{children}</LoomProvider>
    );
    const firstArgs: ResolveConfiguredColumnContextsArgs = {
      project: 'project-first', explorerId: 'explorer-first', authResourcePath: '/auth/first',
      snapshotToken: 'snapshot-first', expectedDraftVersion: 1, expectedDraftDigest: 'sha256:first',
    };
    const nextArgs: ResolveConfiguredColumnContextsArgs = {
      project: 'project-next', explorerId: 'explorer-next', authResourcePath: '/auth/next',
      snapshotToken: 'snapshot-next', expectedDraftVersion: 4, expectedDraftDigest: 'sha256:next',
    };
    const response = (args: ResolveConfiguredColumnContextsArgs, libraryId: string) => new Response(JSON.stringify({
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      libraries: [{ id: libraryId, updatedAt: '2026-10-08T00:00:00Z' }],
      pinnedRevisions: [],
      columns: [],
    }), { status: 200 });
    type Props = { readonly args: ResolveConfiguredColumnContextsArgs | undefined; readonly key: string };
    const { result, rerender } = renderHook<ReturnType<typeof useResolveConfiguredColumnContextsQuery>, Props>(
      ({ args, key }: Props) => useResolveConfiguredColumnContextsQuery(args, key),
      { initialProps: { args: firstArgs, key: 'first-table-progress-owner' }, wrapper },
    );

    await waitFor(() => expect(requests).toHaveLength(1));
    const firstRequest = requests[0];
    if (!firstRequest) throw new Error('Initial configured-context request did not start.');
    expect(new URL(firstRequest.url, 'http://loom.test').pathname)
      .toBe('/api/v1/projects/project-first/explorers/explorer-first/authoring/v2/configured-column-context');
    expect(JSON.parse(firstRequest.body)).toEqual({
      snapshotToken: firstArgs.snapshotToken,
      expectedDraftVersion: firstArgs.expectedDraftVersion,
      expectedDraftDigest: firstArgs.expectedDraftDigest,
    });

    rerender({ args: undefined, key: 'first-table-progress-disabled' });
    await waitFor(() => expect(firstRequest.signal?.aborted).toBe(true));
    expect(result.current.isLoading).toBe(false);
    expect(result.current.isFetching).toBe(false);

    await act(async () => firstRequest.resolve(response(firstArgs, 'stale-disabled-owner')));
    expect(result.current.data).toBeUndefined();
    expect(result.current.error).toBeUndefined();
    expect(result.current.isLoading).toBe(false);
    expect(fetch).toHaveBeenCalledTimes(1);

    rerender({ args: nextArgs, key: 'first-table-progress-reenabled-owner' });
    await waitFor(() => expect(requests).toHaveLength(2));
    const nextRequest = requests[1];
    if (!nextRequest) throw new Error('Re-enabled configured-context request did not start.');
    expect(nextRequest.signal?.aborted).toBe(false);
    expect(new URL(nextRequest.url, 'http://loom.test').pathname)
      .toBe('/api/v1/projects/project-next/explorers/explorer-next/authoring/v2/configured-column-context');
    expect(JSON.parse(nextRequest.body)).toEqual({
      snapshotToken: nextArgs.snapshotToken,
      expectedDraftVersion: nextArgs.expectedDraftVersion,
      expectedDraftDigest: nextArgs.expectedDraftDigest,
    });
    expect(result.current.isLoading).toBe(true);
    expect(result.current.data).toBeUndefined();

    await act(async () => nextRequest.resolve(response(nextArgs, 'current-reenabled-owner')));
    await waitFor(() => expect(result.current.data?.libraries[0]?.id).toBe('current-reenabled-owner'));
    expect(result.current.data?.snapshotToken).toBe(nextArgs.snapshotToken);
    expect(result.current.data?.draftDigest).toBe(nextArgs.expectedDraftDigest);
    expect(result.current.data?.libraries.map(library => library.id)).toEqual(['current-reenabled-owner']);
  });

  it('aborts prior configured-context owners and ignores stale navigation and refresh results', async () => {
    const requests: Array<{
      readonly url: string;
      readonly signal?: AbortSignal;
      readonly resolve: (response: Response) => void;
    }> = [];
    const fetch = vi.fn<typeof globalThis.fetch>((input, init) => new Promise<Response>((resolve) => {
      requests.push({ url: String(input), signal: init?.signal ?? undefined, resolve });
    }));
    const client = createLoomClient({ fetch });
    const wrapper = ({ children }: { readonly children: React.ReactNode }) => (
      <LoomProvider client={client}>{children}</LoomProvider>
    );
    const firstArgs: ResolveConfiguredColumnContextsArgs = {
      project: 'project-a', explorerId: 'explorer-a', authResourcePath: '/auth/a',
      snapshotToken: 'snapshot-a', expectedDraftVersion: 1, expectedDraftDigest: 'sha256:draft-a',
    };
    const nextArgs: ResolveConfiguredColumnContextsArgs = {
      project: 'project-b', explorerId: 'explorer-b', authResourcePath: '/auth/b',
      snapshotToken: 'snapshot-b', expectedDraftVersion: 3, expectedDraftDigest: 'sha256:draft-b',
    };
    const response = (args: ResolveConfiguredColumnContextsArgs, libraryId: string) => new Response(JSON.stringify({
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      libraries: [{ id: libraryId, updatedAt: '2026-10-03T00:00:00Z' }],
      pinnedRevisions: [],
      columns: [],
    }), { status: 200 });
    type Props = { readonly args: ResolveConfiguredColumnContextsArgs; readonly key: string };
    const { result, rerender } = renderHook(
      ({ args, key }: Props) => useResolveConfiguredColumnContextsQuery(args, key),
      { initialProps: { args: firstArgs, key: 'owner-a:snapshot-a:1:draft-a:refresh-0' }, wrapper },
    );

    await waitFor(() => expect(requests).toHaveLength(1));
    rerender({ args: nextArgs, key: 'owner-b:snapshot-b:3:draft-b:refresh-0' });
    await waitFor(() => expect(requests).toHaveLength(2));
    const firstRequest = requests[0];
    const ownerRequest = requests[1];
    if (!firstRequest || !ownerRequest) throw new Error('Expected the first two configured context requests.');
    await waitFor(() => expect(firstRequest.signal?.aborted).toBe(true));
    rerender({ args: nextArgs, key: 'owner-b:snapshot-b:3:draft-b:refresh-1' });
    await waitFor(() => expect(requests).toHaveLength(3));
    const refreshedRequest = requests[2];
    if (!refreshedRequest) throw new Error('Expected the refreshed configured context request.');
    await waitFor(() => expect(ownerRequest.signal?.aborted).toBe(true));

    await act(async () => {
      firstRequest.resolve(response(firstArgs, 'stale-owner'));
      ownerRequest.resolve(response(nextArgs, 'stale-refresh'));
    });
    expect(result.current.isLoading).toBe(true);
    expect(result.current.data).toBeUndefined();

    await act(async () => refreshedRequest.resolve(response(nextArgs, 'current-refresh')));
    await waitFor(() => expect(result.current.data?.libraries[0]?.id).toBe('current-refresh'));
    const changedAuthArgs = { ...nextArgs, authResourcePath: '/auth/b-updated' };
    rerender({ args: changedAuthArgs, key: 'owner-b:snapshot-b:3:draft-b:refresh-1' });
    await waitFor(() => expect(requests).toHaveLength(4));
    const changedAuthRequest = requests[3];
    if (!changedAuthRequest) throw new Error('Auth-scope change did not replace the configured context owner.');
    await act(async () => changedAuthRequest.resolve(response(changedAuthArgs, 'current-auth-owner')));
    await waitFor(() => expect(result.current.data?.libraries[0]?.id).toBe('current-auth-owner'));
    expect(requests.map((request) => new URL(request.url, 'http://loom.test').pathname)).toEqual([
      '/api/v1/projects/project-a/explorers/explorer-a/authoring/v2/configured-column-context',
      '/api/v1/projects/project-b/explorers/explorer-b/authoring/v2/configured-column-context',
      '/api/v1/projects/project-b/explorers/explorer-b/authoring/v2/configured-column-context',
      '/api/v1/projects/project-b/explorers/explorer-b/authoring/v2/configured-column-context',
    ]);
  });

  it('reloads a resolved Builder cache entry when explicitly requested', async () => {
    const builder = (digest: string) => ({
      apiVersion: 'loom.calypr.org/explorer-authoring/v2',
      kind: 'ExplorerBuilderState',
      lifecycleState: 'NEW',
      draftVersion: 1,
      draftDigest: digest,
      workspace: null,
      catalog: {
        snapshotToken: 'snapshot-1',
        generation: 'generation-1',
        routePolicy: {},
        nodes: [],
        edges: [],
        candidates: [],
      },
    });
    const fetch = vi.fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify(builder('old')), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify(builder('new')), { status: 200 }));
    const client = createLoomClient({ fetch });
    const wrapper = ({ children }: { readonly children: React.ReactNode }) => (
      <LoomProvider client={client}>{children}</LoomProvider>
    );
    const { result } = renderHook(
      () => useGetExplorerBuilderStateV2Query({ project: 'NCPI_ACCEPTANCE', explorerId: 'default' }),
      { wrapper },
    );

    await waitFor(() => expect(result.current.data?.draftDigest).toBe('old'));
    let refreshed: Awaited<ReturnType<typeof result.current.refetch>> | undefined;
    await act(async () => {
      refreshed = await result.current.refetch({ reload: true });
    });

    expect(refreshed?.data?.draftDigest).toBe('new');
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it('keeps a shared transport alive when only its first consumer unmounts', async () => {
    let resolveResponse: (response: Response) => void = () => undefined;
    let requestSignal: AbortSignal | undefined;
    const fetch = vi.fn<typeof globalThis.fetch>((_input, init) => new Promise<Response>((resolve, reject) => {
      resolveResponse = resolve;
      requestSignal = init?.signal ?? undefined;
      init?.signal?.addEventListener('abort', () => reject(new DOMException('The operation was aborted.', 'AbortError')));
    }));
    const client = createLoomClient({ fetch });
    const Harness = ({ first }: { readonly first: boolean }) => (
      <>
        {first ? <BuilderStatus /> : null}
        <BuilderStatus />
      </>
    );
    const view = render(
      <LoomProvider client={client}>
        <Harness first />
      </LoomProvider>,
    );

    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    view.rerender(
      <LoomProvider client={client}>
        <Harness first={false} />
      </LoomProvider>,
    );
    await Promise.resolve();
    expect(requestSignal?.aborted).toBe(false);

    resolveResponse(new Response(JSON.stringify({
      apiVersion: 'loom.calypr.org/explorer-authoring/v2',
      kind: 'ExplorerBuilderState',
      lifecycleState: 'NEW',
      draftVersion: 1,
      draftDigest: 'shared',
      workspace: null,
      catalog: { snapshotToken: 'snapshot-1', generation: 'generation-1', routePolicy: {}, nodes: [], edges: [], candidates: [] },
    }), { status: 200 }));
    expect(await screen.findByText('shared')).toBeInTheDocument();
    view.unmount();
  });

  it('aborts shared transport and evicts it after the final consumer leaves', async () => {
    let requestSignal: AbortSignal | undefined;
    const fetch = vi.fn<typeof globalThis.fetch>((_input, init) => {
      requestSignal = init?.signal ?? undefined;
      return new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => reject(new DOMException('The operation was aborted.', 'AbortError')));
      });
    });
    const client = createLoomClient({ fetch });
    const view = render(
      <LoomProvider client={client}>
        <BuilderStatus />
        <BuilderStatus />
      </LoomProvider>,
    );
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    view.unmount();
    await waitFor(() => expect(requestSignal?.aborted).toBe(true));

    render(
      <LoomProvider client={client}>
        <BuilderStatus />
      </LoomProvider>,
    );
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  });

  it('keeps the initial request alive through the Strict Mode subscription probe', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>((_input, init) =>
      new Promise<Response>((resolve, reject) => {
        const timer = window.setTimeout(
          () =>
            resolve(
              new Response('[]', {
                status: 200,
                headers: { 'Content-Type': 'application/json' },
              }),
            ),
          10,
        );
        init?.signal?.addEventListener('abort', () => {
          window.clearTimeout(timer);
          reject(new DOMException('The operation was aborted.', 'AbortError'));
        });
      }),
    );
    const client = createLoomClient({ fetch });

    render(
      <StrictMode>
        <LoomProvider client={client}>
          <ExplorerStatus />
        </LoomProvider>
      </StrictMode>,
    );

    await waitFor(() => expect(screen.getByText('0 explorers')).toBeTruthy());
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('does not let an obsolete request overwrite a newer refresh', async () => {
    const pending: Array<{
      readonly signal: AbortSignal;
      readonly resolve: (value: string) => void;
    }> = [];
    const resource = resourceFor(
      (signal) =>
        new Promise<string>((resolve) => pending.push({ signal, resolve })),
    );
    const unsubscribe = resource.subscribe(() => undefined);

    expect(pending).toHaveLength(1);
    const newer = resource.refresh();
    expect(pending).toHaveLength(2);
    expect(pending[0].signal.aborted).toBe(true);

    pending[1].resolve('newer');
    await newer;
    pending[0].resolve('obsolete');
    await waitFor(() => expect(resource.getSnapshot().data).toBe('newer'));

    unsubscribe();
  });

  it('aborts an in-flight request after a real unmount', async () => {
    let requestSignal: AbortSignal | undefined;
    const fetch = vi.fn<typeof globalThis.fetch>((_input, init) => {
      requestSignal = init?.signal ?? undefined;
      return new Promise<Response>(() => undefined);
    });
    const client = createLoomClient({ fetch });
    const view = render(
      <StrictMode>
        <LoomProvider client={client}>
          <ExplorerStatus />
        </LoomProvider>
      </StrictMode>,
    );

    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    expect(requestSignal?.aborted).toBe(false);
    view.unmount();
    await waitFor(() => expect(requestSignal?.aborted).toBe(true));
  });

  it('evicts a resource after its last subscriber and starts a fresh epoch', async () => {
    const requests: Array<{ readonly signal: AbortSignal; readonly resolve: (value: string) => void }> = [];
    const resource = resourceFor((signal) => new Promise<string>((resolve) => requests.push({ signal, resolve })));
    const firstUnsubscribe = resource.subscribe(() => undefined);
    expect(requests).toHaveLength(1);
    firstUnsubscribe();
    await Promise.resolve();
    expect(requests[0]?.signal.aborted).toBe(true);
    const secondUnsubscribe = resource.subscribe(() => undefined);
    expect(requests).toHaveLength(2);
    requests[0]?.resolve('stale');
    requests[1]?.resolve('fresh');
    await waitFor(() => expect(resource.getSnapshot().data).toBe('fresh'));
    secondUnsubscribe();
  });
});

describe('Loom React mutations', () => {
  it('stays loading when an older invocation settles before a newer one', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>((_input, init) =>
      new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => {
          reject(new DOMException('The operation was aborted.', 'AbortError'));
        });
      }),
    );
    const client = createLoomClient({ fetch });
    const wrapper = ({ children }: { readonly children: React.ReactNode }) => (
      <LoomProvider client={client}>{children}</LoomProvider>
    );
    const { result } = renderHook(
      () => usePreviewExplorerAuthoringV2Mutation(),
      { wrapper },
    );
    const request = {
      project: 'NCPI_ACCEPTANCE',
      explorerId: 'default',
      receiptId: 'receipt-1',
      outputId: 'patients',
      limit: 25 as const,
    };

    let first: ReturnType<typeof result.current[0]>;
    let second: ReturnType<typeof result.current[0]>;
    act(() => {
      first = result.current[0](request);
      second = result.current[0]({ ...request, limit: 50 });
    });
    expect(result.current[1].isLoading).toBe(true);

    await act(async () => {
      first.abort();
      await expect(first.unwrap()).rejects.toBeInstanceOf(DOMException);
    });

    expect(result.current[1].isLoading).toBe(true);

    await act(async () => {
      second.abort();
      await expect(second.unwrap()).rejects.toBeInstanceOf(DOMException);
    });
    expect(result.current[1].isLoading).toBe(false);
  });
});
