// @vitest-environment jsdom
import React from 'react';
import { act, render, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';

import { createLoomClient } from './api';
import { LoomExplorerBuilder } from './Builder';
import type { SelectionPage, SelectionRevision } from './selection';

const renderedPopulationStates = vi.hoisted(
  (): Array<{
    readonly selectionId?: string;
    readonly loading: boolean;
    readonly error?: string;
  }> => [],
);

vi.mock('./features/ExplorerBuilder/BuilderWorkspace', () => ({
  default: ({
    populationSelection,
    populationSelectionLoading,
    populationSelectionError,
  }: {
    readonly populationSelection?: SelectionRevision;
    readonly populationSelectionLoading?: boolean;
    readonly populationSelectionError?: string;
  }) => {
    renderedPopulationStates.push({
      selectionId: populationSelection?.id,
      loading: populationSelectionLoading ?? false,
      error: populationSelectionError,
    });
    return <div>Builder workspace</div>;
  },
}));

const selection = (id: string): SelectionRevision => ({
  id,
  project: 'project',
  generation: 'generation',
  resourceType: 'Patient',
  rule: { kind: 'EXPLICIT' },
  source: { kind: 'EXPLICIT_REFS' },
  scopeDigest: `scope-${id}`,
  ruleDigest: `rule-${id}`,
  membershipDigest: `members-${id}`,
  memberCount: 1,
  memberBytes: 10,
  complete: true,
  createdAt: '2026-09-21T00:00:00.000Z',
  completedAt: '2026-09-21T00:00:01.000Z',
});

const deferred = <T,>() => {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

beforeEach(() => {
  renderedPopulationStates.length = 0;
});

it('never hands an earlier selection to the workspace after the requested revision changes', async () => {
  const first = deferred<SelectionPage>();
  const second = deferred<SelectionPage>();
  const client = createLoomClient();
  vi.spyOn(client, 'getSelection').mockImplementation(({ selectionRevision }) =>
    selectionRevision === 'selection-1' ? first.promise : second.promise,
  );
  const view = render(
    <LoomExplorerBuilder
      project="project"
      explorerId="explorer"
      selectionRevisionId="selection-1"
      client={client}
    />,
  );

  await act(async () => first.resolve({
    revision: selection('selection-1'),
    members: [],
  }));
  await waitFor(() =>
    expect(renderedPopulationStates.at(-1)).toEqual({
      selectionId: 'selection-1',
      loading: false,
      error: undefined,
    }),
  );

  const transitionStart = renderedPopulationStates.length;
  view.rerender(
    <LoomExplorerBuilder
      project="project"
      explorerId="explorer"
      selectionRevisionId="selection-2"
      client={client}
    />,
  );

  expect(
    renderedPopulationStates
      .slice(transitionStart)
      .some(({ selectionId }) => selectionId === 'selection-1'),
  ).toBe(false);
  expect(renderedPopulationStates.at(-1)).toEqual({
    selectionId: undefined,
    loading: true,
    error: undefined,
  });

  await act(async () => second.resolve({
    revision: selection('selection-2'),
    members: [],
  }));
  await waitFor(() =>
    expect(renderedPopulationStates.at(-1)).toEqual({
      selectionId: 'selection-2',
      loading: false,
      error: undefined,
    }),
  );
});
