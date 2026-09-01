// @vitest-environment jsdom
import React from 'react';
import { vi, type Mock } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import {
  useApplyExplorerBuilderCommandsV2Mutation,
  useCreateExplorerAuthoringMutation,
  useDeleteExplorerAuthoringMutation,
  useGetExplorerAuthoringCapabilityV2Query,
  useGetExplorerAuthoringExplorersQuery,
  useGetExplorerBuilderStateV2Query,
  useGetExplorerCandidateSuggestionsV2Mutation,
  usePreviewExplorerAuthoringV2Mutation,
  usePublishExplorerAuthoringV2Mutation,
  useReconcileExplorerBuilderV2Mutation,
} from '../../react';
import BuilderWorkspace from './BuilderWorkspace';

vi.mock('../../react', () => ({
  useApplyExplorerBuilderCommandsV2Mutation: vi.fn(),
  useCreateExplorerAuthoringMutation: vi.fn(),
  useDeleteExplorerAuthoringMutation: vi.fn(),
  useGetExplorerAuthoringCapabilityV2Query: vi.fn(),
  useGetExplorerAuthoringExplorersQuery: vi.fn(),
  useGetExplorerBuilderStateV2Query: vi.fn(),
  useGetExplorerCandidateSuggestionsV2Mutation: vi.fn(),
  usePreviewExplorerAuthoringV2Mutation: vi.fn(),
  usePublishExplorerAuthoringV2Mutation: vi.fn(),
  useReconcileExplorerBuilderV2Mutation: vi.fn(),
}));

vi.mock('./components/BuilderToolbar', () => ({
  BuilderToolbar: ({
    onPreview,
    onPublish,
    previewDisabled,
    publishDisabled,
  }: {
    readonly onPreview: () => void;
    readonly onPublish: () => void;
    readonly previewDisabled: boolean;
    readonly publishDisabled: boolean;
  }) => (
    <div>
      <button type="button" disabled={previewDisabled} onClick={onPreview}>
        Preview
      </button>
      <button type="button" disabled={publishDisabled} onClick={onPublish}>
        Publish
      </button>
    </div>
  ),
}));

vi.mock('./components/GuidedGraphWorkspace', () => ({
  GuidedGraphWorkspace: () => <div>Graph</div>,
}));

vi.mock('./components/ColumnSelector', () => ({
  ColumnSelector: ({
    onChange,
  }: {
    readonly onChange: (value: unknown) => void;
  }) => (
    <button
      type="button"
      onClick={() =>
        onChange({
          column: 'specimen_identifier',
          label: 'Updated specimen identifier',
          occurrenceId: 'base',
          source: {
            kind: 'field',
            fieldPath: 'identifier[].value',
            projectionMode: 'FIRST',
          },
          table: { visible: true, order: 0 },
        })
      }
    >
      Save column change
    </button>
  ),
}));

vi.mock('./components/PreviewTable', () => ({
  PreviewTable: () => <div>Preview table</div>,
}));

const apiVersion = 'loom.calypr.org/explorer-authoring/v2' as const;
const column = {
  column: 'specimen_identifier',
  label: 'Specimen identifier',
  occurrenceId: 'base',
  source: {
    kind: 'field' as const,
    fieldPath: 'identifier[].value',
    projectionMode: 'FIRST',
  },
  table: { visible: true, order: 0 },
};
const workspace = {
  apiVersion,
  kind: 'ExplorerBuilderWorkspace' as const,
  explorer: { title: 'Test Explorer' },
  documents: [
    {
      kind: 'ExplorerBuilderDocument' as const,
      output: { id: 'specimens', title: 'Specimens' },
      rootResourceType: 'Specimen',
      route: { occurrenceId: 'base', resourceType: 'Specimen' },
      columns: [column],
    },
  ],
  tabs: [
    {
      id: 'specimens-tab',
      title: 'Specimens',
      outputId: 'specimens',
      order: 0,
      visible: true,
    },
  ],
};
const catalog = {
  snapshotToken: 'snapshot-1',
  generation: 'generation-1',
  routePolicy: { allowRepeatedEdges: false, allowSelfLoops: false },
  nodes: [
    {
      nodeId: 'specimen-node',
      resourceType: 'Specimen',
      rowRootEligible: true,
      populated: true,
      documentCount: 1,
    },
  ],
  edges: [],
  candidates: [
    {
      candidateId: 'specimen-id',
      nodeId: 'specimen-node',
      fieldPath: 'identifier[].value',
      label: 'Specimen identifier',
      logicalType: 'string',
      projectionModes: ['FIRST'],
      defaultProjectionMode: 'FIRST',
      filterable: true,
      chartable: false,
    },
  ],
};
const builderState = {
  apiVersion,
  kind: 'ExplorerBuilderState' as const,
  lifecycleState: 'READY' as const,
  draftVersion: 1,
  draftDigest: 'sha256:draft-1',
  workspace,
  catalog,
};
const receipt = {
  apiVersion,
  kind: 'ExplorerBuilderReceipt' as const,
  receiptId: 'receipt-1',
  snapshotToken: 'snapshot-1',
  builder: workspace,
  outputs: [
    {
      outputId: 'specimens',
      columns: [
        {
          column: 'specimen_identifier',
          label: 'Specimen identifier',
          logicalType: 'string',
          filterable: true,
          chartable: false,
        },
      ],
    },
  ],
  diagnostics: [],
};

const resolvedRequest = <T,>(value: T) => ({
  unwrap: vi.fn().mockResolvedValue(value),
  abort: vi.fn(),
});

describe('BuilderWorkspace on-demand reconciliation', () => {
  let applyCommands: Mock;
  let reconcile: Mock;
  let preview: Mock;
  let publish: Mock;

  beforeEach(() => {
    applyCommands = vi.fn().mockReturnValue(
      resolvedRequest({
        commandId: 'command-1',
        workspace,
        draftVersion: 2,
        draftDigest: 'sha256:draft-2',
        results: [
          {
            type: 'TABLE_CHANGED',
            outputId: 'specimens',
            column: 'specimen_identifier',
          },
        ],
        diagnostics: [],
      }),
    );
    reconcile = vi.fn().mockReturnValue(resolvedRequest(receipt));
    preview = vi.fn().mockReturnValue(
      resolvedRequest({
        apiVersion,
        kind: 'ExplorerBuilderPreview',
        receiptId: 'receipt-1',
        outputId: 'specimens',
        columns: receipt.outputs[0].columns,
        rows: [],
        rowCount: 0,
        diagnostics: [],
      }),
    );
    publish = vi.fn().mockReturnValue(resolvedRequest({}));

    (useGetExplorerAuthoringExplorersQuery as Mock).mockReturnValue({
      data: [{ explorerId: 'test', title: 'Test Explorer' }],
      isLoading: false,
      refetch: vi.fn(),
    });
    (useGetExplorerBuilderStateV2Query as Mock).mockReturnValue({
      data: builderState,
      isLoading: false,
      refetch: vi.fn(),
    });
    (useGetExplorerAuthoringCapabilityV2Query as Mock).mockReturnValue({
      data: { features: { deleteExplorer: false } },
    });
    (useApplyExplorerBuilderCommandsV2Mutation as Mock).mockReturnValue([
      applyCommands,
      { isLoading: false },
    ]);
    (useReconcileExplorerBuilderV2Mutation as Mock).mockReturnValue([
      reconcile,
      { isLoading: false },
    ]);
    (usePreviewExplorerAuthoringV2Mutation as Mock).mockReturnValue([
      preview,
      { isLoading: false },
    ]);
    (usePublishExplorerAuthoringV2Mutation as Mock).mockReturnValue([
      publish,
      { isLoading: false },
    ]);
    (useCreateExplorerAuthoringMutation as Mock).mockReturnValue([
      vi.fn(),
      { isLoading: false },
    ]);
    (useDeleteExplorerAuthoringMutation as Mock).mockReturnValue([
      vi.fn(),
      { isLoading: false },
    ]);
    (useGetExplorerCandidateSuggestionsV2Mutation as Mock).mockReturnValue(
      [vi.fn(), { isLoading: false }],
    );
  });

  it('does not reconcile a hydrated draft until Preview requests a receipt', async () => {
    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    const previewButton = await screen.findByRole('button', {
      name: 'Preview',
    });
    await waitFor(() => expect(previewButton).toBeEnabled());
    expect(reconcile).not.toHaveBeenCalled();

    fireEvent.click(previewButton);

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
  });

  it('saves commands without reconciling, then reconciles once before Publish', async () => {
    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    fireEvent.click(
      await screen.findByRole('button', { name: 'Save column change' }),
    );
    await waitFor(() => expect(applyCommands).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Publish' })).toBeEnabled(),
    );
    expect(reconcile).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Publish' }));

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(publish).toHaveBeenCalledTimes(1));
  });

  it('previews after one click when reconciliation returns a normalized builder', async () => {
    reconcile.mockReturnValue(
      resolvedRequest({
        ...receipt,
        builder: {
          ...workspace,
          sharedFilters: {
            identifier: [
              { outputId: 'specimens', column: 'specimen_identifier' },
            ],
          },
        },
      }),
    );

    render(
      <BuilderWorkspace
        organization="HTAN_INT"
        project="BForePC"
        explorerId="test"
      />,
    );

    const previewButton = await screen.findByRole('button', {
      name: 'Preview',
    });
    await waitFor(() => expect(previewButton).toBeEnabled());
    fireEvent.click(previewButton);

    await waitFor(() => expect(reconcile).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
  });
});

