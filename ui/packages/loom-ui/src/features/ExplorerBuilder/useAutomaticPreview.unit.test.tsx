// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useAutomaticPreview } from './useAutomaticPreview';

afterEach(() => vi.useRealTimers());

it('refreshes saved revisions and row limits once, without rerunning on callback changes', async () => {
  vi.useFakeTimers();
  const refresh = vi.fn().mockResolvedValue(undefined);
  const initialProps: Parameters<typeof useAutomaticPreview>[0] = { requestKey: 'table:1:25', enabled: true, refresh };
  const hook = renderHook(useAutomaticPreview, { initialProps });
  await act(() => vi.advanceTimersByTimeAsync(250));
  expect(refresh).toHaveBeenCalledTimes(1);
  hook.rerender({ requestKey: 'table:1:25', enabled: true, refresh: async () => refresh() });
  await act(() => vi.advanceTimersByTimeAsync(250));
  expect(refresh).toHaveBeenCalledTimes(1);
  hook.rerender({ requestKey: 'table:2:25', enabled: true, refresh });
  await act(() => vi.advanceTimersByTimeAsync(250));
  hook.rerender({ requestKey: 'table:2:50', enabled: true, refresh });
  await act(() => vi.advanceTimersByTimeAsync(250));
  expect(refresh).toHaveBeenCalledTimes(3);
});

it('waits for saved commands and skips superseded revisions', async () => {
  vi.useFakeTimers();
  const refresh = vi.fn().mockResolvedValue(undefined);
  const hook = renderHook(useAutomaticPreview, { initialProps: { requestKey: 'table:1', enabled: false, refresh } });
  await act(() => vi.advanceTimersByTimeAsync(250));
  expect(refresh).not.toHaveBeenCalled();
  hook.rerender({ requestKey: 'table:1', enabled: true, refresh });
  hook.rerender({ requestKey: 'table:2', enabled: true, refresh });
  await act(() => vi.advanceTimersByTimeAsync(250));
  expect(refresh).toHaveBeenCalledTimes(1);
});

it('cancels an unfinished render and allows the same revision to resume', async () => {
  vi.useFakeTimers();
  const refresh = vi.fn(() => new Promise<void>(() => {}));
  const cancel = vi.fn();
  const hook = renderHook(useAutomaticPreview, { initialProps: { requestKey: 'table:1', enabled: true, refresh, cancel } });
  await act(() => vi.advanceTimersByTimeAsync(250));
  hook.rerender({ requestKey: 'table:1', enabled: false, refresh, cancel });
  expect(cancel).toHaveBeenCalledTimes(1);
  hook.rerender({ requestKey: 'table:1', enabled: true, refresh, cancel });
  await act(() => vi.advanceTimersByTimeAsync(250));
  expect(refresh).toHaveBeenCalledTimes(2);
});
