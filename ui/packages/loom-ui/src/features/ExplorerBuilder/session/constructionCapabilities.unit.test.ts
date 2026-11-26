import { expect, it, vi } from 'vitest';
import type { GetConstructionCapabilitiesArgs } from '../../../client/api';
import type { ConstructionCapabilitiesResponse } from '../../../contracts/types';
import { constructionCapabilitiesResource } from './constructionCapabilities';

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
    sourceInput: { supported: false, stageId: args.stageId, reasonCode: 'TEST_FIXTURE_NO_CHOICES', choices: [] },
    baseConstruction: { version: 1, steps: [] },
    stages: [stage],
    selectedStage: stage,
    groupFieldChoices: [],
    groupRowValueChoices: [],
    workspaceInputs: [],
  };
};

it('aborts the retired capabilities signal and ignores its late response after a new draft becomes current', async () => {
  const requests: Array<{
    readonly args: GetConstructionCapabilitiesArgs;
    readonly signal?: AbortSignal;
    readonly resolve: (response: ConstructionCapabilitiesResponse) => void;
  }> = [];
  const client = {
    getConstructionCapabilities: vi.fn((args: GetConstructionCapabilitiesArgs, signal?: AbortSignal) =>
      new Promise<ConstructionCapabilitiesResponse>((resolve) => requests.push({ args, signal, resolve }))),
  };
  const firstArgs: GetConstructionCapabilitiesArgs = {
    project: 'org/project', explorerId: 'explorer-1', snapshotToken: 'snapshot-1',
    expectedDraftVersion: 3, expectedDraftDigest: 'sha256:draft-3', outputId: 'patients', stageId: 'source',
  };
  const currentArgs: GetConstructionCapabilitiesArgs = {
    ...firstArgs, expectedDraftVersion: 4, expectedDraftDigest: 'sha256:draft-4',
  };
  const first = constructionCapabilitiesResource(client, () => firstArgs, true);
  const unsubscribeFirst = first.subscribe(() => {});
  expect(requests).toHaveLength(1);

  unsubscribeFirst();
  const active = constructionCapabilitiesResource(client, () => currentArgs, true);
  const unsubscribeActive = active.subscribe(() => {});
  expect(requests).toHaveLength(2);
  const [retired, current] = requests;
  await vi.waitFor(() => expect(retired.signal?.aborted).toBe(true));
  expect(current.signal?.aborted).toBe(false);

  current.resolve(capabilitiesFor(current.args));
  await vi.waitFor(() => expect(active.getSnapshot().data).toMatchObject({
    status: 'ready',
    response: { draftDigest: currentArgs.expectedDraftDigest },
  }));
  retired.resolve(capabilitiesFor(retired.args));
  await Promise.resolve();
  expect(active.getSnapshot().data).toMatchObject({
    status: 'ready',
    response: { draftDigest: currentArgs.expectedDraftDigest },
  });
  expect(first.getSnapshot().data).toBeUndefined();

  unsubscribeActive();
  await vi.waitFor(() => expect(active.getSnapshot().data).toBeUndefined());
});
