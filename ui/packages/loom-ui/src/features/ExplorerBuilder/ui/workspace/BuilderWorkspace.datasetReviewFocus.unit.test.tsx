// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { createLoomClient } from '../../../../client/api';
import { LoomProvider } from '../../../../react';
import { useBuilderToolbarSession } from '../../session/BuilderToolbarSession';
import BuilderWorkspace from './BuilderWorkspace';

vi.mock('../toolbar/BuilderToolbar', () => ({
  BuilderToolbar: function ReviewEntry() {
    const openReview = useBuilderToolbarSession(session => session.onReview);
    return <button type="button" onClick={openReview}>Review dataset</button>;
  },
}));

it('focuses the open table name when review directs the user to create the first table', async () => {
  const client = createLoomClient();
  const apiVersion = 'loom.calypr.org/explorer-authoring/v2';
  vi.spyOn(client, 'listExplorers').mockResolvedValue([{
    project: 'HTAN_INT/BForePC',
    explorerId: 'test',
    title: 'Test explorer',
    management: 'INTERACTIVE',
    updatedAt: '2026-10-10T00:00:00Z',
  }]);
  vi.spyOn(client, 'getBuilder').mockResolvedValue({
    apiVersion,
    kind: 'ExplorerBuilderState',
    lifecycleState: 'NEW',
    draftVersion: 0,
    draftDigest: '',
    workspace: null,
    catalog: {
      snapshotToken: 'first-snapshot',
      generation: 'first-generation',
      routePolicy: { allowRepeatedEdges: false, allowSelfLoops: false },
      nodes: [],
      edges: [],
      candidates: [],
    },
  });
  vi.spyOn(client, 'getCapability').mockResolvedValue({
    apiVersion,
    kind: 'ExplorerAuthoringCapabilities',
    operations: [],
    features: {
      emissionFilters: false,
      emissionCharts: false,
      sharedFilters: false,
      fixedFilters: false,
      fileActions: false,
      deleteExplorer: false,
    },
  });
  vi.spyOn(client, 'reconcile').mockRejectedValue(new Error('No draft to reconcile'));

  render(<LoomProvider client={client}>
    <BuilderWorkspace organization="HTAN_INT" project="BForePC" explorerId="test" />
  </LoomProvider>);

  const name = await screen.findByLabelText('Table name (optional)');
  fireEvent.click(screen.getByRole('button', { name: 'Review dataset' }));
  fireEvent.click(screen.getByRole('button', { name: 'Create your first table' }));
  expect(screen.queryByRole('region', { name: 'Dataset review' })).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(name));
});
