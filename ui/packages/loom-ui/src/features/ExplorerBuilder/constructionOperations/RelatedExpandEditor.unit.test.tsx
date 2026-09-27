// @vitest-environment jsdom
import React from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ConstructionCapabilitiesResponse, ExplorerBuilderCatalog, RelatedExpandContributorSearchResponse } from '../../../types';
import { RelatedExpandEditor } from './RelatedExpandEditor';

const searchRelatedExpandChoices = vi.fn();
const searchRelatedExpandContributors = vi.fn(async (args: { snapshotToken: string; expectedDraftVersion: number; expectedDraftDigest: string; outputId: string; stageId: string; routeChoiceId: string }): Promise<RelatedExpandContributorSearchResponse> => ({
  snapshotToken: args.snapshotToken, draftVersion: args.expectedDraftVersion, draftDigest: args.expectedDraftDigest,
  outputId: args.outputId, stageId: args.stageId, routeChoiceId: args.routeChoiceId,
  complete: true, truncated: false, choices: [],
}));
const client = { searchRelatedExpandChoices, searchRelatedExpandContributors };
vi.mock('../../../react', () => ({
  useLoomClient: () => client,
}));

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
  baseConstruction: { version: 1, steps: [] },
  stages: [],
  selectedStage: {
    id: 'source_projection', inputStageId: '',
    columns: [{ id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string', cardinality: 'required_one' }],
    capabilities: [{ kind: 'RELATED_EXPAND', supported: true }],
  },
};

describe('RelatedExpandEditor', () => {
  it('suggests a unique related-record ID column when a related ID list already exists', async () => {
    searchRelatedExpandChoices.mockReset().mockResolvedValue({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1',
      outputId: 'patients', stageId: 'source_projection', complete: true, truncated: false, choices: [],
    });
    const withRelatedList = {
      ...capabilities,
      selectedStage: {
        ...capabilities.selectedStage,
        columns: [...capabilities.selectedStage.columns, {
          id: 'related-ids', name: 'related_Encounter_id', label: 'Related Encounter IDs', type: 'string', cardinality: 'many' as const,
        }],
      },
    } satisfies ConstructionCapabilitiesResponse;
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={withRelatedList}
      disabled={false} onCandidateChange={vi.fn()}
    />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    fireEvent.click(screen.getByText('Advanced options'));
    expect(screen.getByLabelText<HTMLInputElement>('Related FHIR resource ID column').value).toBe('related_encounter_id_2');
    await screen.findByText('No supported path reaches this record type from these rows.');
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
    expect(screen.queryByRole('radio', { name: 'Patient to Encounter through subject' })).not.toBeInTheDocument();
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
    expect(screen.queryByRole('radio', { name: 'Patient to Encounter through subject' })).not.toBeInTheDocument();
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
      expect.objectContaining({ stageId: 'source_projection', targetResourceType: 'Encounter', limit: 10 }),
      expect.any(AbortSignal),
    ));
    expect(screen.getByTestId('construction-related-expand-advanced')).not.toHaveAttribute('open');
    fireEvent.click(await screen.findByRole('radio', { name: 'Patient to Encounter through subject' }));
    expect(screen.getByText('Find Encounter records whose Subject points to this Patient.')).toBeInTheDocument();

    const candidate = onCandidateChange.mock.lastCall?.[0];
    const step = candidate?.candidateConstruction.steps[0];
    expect(candidate).toBeDefined();
    expect(step?.inputs).toEqual([{ kind: 'SOURCE_PROJECTION' }]);
    expect(step?.outputs.map((column: { id: string }) => column.id)).toEqual(['patient-id', step.operation.relatedExpand.relatedRecordColumnId]);
    expect(step?.operation).toMatchObject({
      kind: 'RELATED_EXPAND',
      relatedExpand: {
        anchorColumnId: '_key', choiceId: 'signed-choice', targetNodeId: 'encounter-node',
        targetResourceType: 'Encounter', route, emptyPolicy: 'PRESERVE_PARENT',
      },
    });
    expect(step?.outputs.map((column: { name: string }) => column.name)).toEqual(['patient_id', 'related_encounter_id']);
    expect(screen.getByTestId('construction-related-expand-effect')).toHaveTextContent('Multiple matches produce multiple rows.');
    expect(screen.getByTestId('construction-related-expand-effect')).toHaveTextContent(
      'A parent with no match stays as one row with no related record ID.',
    );

    fireEvent.click(screen.getByText('Advanced options'));
    expect((screen.getByLabelText('When a parent has no matching record') as HTMLSelectElement).value)
      .toBe('PRESERVE_PARENT');
    fireEvent.change(screen.getByLabelText('When a parent has no matching record'), { target: { value: 'EXCLUDE' } });
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand.emptyPolicy)
      .toBe('EXCLUDE');
    expect(screen.getByTestId('construction-related-expand-effect')).toHaveTextContent('A parent with no match is left out.');
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
    const subjectRoute = await screen.findByRole('radio', { name: 'Patient to Encounter through subject' });
    const patientRoute = screen.getByRole('radio', { name: 'Patient to Encounter through patient' });
    expect(subjectRoute).toBeInTheDocument();
    expect(patientRoute).toBeInTheDocument();
    expect(screen.getByTestId('construction-related-expand-advanced')).not.toHaveAttribute('open');
    expect((subjectRoute as HTMLInputElement).checked).toBe(false);
    expect((patientRoute as HTMLInputElement).checked).toBe(false);
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);

    fireEvent.click(patientRoute);
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand)
      .toMatchObject({ choiceId: 'patient-route', route: alternateRoute, emptyPolicy: 'PRESERVE_PARENT' });
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
    fireEvent.click(await screen.findByRole('radio', { name: 'Patient to Encounter through subject' }));
    fireEvent.click(screen.getByText('Advanced options'));
    fireEvent.change(screen.getByLabelText('When a parent has no matching record'), { target: { value: 'EXCLUDE' } });
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
    expect(screen.getByTestId('construction-related-expand-advanced')).not.toHaveAttribute('open');
    fireEvent.click(screen.getByText('Advanced options'));
    expect((screen.getByLabelText('When a parent has no matching record') as HTMLSelectElement).value)
      .toBe('EXCLUDE');
    expect((screen.getByLabelText('Related FHIR resource ID column') as HTMLInputElement).value)
      .toBe('encounter_id');
    fireEvent.change(screen.getByLabelText('Column label'), { target: { value: 'Encounter source ID' } });
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
        { id: 'encounter-id', name: 'encounter_id', label: 'Encounter source ID' },
      ],
    });
  });

  it('can choose a supported route from a later page', async () => {
    searchRelatedExpandChoices.mockReset().mockImplementation(async (args: { cursor?: string }) => ({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key',
      complete: Boolean(args.cursor), truncated: !args.cursor,
      ...(args.cursor ? {} : { nextCursor: 'next-route-page' }),
      choices: args.cursor
        ? [{ ...rootAnchor, choiceId: 'second-page-choice', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }]
        : [],
    }));
    const onCandidateChange = vi.fn();
    render(<RelatedExpandEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={onCandidateChange}
    />);
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Encounter' } });
    const more = await screen.findByRole('button', { name: 'Load more paths' });
    await waitFor(() => expect(more).toBeEnabled());
    fireEvent.click(more);
    await waitFor(() => expect(searchRelatedExpandChoices).toHaveBeenLastCalledWith(
      expect.objectContaining({ cursor: 'next-route-page' }), expect.any(AbortSignal),
    ));
    fireEvent.click(await screen.findByRole('radio', { name: 'Patient to Encounter through subject' }));
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand)
      .toMatchObject({ choiceId: 'second-page-choice', emptyPolicy: 'PRESERVE_PARENT' });
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
      expect.objectContaining({ stageId: 'keep-patients' }), expect.any(AbortSignal),
    ));
    fireEvent.click(await screen.findByRole('radio', { name: 'Patient to Encounter through subject' }));
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

    fireEvent.click(screen.getByText('Advanced options'));
    expect((screen.getByLabelText('Start from') as HTMLSelectElement).value).toBe('__loom_encounter_id');
    expect(screen.getByRole('option', { name: 'Original Patient record' })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Related record type'), { target: { value: 'Observation' } });
    fireEvent.click(await screen.findByRole('radio', { name: 'Encounter to Observation through encounter' }));
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedExpand)
      .toMatchObject({ anchorColumnId: '__loom_encounter_id', choiceId: 'onward-choice', route: onwardRoute,
        emptyPolicy: 'PRESERVE_PARENT' });

    fireEvent.change(screen.getByLabelText('Start from'), { target: { value: '_key' } });
    expect(onCandidateChange).toHaveBeenLastCalledWith(undefined);
    await waitFor(() => expect(searchRelatedExpandChoices).toHaveBeenLastCalledWith(
      expect.objectContaining({ anchorColumnId: '_key', targetResourceType: 'Observation' }), expect.any(AbortSignal),
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
    expect(await screen.findByRole('radio', { name: 'Patient to Observation through subject' })).toBeInTheDocument();
    await act(async () => resolveEncounter?.({
      snapshotToken: 'snapshot-1', draftVersion: 1, draftDigest: 'draft-1', outputId: 'patients', stageId: 'source_projection', anchorColumnId: '_key',
      complete: true, truncated: false,
      choices: [{ ...rootAnchor, choiceId: 'late-encounter', targetNodeId: 'encounter-node', targetResourceType: 'Encounter', route }],
    }));
    expect(screen.queryByRole('radio', { name: 'Patient to Encounter through subject' })).not.toBeInTheDocument();
  });
});
