// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import type { ReactNode } from 'react';
import { createLoomClientMock } from '../../../../testSupport/loomClientMock';
import type { ConstructionCapabilitiesResponse, RelatedFieldChoiceSearchResponse } from '../../../contracts/types';
import { LoomProvider } from '../../../react';
import { useRelatedFieldChoices } from './useRelatedFieldChoices';

const selectedStage: ConstructionCapabilitiesResponse['selectedStage'] = {
  id: 'expanded',
  inputStageId: 'source',
  operation: 'RELATED_EXPAND',
  rowIdentityColumn: 'row-id',
  columns: [],
  capabilities: [],
  activeRelatedRecord: {
    targetNodeId: 'observation',
    targetResourceType: 'Observation',
    terminalIdentityColumn: 'row-id',
  },
};
const scope = { snapshotToken: 'snapshot', draftVersion: 2, draftDigest: 'digest', outputId: 'patients', stageId: selectedStage.id };
const capabilities: ConstructionCapabilitiesResponse = {
  ...scope,
  baseConstruction: { version: 1, steps: [] },
  selectedStage,
  stages: [selectedStage],
  groupFieldChoices: [],
  groupRowValueChoices: [],
  sourceInput: { supported: false, stageId: selectedStage.id, choices: [] },
  workspaceInputs: [],
};
const response = (label: string, nextCursor?: string): RelatedFieldChoiceSearchResponse => ({
  ...scope, complete: !nextCursor, truncated: Boolean(nextCursor), nextCursor,
  choices: label ? [{
    choiceId: label, label,
    source: {
      kind: 'FIELD', candidateId: label, path: label, nodeId: 'observation',
      resourceType: 'Observation', cardinality: 'optional_one', logicalType: 'string',
    },
  }] : [],
});

it('skips empty pages, retries a rejected continuation, and keeps existing choices', async () => {
  const searchRelatedFieldChoices = vi.fn()
    .mockResolvedValueOnce(response('', 'empty'))
    .mockResolvedValueOnce(response('first', 'next'))
    .mockResolvedValueOnce({ ...response('stale'), draftVersion: 3 })
    .mockResolvedValueOnce(response('second'));
  const client = createLoomClientMock({ searchRelatedFieldChoices });
  const { result } = renderHook(() => useRelatedFieldChoices({ project: 'project', explorerId: 'explorer', ...scope, capabilities }), {
    wrapper: ({ children }: { children: ReactNode }) => <LoomProvider client={client}>{children}</LoomProvider>,
  });

  await waitFor(() => expect(result.current.choices.map(({ label }) => label)).toEqual(['first']));
  expect(searchRelatedFieldChoices.mock.calls.map(([args]) => args.cursor)).toEqual([undefined, 'empty']);
  await act(() => result.current.loadMore());
  await waitFor(() => expect(result.current.error).toContain('Available fields changed'));
  expect(result.current.choices.map(({ label }) => label)).toEqual(['first']);
  await act(() => result.current.loadMore());
  await waitFor(() => expect(result.current.choices.map(({ label }) => label)).toEqual(['first', 'second']));
  expect(result.current.cursor).toBeUndefined();
  expect(result.current.error).toBe('');
});

it('aborts the continuation when search changes and never exposes the old page', async () => {
  let continuationSignal: AbortSignal | undefined;
  let finishContinuation: (value: RelatedFieldChoiceSearchResponse) => void = () => {};
  const searchRelatedFieldChoices = vi.fn()
    .mockResolvedValueOnce(response('first', 'next'))
    .mockImplementationOnce((_args, signal: AbortSignal) => {
      continuationSignal = signal;
      return new Promise<RelatedFieldChoiceSearchResponse>((resolve) => { finishContinuation = resolve; });
    })
    .mockResolvedValueOnce(response('new search'));
  const client = createLoomClientMock({ searchRelatedFieldChoices });
  const { result } = renderHook(() => useRelatedFieldChoices({ project: 'project', explorerId: 'explorer', ...scope, capabilities }), {
    wrapper: ({ children }: { children: ReactNode }) => <LoomProvider client={client}>{children}</LoomProvider>,
  });

  await waitFor(() => expect(result.current.cursor).toBe('next'));
  act(() => { void result.current.loadMore(); });
  expect(continuationSignal?.aborted).toBe(false);
  act(() => result.current.searchFor(' new '));
  await waitFor(() => expect(result.current.choices.map(({ label }) => label)).toEqual(['new search']));
  expect(continuationSignal?.aborted).toBe(true);
  expect(searchRelatedFieldChoices.mock.calls.at(-1)?.[0]).toMatchObject({ query: 'new' });
  await act(async () => { finishContinuation(response('old continuation')); });
  expect(result.current.choices.map(({ label }) => label)).toEqual(['new search']);
});
