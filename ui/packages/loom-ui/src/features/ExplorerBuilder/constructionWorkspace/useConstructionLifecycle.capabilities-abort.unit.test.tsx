// @vitest-environment jsdom

import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { GetConstructionCapabilitiesArgs } from '../../../api';
import type { ConstructionCapabilitiesResponse } from '../../../types';
import {
  useConstructionLifecycle,
  type ConstructionLifecycleClient,
} from './useConstructionLifecycle';

const capabilitiesFor = (args: GetConstructionCapabilitiesArgs): ConstructionCapabilitiesResponse => {
  const stage = {
    id: args.stageId,
    inputStageId: '',
    rowIdentityColumn: 'source-row-id',
    columns: [{ id: 'value-id', name: 'value', label: 'Value', type: 'decimal' }],
    capabilities: [
      { kind: 'FILTER' as const, supported: true },
      { kind: 'DERIVE' as const, supported: true },
      { kind: 'PIVOT' as const, supported: false, reason: 'Not available.' },
      { kind: 'UNPIVOT' as const, supported: false, reason: 'Not available.' },
    ],
  };
  return {
    snapshotToken: args.snapshotToken,
    draftVersion: args.expectedDraftVersion,
    draftDigest: args.expectedDraftDigest,
    outputId: args.outputId,
    stageId: args.stageId,
    baseConstruction: { version: 1, steps: [] },
    stages: [stage],
    selectedStage: stage,
    workspaceInputs: [],
  };
};

describe('useConstructionLifecycle capabilities request ownership', () => {
  it('aborts the retired capabilities signal and ignores its late response after a new draft becomes current', async () => {
    const requests: Array<{
      readonly args: GetConstructionCapabilitiesArgs;
      readonly signal?: AbortSignal;
      readonly resolve: (response: ConstructionCapabilitiesResponse) => void;
    }> = [];
    const client = {
      getConstructionCapabilities: vi.fn((args: GetConstructionCapabilitiesArgs, signal?: AbortSignal) =>
        new Promise<ConstructionCapabilitiesResponse>((resolve) => requests.push({ args, signal, resolve }))),
      discoverConstructionCategories: vi.fn(async () => { throw new Error('Unused in this focused owner test.'); }),
      proposeConstruction: vi.fn(async () => { throw new Error('Unused in this focused owner test.'); }),
      preview: vi.fn(async () => { throw new Error('Unused in this focused owner test.'); }),
    } satisfies ConstructionLifecycleClient;
    const firstArgs: GetConstructionCapabilitiesArgs = {
      project: 'org/project', explorerId: 'explorer-1', snapshotToken: 'snapshot-1',
      expectedDraftVersion: 3, expectedDraftDigest: 'sha256:draft-3', outputId: 'patients', stageId: 'source',
    };
    const currentArgs: GetConstructionCapabilitiesArgs = {
      ...firstArgs, expectedDraftVersion: 4, expectedDraftDigest: 'sha256:draft-4',
    };
    const { result, rerender, unmount } = renderHook(
      ({ args }: { readonly args: GetConstructionCapabilitiesArgs }) => useConstructionLifecycle({
        client,
        capabilitiesRequest: args,
      }),
      { initialProps: { args: firstArgs } },
    );

    await waitFor(() => expect(requests).toHaveLength(1));
    rerender({ args: currentArgs });
    await waitFor(() => expect(requests).toHaveLength(2));
    const retired = requests[0];
    const current = requests[1];
    if (!retired || !current) throw new Error('Both capabilities request owners should have started.');
    await waitFor(() => expect(retired.signal?.aborted).toBe(true));
    expect(current.signal?.aborted).toBe(false);

    await act(async () => current.resolve(capabilitiesFor(current.args)));
    await waitFor(() => expect(result.current.capabilities).toMatchObject({
      status: 'ready',
      response: { draftDigest: currentArgs.expectedDraftDigest },
    }));
    await act(async () => retired.resolve(capabilitiesFor(retired.args)));
    expect(result.current.capabilities).toMatchObject({
      status: 'ready',
      response: { draftDigest: currentArgs.expectedDraftDigest },
    });

    unmount();
    await waitFor(() => expect(current.signal?.aborted).toBe(true));
  });
});
