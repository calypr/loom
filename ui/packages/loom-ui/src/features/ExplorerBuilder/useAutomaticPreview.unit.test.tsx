// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useAutomaticPreview, type AutomaticPreviewIdentity, type AutomaticPreviewRun } from './useAutomaticPreview';

const request = (key: string): AutomaticPreviewIdentity => ({
  key,
  ownerKey: 'owner-a',
  snapshotToken: 'snapshot-a',
  draftVersion: 1,
  draftDigest: 'sha256:draft-a',
  previewRequestVersion: 0,
  outputId: 'table-a',
  limit: 25,
});
const completedRun = (identity: AutomaticPreviewIdentity): AutomaticPreviewRun => ({
  identity,
  receipt: { receiptId: 'receipt-a' } as AutomaticPreviewRun['receipt'],
  preview: { receiptId: 'receipt-a', outputId: identity.outputId } as AutomaticPreviewRun['preview'],
});

afterEach(() => vi.useRealTimers());

it('starts immediately with a synchronous loading snapshot and reuses a settled key', async () => {
  const refresh = vi.fn(async (identity: AutomaticPreviewIdentity) => completedRun(identity));
  const hook = renderHook(
    ({ identity, enabled }: { identity: AutomaticPreviewIdentity; enabled: boolean }) =>
      useAutomaticPreview({ request: identity, enabled, refresh }),
    { initialProps: { identity: request('table-a:snapshot-a:1:draft-a:0:25'), enabled: true } },
  );

  expect(hook.result.current.isLoading).toBe(true);
  await waitFor(() => expect(refresh).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(hook.result.current.data?.receipt.receiptId).toBe('receipt-a'));
  hook.rerender({ identity: request('table-a:snapshot-a:1:draft-a:0:25'), enabled: true });
  expect(refresh).toHaveBeenCalledTimes(1);

  hook.rerender({ identity: request('table-a:snapshot-a:1:draft-a:0:25'), enabled: false });
  expect(hook.result.current.isLoading).toBe(false);
  hook.rerender({ identity: request('table-a:snapshot-a:1:draft-a:0:25'), enabled: true });
  await waitFor(() => expect(hook.result.current.data?.receipt.receiptId).toBe('receipt-a'));
  expect(refresh).toHaveBeenCalledTimes(1);
});

it('cancels superseded request owners and retries a canceled same key', async () => {
  const signals: AbortSignal[] = [];
  const refresh = vi.fn((identity: AutomaticPreviewIdentity, signal: AbortSignal) => {
    signals.push(signal);
    return new Promise<AutomaticPreviewRun | undefined>(() => {});
  });
  const hook = renderHook(
    ({ identity, enabled }: { identity: AutomaticPreviewIdentity; enabled: boolean }) =>
      useAutomaticPreview({ request: identity, enabled, refresh }),
    { initialProps: { identity: request('table-a:snapshot-a:1:draft-a:0:25'), enabled: false } },
  );
  expect(refresh).not.toHaveBeenCalled();

  hook.rerender({ identity: request('table-a:snapshot-a:1:draft-a:0:25'), enabled: true });
  await waitFor(() => expect(refresh).toHaveBeenCalledTimes(1));
  hook.rerender({ identity: request('table-a:snapshot-b:1:draft-a:0:25'), enabled: true });
  await waitFor(() => expect(refresh).toHaveBeenCalledTimes(2));
  await waitFor(() => expect(signals[0]?.aborted).toBe(true));
  expect(signals[1]?.aborted).toBe(false);

  hook.rerender({ identity: request('table-a:snapshot-b:1:draft-a:0:25'), enabled: false });
  await waitFor(() => expect(signals[1]?.aborted).toBe(true));
  hook.rerender({ identity: request('table-a:snapshot-b:1:draft-a:0:25'), enabled: true });
  await waitFor(() => expect(refresh).toHaveBeenCalledTimes(3));
});

it('lets an explicit reload bypass a settled result on the same key', async () => {
  const reloadOptions: Array<boolean | undefined> = [];
  const refresh = vi.fn(async (identity: AutomaticPreviewIdentity, _signal: AbortSignal, options?: { readonly reload?: boolean }) => {
    reloadOptions.push(options?.reload);
    return completedRun(identity);
  });
  const hook = renderHook(() => useAutomaticPreview({
    request: request('table-a:snapshot-a:1:draft-a:0:25'),
    enabled: true,
    refresh,
  }));
  await waitFor(() => expect(refresh).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(hook.result.current.data?.receipt.receiptId).toBe('receipt-a'));
  await waitFor(() => expect(hook.result.current.isLoading).toBe(false));
  expect(reloadOptions).toEqual([undefined]);
  await act(async () => { await hook.result.current.refetch({ reload: true }); });
  expect(refresh).toHaveBeenCalledTimes(2);
  expect(reloadOptions).toEqual([undefined, true]);
});

it('clears a previous key result and retains a new key error across disable and re-enable', async () => {
  const failure = new Error('RELATIONSHIP_CARDINALITY_VIOLATION');
  const failedKey = 'table-a:snapshot-a:2:draft-b:0:25';
  const refresh = vi.fn(async (identity: AutomaticPreviewIdentity) => {
    if (identity.key === failedKey) throw failure;
    return {
      ...completedRun(identity),
      receipt: { receiptId: identity.key } as AutomaticPreviewRun['receipt'],
    };
  });
  const hook = renderHook(
    ({ identity, enabled }: { identity: AutomaticPreviewIdentity; enabled: boolean }) =>
      useAutomaticPreview({ request: identity, enabled, refresh }),
    { initialProps: { identity: request('table-a:snapshot-a:1:draft-a:0:25'), enabled: true } },
  );

  await waitFor(() => expect(hook.result.current.data?.receipt.receiptId).toBe('table-a:snapshot-a:1:draft-a:0:25'));
  expect(hook.result.current.error).toBeUndefined();

  hook.rerender({ identity: request(failedKey), enabled: true });
  await waitFor(() => expect(hook.result.current.error).toBe(failure));
  expect(hook.result.current.isLoading).toBe(false);
  expect(hook.result.current.data).toBeUndefined();
  expect(refresh).toHaveBeenCalledTimes(2);

  hook.rerender({ identity: request(failedKey), enabled: false });
  hook.rerender({ identity: request(failedKey), enabled: true });
  await waitFor(() => expect(hook.result.current.error).toBe(failure));
  expect(hook.result.current.data).toBeUndefined();
  expect(refresh).toHaveBeenCalledTimes(2);

  hook.rerender({ identity: request('table-a:snapshot-a:3:draft-c:0:25'), enabled: true });
  await waitFor(() => expect(hook.result.current.data?.receipt.receiptId).toBe('table-a:snapshot-a:3:draft-c:0:25'));
  expect(hook.result.current.error).toBeUndefined();
  expect(refresh).toHaveBeenCalledTimes(3);
});
