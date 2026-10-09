// @vitest-environment jsdom

import React from 'react';
import { act, renderHook, waitFor } from '@testing-library/react';
import { createLoomClient, type ResolveConfiguredColumnContextsArgs } from './api';
import {
  LoomProvider,
  useResolveConfiguredColumnContextsQuery,
} from './react';

describe('configured column context request ownership', () => {
  it('binds a unique request ID to each real query attempt and aborts retired owners', async () => {
    const requests: Array<{
      readonly url: string;
      readonly body: string;
      readonly headers: Headers;
      readonly signal?: AbortSignal;
      readonly resolve: (response: Response) => void;
    }> = [];
    const fetch = vi.fn<typeof globalThis.fetch>((input, init) => new Promise<Response>((resolve, reject) => {
      const signal = init?.signal ?? undefined;
      requests.push({
        url: String(input),
        body: String(init?.body),
        headers: new Headers(init?.headers),
        signal,
        resolve,
      });
      signal?.addEventListener('abort', () => reject(new DOMException('The operation was aborted.', 'AbortError')), { once: true });
    }));
    const client = createLoomClient({ fetch });
    const wrapper = ({ children }: { readonly children: React.ReactNode }) => (
      <LoomProvider client={client}>{children}</LoomProvider>
    );
    const args: ResolveConfiguredColumnContextsArgs = {
      project: 'org/project',
      explorerId: 'explorer-1',
      authResourcePath: '/programs/org/projects/project',
      snapshotToken: 'snapshot-1',
      expectedDraftVersion: 3,
      expectedDraftDigest: 'sha256:draft-3',
    };
    type Props = { readonly args: ResolveConfiguredColumnContextsArgs; readonly ownerKey: string };
    const { result, rerender, unmount } = renderHook(
      ({ args: queryArgs, ownerKey }: Props) => useResolveConfiguredColumnContextsQuery(queryArgs, ownerKey),
      { initialProps: { args, ownerKey: 'owner-1' }, wrapper },
    );

    await waitFor(() => expect(requests).toHaveLength(1));
    rerender({ args: { ...args }, ownerKey: 'owner-1' });
    expect(fetch).toHaveBeenCalledTimes(1);

    rerender({ args, ownerKey: 'owner-2' });
    await waitFor(() => expect(requests).toHaveLength(2));
    const first = requests[0];
    const current = requests[1];
    if (!first || !current) throw new Error('Both configured-context owner requests should have started.');
    await waitFor(() => expect(first.signal?.aborted).toBe(true));

    const [firstRequestId, currentRequestId] = [first, current]
      .map((request) => request.headers.get('x-request-id'));
    if (!firstRequestId || !currentRequestId) throw new Error('Each configured-context fetch should carry its owner request ID.');
    expect(firstRequestId).toMatch(/^configured-column-context-[0-9a-f-]{36}$/);
    expect(currentRequestId).toMatch(/^configured-column-context-[0-9a-f-]{36}$/);
    expect(new Set([firstRequestId, currentRequestId]).size).toBe(2);
    for (const request of [first, current]) {
      expect(new URL(request.url, 'http://loom.test').pathname)
        .toBe('/api/v1/projects/org%252Fproject/explorers/explorer-1/authoring/v2/configured-column-context');
      expect(JSON.parse(request.body)).toEqual({
        snapshotToken: args.snapshotToken,
        expectedDraftVersion: args.expectedDraftVersion,
        expectedDraftDigest: args.expectedDraftDigest,
      });
    }

    await act(async () => current.resolve(new Response(JSON.stringify({
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      libraries: [],
      pinnedRevisions: [],
      columns: [],
    }), { status: 200 })));
    await waitFor(() => expect(result.current.data?.draftDigest).toBe(args.expectedDraftDigest));
    expect(result.current.data?.snapshotToken).toBe(args.snapshotToken);

    unmount();
    await waitFor(() => expect(current.signal?.aborted).toBe(true));
  });
});
