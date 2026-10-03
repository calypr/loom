// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { LoomRequestError } from '../../../api';
import type { GetConstructionCapabilitiesArgs, ProposeConstructionArgs } from '../../../api';
import {
  EXPLORER_AUTHORING_API_VERSION,
  type ConstructionCapabilitiesResponse,
  type Construction,
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
  rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
  receiptId: 'filter-female-proposal',
  outputId: capabilitiesRequest.outputId,
  columns: [{ column: 'id', label: 'ID', logicalType: 'string', filterable: true, chartable: false }],
  rows: [{ id: 'patient-1' }],
  rowCount: 1,
  diagnostics: [],
};

const FilterProposalHarness = ({ client, onApplyReceipt }: { readonly client: ConstructionLifecycleClient; readonly onApplyReceipt?: (proposalId: string) => void }) => {
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
        onApply={() => {
          const proposalId = lifecycle.beginApply();
          if (proposalId) onApplyReceipt?.(proposalId);
        }}
        onCancel={lifecycle.cancel}
        onRetry={lifecycle.retry}
      />
    </>
  );
};

afterEach(cleanup);

describe('construction filter proposal wiring', () => {
  it('shows manual retry only for retryable proposal errors', () => {
    const onRetry = vi.fn();
    const { rerender } = render(<ConstructionProposalPanel state={{
      status: 'error', message: 'The policy has no matches.', retryable: false,
    }} canApply={false} onApply={() => undefined} onCancel={() => undefined} onRetry={onRetry} />);
    expect(screen.queryByRole('button', { name: 'Retry preview' })).not.toBeInTheDocument();

    rerender(<ConstructionProposalPanel state={{
      status: 'error', message: 'The database is temporarily unavailable.', retryable: true,
    }} canApply={false} onApply={() => undefined} onCancel={() => undefined} onRetry={onRetry} />);
    expect(screen.getByRole('button', { name: 'Retry preview' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry preview' }));
    expect(onRetry).toHaveBeenCalledOnce();
  });

  it('names every dependent removal before Apply and lets Cancel leave the proposal', () => {
    const construction: Construction = {
      version: 1,
      steps: ['Patient', 'Observation', 'Medication'].map((resource, index) => ({
        id: `expand-${resource}`, inputs: [{ kind: 'SOURCE_PROJECTION' }], outputs: [],
        operation: { kind: 'RELATED_EXPAND', relatedExpand: {
          anchorColumnId: `anchor-${index}`, choiceId: `choice-${index}`,
          targetNodeId: `node-${resource}`, targetResourceType: resource,
          route: [], contributorRule: { policy: 'ALL_MATCHES' },
          emptyPolicy: 'PRESERVE_PARENT', relatedRecordColumnId: `record-${resource}`,
        } },
      })),
    };
    const response: ConstructionProposalResponse = {
      proposalId: preview.receiptId, outputId: preview.outputId,
      snapshotToken: 'snapshot-1', draftVersion: 4, draftDigest: 'draft-4',
      baseDocumentDigest: 'document-4', candidateWorkspaceDigest: 'removed-workspace', changedStepId: '',
      candidateConstruction: { version: 1, steps: [] },
      dependencyImpact: { removedStepIds: construction.steps.map((step) => step.id), affectedStepIds: [] },
      stages: [], previewStatus: 'READY', previewDurationMs: 8,
    };
    const onApply = vi.fn();
    const onCancel = vi.fn();
    const { rerender } = render(<ConstructionProposalPanel state={{ status: 'ready', response, preview }}
      baseConstruction={construction} canApply onApply={onApply} onCancel={onCancel} onRetry={() => undefined} />);
    expect(screen.getByTestId('construction-removal-summary').textContent)
      .toContain('Remove Patient expansion and its dependent Observation and Medication expansions.');
    for (const step of construction.steps) expect(screen.getByTestId(`construction-removal-step-${step.id}`)).toBeInTheDocument();
    expect(onApply).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onCancel).toHaveBeenCalledOnce();
    expect(onApply).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Apply removal' }));
    expect(onApply).toHaveBeenCalledOnce();
    const editedResponse: ConstructionProposalResponse = {
      ...response, changedStepId: 'expand-Patient',
      candidateConstruction: { version: 1, steps: [construction.steps[0]] },
      dependencyImpact: { affectedStepIds: ['expand-Patient'], removedStepIds: ['expand-Observation', 'expand-Medication'] },
    };
    rerender(<ConstructionProposalPanel state={{ status: 'ready', response: editedResponse, preview }}
      baseConstruction={construction} canApply onApply={onApply} onCancel={onCancel} onRetry={() => undefined} />);
    expect(screen.getByTestId('construction-removal-summary').textContent)
      .toContain('This edit also removes the dependent Observation and Medication expansions.');
    expect(screen.queryByTestId('construction-removal-step-expand-Patient')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Apply change' })).toBeInTheDocument();
  });

  it('sends one typed gender equals female proposal and enables Apply only after its preview', async () => {
    const proposeConstruction = vi.fn(async (args: ProposeConstructionArgs): Promise<ConstructionProposalResponse> => {
      const step = args.candidateConstruction.steps[0];
      const filter = step?.operation.kind === 'FILTER' ? step.operation.filter : undefined;
      const value = filter?.values?.[0];
      const proposalId = filter && value?.kind === 'STRING'
        ? `filter-${filter.columnId}-${value.string}`
        : 'filter-invalid';
      return {
        proposalId,
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
      };
    });
    const client = {
      getConstructionCapabilities: vi.fn(async () => capabilities),
      discoverConstructionCategories: vi.fn(async () => { throw new Error('category discovery is not used by this test'); }),
      proposeConstruction,
      preview: vi.fn(async (args: Parameters<ConstructionLifecycleClient['preview']>[0]) => ({ ...preview, receiptId: args.receiptId })),
    } satisfies ConstructionLifecycleClient;
    const onApplyReceipt = vi.fn();
    const user = userEvent.setup();
    render(<FilterProposalHarness client={client} onApplyReceipt={onApplyReceipt} />);

    const column = await screen.findByRole('combobox', { name: 'Column' });
    await user.selectOptions(column, 'gender-id');
    await waitFor(() => expect((column as HTMLSelectElement).value).toBe('gender-id'));
    await new Promise((resolve) => setTimeout(resolve, 350));
    expect(proposeConstruction).not.toHaveBeenCalled();
    const valueInput = screen.getByRole('textbox', { name: 'Value' });
    fireEvent.change(valueInput, { target: { value: 'female' } });

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
    expect(client.preview).toHaveBeenCalledOnce();
    expect(client.preview.mock.calls[0]?.[0]).toMatchObject({
      receiptId: 'filter-gender-id-female',
      outputId: capabilitiesRequest.outputId,
    });
    fireEvent.click(screen.getByTestId('construction-apply-proposal'));
    await waitFor(() => expect(onApplyReceipt).toHaveBeenCalledOnce());
    expect(onApplyReceipt).toHaveBeenCalledWith('filter-gender-id-female');
  });

  it('keeps editing and auto-preview active after a non-retryable no-match error', async () => {
    let proposalCalls = 0;
    const proposeConstruction = vi.fn(async (args: ProposeConstructionArgs): Promise<ConstructionProposalResponse> => {
      proposalCalls += 1;
      if (proposalCalls === 1) {
        throw new LoomRequestError({
          status: 422,
          code: 'CONSTRUCTION_EXPANSION_EMPTY',
          message: 'At least one row has no matching related records.',
          retryable: false,
        });
      }
      return {
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
      };
    });
    const client = {
      getConstructionCapabilities: vi.fn(async () => capabilities),
      discoverConstructionCategories: vi.fn(async () => { throw new Error('category discovery is not used by this test'); }),
      proposeConstruction,
      preview: vi.fn(async () => preview),
    } satisfies ConstructionLifecycleClient;
    render(<FilterProposalHarness client={client} />);

    await screen.findByRole('combobox', { name: 'Column' });
    fireEvent.change(screen.getByRole('combobox', { name: 'Column' }), { target: { value: 'gender-id' } });
    const valueInput = screen.getByRole('textbox', { name: 'Value' }) as HTMLInputElement;
    fireEvent.change(valueInput, { target: { value: 'female' } });

    await waitFor(() => expect(proposeConstruction).toHaveBeenCalledOnce());
    expect(await screen.findByTestId('construction-proposal-error')).toHaveTextContent('no matching related records');
    expect(screen.queryByRole('button', { name: 'Retry preview' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled();
    expect(valueInput).toBeEnabled();

    fireEvent.change(valueInput, { target: { value: 'male' } });
    await waitFor(() => expect(proposeConstruction).toHaveBeenCalledTimes(2));
    expect(await screen.findByTestId('construction-proposal-ready')).toBeInTheDocument();
  });
});
