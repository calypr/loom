// @vitest-environment jsdom

import React, { StrictMode } from 'react';
import { act, render, renderHook, screen, waitFor } from '@testing-library/react';
import { createLoomClient } from './api';
import {
  LoomProvider,
  resourceFor,
  useGetExplorerAuthoringExplorersQuery,
  usePreviewExplorerAuthoringV2Mutation,
} from './react';

const ExplorerStatus = () => {
  const query = useGetExplorerAuthoringExplorersQuery({ project: 'NCPI_ACCEPTANCE' });
  if (query.isLoading) return <span>loading</span>;
  if (query.error) return <span>error</span>;
  return <span>{query.data?.length} explorers</span>;
};

describe('Loom React queries', () => {
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
