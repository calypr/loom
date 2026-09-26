// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { GetConstructionCapabilitiesArgs, ProposeConstructionArgs } from '../../../api';
import {
  EXPLORER_AUTHORING_API_VERSION,
  type ConstructionCapabilitiesResponse,
  type ConstructionProposalResponse,
  type ConstructionStageDescriptor,
  type ExplorerBuilderPreviewResult,
} from '../../../types';
import { ConstructionOperationEditor } from '../constructionOperations/ConstructionOperationEditor';
import { ConstructionProposalPanel } from './ConstructionProposalPanel';
import { useConstructionLifecycle, type ConstructionLifecycleClient } from './useConstructionLifecycle';

const capabilitiesRequest: GetConstructionCapabilitiesArgs = {
  project: 'org/project',
  explorerId: 'patient-explorer',
  snapshotToken: 'snapshot-1',
  expectedDraftVersion: 4,
  expectedDraftDigest: 'draft-4',
  outputId: 'patient-table',
  stageId: 'source_projection',
};

const sourceStage: ConstructionStageDescriptor = {
  id: 'source_projection',
  inputStageId: '',
  columns: [
    { id: 'patient-id', name: 'id', label: 'ID', type: 'string' },
    { id: 'gender-id', name: 'gender', label: 'Gender', type: 'string' },
  ],
  capabilities: [
    { kind: 'FILTER', supported: true },
    { kind: 'DERIVE', supported: true },
    { kind: 'PIVOT', supported: false, reason: 'Not needed by this test.' },
    { kind: 'UNPIVOT', supported: false, reason: 'Not needed by this test.' },
  ],
};

const capabilities: ConstructionCapabilitiesResponse = {
  snapshotToken: capabilitiesRequest.snapshotToken,
  draftVersion: capabilitiesRequest.expectedDraftVersion,
  draftDigest: capabilitiesRequest.expectedDraftDigest,
  outputId: capabilitiesRequest.outputId,
  stageId: capabilitiesRequest.stageId,
  baseConstruction: { version: 1, steps: [] },
  stages: [sourceStage],
  selectedStage: sourceStage,
};

const preview: ExplorerBuilderPreviewResult = {
  apiVersion: EXPLORER_AUTHORING_API_VERSION,
  kind: 'ExplorerBuilderPreview',
  receiptId: 'filter-female-proposal',
  outputId: capabilitiesRequest.outputId,
  columns: [{ column: 'id', label: 'ID', logicalType: 'string', filterable: true, chartable: false }],
  rows: [{ id: 'patient-1' }],
  rowCount: 1,
  diagnostics: [],
};

const FilterProposalHarness = ({ client }: { readonly client: ConstructionLifecycleClient }) => {
  const lifecycle = useConstructionLifecycle({ client, capabilitiesRequest });
  if (lifecycle.capabilities.status !== 'ready') return <p>Loading capabilities</p>;

  return (
    <>
      <ConstructionOperationEditor
        family="KEEP_ROWS"
        construction={lifecycle.capabilities.response.baseConstruction}
        capabilities={lifecycle.capabilities.response}
        selectedColumns={['patient-id']}
        disabled={false}
        onCandidateChange={lifecycle.onCandidateChange}
        onEditStep={() => undefined}
      />
      <ConstructionProposalPanel
        state={lifecycle.proposal}
        canApply={lifecycle.canApply}
        onApply={() => undefined}
        onCancel={lifecycle.cancel}
        onRetry={lifecycle.retry}
      />
    </>
  );
};

afterEach(cleanup);

describe('construction filter proposal wiring', () => {
  it('sends one typed gender equals female proposal and enables Apply only after its preview', async () => {
    const proposeConstruction = vi.fn(async (args: ProposeConstructionArgs): Promise<ConstructionProposalResponse> => ({
      proposalId: 'filter-female-proposal',
      outputId: args.outputId,
      snapshotToken: args.snapshotToken,
      draftVersion: args.expectedDraftVersion,
      draftDigest: args.expectedDraftDigest,
      baseDocumentDigest: 'document-4',
      candidateWorkspaceDigest: 'candidate-workspace-5',
      changedStepId: args.changedStepId ?? '',
      candidateConstruction: args.candidateConstruction,
      dependencyImpact: { affectedStepIds: [args.changedStepId ?? ''] },
      stages: [sourceStage],
      previewStatus: 'READY',
      previewDurationMs: 8,
    }));
    const client = {
      getConstructionCapabilities: vi.fn(async () => capabilities),
      proposeConstruction,
      preview: vi.fn(async () => preview),
    } satisfies ConstructionLifecycleClient;
    render(<FilterProposalHarness client={client} />);

    await screen.findByRole('combobox', { name: 'Column' });
    fireEvent.change(screen.getByRole('combobox', { name: 'Column' }), { target: { value: 'gender-id' } });
    fireEvent.change(screen.getByRole('textbox', { name: 'Value' }), { target: { value: 'female' } });

    await waitFor(() => expect(proposeConstruction).toHaveBeenCalledOnce());
    const proposalRequest = proposeConstruction.mock.calls[0]?.[0];
    expect(proposalRequest).toMatchObject({
      outputId: capabilitiesRequest.outputId,
      expectedDraftVersion: capabilitiesRequest.expectedDraftVersion,
      expectedDraftDigest: capabilitiesRequest.expectedDraftDigest,
      candidateConstruction: {
        version: 1,
        steps: [{
          inputs: [{ kind: 'SOURCE_PROJECTION' }],
          operation: {
            kind: 'FILTER',
            filter: {
              columnId: 'gender-id',
              operator: 'EQUALS',
              values: [{ kind: 'STRING', string: 'female' }],
            },
          },
        }],
      },
    });
    const ready = await screen.findByTestId('construction-proposal-ready');
    expect(ready.textContent).toContain('Proposal preview');
    expect(ready.textContent).not.toContain('later step');
    expect((screen.getByTestId('construction-apply-proposal') as HTMLButtonElement).disabled).toBe(false);
  });
});
