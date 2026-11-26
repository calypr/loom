import { expect, it, vi } from 'vitest';
import { createAutomaticPreview, type AutomaticPreviewIdentity, type AutomaticPreviewRun } from './automaticPreview';


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

function previewSession(refresh: Parameters<ReturnType<typeof createAutomaticPreview>['update']>[1]) {
  const owner = createAutomaticPreview();
  let resource: ReturnType<typeof owner.createResource>;
  let unsubscribe: (() => void) | undefined;
  let key: string | undefined;
  let wasEnabled: boolean | undefined;
  return {
    update(identity: AutomaticPreviewIdentity, enabled = true) {
      owner.update(identity, refresh);
      if (key === identity.key && wasEnabled === enabled) return;
      unsubscribe?.();
      key = identity.key;
      wasEnabled = enabled;
      resource = owner.createResource(enabled);
      unsubscribe = resource.subscribe(() => undefined);
    },
    read: () => owner.read(resource),
    dispose: () => unsubscribe?.(),
  };
}

it('starts immediately with a synchronous loading snapshot and reuses a settled key', async () => {
  const refresh = vi.fn(async (identity: AutomaticPreviewIdentity) => completedRun(identity));
  const session = previewSession(refresh);
  const identity = request('table-a:snapshot-a:1:draft-a:0:25');
  session.update(identity);
  expect(session.read().isLoading).toBe(true);
  expect(refresh).toHaveBeenCalledTimes(1);
  await vi.waitFor(() => expect(session.read().data?.receipt.receiptId).toBe('receipt-a'));

  session.update({ ...identity });
  expect(refresh).toHaveBeenCalledTimes(1);
  session.update({ ...identity }, false);
  expect(session.read().isLoading).toBe(false);
  session.update({ ...identity });
  await vi.waitFor(() => expect(session.read().data?.receipt.receiptId).toBe('receipt-a'));
  expect(refresh).toHaveBeenCalledTimes(1);
  session.dispose();
});

it('cancels superseded request owners and retries a canceled same key', async () => {
  const signals: AbortSignal[] = [];
  const refresh = vi.fn((_identity: AutomaticPreviewIdentity, signal: AbortSignal) => {
    signals.push(signal);
    return new Promise<AutomaticPreviewRun | undefined>(() => {});
  });
  const session = previewSession(refresh);
  const first = request('table-a:snapshot-a:1:draft-a:0:25');
  const second = request('table-a:snapshot-b:1:draft-a:0:25');
  session.update(first, false);
  expect(refresh).not.toHaveBeenCalled();

  session.update(first);
  expect(refresh).toHaveBeenCalledTimes(1);
  session.update(second);
  expect(refresh).toHaveBeenCalledTimes(2);
  await vi.waitFor(() => expect(signals[0]?.aborted).toBe(true));
  expect(signals[1]?.aborted).toBe(false);

  session.update(second, false);
  await vi.waitFor(() => expect(signals[1]?.aborted).toBe(true));
  session.update(second);
  expect(refresh).toHaveBeenCalledTimes(3);
  session.dispose();
});

it('lets an explicit reload bypass a settled result on the same key', async () => {
  const reloadOptions: Array<boolean | undefined> = [];
  const refresh = vi.fn(async (identity: AutomaticPreviewIdentity, _signal: AbortSignal, options?: { readonly reload?: boolean }) => {
    reloadOptions.push(options?.reload);
    return completedRun(identity);
  });
  const session = previewSession(refresh);
  session.update(request('table-a:snapshot-a:1:draft-a:0:25'));
  expect(refresh).toHaveBeenCalledTimes(1);
  await vi.waitFor(() => expect(session.read().data?.receipt.receiptId).toBe('receipt-a'));
  expect(session.read().isLoading).toBe(false);
  expect(reloadOptions).toEqual([undefined]);
  await session.read().refetch({ reload: true });
  expect(refresh).toHaveBeenCalledTimes(2);
  expect(reloadOptions).toEqual([undefined, true]);
  session.dispose();
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
  const session = previewSession(refresh);
  session.update(request('table-a:snapshot-a:1:draft-a:0:25'));
  await vi.waitFor(() => expect(session.read().data?.receipt.receiptId).toBe('table-a:snapshot-a:1:draft-a:0:25'));
  expect(session.read().error).toBeUndefined();

  session.update(request(failedKey));
  await vi.waitFor(() => expect(session.read().error).toBe(failure));
  expect(session.read().isLoading).toBe(false);
  expect(session.read().data).toBeUndefined();
  expect(refresh).toHaveBeenCalledTimes(2);

  session.update(request(failedKey), false);
  session.update(request(failedKey));
  await vi.waitFor(() => expect(session.read().error).toBe(failure));
  expect(session.read().data).toBeUndefined();
  expect(refresh).toHaveBeenCalledTimes(2);

  session.update(request('table-a:snapshot-a:3:draft-c:0:25'));
  await vi.waitFor(() => expect(session.read().data?.receipt.receiptId).toBe('table-a:snapshot-a:3:draft-c:0:25'));
  expect(session.read().error).toBeUndefined();
  expect(refresh).toHaveBeenCalledTimes(3);
  session.dispose();
});
