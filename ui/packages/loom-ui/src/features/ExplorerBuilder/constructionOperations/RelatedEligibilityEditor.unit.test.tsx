// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ConstructionCapabilitiesResponse, ExplorerBuilderCatalog } from '../../../types';
import { RelatedEligibilityEditor } from './RelatedEligibilityEditor';

const searchRelatedExpandChoices = vi.fn();
vi.mock('../../../react', () => ({
  useLoomClient: () => ({ searchRelatedExpandChoices }),
}));

const route = [{
  edgeId: 'patient-observation', fromNodeId: 'patient-node', toNodeId: 'observation-node',
  fromResourceType: 'Patient', toResourceType: 'Observation', relationship: 'subject_Patient',
  storageDirection: 'INBOUND' as const, matchMode: 'OPTIONAL' as const,
}];
const catalog: ExplorerBuilderCatalog = {
  snapshotToken: 'snapshot-1', generation: 'cda-v1', routePolicy: {},
  nodes: [
    { nodeId: 'patient-node', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 2 },
    { nodeId: 'observation-node', resourceType: 'Observation', rowRootEligible: false, populated: true, documentCount: 38 },
  ],
  edges: [{ edgeId: 'patient-observation', fromNodeId: 'patient-node', toNodeId: 'observation-node', label: 'subject_Patient' }],
};
const capabilities: ConstructionCapabilitiesResponse = {
  snapshotToken: 'snapshot-1', draftVersion: 3, draftDigest: 'draft-3', outputId: 'patients',
  stageId: 'source_projection', baseConstruction: { version: 1, steps: [] }, stages: [],
  selectedStage: {
    id: 'source_projection', inputStageId: '',
    columns: [{ id: 'patient-id', name: 'patient_id', label: 'Patient ID', type: 'string', cardinality: 'required_one' }],
    capabilities: [{ kind: 'RELATED_ELIGIBILITY', supported: true }],
    relatedExpandAnchors: [{ anchorColumnId: '_key', kind: 'root', nodeId: 'patient-node', resourceType: 'Patient', label: 'Original Patient record' }],
  },
};

describe('RelatedEligibilityEditor', () => {
  beforeEach(() => searchRelatedExpandChoices.mockReset().mockResolvedValue({
    snapshotToken: 'snapshot-1', draftVersion: 3, draftDigest: 'draft-3', outputId: 'patients',
    stageId: 'source_projection', complete: true, truncated: false,
    choices: [{
      choiceId: 'signed-subject-route', anchorColumnId: '_key', kind: 'root', nodeId: 'patient-node',
      resourceType: 'Patient', label: 'Original Patient record',
      targetNodeId: 'observation-node', targetResourceType: 'Observation', route,
    }],
  }));

  it('builds one row-filter step from a server route without adding a helper column', async () => {
    const onCandidateChange = vi.fn();
    render(<RelatedEligibilityEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={capabilities}
      disabled={false} onCandidateChange={onCandidateChange}
    />);
    fireEvent.change(screen.getByLabelText('Related eligibility record type'), { target: { value: 'Observation' } });
    fireEvent.click(await screen.findByRole('radio', { name: 'Patient to Observation through subject' }));
    const exists = onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0];
    expect(exists?.operation.relatedEligibility).toMatchObject({
      anchorColumnId: '_key', choiceId: 'signed-subject-route', targetNodeId: 'observation-node',
      route, match: { kind: 'EXISTS' },
    });
    expect(exists?.outputs.map((column: { id: string }) => column.id)).toEqual(['patient-id']);
    fireEvent.change(screen.getByLabelText('Related eligibility rule'), { target: { value: 'ABSENT' } });
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedEligibility.match).toEqual({ kind: 'ABSENT' });
    fireEvent.change(screen.getByLabelText('Related eligibility rule'), { target: { value: 'COUNT_AT_LEAST' } });
    expect(onCandidateChange.mock.lastCall?.[0]?.candidateConstruction.steps[0].operation.relatedEligibility.match).toEqual({ kind: 'COUNT_AT_LEAST', threshold: 2 });
  });

  it('does not offer a route when the compiler has not enabled this operation', () => {
    const unsupported = {
      ...capabilities,
      selectedStage: { ...capabilities.selectedStage, capabilities: [{ kind: 'RELATED_ELIGIBILITY', supported: false, reason: 'No retained row anchor.' }] },
    } satisfies ConstructionCapabilitiesResponse;
    render(<RelatedEligibilityEditor
      project="project" explorerId="explorer" snapshotToken="snapshot-1" outputId="patients"
      catalog={catalog} construction={capabilities.baseConstruction} capabilities={unsupported}
      disabled={false} onCandidateChange={vi.fn()}
    />);
    expect(screen.getByTestId('construction-related-eligibility-unavailable')).toHaveTextContent('No retained row anchor.');
    expect(searchRelatedExpandChoices).not.toHaveBeenCalled();
  });
});
