import { expect, it, vi } from 'vitest';
import type { LoomClient } from '../../../client/api';
import { createBuilderSession } from './createBuilderSession';

it('loads on first subscription and aborts only after the last subscriber leaves', async () => {
  const scope = { project: 'org/project', explorerId: 'draft', authResourcePath: '/projects/project' };
  const getBuilder = vi.fn<LoomClient['getBuilder']>(() => new Promise(() => {}));
  const session = createBuilderSession({ client: { getBuilder }, scope });

  expect(getBuilder).not.toHaveBeenCalled();
  const unsubscribeFirst = session.subscribe(() => {});
  expect(getBuilder).toHaveBeenCalledExactlyOnceWith(scope, {
    signal: expect.any(AbortSignal),
    reload: undefined,
  });
  const signal = getBuilder.mock.calls[0]?.[1]?.signal;
  unsubscribeFirst();
  const unsubscribeSecond = session.subscribe(() => {});
  await Promise.resolve();
  expect(signal?.aborted).toBe(false);
  expect(getBuilder).toHaveBeenCalledTimes(1);

  unsubscribeSecond();
  expect(signal?.aborted).toBe(false);
  await Promise.resolve();
  expect(signal?.aborted).toBe(true);
});
