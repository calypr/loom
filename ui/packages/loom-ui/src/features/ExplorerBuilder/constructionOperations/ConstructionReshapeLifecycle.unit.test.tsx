// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { GetConstructionCapabilitiesArgs, ProposeConstructionArgs } from '../../../api';
import {
  EXPLORER_AUTHORING_API_VERSION,
  type Construction,
  type ConstructionCapabilitiesResponse,
  type ConstructionCategoryDiscoveryResponse,
  type ConstructionProposalResponse,
  type ExplorerBuilderPreviewResult,
} from '../../../types';
import {
  ConstructionReshapeEditor,
  type ConstructionReshapePivotCategory,
} from './ConstructionReshapeEditor';
import {
  useConstructionLifecycle,
  type ConstructionLifecycleClient,
} from '../constructionWorkspace/useConstructionLifecycle';

const priorStep: Construction['steps'][number] = {
  id: 'filter-specimens',
  inputs: [{ kind: 'SOURCE_PROJECTION' }],
  operation: { kind: 'FILTER', filter: { columnId: 'specimen-id', operator: 'EXISTS' } },
  outputs: [
    { id: 'specimen-id', name: 'specimen_id', label: 'Specimen ID', type: 'string' },
    { id: 'tags-id', name: 'tags', label: 'Tags', type: 'string' },
  ],
};

const construction: Construction = { version: 1, steps: [priorStep] };

const sourceStage: ConstructionCapabilitiesResponse['selectedStage'] = {
  id: 'source_projection',
  inputStageId: '',
  columns: [
    { id: 'specimen-id', name: 'specimen_id', label: 'Specimen ID', type: 'string', cardinality: 'required_one' },
    { id: 'tags-id', name: 'tags', label: 'Tags', type: 'string', cardinality: 'many' },
  ],
  capabilities: [],
};

const intermediateStage: ConstructionCapabilitiesResponse['selectedStage'] = {
  ...sourceStage,
  id: priorStep.id,
  inputStageId: sourceStage.id,
  operation: 'FILTER',
  capabilities: [
    { kind: 'GROUP', supported: true },
    { kind: 'EXPAND', supported: true },
  ],
};

const request: GetConstructionCapabilitiesArgs = {
  project: 'org/project',
  explorerId: 'explorer-1',
  snapshotToken: 'snapshot-1',
  expectedDraftVersion: 1,
  expectedDraftDigest: 'draft-1',
  outputId: 'specimens',
  stageId: intermediateStage.id,
};

const capabilities: ConstructionCapabilitiesResponse = {
  snapshotToken: request.snapshotToken,
  draftVersion: request.expectedDraftVersion,
  draftDigest: request.expectedDraftDigest,
  outputId: request.outputId,
  stageId: request.stageId,
  baseConstruction: construction,
  stages: [sourceStage, intermediateStage],
  selectedStage: intermediateStage,
  groupFieldChoices: [],
  groupRowValueChoices: [],
  workspaceInputs: [],
};

const makeClient = () => {
  const proposeConstruction = vi.fn(async (args: ProposeConstructionArgs): Promise<ConstructionProposalResponse> => ({
    proposalId: 'proposal-1',
    outputId: args.outputId,
    snapshotToken: args.snapshotToken,
    draftVersion: args.expectedDraftVersion,
    draftDigest: args.expectedDraftDigest,
    baseDocumentDigest: 'document-1',
    candidateWorkspaceDigest: 'candidate-1',
    changedStepId: args.changedStepId ?? '',
    candidateConstruction: args.candidateConstruction,
    dependencyImpact: { affectedStepIds: [] },
    stages: capabilities.stages,
    previewStatus: 'READY',
    previewDurationMs: 12,
  }));
  const preview = vi.fn(async (args: { readonly receiptId: string; readonly outputId: string }): Promise<ExplorerBuilderPreviewResult> => ({
    apiVersion: EXPLORER_AUTHORING_API_VERSION,
    kind: 'ExplorerBuilderPreview',
    rowLineageCapability: { status: 'UNAVAILABLE', reasonCode: 'TEST_FIXTURE' },
    receiptId: args.receiptId,
    outputId: args.outputId,
    columns: [{ column: 'specimen_id', label: 'Specimen ID', logicalType: 'string', filterable: true, chartable: false }],
    rows: [{ specimen_id: 'specimen-1' }],
    rowCount: 1,
    diagnostics: [],
  }));
  const client = {
    getConstructionCapabilities: vi.fn(async () => capabilities),
    discoverConstructionCategories: vi.fn(async () => { throw new Error('category discovery is not used by this test'); }),
    proposeConstruction,
    preview,
  } satisfies ConstructionLifecycleClient;
  return { client, proposeConstruction, preview };
};

const LifecycleEditor = (props: {
  readonly client: ConstructionLifecycleClient;
  readonly onApply: (proposalId: string) => void;
  readonly chooseGroupDuringLayout?: boolean;
}) => {
  const lifecycle = useConstructionLifecycle({ client: props.client, capabilitiesRequest: request });
  const [groupIntentSelected, setGroupIntentSelected] = React.useState(false);
  const layoutChoiceTriggered = React.useRef(false);
  React.useLayoutEffect(() => {
    if (!props.chooseGroupDuringLayout || lifecycle.capabilities.status !== 'ready' || layoutChoiceTriggered.current) return;
    if (!groupIntentSelected) {
      const groupIntent = document.querySelector<HTMLButtonElement>('[data-testid="construction-reshape-intent-group"]');
      if (!groupIntent) return;
      groupIntent.click();
      setGroupIntentSelected(true);
      return;
    }
    const choice = document.querySelector<HTMLButtonElement>('[data-testid="construction-reshape-choice-group"]');
    if (!choice) return;
    layoutChoiceTriggered.current = true;
    choice.click();
  }, [props.chooseGroupDuringLayout, lifecycle.capabilities.status, groupIntentSelected]);
  if (lifecycle.capabilities.status !== 'ready') return <p role="status">Loading stage capabilities</p>;
  const response = lifecycle.capabilities.response;
  return (
    <>
      <ConstructionReshapeEditor
        construction={response.baseConstruction}
        capabilities={response}
        disabled={false}
        onCandidateChange={lifecycle.onCandidateChange}
        onEditStep={() => undefined}
      />
      <p data-testid="proposal-status">{lifecycle.proposal.status}</p>
      <button
        type="button"
        disabled={!lifecycle.canApply}
        onClick={() => {
          const apply = lifecycle.beginApply();
          if (apply) {
            props.onApply(apply.proposalId);
            apply.finish(true);
          }
        }}
      >
        Apply change
      </button>
    </>
  );
};

describe('ConstructionReshapeEditor construction lifecycle', () => {
  it('preserves a GROUP choice issued from the committed chooser', async () => {
    const { client, proposeConstruction } = makeClient();
    render(<LifecycleEditor client={client} onApply={() => undefined} chooseGroupDuringLayout />);

    const summaryLabel = await screen.findByLabelText('Summary output label 1');
    fireEvent.change(summaryLabel, { target: { value: 'Specimens per group' } });
    await waitFor(() => expect(screen.getByTestId('proposal-status')).toHaveTextContent('ready'));

    expect(proposeConstruction).toHaveBeenCalledOnce();
    const proposed = proposeConstruction.mock.calls[0]?.[0];
    expect(proposed?.candidateConstruction.steps.at(-1)?.operation).toMatchObject({
      kind: 'GROUP',
      group: { aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: expect.any(String) }] },
    });
    expect(proposed?.candidateConstruction.steps.at(-1)?.outputs.filter((column) => column.label === 'Specimens per group')).toHaveLength(1);
  });

  it.each([
    { kind: 'GROUP' as const },
    { kind: 'EXPAND' as const },
  ])('previews an intermediate-stage $kind proposal before Apply', async ({ kind }) => {
    const { client, proposeConstruction, preview } = makeClient();
    const onApply = vi.fn();
    render(<LifecycleEditor client={client} onApply={onApply} />);

    await screen.findByTestId('construction-reshape-editor');
    const apply = screen.getByRole('button', { name: 'Apply change' });
    expect(apply).toBeDisabled();
    fireEvent.click(screen.getByTestId('construction-reshape-intent-group'));
    fireEvent.click(await screen.findByTestId('construction-reshape-choice-group'));
    if (kind === 'EXPAND') {
      fireEvent.click(await screen.findByRole('button', { name: 'Expand Tags' }));
    }
    await screen.findByLabelText(kind === 'EXPAND' ? 'Empty list policy' : 'Summary output label 1');
    if (kind === 'EXPAND') {
      fireEvent.change(screen.getByLabelText('Empty list policy'), { target: { value: 'PRESERVE_PARENT' } });
    } else {
      fireEvent.change(screen.getByLabelText('Summary output label 1'), { target: { value: 'Specimens per group' } });
    }
    expect(apply).toBeDisabled();

    await waitFor(() => expect(screen.getByTestId('proposal-status')).toHaveTextContent('ready'));
    expect(proposeConstruction).toHaveBeenCalledOnce();
    expect(preview).toHaveBeenCalledWith(expect.objectContaining({ receiptId: 'proposal-1', outputId: 'specimens' }), expect.any(AbortSignal));
    const proposed = proposeConstruction.mock.calls[0]?.[0];
    expect(proposed?.candidateConstruction.steps.at(-1)?.inputs).toEqual([
      { kind: 'STEP_OUTPUT', stepId: priorStep.id },
    ]);
    expect(proposed?.candidateConstruction.steps.at(-1)?.operation.kind).toBe(kind);
    if (kind === 'GROUP') {
      expect(proposed?.candidateConstruction.steps.at(-1)?.operation).toMatchObject({
        kind: 'GROUP',
        group: { aggregates: [{ operation: 'COUNT_ROWS', outputColumnId: expect.any(String) }] },
      });
      expect(proposed?.candidateConstruction.steps.at(-1)?.outputs.filter((column) => column.label === 'Specimens per group')).toHaveLength(1);
    }
    expect(apply).toBeEnabled();

    fireEvent.click(apply);
    expect(onApply).toHaveBeenCalledWith('proposal-1');
  });

  it('resets the selected operation when the editor context changes', async () => {
    const onCandidateChange = vi.fn();
    const renderEditor = (activeCapabilities: ConstructionCapabilitiesResponse) => (
      <ConstructionReshapeEditor
        construction={activeCapabilities.baseConstruction}
        capabilities={activeCapabilities}
        disabled={false}
        onCandidateChange={onCandidateChange}
        onEditStep={() => undefined}
      />
    );
    const { rerender } = render(renderEditor(capabilities));

    await screen.findByTestId('construction-reshape-intent-group');
    fireEvent.click(screen.getByTestId('construction-reshape-intent-group'));
    fireEvent.click(await screen.findByTestId('construction-reshape-choice-group'));
    await screen.findByLabelText('Summary output label 1');

    const nextCapabilities = {
      ...capabilities,
      snapshotToken: 'snapshot-2',
      draftDigest: 'draft-2',
    };
    rerender(renderEditor(nextCapabilities));
    await waitFor(() => expect(screen.queryByLabelText('Summary output label 1')).not.toBeInTheDocument());
    expect(screen.getByTestId('construction-reshape-intent-group')).toBeInTheDocument();
    expect(screen.queryByTestId('construction-reshape-choice-group')).not.toBeInTheDocument();
  });
});

const pivotStage: ConstructionCapabilitiesResponse['selectedStage'] = {
  id: 'pivot-stage',
  inputStageId: sourceStage.id,
  operation: 'FILTER',
  columns: [
    { id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string', cardinality: 'required_one' },
    { id: 'sex-id', name: 'sex', label: 'Sex', type: 'string', cardinality: 'required_one' },
    { id: 'score-id', name: 'score', label: 'Score', type: 'decimal', cardinality: 'optional_one' },
  ],
  capabilities: [
    { kind: 'GROUP', supported: true },
    { kind: 'EXPAND', supported: true },
    { kind: 'PIVOT', supported: true },
  ],
};

const pivotRequest: GetConstructionCapabilitiesArgs = { ...request, stageId: pivotStage.id };

const pivotCapabilities: ConstructionCapabilitiesResponse = {
  ...capabilities,
  stageId: pivotStage.id,
  stages: [sourceStage, pivotStage],
  selectedStage: pivotStage,
};

type Deferred = {
  readonly promise: Promise<unknown>;
  readonly resolve: (value: unknown) => void;
  readonly reject: (reason: unknown) => void;
};

const deferred = (): Deferred => {
  let resolve!: (value: unknown) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<unknown>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
};

const discoveryResponse = (
  args: { readonly categoryColumnId: string; readonly valueColumnId: string },
  categories: ConstructionReshapePivotCategory[],
): ConstructionCategoryDiscoveryResponse => ({
  snapshotToken: pivotRequest.snapshotToken,
  draftVersion: pivotRequest.expectedDraftVersion,
  draftDigest: pivotRequest.expectedDraftDigest,
  outputId: pivotRequest.outputId,
  stageId: pivotStage.id,
  categoryColumnId: args.categoryColumnId,
  valueColumnId: args.valueColumnId,
  outcome: 'COMPLETE',
  complete: true,
  proofFingerprint: 'proof-1',
  categories,
});

const makePivotClient = () => {
  const base = makeClient();
  const gates: Deferred[] = [];
  const categoriesByPair: Record<string, ConstructionReshapePivotCategory[]> = {
    'sex-id': [
      { key: { kind: 'STRING', string: 'female' }, label: 'Female' },
      { key: { kind: 'STRING', string: 'male' }, label: 'Male' },
    ],
  };
  const discoverConstructionCategories = vi.fn(async (args: {
    readonly categoryColumnId: string;
    readonly valueColumnId: string;
  }): Promise<ConstructionCategoryDiscoveryResponse> => {
    const gate = deferred();
    gates.push(gate);
    await gate.promise;
    return discoveryResponse(args, [...(categoriesByPair[args.categoryColumnId] ?? [])]);
  });
  const client = {
    getConstructionCapabilities: vi.fn(async () => pivotCapabilities),
    discoverConstructionCategories,
    proposeConstruction: base.proposeConstruction,
    preview: base.preview,
  } satisfies ConstructionLifecycleClient;
  return { client, discoverConstructionCategories, gates };
};

const PivotLifecycleEditor = (props: {
  readonly client: ConstructionLifecycleClient;
  readonly onOutputNames: (names: ReadonlyArray<string>) => void;
}) => {
  const lifecycle = useConstructionLifecycle({ client: props.client, capabilitiesRequest: pivotRequest });
  if (lifecycle.capabilities.status !== 'ready') return <p role="status">Loading stage capabilities</p>;
  const response = lifecycle.capabilities.response;
  return (
    <ConstructionReshapeEditor
      construction={response.baseConstruction}
      capabilities={response}
      initialKind="pivot"
      selectedColumns={['patient-id']}
      disabled={false}
      onCandidateChange={(intent) => {
        const outputs = intent?.candidateConstruction.steps.at(-1)?.outputs ?? [];
        props.onOutputNames(outputs.map((column) => column.name));
        lifecycle.onCandidateChange(intent);
      }}
      onEditStep={() => undefined}
      pivotDiscovery={lifecycle.pivotDiscovery}
      onDiscoverCategories={lifecycle.onDiscoverCategories}
    />
  );
};

const choosePivotFields = (categoryLabel: string, valueLabel: string) => {
  fireEvent.change(screen.getByLabelText('Pivot category field'), { target: { value: categoryLabel } });
  fireEvent.change(screen.getByLabelText('Pivot values field'), { target: { value: valueLabel } });
};

describe('ConstructionReshapeEditor pivot discovery lifecycle', () => {
  it('shows loading then resolved categories from the real host discovery path', async () => {
    const { client, discoverConstructionCategories, gates } = makePivotClient();
    render(<PivotLifecycleEditor client={client} onOutputNames={() => undefined} />);
    await screen.findByTestId('construction-reshape-pivot');

    choosePivotFields('sex-id', 'score-id');

    await waitFor(() => expect(discoverConstructionCategories).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId('construction-reshape-pivot-discovery-status')).toHaveAttribute('aria-busy', 'true');
    expect(screen.getByTestId('construction-reshape-pivot-discovery-status').textContent).toContain('Finding categories…');
    expect(screen.getByTestId('construction-reshape-pivot-discovery-status').textContent).toContain('Sex');
    expect(screen.getByTestId('construction-reshape-pivot-discovery-status').textContent).toContain('Score');
    expect(screen.queryByTestId('construction-reshape-pivot-category-summary')).not.toBeInTheDocument();

    gates[0]?.resolve(undefined);

    await waitFor(() => expect(screen.getByTestId('construction-reshape-pivot-category-summary'))
      .toHaveTextContent('Selected 2 of 2 categories: Female, Male'));
    expect(screen.queryByTestId('construction-reshape-pivot-discovery-status')).not.toBeInTheDocument();
  });

  it('shows loading then failure, and retrying the same current choice keeps unrelated editor edits', async () => {
    const { client, discoverConstructionCategories, gates } = makePivotClient();
    const outputNames: ReadonlyArray<string>[] = [];
    render(<PivotLifecycleEditor client={client} onOutputNames={(names) => outputNames.push(names)} />);
    await screen.findByTestId('construction-reshape-pivot');

    choosePivotFields('sex-id', 'score-id');
    await waitFor(() => expect(discoverConstructionCategories).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId('construction-reshape-pivot-discovery-status')).toHaveAttribute('aria-busy', 'true');

    gates[0]?.reject(new Error('Category discovery timed out listing the category values.'));

    await waitFor(() => expect(screen.getByText('Category discovery timed out listing the category values.')).toBeInTheDocument());
    expect(screen.queryByTestId('construction-reshape-pivot-discovery-status')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Retry finding categories' }));
    await waitFor(() => expect(discoverConstructionCategories).toHaveBeenCalledTimes(2));
    expect(discoverConstructionCategories.mock.calls[1]?.[0]).toMatchObject({
      stageId: pivotStage.id,
      categoryColumnId: 'sex-id',
      valueColumnId: 'score-id',
    });
    expect(screen.getByTestId('construction-reshape-pivot-discovery-status')).toHaveAttribute('aria-busy', 'true');

    gates[1]?.resolve(undefined);

    await waitFor(() => expect(screen.getByTestId('construction-reshape-pivot-category-summary'))
      .toHaveTextContent('Selected 2 of 2 categories: Female, Male'));
    expect(screen.queryByTestId('construction-reshape-pivot-discovery-status')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('Advanced settings'));
    fireEvent.change(screen.getByLabelText('Pivot output name female'), { target: { value: 'female_score' } });
    fireEvent.change(screen.getByLabelText('Pivot missing cell policy'), { target: { value: 'ERROR' } });

    await waitFor(() => expect(outputNames.at(-1)).toContain('female_score'));
    expect(screen.getByLabelText('Pivot output name female')).toHaveProperty('value', 'female_score');
    expect(screen.getByLabelText('Pivot missing cell policy')).toHaveProperty('value', 'ERROR');
  });
});

