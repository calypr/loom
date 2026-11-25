// @vitest-environment jsdom
import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { relatedExpandContributorSearchResponseSchema, type ConstructionCapabilitiesResponse, type ExplorerBuilderCatalog, type RelatedExpandContributorSearchResponse } from '../../../contracts/types';
import { ConstructionProposalPanel } from '../constructionWorkspace/ConstructionProposalPanel';
import { RelatedExpandEditor } from './RelatedExpandEditor';
import type { RelatedExpandQueryOwner } from '../session/commandQueue';
import { RelatedExpandContributorEditor } from './RelatedExpandContributorEditor';

const searchRelatedExpandChoices = vi.fn();
const searchRelatedExpandContributors = vi.fn(async (args: { snapshotToken: string; expectedDraftVersion: number; expectedDraftDigest: string; outputId: string; stageId: string; routeChoiceId: string; query?: string; cursor?: string }, _signal?: AbortSignal): Promise<RelatedExpandContributorSearchResponse> => ({
  snapshotToken: args.snapshotToken, draftVersion: args.expectedDraftVersion, draftDigest: args.expectedDraftDigest,
  outputId: args.outputId, stageId: args.stageId, routeChoiceId: args.routeChoiceId,
  complete: true, truncated: false, choices: [],
}));
const client = { searchRelatedExpandChoices, searchRelatedExpandContributors };
vi.mock('../../../react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../react')>();
  return { ...actual, useLoomClient: () => client };
});

const route = [{
  edgeId: 'patient-encounter',
  fromNodeId: 'patient-node',
  toNodeId: 'encounter-node',
  fromResourceType: 'Patient',
  toResourceType: 'Encounter',
  relationship: 'subject_Patient',
  storageDirection: 'INBOUND' as const,
  matchMode: 'OPTIONAL' as const,
}];
const rootAnchor = {
  anchorColumnId: '_key', kind: 'root' as const, nodeId: 'patient-node',
  resourceType: 'Patient', label: 'Original Patient record',
};
const encounterAnchor = {
  anchorColumnId: '__loom_encounter_id', kind: 'activeRelatedRecord' as const,
  nodeId: 'encounter-node', resourceType: 'Encounter', label: 'Current Encounter record',
};

const contributorPage = (args: { snapshotToken: string; expectedDraftVersion: number; expectedDraftDigest: string; outputId: string; stageId: string; routeChoiceId: string }, choices: RelatedExpandContributorSearchResponse['choices'], nextCursor?: string): RelatedExpandContributorSearchResponse => ({
  snapshotToken: args.snapshotToken, draftVersion: args.expectedDraftVersion, draftDigest: args.expectedDraftDigest,
  outputId: args.outputId, stageId: args.stageId, routeChoiceId: args.routeChoiceId,
  complete: nextCursor === undefined, truncated: nextCursor !== undefined,
  ...(nextCursor ? { nextCursor } : {}), choices,
});
const repeatedContributor: RelatedExpandContributorSearchResponse['choices'][number] = {
  choiceId: 'repeated-status-choice', label: 'Repeated status',
  source: { kind: 'FIELD', candidateId: 'encounter-observations', nodeId: 'encounter-node', resourceType: 'Encounter',
    path: 'Encounter.observation[].status', cardinality: 'many', logicalType: 'string',
    repeatedBoundaries: [{ path: 'observation[]', maxItems: 4 }] },
  operators: ['EXISTS'], suggestedValues: [], suggestionsComplete: true, suggestionsTruncated: false, suggestionsSource: 'catalog',
};
const scalarContributor: RelatedExpandContributorSearchResponse['choices'][number] = {
  choiceId: 'scalar-status-choice', label: 'Status',
  source: { kind: 'FIELD', candidateId: 'encounter-status', nodeId: 'encounter-node', resourceType: 'Encounter', path: 'Encounter.status', cardinality: 'optional_one', logicalType: 'string' },
  operators: ['EXISTS'], suggestedValues: [], suggestionsComplete: true, suggestionsTruncated: false, suggestionsSource: 'catalog',
};
function openEncounterContributorCondition() {
  render(<RelatedExpandContributorEditor project="project" explorerId="explorer" snapshotToken="snapshot-1"
    draftVersion={1} draftDigest="draft-1" outputId="patients" stageId="source_projection"
    routeChoiceId="signed-choice" targetNodeId="encounter-node" targetResourceType="Encounter"
    allowRepeatedFields={false} condition={{ kind: 'CHOOSE' }} disabled={false} onChange={vi.fn()} />);
}

const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot-1',
  generation: 'fixture-1',
  routePolicy: {},
  nodes: [
    { nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 2 },
    { nodeId: 'encounter-node', resourceType: 'Encounter', rowRootEligible: false, populated: true, documentCount: 3 },
    { nodeId: 'observation-node', resourceType: 'Observation', rowRootEligible: false, populated: true, documentCount: 5 },
  ],
  edges: [{ edgeId: 'patient-encounter', fromNodeId: 'patient-node', toNodeId: 'encounter-node', label: 'subject_Patient' }],
};

const capabilities: ConstructionCapabilitiesResponse = {
  snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients',
  stageId: 'source_projection',
  sourceInput: { supported: false, stageId: 'source_projection', reasonCode: 'TEST_FIXTURE_NO_CHOICES', choices: [] },
  baseConstruction: { version: 1, steps: [] },
  stages: [],
  groupFieldChoices: [],
  groupRowValueChoices: [],
  workspaceInputs: [],
  selectedStage: {
    id: 'source_projection', inputStageId: '',
    columns: [{ id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string', cardinality: 'required_one' }],
    capabilities: [{ kind: 'RELATED_EXPAND', supported: true }],
  },
};

const fieldValue = (label: string): string => {
  const element = screen.getByLabelText(label);
  if (element instanceof HTMLInputElement || element instanceof HTMLSelectElement) return element.value;
  throw new Error(`Expected ${label} to label an input or select.`);
};

describe('RelatedExpandEditor', () => {
  it('continues past filtered-only pages until an eligible contributor is found', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({ snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'signed-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }] });
    searchRelatedExpandContributors.mockReset().mockImplementation(async (args) => 'cursor' in args
      ? contributorPage(args, [scalarContributor]) : contributorPage(args, [repeatedContributor], 'page-2'));
    openEncounterContributorCondition();
    expect(await screen.findByRole('button', { name: /Status/ })).toBeInTheDocument();
    expect(screen.queryByText('No supported fields match this search.')).not.toBeInTheDocument();
    expect(searchRelatedExpandContributors).toHaveBeenCalledTimes(2);
    expect(searchRelatedExpandContributors.mock.calls[1]?.[0]).toMatchObject({ cursor: 'page-2', query: '' });
  });

  it('shows empty only after exhausting pages with no eligible contributors', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({ snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'signed-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }] });
    searchRelatedExpandContributors.mockReset().mockImplementation(async (args) => contributorPage(args, [repeatedContributor], 'cursor' in args ? undefined : 'page-2'));
    openEncounterContributorCondition();
    expect(await screen.findByText('No supported fields match this search.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Repeated status/ })).not.toBeInTheDocument();
    expect(searchRelatedExpandContributors).toHaveBeenCalledTimes(2);
  });

  it('keeps a browse action visible when the automatic page bound is reached', async () => {
    searchRelatedExpandContributors.mockReset().mockImplementation(async (args) => {
      const cursor = 'cursor' in args ? Number(String(args.cursor).replace('page-', '')) : 0;
      return contributorPage(args, [{ ...repeatedContributor, choiceId: `repeated-${cursor}` }], `page-${cursor + 1}`);
    });
    openEncounterContributorCondition();
    expect(await screen.findByText('More supported fields are available. Load more to continue browsing.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Load more fields' })).toBeEnabled();
    expect(screen.queryByText('No supported fields match this search.')).not.toBeInTheDocument();
    expect(searchRelatedExpandContributors).toHaveBeenCalledTimes(3);
  });

  it('preserves a failed automatic continuation cursor for retry', async () => {
    searchRelatedExpandContributors.mockReset().mockImplementation(async (args) => {
      if (!('cursor' in args)) return contributorPage(args, [repeatedContributor], 'page-2');
      if (searchRelatedExpandContributors.mock.calls.length === 2) throw new Error('Temporary page failure');
      return contributorPage(args, [scalarContributor]);
    });
    openEncounterContributorCondition();
    expect(await screen.findByRole('alert')).toHaveTextContent('Temporary page failure');
    const retry = screen.getByRole('button', { name: 'Retry loading more fields' });
    expect(retry).toBeEnabled();
    fireEvent.click(retry);
    expect(await screen.findByRole('button', { name: /Status/ })).toBeInTheDocument();
    expect(searchRelatedExpandContributors.mock.calls.at(-1)?.[0]).toMatchObject({ cursor: 'page-2' });
  });

  it('preserves successfully browsed choices and resumes after a later page fails', async () => {
    let failThirdPage = true;
    searchRelatedExpandContributors.mockReset().mockImplementation(async (args) => {
      if (!('cursor' in args)) return contributorPage(args, [scalarContributor], 'page-2');
      if (args.cursor === 'page-2') return contributorPage(args, [{ ...scalarContributor, choiceId: 'page-two-choice', label: 'Page two status' }], 'page-3');
      if (args.cursor === 'page-3' && failThirdPage) {
        failThirdPage = false;
        throw new Error('Temporary page three failure');
      }
      return contributorPage(args, [{ ...scalarContributor, choiceId: 'page-three-choice', label: 'Page three status' }]);
    });
    openEncounterContributorCondition();
    expect(await screen.findByRole('button', { name: /Status/ })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Load more fields' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Temporary page three failure');
    expect(screen.getByRole('button', { name: /Page two status/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry loading more fields' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry loading more fields' }));
    expect(await screen.findByRole('button', { name: /Page three status/ })).toBeInTheDocument();
    expect(searchRelatedExpandContributors.mock.calls.at(-1)?.[0]).toMatchObject({ cursor: 'page-3' });
  });

  it('aborts continuation paging when the user switches back to all matching records', async () => {
    let resolveContinuation!: (response: RelatedExpandContributorSearchResponse) => void;
    searchRelatedExpandContributors.mockReset().mockImplementation((args) => 'cursor' in args
      ? new Promise((resolve) => { resolveContinuation = resolve; })
      : Promise.resolve(contributorPage(args, [scalarContributor], 'page-2')));
    openEncounterContributorCondition();
    expect(await screen.findByRole('button', { name: /Status/ })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Load more fields' }));
    await waitFor(() => expect(searchRelatedExpandContributors).toHaveBeenCalledTimes(2));
    const continuationSignal = searchRelatedExpandContributors.mock.calls[1]?.[1];
    fireEvent.click(screen.getByRole('radio', { name: 'All matching records' }));
    expect(continuationSignal?.aborted).toBe(true);
    await act(async () => resolveContinuation(contributorPage(
      { snapshotToken: 'snapshot-1', expectedDraftVersion: 1, expectedDraftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', routeChoiceId: 'signed-choice' },
      [{ ...scalarContributor, choiceId: 'page-two-choice', label: 'Page two status' }], 'page-3')));
    expect(searchRelatedExpandContributors).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole('button', { name: /Page two status/ })).not.toBeInTheDocument();
  });

  it('rejects a stale continuation response without showing its choices', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({ snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'signed-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }] });
    searchRelatedExpandContributors.mockReset().mockImplementation(async (args) => 'cursor' in args
      ? { ...contributorPage(args, [scalarContributor]), draftDigest: 'stale-draft' }
      : contributorPage(args, [repeatedContributor], 'page-2'));
    openEncounterContributorCondition();
    expect(await screen.findByRole('alert')).toHaveTextContent('Available conditions changed. Reload this table before expanding records.');
    expect(screen.queryByRole('button', { name: /Status/ })).not.toBeInTheDocument();
    expect(screen.getByLabelText('Find a field on Encounter')).toBeInTheDocument();
  });

  it('ignores an aborted empty-query response after the user starts a new search', async () => {
    let resolveOld!: (response: RelatedExpandContributorSearchResponse) => void;
    searchRelatedExpandContributors.mockReset().mockImplementation((args) => args.query
      ? Promise.resolve(contributorPage(args, [scalarContributor]))
      : new Promise((resolve) => { resolveOld = resolve; }));
    openEncounterContributorCondition();
    await waitFor(() => expect(searchRelatedExpandContributors).toHaveBeenCalledTimes(1));
    const oldSignal = searchRelatedExpandContributors.mock.calls[0]?.[1];
    fireEvent.change(screen.getByLabelText('Find a field on Encounter'), { target: { value: 'status' } });
    expect(await screen.findByRole('button', { name: /Status/ })).toBeInTheDocument();
    expect(oldSignal?.aborted).toBe(true);
    await act(async () => resolveOld(contributorPage({
      snapshotToken: 'snapshot-1', expectedDraftVersion: 1, expectedDraftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', routeChoiceId: 'signed-choice',
    }, [{ ...scalarContributor, choiceId: 'stale-choice', label: 'Stale status' }])));
    expect(screen.queryByRole('button', { name: /Stale status/ })).not.toBeInTheDocument();
  });

  it('stops repeated continuation cursors with an actionable error', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({ snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'signed-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }] });
    searchRelatedExpandContributors.mockReset().mockImplementation(async (args) => contributorPage(args, [repeatedContributor], 'page-2'));
    openEncounterContributorCondition();
    expect(await screen.findByRole('alert')).toHaveTextContent('The related-field catalog repeated a page cursor.');
    expect(screen.getByLabelText('Find a field on Encounter')).toBeInTheDocument();
    expect(searchRelatedExpandContributors).toHaveBeenCalledTimes(2);
  });

  it('automatically checks saved graph paths and one catalog page', async () => {
    searchRelatedExpandChoices.mockReset().mockImplementation(async (args: { savedGraphOnly?: boolean }) => ({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', complete: true, truncated: false, choices: [],
    }));
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={vi.fn()}
    />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    expect(await screen.findByText('No supported path reaches this record type from these rows.')).toBeInTheDocument();
    await waitFor(() => expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(2));
    expect(searchRelatedExpandChoices).toHaveBeenNthCalledWith(1,
      expect.objectContaining({ savedGraphOnly: true, limit: 12 }), expect.any(AbortSignal));
    expect(searchRelatedExpandChoices).toHaveBeenNthCalledWith(2,
      expect.not.objectContaining({ savedGraphOnly: true, cursor: expect.anything() }), expect.any(AbortSignal));
    expect(screen.queryByRole('button', { name: 'Explore other paths' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Load more paths' })).not.toBeInTheDocument();
  });

  it('does not offer a guessed root anchor when an active record lacks anchor metadata', () => {
    searchRelatedExpandChoices.mockReset();
    const unknown = {
      ...capabilities,
      selectedStage: {
        ...capabilities.selectedStage,
        activeRelatedRecord: {
          targetNodeId: 'encounter-node', targetResourceType: 'Encounter',
          terminalIdentityColumn: '__loom_encounter_id',
        },
      },
    } satisfies ConstructionCapabilitiesResponse;
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={unknown}
      disabled={false} onCandidateChange={vi.fn()}
    />);
    expect(screen.getByText(/has not confirmed a starting record/)).toBeInTheDocument();
    expect(screen.getByLabelText('Related record type')).toBeDisabled();
    expect(searchRelatedExpandChoices).not.toHaveBeenCalled();
  });

  it('automatically searches paths and retries a failed initial lookup', async () => {
    searchRelatedExpandChoices.mockReset()
      .mockRejectedValueOnce(new Error('Temporary route search failure'))
      .mockResolvedValue({
        snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
        outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key',
        complete: true, truncated: false,
        choices: [{ ...rootAnchor, choiceId: 'retry-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
      });
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={vi.fn()}
    />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    expect(await screen.findByRole('alert')).toHaveTextContent('Temporary route search failure');
    fireEvent.click(screen.getByRole('button', { name: 'Retry finding paths' }));
    expect(await screen.findByRole('radio', { name: 'Patient <-[subject]- Encounter' })).toBeInTheDocument();
    await waitFor(() => expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(3));
    expect(searchRelatedExpandChoices.mock.calls[0]?.[0]).toMatchObject({ savedGraphOnly: true });
    expect(searchRelatedExpandChoices.mock.calls[1]?.[0]).toMatchObject({ savedGraphOnly: true });
    expect(searchRelatedExpandChoices.mock.calls[2]?.[0]).not.toHaveProperty('savedGraphOnly');
  });

  it('omits the start-from choice when only one anchor exists and explains the operation on demand', () => {
    searchRelatedExpandChoices.mockReset();
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={vi.fn()}
    />);
    expect(screen.queryByLabelText('Start from')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'About expanding related records' }));
    const dialog = screen.getByRole('dialog', { name: 'How expanding related records works' });
    expect(dialog).toHaveTextContent('Each match becomes its own output row');
    expect(dialog).toHaveTextContent('Row identity becomes the parent row identity plus the matched record identity.');
  });

  it('rejects paths issued for a different draft', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 2, draftDigest: 'draft-2',
      outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key', complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'stale-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
    });
    const onCandidateChange = vi.fn();
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={onCandidateChange}
    />);

    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    expect(await screen.findByText('The available paths changed. Reload this table before expanding records.')).toBeInTheDocument();
    expect(screen.queryByRole('radio', { name: 'Patient <-[subject]- Encounter' })).not.toBeInTheDocument();
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);
  });

  it('rejects a route issued for a different starting record', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', complete: true, truncated: false,
      choices: [{ ...encounterAnchor, choiceId: 'wrong-anchor', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
    });
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={vi.fn()}
    />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    expect(await screen.findByText('The available paths changed. Reload this table before expanding records.')).toBeInTheDocument();
    expect(screen.queryByRole('radio', { name: 'Patient <-[subject]- Encounter' })).not.toBeInTheDocument();
  });

  it('uses the data-preserving no-match default and explains how matches become rows', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key',
      complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'signed-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
    });
    const onCandidateChange = vi.fn();
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={onCandidateChange}
    />);

    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    await waitFor(() => expect(searchRelatedExpandChoices).toHaveBeenCalledWith(
      expect.objectContaining({ stageId: 'source_projection', targetResourceType: 'Encounter' }),
      expect.any(AbortSignal),
    ));
    expect(screen.queryByLabelText('Start from')).not.toBeInTheDocument();
    fireEvent.click(await screen.findByRole('radio', { name: 'Patient <-[subject]- Encounter' }));
    expect(screen.getByRole('radio', { name: 'Patient <-[subject]- Encounter' })).toBeInTheDocument();

    const candidate = onCandidateChange.mock.lastCall?.[0];
    const step = candidate?.candidateConstruction.steps[0];
    expect(candidate).toBeDefined();
    expect(step?.inputs).toEqual([{ kind: 'SOURCE_PROJECTION' }]);
    expect(step?.outputs.map((column: { id: string }) => column.id)).toEqual(['patient-id', step.operation.relatedExpand.relatedRecordColumnId]);
    expect(step?.outputs.find((column: { id: string }) => column.id === step.operation.relatedExpand.relatedRecordColumnId))
      .toMatchObject({ nullable: true });
    expect(step?.operation).toMatchObject({
      kind: 'RELATED_EXPAND',
      relatedExpand: {
        anchorColumnId: '_key', choiceId: 'signed-choice', targetNodeId: 'encounter-node',
        targetResourceType: 'Encounter', route, emptyPolicy: 'PRESERVE_PARENT',
      },
    });
    expect(step?.outputs.map((column: { name: string }) => column.name)).toEqual(['patient_id', 'related_encounter_id']);
    expect((screen.getByLabelText('If a current row has no matches') as HTMLSelectElement).value)
      .toBe('PRESERVE_PARENT');
    fireEvent.change(screen.getByLabelText('If a current row has no matches'), { target: { value: 'EXCLUDE' } });
    const excludedCandidateStep = onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0];
    expect(excludedCandidateStep?.operation.relatedExpand.emptyPolicy).toBe('EXCLUDE');
    expect(excludedCandidateStep?.outputs.find((column: { id: string }) => column.id === excludedCandidateStep.operation.relatedExpand.relatedRecordColumnId))
      .toMatchObject({ nullable: false });
    fireEvent.change(screen.getByLabelText('If a current row has no matches'), { target: { value: 'ERROR' } });
    const errorCandidateStep = onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0];
    expect(errorCandidateStep?.outputs.find((column: { id: string }) => column.id === errorCandidateStep.operation.relatedExpand.relatedRecordColumnId))
      .toMatchObject({ nullable: false });

    onCandidateChange.mockClear();
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Observation' } });
    expect(onCandidateChange).toHaveBeenCalledExactlyOnceWith(undefined);
    expect(screen.queryByRole('radio')).not.toBeInTheDocument();
    await screen.findByText('The available paths changed. Reload this table before expanding records.');
  });

  it('keeps the no-match policy editable alongside a non-retryable proposal error', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key',
      complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'signed-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
    });
    const onCandidateChange = vi.fn();
    render(<>
      <RelatedExpandEditor
        project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
        catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
        disabled={false} onCandidateChange={onCandidateChange}
      />
      <ConstructionProposalPanel
        state={{ status: 'error', message: 'At least one current row has no match.', retryable: false }}
        canApply={false} onApply={() => undefined} onCancel={() => undefined} onRetry={() => undefined}
      />
    </>);

    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    fireEvent.click(await screen.findByRole('radio', { name: 'Patient <-[subject]- Encounter' }));
    const emptyPolicy = screen.getByLabelText('If a current row has no matches') as HTMLSelectElement;
    expect(emptyPolicy).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'Retry preview' })).not.toBeInTheDocument();

    fireEvent.change(emptyPolicy, { target: { value: 'EXCLUDE' } });
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand.emptyPolicy)
      .toBe('EXCLUDE');
  });

  it('keeps distinct supported routes visible and waits for an explicit route choice', async () => {
    const alternateRoute = [{ ...route[0], edgeId: 'patient-encounter-alt', relationship: 'patient_Encounter' }];
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key',
      complete: true, truncated: false,
      choices: [
        { ...rootAnchor, choiceId: 'subject-route', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route },
        { ...rootAnchor, choiceId: 'patient-route', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route: alternateRoute },
      ],
    });
    const onCandidateChange = vi.fn();
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={onCandidateChange}
    />);

    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    const subjectRoute = await screen.findByRole('radio', { name: 'Patient <-[subject]- Encounter' });
    const patientRoute = screen.getByRole('radio', { name: 'Patient <-[patient]- Encounter' });
    expect(subjectRoute).toBeInTheDocument();
    expect(patientRoute).toBeInTheDocument();
    expect((subjectRoute as HTMLInputElement).checked).toBe(false);
    expect((patientRoute as HTMLInputElement).checked).toBe(false);
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);

    fireEvent.click(patientRoute);
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand)
      .toMatchObject({ choiceId: 'patient-route', route: alternateRoute, emptyPolicy: 'PRESERVE_PARENT' });
    expect(screen.getByTestId('construction-related-expand-other-routes')).not.toHaveAttribute('open');
    expect(screen.getByRole('radio', { name: 'Patient <-[patient]- Encounter' })).toBeInTheDocument();
    fireEvent.click(screen.getByText('Other relationship paths (1)'));
    expect(screen.getByTestId('construction-related-expand-other-routes')).toHaveAttribute('open');
    await waitFor(() => expect(screen.getByRole('radio', { name: 'Patient <-[subject]- Encounter' })).toBeInTheDocument());
  });

  it('mounts alternate route controls only when their disclosure is open and retains the selected route', async () => {
    const longRoutes = Array.from({ length: 64 }, (_, index) => ({
      ...rootAnchor,
      choiceId: `alternate-route-${index}`,
      targetNodeId: 'encounter-node',
      targetResourceType: 'Encounter',
      route: [
        ...route,
        {
          ...route[0],
          edgeId: `encounter-linked-${index}`,
          fromNodeId: 'encounter-node',
          toNodeId: 'encounter-node',
          fromResourceType: 'Encounter',
          toResourceType: 'Encounter',
          relationship: `linked_${index}`,
          storageDirection: 'OUTBOUND' as const,
        },
      ],
    }));
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key',
      complete: true, truncated: false,
      choices: [
        { ...rootAnchor, choiceId: 'shortest-route', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route },
        ...longRoutes,
      ],
    });
    const onCandidateChange = vi.fn();
    const { container } = render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={onCandidateChange}
    />);

    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    const selectedRoute = await screen.findByRole('radio', { name: 'Patient <-[subject]- Encounter' });
    const editor = container.querySelector('[data-testid="construction-related-expand-editor"]');
    const otherRoutes = screen.getByTestId('construction-related-expand-other-routes');
    expect(editor?.querySelectorAll('input[type="radio"]')).toHaveLength(1);
    expect(otherRoutes.querySelectorAll('input[type="radio"]')).toHaveLength(0);

    fireEvent.click(selectedRoute);
    expect((selectedRoute as HTMLInputElement).checked).toBe(true);
    expect(otherRoutes.querySelectorAll('input[type="radio"]')).toHaveLength(0);
    const selectedChoiceId = onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand.choiceId;
    expect(selectedChoiceId).toBe('shortest-route');

    fireEvent.click(screen.getByText('Other relationship paths (64)'));
    await waitFor(() => expect(otherRoutes.querySelectorAll('input[type="radio"]')).toHaveLength(64));
    expect((screen.getByRole('radio', { name: 'Patient <-[subject]- Encounter' }) as HTMLInputElement).checked).toBe(true);
    expect(screen.getByRole('radio', { name: 'Patient <-[subject]- Encounter -[linked_0]-> Encounter' }))
      .toBeInTheDocument();
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand.choiceId)
      .toBe(selectedChoiceId);
  });

  it('authors a route-bound scalar contributor condition and can edit its exact value', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'signed-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
    });
    searchRelatedExpandContributors.mockImplementation(async (args) => ({
      snapshotToken: args.snapshotToken, draftVersion: args.expectedDraftVersion, draftDigest: args.expectedDraftDigest,
      outputId: args.outputId, stageId: args.stageId, routeChoiceId: args.routeChoiceId,
      complete: true, truncated: false,
      choices: [{ choiceId: 'signed-status-choice', label: 'Status',
        source: { kind: 'FIELD', candidateId: 'encounter-status', nodeId: 'encounter-node',
          resourceType: 'Encounter', path: 'Encounter.status', cardinality: 'optional_one', logicalType: 'string' },
        operators: ['EXISTS', 'EQUALS'], suggestedValues: ['finished'], suggestionsComplete: true,
        suggestionsTruncated: false,
        suggestionsSource: 'catalog' }],
    }));
    const onCandidateChange = vi.fn();
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={onCandidateChange}
    />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    fireEvent.click(await screen.findByRole('radio', { name: 'Patient <-[subject]- Encounter' }));
    fireEvent.change(screen.getByLabelText('If a current row has no matches'), { target: { value: 'EXCLUDE' } });
    fireEvent.click(screen.getByRole('radio', { name: 'Only records meeting a condition' }));
    await waitFor(() => expect(searchRelatedExpandContributors).toHaveBeenCalledWith(
      expect.objectContaining({ stageId: 'source_projection', routeChoiceId: 'signed-choice' }), expect.any(AbortSignal),
    ));
    fireEvent.click(await screen.findByRole('button', { name: /Status/ }));
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand)
      .toMatchObject({ contributorRule: { predicate: { candidateId: 'encounter-status', operator: 'EXISTS' } },
        contributorChoiceId: 'signed-status-choice', contributorSource: { path: 'Encounter.status' } });

    fireEvent.change(screen.getByLabelText('Condition'), { target: { value: 'EQUALS' } });
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);
    fireEvent.change(screen.getByLabelText('Exact value'), { target: { value: 'finished' } });
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand.contributorRule)
      .toEqual({ policy: 'ALL_MATCHES', predicate: { candidateId: 'encounter-status', operator: 'EQUALS',
        value: { kind: 'STRING', string: 'finished' } } });
    fireEvent.click(screen.getByRole('radio', { name: 'All matching records' }));
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand)
      .toMatchObject({ contributorRule: { policy: 'ALL_MATCHES' } });
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand.contributorSource)
      .toBeUndefined();
  });

  it('uses a field-neutral empty message for related-contributor searches', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'signed-choice', targetNodeId: 'encounter-node',
        targetResourceType: 'Encounter', route }],
    });
    searchRelatedExpandContributors.mockReset().mockImplementation(async (args) => ({
      snapshotToken: args.snapshotToken, draftVersion: args.expectedDraftVersion, draftDigest: args.expectedDraftDigest,
      outputId: args.outputId, stageId: args.stageId, routeChoiceId: args.routeChoiceId,
      complete: true, truncated: false, choices: [],
    }));
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={vi.fn()}
    />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    fireEvent.click(await screen.findByRole('radio', { name: 'Patient <-[subject]- Encounter' }));
    fireEvent.click(screen.getByRole('radio', { name: 'Only records meeting a condition' }));
    expect(await screen.findByText('No supported fields match this search.')).toBeInTheDocument();
    expect(screen.queryByText('No supported scalar fields match this search.')).not.toBeInTheDocument();
  });

  it('retries an initial related-field lookup without changing the selected condition', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'signed-choice', targetNodeId: 'encounter-node',
        targetResourceType: 'Encounter', route }],
    });
    searchRelatedExpandContributors.mockReset()
      .mockRejectedValueOnce(new Error('Temporary contributor lookup failure'))
      .mockImplementation(async (args) => ({
        snapshotToken: args.snapshotToken, draftVersion: args.expectedDraftVersion, draftDigest: args.expectedDraftDigest,
        outputId: args.outputId, stageId: args.stageId, routeChoiceId: args.routeChoiceId,
        complete: true, truncated: false,
        choices: [{ choiceId: 'signed-status-choice', label: 'Status',
          source: { kind: 'FIELD', candidateId: 'encounter-status', nodeId: 'encounter-node',
            resourceType: 'Encounter', path: 'Encounter.status', cardinality: 'optional_one', logicalType: 'string' },
          operators: ['EXISTS', 'EQUALS'], suggestedValues: [], suggestionsComplete: true,
          suggestionsTruncated: false, suggestionsSource: 'catalog' }],
      }));
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={vi.fn()}
    />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    fireEvent.click(await screen.findByRole('radio', { name: 'Patient <-[subject]- Encounter' }));
    fireEvent.click(screen.getByRole('radio', { name: 'Only records meeting a condition' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Temporary contributor lookup failure');
    fireEvent.click(screen.getByRole('button', { name: 'Retry field search' }));
    expect(await screen.findByRole('button', { name: /Status/ })).toBeInTheDocument();
    expect(searchRelatedExpandContributors).toHaveBeenCalledTimes(2);
  });

  it('shows a field-list load failure and preserves schema diagnostics under technical details', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'signed-choice', targetNodeId: 'encounter-node',
        targetResourceType: 'Encounter', route }],
    });
    const malformedResponse = relatedExpandContributorSearchResponseSchema.safeParse({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', routeChoiceId: 'signed-choice',
      complete: true, truncated: false,
      choices: [{
        choiceId: 'signed-status-choice', label: 'Status',
        source: { kind: 'FIELD', candidateId: 'encounter-status', nodeId: 'encounter-node',
          resourceType: 'Encounter', path: 'Encounter.status', cardinality: 'many', logicalType: 'string' },
        operators: ['EXISTS'], suggestedValues: [], suggestionsComplete: true,
        suggestionsTruncated: false, suggestionsSource: 'catalog',
      }],
    });
    if (malformedResponse.success) throw new Error('Expected a repeated source without boundaries to fail validation.');
    searchRelatedExpandContributors.mockReset().mockRejectedValue(malformedResponse.error);
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={vi.fn()}
    />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    fireEvent.click(await screen.findByRole('radio', { name: 'Patient <-[subject]- Encounter' }));
    fireEvent.click(screen.getByRole('radio', { name: 'Only records meeting a condition' }));

    const alert = await screen.findByRole('alert');
    expect(alert.querySelector('p')).toHaveTextContent(
      'Could not load the related-record field list because the response did not match the expected format.',
    );
    const technicalDetails = alert.querySelector('details');
    expect(technicalDetails).not.toHaveAttribute('open');
    expect(technicalDetails).toHaveTextContent('Repeated related fields must include repeated-boundary metadata.');
    expect(technicalDetails).toHaveTextContent('repeatedBoundaries');
    expect(screen.queryByText('No supported fields match this search.')).not.toBeInTheDocument();
  });

  it('authors repeated contributor predicates with ANY and keeps the signed source boundaries', async () => {
    const observationRoute = [{
      ...route[0], toNodeId: 'observation-node', toResourceType: 'Observation', relationship: 'subject_Patient',
    }];
    const repeatedSource = {
      kind: 'FIELD' as const,
      candidateId: 'observation-category-code',
      nodeId: 'observation-node',
      resourceType: 'Observation',
      path: 'category[].coding[].code',
      cardinality: 'many' as const,
      logicalType: 'code',
      repeatedBoundaries: [
        { path: 'category[]', maxItems: 8 },
        { path: 'category[].coding[]', maxItems: 8 },
      ],
    };
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'observation-route', targetNodeId: 'observation-node',
        targetResourceType: 'Observation', route: observationRoute }],
    });
    searchRelatedExpandContributors.mockReset().mockImplementation(async (args) => ({
      snapshotToken: args.snapshotToken, draftVersion: args.expectedDraftVersion, draftDigest: args.expectedDraftDigest,
      outputId: args.outputId, stageId: args.stageId, routeChoiceId: args.routeChoiceId,
      complete: true, truncated: false,
      choices: [{ choiceId: 'signed-category-code-choice', label: 'Category coding code',
        source: repeatedSource, operators: ['EXISTS', 'EQUALS'], suggestedValues: ['x'],
        suggestionsComplete: true, suggestionsTruncated: false, suggestionsSource: 'catalog' }],
    }));
    const onCandidateChange = vi.fn();
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={onCandidateChange}
    />);

    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Observation' } });
    fireEvent.click(await screen.findByRole('radio', { name: 'Patient <-[subject]- Observation' }));
    fireEvent.click(screen.getByRole('radio', { name: 'Only records meeting a condition' }));
    await waitFor(() => expect(searchRelatedExpandContributors).toHaveBeenCalledWith(
      expect.objectContaining({ routeChoiceId: 'observation-route' }), expect.any(AbortSignal),
    ));
    const repeatedField = await screen.findByRole('button', { name: /Category coding code.*Repeated values \(any item can match\)/ });
    expect(repeatedField).toBeInTheDocument();
    fireEvent.click(repeatedField);
    expect(screen.queryByLabelText(/quantifier/i)).not.toBeInTheDocument();
    expect(screen.getByRole('note')).toHaveTextContent('matches when any value in the repeated field is present.');
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand)
      .toMatchObject({
        contributorRule: { policy: 'ALL_MATCHES', predicate: {
          candidateId: 'observation-category-code', quantifier: 'ANY', operator: 'EXISTS',
        } },
        contributorChoiceId: 'signed-category-code-choice',
        contributorSource: repeatedSource,
      });

    fireEvent.change(screen.getByLabelText('Condition'), { target: { value: 'EQUALS' } });
    fireEvent.change(screen.getByLabelText('Code'), { target: { value: 'x' } });
    expect(screen.getByRole('note')).toHaveTextContent('matches when any value in the repeated field equals the exact code below; matching uses the code alone.');
    const related = onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand;
    expect(related?.contributorRule).toEqual({
      policy: 'ALL_MATCHES',
      predicate: { candidateId: 'observation-category-code', quantifier: 'ANY', operator: 'EQUALS',
        value: { kind: 'CODE', code: { code: 'x' } } },
    });
    expect(related?.contributorSource).toEqual(repeatedSource);
    expect(related?.contributorChoiceId).toBe('signed-category-code-choice');
  });

  it('restores a saved repeated ANY condition and keeps fresh source metadata through edits', async () => {
    const observationRoute = [{
      ...route[0], toNodeId: 'observation-node', toResourceType: 'Observation', relationship: 'subject_Patient',
    }];
    const repeatedSource = {
      kind: 'FIELD' as const,
      candidateId: 'observation-category-code',
      nodeId: 'observation-node',
      resourceType: 'Observation',
      path: 'category[].coding[].code',
      cardinality: 'many' as const,
      logicalType: 'code',
      repeatedBoundaries: [
        { path: 'category[]', maxItems: 8 },
        { path: 'category[].coding[]', maxItems: 8 },
      ],
    };
    const freshChoice = {
      choiceId: 'signed-category-code-choice',
      source: repeatedSource,
      label: 'Category coding code (fresh route choice)',
      operators: ['EXISTS', 'EQUALS'],
      suggestedValues: ['x'],
      suggestionsComplete: true,
      suggestionsTruncated: false,
      suggestionsSource: 'catalog',
    } satisfies RelatedExpandContributorSearchResponse['choices'][number];
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'observation-route', targetNodeId: 'observation-node',
        targetResourceType: 'Observation', route: observationRoute }],
    });
    searchRelatedExpandContributors.mockReset().mockImplementation(async (args) => ({
      snapshotToken: args.snapshotToken, draftVersion: args.expectedDraftVersion, draftDigest: args.expectedDraftDigest,
      outputId: args.outputId, stageId: args.stageId, routeChoiceId: args.routeChoiceId,
      complete: true, truncated: false, choices: [freshChoice],
    }));
    const saved = {
      id: 'expand-observations',
      inputs: [{ kind: 'SOURCE_PROJECTION' as const }],
      operation: {
        kind: 'RELATED_EXPAND' as const,
        relatedExpand: {
          anchorColumnId: '_key',
          choiceId: 'observation-route',
          targetNodeId: 'observation-node',
          targetResourceType: 'Observation',
          route: observationRoute,
          contributorRule: { policy: 'ALL_MATCHES' as const, predicate: {
            candidateId: 'observation-category-code', quantifier: 'ANY' as const, operator: 'EQUALS' as const,
            value: { kind: 'CODE' as const, code: { code: 'x' } },
          } },
          contributorSource: repeatedSource,
          contributorChoiceId: 'signed-category-code-choice',
          emptyPolicy: 'PRESERVE_PARENT' as const,
          relatedRecordColumnId: 'observation-id',
        },
      },
      outputs: [
        { id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string' },
        { id: 'observation-id', name: 'observation_id', label: 'Observation ID', type: 'string' },
      ],
    };
    const onCandidateChange = vi.fn();
    const props = {
      project: 'project', explorerId: 'explorer', snapshotToken: 'snapshot-1', outputId: 'patients',
      catalog, construction: { version: 1, steps: [saved] }, capabilities, step: saved,
      disabled: false, onCandidateChange,
    };

    const view = render(<RelatedExpandEditor {...props} />);
    fireEvent.click(await screen.findByRole('button', { name: /Category coding code \(fresh route choice\)/ }));
    fireEvent.change(screen.getByLabelText('Condition'), { target: { value: 'EQUALS' } });
    fireEvent.change(screen.getByLabelText('Code'), { target: { value: 'x' } });
    expect(fieldValue('Condition')).toBe('EQUALS');
    expect(fieldValue('Code')).toBe('x');
    fireEvent.change(screen.getByLabelText('If a current row has no matches'), { target: { value: 'EXCLUDE' } });
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand)
      .toMatchObject({
        contributorChoiceId: 'signed-category-code-choice',
        contributorSource: repeatedSource,
        contributorRule: { predicate: {
          candidateId: 'observation-category-code', quantifier: 'ANY', operator: 'EQUALS',
          value: { kind: 'CODE', code: { code: 'x' } },
        } },
        emptyPolicy: 'EXCLUDE',
      });

    const edited = onCandidateChange.mock.lastCall?.[0]?.candidateConstruction;
    const editedStep = edited?.steps[0];
    expect(editedStep?.operation.kind).toBe('RELATED_EXPAND');
    if (!edited || !editedStep || editedStep.operation.kind !== 'RELATED_EXPAND') {
      throw new Error('Editing a repeated contributor did not produce a Related Expand step.');
    }
    view.unmount();
    const restoredChange = vi.fn();
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={edited} capabilities={capabilities} step={editedStep}
      disabled={false} onCandidateChange={restoredChange}
    />);
    await screen.findByLabelText('Code');
    expect(fieldValue('Code')).toBe('x');
    expect(fieldValue('Condition')).toBe('EQUALS');
  });

  it('edits the saved step without replacing its identity or contributor condition', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key',
      complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'signed-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
    });
    const saved = {
      id: 'expand-encounters',
      inputs: [{ kind: 'SOURCE_PROJECTION' as const }],
      operation: {
        kind: 'RELATED_EXPAND' as const,
        relatedExpand: {
          anchorColumnId: '_key', choiceId: 'signed-choice', targetNodeId: 'encounter-node',
          targetResourceType: 'Encounter', route,
          contributorRule: { policy: 'ALL_MATCHES' as const, predicate: {
            candidateId: 'encounter-status', operator: 'EQUALS' as const,
            value: { kind: 'STRING' as const, string: 'finished' },
          } },
          contributorSource: {
            kind: 'FIELD' as const, candidateId: 'encounter-status', nodeId: 'encounter-node',
            resourceType: 'Encounter', path: 'Encounter.status', cardinality: 'optional_one' as const,
            logicalType: 'string',
          },
          contributorChoiceId: 'signed-field-choice',
          emptyPolicy: 'EXCLUDE' as const,
          relatedRecordColumnId: 'encounter-id',
        },
      },
      outputs: [
        { id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string' },
        { id: 'encounter-id', name: 'encounter_id', label: 'Encounter ID', type: 'string' },
      ],
    };
    const onCandidateChange = vi.fn();
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={{ version: 1, steps: [saved] }} capabilities={capabilities}
      step={saved} disabled={false} onCandidateChange={onCandidateChange}
    />);
    expect((screen.getByLabelText('If a current row has no matches') as HTMLSelectElement).value)
      .toBe('EXCLUDE');
    fireEvent.change(screen.getByLabelText('If a current row has no matches'), { target: { value: 'PRESERVE_PARENT' } });
    const candidate = onCandidateChange.mock.lastCall?.[0];
    expect(candidate.changedStepId).toBe('expand-encounters');
    expect(candidate.candidateConstruction.steps).toHaveLength(1);
    expect(candidate.candidateConstruction.steps[0]).toMatchObject({
      id: 'expand-encounters',
      operation: { relatedExpand: {
        relatedRecordColumnId: 'encounter-id',
        contributorChoiceId: 'signed-field-choice',
        contributorRule: { predicate: { candidateId: 'encounter-status', value: { string: 'finished' } } },
      } },
      outputs: [
        { id: 'patient-id', name: 'patient_id' },
        { id: 'encounter-id', name: 'encounter_id', label: 'Encounter ID' },
      ],
    });
  });

  it('updates a saved related-record output nullability when its no-match policy changes', async () => {
    const observationRoute = [{
      ...route[0], edgeId: 'patient-observation', toNodeId: 'observation-node', toResourceType: 'Observation',
    }];
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key', complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'observation-route', targetNodeId: 'observation-node',
        targetResourceType: 'Observation', route: observationRoute }],
    });
    const saved = {
      id: 'expand-observations',
      inputs: [{ kind: 'SOURCE_PROJECTION' as const }],
      operation: {
        kind: 'RELATED_EXPAND' as const,
        relatedExpand: {
          anchorColumnId: '_key',
          choiceId: 'observation-route',
          targetNodeId: 'observation-node',
          targetResourceType: 'Observation',
          route: observationRoute,
          contributorRule: { policy: 'ALL_MATCHES' as const },
          emptyPolicy: 'PRESERVE_PARENT' as const,
          relatedRecordColumnId: 'observation-id',
        },
      },
      outputs: [
        { id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string', nullable: false },
        { id: 'observation-id', name: 'observation_id', label: 'Observation ID', type: 'string', nullable: true },
      ],
    };
    const onCandidateChange = vi.fn();
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={{ version: 1, steps: [saved] }} capabilities={capabilities}
      step={saved} disabled={false} onCandidateChange={onCandidateChange}
    />);

    await screen.findByRole('radio', { name: 'Patient <-[subject]- Observation' });
    expect((screen.getByLabelText('If a current row has no matches') as HTMLSelectElement).value)
      .toBe('PRESERVE_PARENT');
    fireEvent.change(screen.getByLabelText('If a current row has no matches'), { target: { value: 'EXCLUDE' } });

    const excludedCandidate = onCandidateChange.mock.lastCall?.[0];
    expect(excludedCandidate).toBeDefined();
    if (!excludedCandidate) throw new Error('Changing the saved no-match policy did not produce a candidate.');
    expect(excludedCandidate.changedStepId).toBe('expand-observations');
    expect(excludedCandidate.candidateConstruction.steps).toHaveLength(1);
    const excludedStep = excludedCandidate.candidateConstruction.steps[0];
    expect(excludedStep).toMatchObject({
      id: 'expand-observations',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'RELATED_EXPAND',
        relatedExpand: {
          anchorColumnId: '_key',
          choiceId: 'observation-route',
          targetNodeId: 'observation-node',
          targetResourceType: 'Observation',
          route: observationRoute,
          emptyPolicy: 'EXCLUDE',
          relatedRecordColumnId: 'observation-id',
        },
      },
      outputs: [
        { id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string', nullable: false },
        { id: 'observation-id', name: 'observation_id', label: 'Observation ID', type: 'string', nullable: false },
      ],
    });

    fireEvent.change(screen.getByLabelText('If a current row has no matches'), { target: { value: 'PRESERVE_PARENT' } });
    const preservedCandidate = onCandidateChange.mock.lastCall?.[0];
    expect(preservedCandidate).toBeDefined();
    if (!preservedCandidate) throw new Error('Restoring the saved no-match policy did not produce a candidate.');
    expect(preservedCandidate.changedStepId).toBe('expand-observations');
    expect(preservedCandidate.candidateConstruction.steps).toHaveLength(1);
    const preservedStep = preservedCandidate.candidateConstruction.steps[0];
    expect(preservedStep).toMatchObject({
      id: 'expand-observations',
      inputs: [{ kind: 'SOURCE_PROJECTION' }],
      operation: {
        kind: 'RELATED_EXPAND',
        relatedExpand: {
          anchorColumnId: '_key',
          choiceId: 'observation-route',
          targetNodeId: 'observation-node',
          targetResourceType: 'Observation',
          route: observationRoute,
          emptyPolicy: 'PRESERVE_PARENT',
          relatedRecordColumnId: 'observation-id',
        },
      },
      outputs: [
        { id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string', nullable: false },
        { id: 'observation-id', name: 'observation_id', label: 'Observation ID', type: 'string', nullable: true },
      ],
    });
    expect(preservedStep.id).toBe(excludedStep.id);
  });

  it('keeps route discovery source-scoped and does not restart it when Apply disables editing', async () => {
    let resolveRoute: ((value: unknown) => void) | undefined;
    searchRelatedExpandChoices.mockReset().mockImplementation(() => new Promise((resolve) => { resolveRoute = resolve; }));
    const props = {
      project: 'project', explorerId: 'explorer', snapshotToken: 'snapshot-1', outputId: 'patients',
      catalog, construction: capabilities.baseConstruction, capabilities, onCandidateChange: vi.fn(),
    };
    const view = render(<RelatedExpandEditor {...props} disabled={false} />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    await waitFor(() => expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId('construction-related-expand-editor')).toHaveAttribute('data-related-stage-id', 'source_projection');
    expect(screen.getByTestId('construction-related-expand-editor')).toHaveAttribute('data-related-output-id', 'patients');
    view.rerender(<RelatedExpandEditor {...props} disabled />);
    expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(1);
    await act(async () => resolveRoute?.({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients',
      stageId: 'source_projection', anchorColumnId: '_key', complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'while-disabled-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
    }));
    expect(await screen.findByRole('radio', { name: 'Patient <-[subject]- Encounter' })).toBeDisabled();
    expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(2);
    expect(searchRelatedExpandChoices.mock.calls[1]?.[0]).not.toHaveProperty('savedGraphOnly');
    view.unmount();
  });

  it('retires stale catalog requests when the pinned draft changes', async () => {
    const pending: Array<{
      readonly args: { readonly expectedDraftVersion: number; readonly expectedDraftDigest: string; readonly requestId: string; readonly savedGraphOnly?: boolean };
      readonly resolve: (value: unknown) => void;
    }> = [];
    searchRelatedExpandChoices.mockReset().mockImplementation((args, signal) => {
      if (args.savedGraphOnly) return Promise.resolve({
        snapshotToken: 'snapshot-1', draftVersion: args.expectedDraftVersion, draftDigest: args.expectedDraftDigest,
        outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key', complete: true, truncated: false, choices: [],
      });
      return new Promise((resolve, reject) => {
        pending.push({ args, resolve });
        signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true });
      });
    });
    const onCandidateChange = vi.fn();
    const queryOwnerRef = React.createRef<RelatedExpandQueryOwner>();
    const props = {
      project: 'project', explorerId: 'explorer', snapshotToken: 'snapshot-1', outputId: 'patients',
      catalog, construction: capabilities.baseConstruction, capabilities, disabled: false, queryOwnerRef, onCandidateChange,
    };
    const view = render(<RelatedExpandEditor {...props} />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    await waitFor(() => expect(pending).toHaveLength(1));
    const retiredOwner = queryOwnerRef.current;

    const nextCapabilities = { ...capabilities, draftVersion: 2, draftDigest: 'draft-2' };
    view.rerender(<RelatedExpandEditor {...props} capabilities={nextCapabilities} />);
    await waitFor(() => expect(pending).toHaveLength(2));
    expect(queryOwnerRef.current).not.toBe(retiredOwner);
    expect(pending.map(({ args }) => args.expectedDraftVersion)).toEqual([1, 2]);
    expect(pending.map(({ args }) => args.savedGraphOnly)).toEqual([undefined, undefined]);
    expect(pending[0]?.args.requestId).not.toBe(pending[1]?.args.requestId);

    await act(async () => pending[0]?.resolve({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients',
      stageId: 'source_projection', anchorColumnId: '_key', complete: false, truncated: true, nextCursor: 'stale-next-page',
      choices: [{ ...rootAnchor, choiceId: 'stale-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
    }));
    expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(4);
    expect(screen.queryByRole('radio', { name: 'Patient <-[subject]- Encounter' })).not.toBeInTheDocument();

    await act(async () => pending[1]?.resolve({
      snapshotToken: 'snapshot-1', draftVersion: 2, draftDigest: 'draft-2', outputId: 'patients',
      stageId: 'source_projection', anchorColumnId: '_key', complete: false, truncated: true, nextCursor: 'current-next-page',
      choices: [{ ...rootAnchor, choiceId: 'current-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
    }));
    expect(await screen.findByRole('radio', { name: 'Patient <-[subject]- Encounter' })).toBeInTheDocument();
    expect(screen.getByTestId('construction-related-route-truncation')).toBeInTheDocument();
    expect(searchRelatedExpandChoices).not.toHaveBeenCalledWith(expect.objectContaining({ cursor: 'current-next-page' }), expect.anything());
    fireEvent.click(screen.getByRole('radio', { name: 'Patient <-[subject]- Encounter' }));
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand.choiceId)
      .toBe('current-choice');
  });

  it('automatically loads one bounded catalog page after checking saved paths', async () => {
    const foundChoices = Array.from({ length: 25 }, (_, index) => ({
      ...rootAnchor, choiceId: `bounded-choice-${index}`, targetNodeId: 'encounter-node',
      targetResourceType: 'Encounter',
      route: [{ ...route[0], edgeId: `patient-encounter-${index}`, relationship: `subject_${index}` }],
    }));
    searchRelatedExpandChoices.mockReset().mockImplementation(async (args: { cursor?: string; savedGraphOnly?: boolean }) => {
      if (args.savedGraphOnly) return {
        snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients',
        stageId: 'source_projection', anchorColumnId: '_key', complete: true, truncated: false, choices: [],
      };
      const offset = Number(args.cursor ?? 0);
      const choices = foundChoices.slice(offset, offset + 12);
      return {
        snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients',
        stageId: 'source_projection', anchorColumnId: '_key', complete: false, truncated: false,
        nextCursor: String(offset + choices.length), choices,
      };
    });
    const onCandidateChange = vi.fn();
    render(<RelatedExpandEditor project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={onCandidateChange} />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    expect(await screen.findByRole('radio', { name: 'Patient <-[subject_0]- Encounter' })).toBeInTheDocument();
    await waitFor(() => expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(2));
    expect(searchRelatedExpandChoices.mock.calls[0]?.[0]).toMatchObject({ savedGraphOnly: true, limit: 12 });
    expect(searchRelatedExpandChoices.mock.calls[1]?.[0]).toMatchObject({ limit: 12 });
    expect(searchRelatedExpandChoices.mock.calls[1]?.[0]).not.toHaveProperty('savedGraphOnly');
    expect(searchRelatedExpandChoices.mock.calls[1]?.[0]).not.toHaveProperty('cursor');
    expect(screen.getByTestId('construction-related-route-truncation')).toHaveTextContent('Showing an initial set of supported paths.');
    expect(screen.getAllByRole('radio')).toHaveLength(3);
    expect(screen.queryByRole('button', { name: 'Load more paths' })).not.toBeInTheDocument();
    fireEvent.click(screen.getAllByRole('radio')[0]!);
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand.choiceId)
      .toBe('bounded-choice-0');
  });

  it('shows a bounded-search notice when no path appears in the initial catalog page', async () => {
    searchRelatedExpandChoices.mockReset().mockImplementation(async (args: { savedGraphOnly?: boolean }) => ({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients',
      stageId: 'source_projection', anchorColumnId: '_key',
      complete: true, truncated: !args.savedGraphOnly, choices: [],
    }));
    render(<RelatedExpandEditor project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={vi.fn()} />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    expect(await screen.findByTestId('construction-related-route-truncation')).toHaveTextContent('Showing an initial set of supported paths.');
    expect(screen.queryByText('No supported path reaches this record type from these rows.')).not.toBeInTheDocument();
    expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(2);
  });

  it('rejects an incomplete route page without a cursor instead of repeating the first page', async () => {
    searchRelatedExpandChoices.mockReset().mockImplementation(async (args: { savedGraphOnly?: boolean }) => ({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key',
      complete: false, truncated: false,
      choices: args.savedGraphOnly ? [] : [{ ...rootAnchor, choiceId: 'partial-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
      ...(args.savedGraphOnly ? { complete: true, truncated: false } : {}),
    }));
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={vi.fn()}
    />);

    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Could not finish loading relationship paths. Reopen this editor to retry.',
    );
    expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(2);
    expect(searchRelatedExpandChoices).toHaveBeenCalledWith(
      expect.not.objectContaining({ cursor: expect.anything() }),
      expect.any(AbortSignal),
    );
  });

  it('drains the current request and does not fetch another page after Apply failure', async () => {
    const pending: Array<{
      readonly args: { readonly cursor?: string };
      readonly resolve: (value: unknown) => void;
    }> = [];
    searchRelatedExpandChoices.mockReset().mockImplementation((args, signal) => {
      if (args.savedGraphOnly) return Promise.resolve({ snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key', complete: true, truncated: false, choices: [] });
      return new Promise((resolve, reject) => {
      pending.push({ args, resolve });
      signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true });
      });
    });
    const queryOwnerRef = React.createRef<RelatedExpandQueryOwner>();
    const onCandidateChange = vi.fn();
    const props = {
      project: 'project', explorerId: 'explorer', snapshotToken: 'snapshot-1', outputId: 'patients',
      catalog, construction: capabilities.baseConstruction, capabilities, queryOwnerRef, onCandidateChange,
    };
    const view = render(<RelatedExpandEditor {...props} disabled={false} />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    await waitFor(() => expect(pending).toHaveLength(1));

    const mutationOrder: string[] = [];
    let failMutation: (() => void) | undefined;
    const mutationSettlement = new Promise<void>((resolve) => { failMutation = resolve; });
    const mutation = (async () => {
      await queryOwnerRef.current?.pauseAndDrain();
      mutationOrder.push('command-started');
      await mutationSettlement;
      mutationOrder.push('command-failed');
      queryOwnerRef.current?.resume();
    })();
    expect(mutationOrder).toEqual([]);

    view.rerender(<RelatedExpandEditor {...props} disabled />);
    await act(async () => pending[0]?.resolve({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients',
      stageId: 'source_projection', anchorColumnId: '_key', complete: false, truncated: true, nextCursor: 'saved-route-cursor',
      choices: [{ ...rootAnchor, choiceId: 'first-page-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
    }));
    await waitFor(() => expect(mutationOrder).toEqual(['command-started']));
    expect(screen.getByRole('radio', { name: 'Patient <-[subject]- Encounter' })).toBeDisabled();
    expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(2);

    await act(async () => failMutation?.());
    queryOwnerRef.current?.resume();
    view.rerender(<RelatedExpandEditor {...props} disabled={false} />);
    expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole('button', { name: 'Load more paths' })).not.toBeInTheDocument();
    expect(searchRelatedExpandChoices).not.toHaveBeenCalledWith(expect.objectContaining({ cursor: 'saved-route-cursor' }), expect.anything());
    await mutation;
    expect(mutationOrder).toEqual(['command-started', 'command-failed']);
    view.unmount();
  });

  it('settles a draining page on unmount and leaves the retired owner inert', async () => {
    let resolveFirstPage: ((value: unknown) => void) | undefined;
    let routeSignal: AbortSignal | undefined;
    searchRelatedExpandChoices.mockReset().mockImplementation((args, signal) => {
      if (args.savedGraphOnly) return Promise.resolve({ snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key', complete: true, truncated: false, choices: [] });
      return new Promise((resolve, reject) => {
      resolveFirstPage = resolve;
      routeSignal = signal;
      signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true });
      });
    });
    const onCandidateChange = vi.fn();
    const queryOwnerRef = React.createRef<RelatedExpandQueryOwner>();
    const view = render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} queryOwnerRef={queryOwnerRef} onCandidateChange={onCandidateChange}
    />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    await waitFor(() => expect(searchRelatedExpandChoices).toHaveBeenCalledWith(
      expect.objectContaining({ savedGraphOnly: true }), expect.any(AbortSignal),
    ));
    await waitFor(() => expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(2));
    const changesBeforeRetirement = onCandidateChange.mock.calls.length;
    const retiredOwner = queryOwnerRef.current;
    const drained = retiredOwner?.pauseAndDrain();
    view.unmount();
    await act(async () => drained);
    expect(routeSignal?.aborted).toBe(true);
    retiredOwner?.resume();
    await act(async () => resolveFirstPage?.({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients',
      stageId: 'source_projection', anchorColumnId: '_key', complete: false, truncated: true, nextCursor: 'must-not-request',
      choices: [{ ...rootAnchor, choiceId: 'retired-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
    }));
    expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(2);
    expect(onCandidateChange).toHaveBeenCalledTimes(changesBeforeRetirement);
  });

  it('drains the active page without starting its next cursor when paused', async () => {
    let resolveFirstPage: ((value: unknown) => void) | undefined;
    searchRelatedExpandChoices.mockReset().mockImplementation((args) => args.savedGraphOnly ? Promise.resolve({ snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key', complete: true, truncated: false, choices: [] }) : new Promise((resolve) => {
      resolveFirstPage = resolve;
    }));
    const queryOwnerRef = React.createRef<RelatedExpandQueryOwner>();
    const view = render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} queryOwnerRef={queryOwnerRef} onCandidateChange={vi.fn()}
    />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    await waitFor(() => expect(searchRelatedExpandChoices).toHaveBeenCalledWith(
      expect.objectContaining({ savedGraphOnly: true }), expect.any(AbortSignal),
    ));
    await waitFor(() => expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(2));

    const drain = queryOwnerRef.current?.pauseAndDrain();
    await act(async () => resolveFirstPage?.({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients',
      stageId: 'source_projection', anchorColumnId: '_key', complete: false, truncated: true, nextCursor: 'next-route-page',
      choices: [],
    }));
    await act(async () => drain);

    expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(2);
    view.unmount();
  });

  it('loads saved graph paths and one catalog page automatically without following cursors', async () => {
    const requestIds: string[] = [];
    searchRelatedExpandChoices.mockReset().mockImplementation(async (args: { cursor?: string; requestId: string; savedGraphOnly?: boolean }) => {
      requestIds.push(args.requestId);
      return {
        snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients',
        stageId: 'source_projection', anchorColumnId: '_key', complete: Boolean(args.savedGraphOnly),
        truncated: !args.savedGraphOnly, ...(args.savedGraphOnly ? {} : { nextCursor: 'next-route-page' }),
        choices: [{ ...rootAnchor, choiceId: args.savedGraphOnly ? 'saved-choice' : 'catalog-choice',
          targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
      };
    });
    const onCandidateChange = vi.fn();
    render(<RelatedExpandEditor project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={onCandidateChange} />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    expect(await screen.findByRole('radio', { name: 'Patient <-[subject]- Encounter' })).toBeInTheDocument();
    await waitFor(() => expect(searchRelatedExpandChoices).toHaveBeenCalledTimes(2));
    expect(searchRelatedExpandChoices).toHaveBeenNthCalledWith(1,
      expect.objectContaining({ savedGraphOnly: true, limit: 12 }), expect.any(AbortSignal));
    expect(searchRelatedExpandChoices).toHaveBeenNthCalledWith(2,
      expect.objectContaining({ limit: 12 }), expect.any(AbortSignal));
    expect(searchRelatedExpandChoices.mock.calls[1]?.[0]).not.toHaveProperty('savedGraphOnly');
    expect(searchRelatedExpandChoices).not.toHaveBeenCalledWith(expect.objectContaining({ cursor: expect.anything() }), expect.anything());
    expect(screen.getByTestId('construction-related-route-truncation')).toBeInTheDocument();
    expect(requestIds).toHaveLength(2);
    expect(new Set(requestIds).size).toBe(2);
    expect(screen.queryByRole('button', { name: 'Explore other paths' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Load more paths' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('radio', { name: 'Patient <-[subject]- Encounter' }));
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand)
      .toMatchObject({ choiceId: 'saved-choice', emptyPolicy: 'PRESERVE_PARENT' });
  });

  it('uses a filtered input stage when the backend proves its source anchor survived', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'keep-patients', anchorColumnId: '_key',
      complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'filtered-stage-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
    });
    const onCandidateChange = vi.fn();
    const filtered = {
      ...capabilities,
      stageId: 'keep-patients',
      selectedStage: { ...capabilities.selectedStage, id: 'keep-patients', inputStageId: 'source_projection' },
      baseConstruction: { version: 1, steps: [{
        id: 'keep-patients', inputs: [{ kind: 'SOURCE_PROJECTION' as const }],
        operation: { kind: 'FILTER' as const, filter: { columnId: 'patient-id', operator: 'EXISTS' as const } },
        outputs: [{ id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string' }],
      }] },
    } satisfies ConstructionCapabilitiesResponse;
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={filtered.baseConstruction} capabilities={filtered}
      disabled={false} onCandidateChange={onCandidateChange}
    />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    await waitFor(() => expect(searchRelatedExpandChoices).toHaveBeenCalledWith(
      expect.objectContaining({ stageId: 'keep-patients' }),
      expect.any(AbortSignal),
    ));
    fireEvent.click(await screen.findByRole('radio', { name: 'Patient <-[subject]- Encounter' }));
    const steps = onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps;
    expect(steps).toHaveLength(2);
    expect(steps[1].inputs).toEqual([{ kind: 'STEP_OUTPUT', stepId: 'keep-patients' }]);
    expect(steps?.[1].operation).toMatchObject({ relatedExpand: { emptyPolicy: 'PRESERVE_PARENT' } });
  });

  it('expands from the selected compiler-proven record and keeps the original row available', async () => {
    const onwardRoute = [{
      edgeId: 'encounter-observation', fromNodeId: 'encounter-node', toNodeId: 'observation-node',
      fromResourceType: 'Encounter', toResourceType: 'Observation', relationship: 'encounter_Observation',
      storageDirection: 'INBOUND' as const, matchMode: 'OPTIONAL' as const,
    }];
    const expanded = {
      ...capabilities,
      stageId: 'expand-encounters',
      selectedStage: {
        ...capabilities.selectedStage,
        id: 'expand-encounters', inputStageId: 'source_projection',
        activeRelatedRecord: {
          targetNodeId: 'encounter-node', targetResourceType: 'Encounter',
          terminalIdentityColumn: '__loom_encounter_id',
        },
        relatedExpandAnchors: [
          { anchorColumnId: '_key', kind: 'root' as const, resourceType: 'Patient', label: 'Original Patient record' },
          { anchorColumnId: '__loom_encounter_id', kind: 'activeRelatedRecord' as const, resourceType: 'Encounter', label: 'Current Encounter record' },
        ],
      },
    } satisfies ConstructionCapabilitiesResponse;
    searchRelatedExpandChoices.mockReset().mockImplementation(async (args: { anchorColumnId: string }) => ({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients',
      stageId: 'expand-encounters', anchorColumnId: args.anchorColumnId,
      complete: true, truncated: false,
      choices: args.anchorColumnId === '__loom_encounter_id'
        ? [{ ...encounterAnchor, choiceId: 'onward-choice', targetNodeId: 'observation-node', targetResourceType: 'Observation', route: onwardRoute }]
        : [],
    }));
    const onCandidateChange = vi.fn();
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={expanded}
      disabled={false} onCandidateChange={onCandidateChange}
    />);

    expect((screen.getByLabelText('Start from') as HTMLSelectElement).value).toBe('__loom_encounter_id');
    expect(screen.getByRole('option', { name: 'Original Patient record' })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Observation' } });
    fireEvent.click(await screen.findByRole('radio', { name: 'Encounter <-[encounter]- Observation' }));
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand)
      .toMatchObject({ anchorColumnId: '__loom_encounter_id', choiceId: 'onward-choice', route: onwardRoute,
        emptyPolicy: 'PRESERVE_PARENT' });

    fireEvent.change(screen.getByLabelText('Start from'), { target: { value: '_key' } });
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);
    await waitFor(() => expect(searchRelatedExpandChoices).toHaveBeenLastCalledWith(
      expect.objectContaining({ anchorColumnId: '_key', targetResourceType: 'Observation' }),
      expect.any(AbortSignal),
    ));
  });

  it('ignores a superseded route response after the target type changes', async () => {
    let resolveEncounter: ((value: unknown) => void) | undefined;
    searchRelatedExpandChoices.mockReset().mockImplementation((args: { targetResourceType: string }) =>
      args.targetResourceType === 'Encounter'
        ? new Promise((resolve) => { resolveEncounter = resolve; })
        : Promise.resolve({
          snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key',
          complete: true, truncated: false,
          choices: [{
            ...rootAnchor, choiceId: 'observation-choice', targetNodeId: 'observation-node', targetResourceType: 'Observation',
            route: [{ ...route[0], toNodeId: 'observation-node', toResourceType: 'Observation', relationship: 'subject_Patient' }],
          }],
        }),
    );
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={vi.fn()}
    />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    await waitFor(() => expect(resolveEncounter).toBeDefined());
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Observation' } });
    expect(await screen.findByRole('radio', { name: 'Patient <-[subject]- Observation' })).toBeInTheDocument();
    await act(async () => resolveEncounter?.({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key',
      complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'late-encounter', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
    }));
    expect(screen.queryByRole('radio', { name: 'Patient <-[subject]- Encounter' })).not.toBeInTheDocument();
  });
});
