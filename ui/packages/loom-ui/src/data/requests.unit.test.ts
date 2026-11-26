import { describe, expect, it, vi } from 'vitest';
import { RequestOwner } from './requests';

const pending = <Result,>() => {
  let resolve!: (value: Result) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<Result>((accept, decline) => {
    resolve = accept;
    reject = decline;
  });
  return { promise, resolve, reject };
};

describe('RequestOwner', () => {
  it('aborts a request when another request takes the same slot', async () => {
    const owner = new RequestOwner();
    owner.setIdentity('draft-a');
    const first = pending<string>();
    let firstSignal: AbortSignal | undefined;

    const firstRun = owner.run('preview', 'draft-a', signal => {
      firstSignal = signal;
      return first.promise;
    });
    const secondRun = owner.run('preview', 'draft-a', async () => 'current');

    expect(firstSignal?.aborted).toBe(true);
    await expect(firstRun).resolves.toBeUndefined();
    await expect(secondRun).resolves.toBe('current');
    first.resolve('stale');
    owner.dispose();
  });

  it('aborts slots tied to an old identity', async () => {
    const owner = new RequestOwner();
    owner.setIdentity('draft-a');
    const request = pending<string>();
    let signal: AbortSignal | undefined;
    const run = owner.run('network', 'draft-a', value => {
      signal = value;
      return request.promise;
    });

    owner.setIdentity('draft-b');

    expect(signal?.aborted).toBe(true);
    request.resolve('old draft');
    await expect(run).resolves.toBeUndefined();
    owner.dispose();
  });

  it('keeps a replacement started by an abort listener on the new identity', async () => {
    const owner = new RequestOwner();
    owner.setIdentity('draft-a');
    const request = pending<string>();
    const replacement = pending<string>();
    let newRun: Promise<string | undefined> | undefined;

    const oldRun = owner.run('preview', 'draft-a', signal => {
      signal.addEventListener('abort', () => {
        newRun = owner.run('preview', 'draft-b', () => replacement.promise);
      }, { once: true });
      return request.promise;
    });

    owner.setIdentity('draft-b');
    request.resolve('old');
    replacement.resolve('current');

    await expect(oldRun).resolves.toBeUndefined();
    expect(newRun).toBeDefined();
    await expect(newRun).resolves.toBe('current');
  });

  it('drops a late result from a superseded request', async () => {
    const owner = new RequestOwner();
    owner.setIdentity('draft-a');
    const old = pending<string>();
    const apply = vi.fn();
    const oldRun = owner.run('preview', 'draft-a', async signal => {
      const result = await old.promise;
      if (!signal.aborted) apply(result);
      return result;
    });
    await owner.run('preview', 'draft-a', async () => 'new');
    old.resolve('old');
    await expect(oldRun).resolves.toBeUndefined();
    expect(apply).not.toHaveBeenCalled();
    owner.dispose();
  });

  it('swallows a rejection after abort and propagates a current rejection', async () => {
    const owner = new RequestOwner();
    owner.setIdentity('draft-a');
    const request = pending<string>();
    const run = owner.run('preview', 'draft-a', () => request.promise);
    owner.setIdentity('draft-b');
    request.reject(new Error('aborted'));
    await expect(run).resolves.toBeUndefined();

    await expect(owner.run('network', 'draft-b', async () => {
      throw new Error('current failure');
    })).rejects.toThrow('current failure');
    owner.dispose();
  });

  it('aborts all slots on disposal and ignores later work', async () => {
    const owner = new RequestOwner();
    owner.setIdentity('draft-a');
    const request = pending<string>();
    let signal: AbortSignal | undefined;
    const run = owner.run('preview', 'draft-a', value => {
      signal = value;
      return request.promise;
    });

    owner.dispose();

    expect(signal?.aborted).toBe(true);
    request.resolve('late');
    await expect(run).resolves.toBeUndefined();
    await expect(owner.run('other', 'draft-a', async () => 'ignored')).resolves.toBeUndefined();
  });
});
