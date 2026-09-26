// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ConstructionCapabilitiesResponse } from '../../../types';
import { RelatedFieldEditor } from './RelatedFieldEditor';

const searchRelatedFieldChoices = vi.fn();
vi.mock('../../../react', () => ({
  useLoomClient: () => ({ searchRelatedFieldChoices }),
}));

const source = {
  kind: 'FIELD' as const, candidateId: 'encounter-status', nodeId: 'encounter-node',
  resourceType: 'Encounter', path: 'Encounter.status', cardinality: 'optional_one' as const,
  logicalType: 'string',
};
const response = {
  snapshotToken: 'snapshot-1', draftVersion: 4, draftDigest: 'draft-4',
  outputId: 'patients', stageId: 'expand-encounters', complete: true, truncated: false,
  choices: [{ choiceId: 'exact-field-choice', label: 'Encounter status', source }],
};
const capabilities: ConstructionCapabilitiesResponse = {
  snapshotToken: 'snapshot-1', draftVersion: 4, draftDigest: 'draft-4',
  outputId: 'patients', stageId: 'expand-encounters',
  baseConstruction: { version: 1, steps: [] }, stages: [],
  selectedStage: {
    id: 'expand-encounters', inputStageId: 'source_projection', operation: 'RELATED_EXPAND',
    rowIdentityColumn: '__loom_row_id',
    columns: [{ id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string' }],
    capabilities: [{ kind: 'RELATED_FIELD', supported: true }],
    activeRelatedRecord: { targetNodeId: 'encounter-node', targetResourceType: 'Encounter', terminalIdentityColumn: '__loom_related_terminal_id' },
  },
};
const props = {
  project: 'project', explorerId: 'explorer', snapshotToken: 'snapshot-1',
  outputId: 'patients', construction: capabilities.baseConstruction,
  capabilities, disabled: false,
};

describe('RelatedFieldEditor', () => {
  it('builds a row-preserving field step from a server-issued exact choice', async () => {
    searchRelatedFieldChoices.mockReset().mockResolvedValue(response);
    const onCandidateChange = vi.fn();
    render(<RelatedFieldEditor {...props} onCandidateChange={onCandidateChange} />);

    fireEvent.click(await screen.findByRole('button', { name: /Encounter status/ }));
    const candidate = onCandidateChange.mock.lastCall?.[0];
    const step = candidate?.candidateConstruction.steps[0];
    expect(step?.inputs).toEqual([{ kind: 'STEP_OUTPUT', stepId: 'expand-encounters' }]);
    expect(step?.operation).toMatchObject({
      kind: 'RELATED_FIELD', relatedField: { choiceId: 'exact-field-choice', source },
    });
    expect(step?.outputs).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: 'patient-id', name: 'patient_id' }),
      expect.objectContaining({ name: 'related_encounter_status', label: 'Encounter status' }),
    ]));
  });

  it('rejects stale field choices before they enter the proposal', async () => {
    searchRelatedFieldChoices.mockReset().mockResolvedValue({ ...response, draftDigest: 'stale-draft' });
    const onCandidateChange = vi.fn();
    render(<RelatedFieldEditor {...props} onCandidateChange={onCandidateChange} />);

    expect(await screen.findByRole('alert')).toHaveTextContent('Available fields changed');
    expect(screen.queryByRole('button', { name: /Encounter status/ })).not.toBeInTheDocument();
    expect(onCandidateChange).not.toHaveBeenCalled();
  });

  it('keeps the saved step identity when changing its field', async () => {
    searchRelatedFieldChoices.mockReset().mockResolvedValue(response);
    const saved = {
      id: 'saved-related-field', inputs: [{ kind: 'STEP_OUTPUT' as const, stepId: 'expand-encounters' }],
      operation: { kind: 'RELATED_FIELD' as const, relatedField: {
        choiceId: 'old-choice', source: { ...source, candidateId: 'encounter-class', path: 'Encounter.class' },
        outputColumnId: 'saved-output',
      } },
      outputs: [
        { id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string' },
        { id: 'saved-output', name: 'encounter_class', label: 'Encounter class', type: 'string' },
      ],
    };
    const onCandidateChange = vi.fn();
    render(<RelatedFieldEditor {...props} construction={{ version: 1, steps: [saved] }} step={saved} onCandidateChange={onCandidateChange} />);

    fireEvent.click(await screen.findByRole('button', { name: /Encounter status/ }));
    await waitFor(() => expect(onCandidateChange).toHaveBeenCalled());
    const candidate = onCandidateChange.mock.lastCall?.[0];
    expect(candidate?.changedStepId).toBe('saved-related-field');
    expect(candidate?.candidateConstruction.steps[0].operation.relatedField.outputColumnId).toBe('saved-output');
  });
});
