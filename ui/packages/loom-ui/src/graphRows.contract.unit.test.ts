import { describe, expect, it, vi } from 'vitest';
import { createLoomClient } from './api';
import { sourceRelationshipChangeSchema, explorerBuilderDocumentSchema, sourceRelationshipProposalSchema } from './types';
describe('record network public contracts', () => {
  it('rejects mixed or executable source relationship payloads', () => {
    expect(sourceRelationshipChangeSchema.safeParse({ action: 'REMOVE', occurrenceId: 'x', edgeId: 'edge' }).success).toBe(false);
    expect(sourceRelationshipChangeSchema.safeParse({ action: 'ADD', parentOccurrenceId: 'base', edgeId: 'edge', matchMode: 'OPTIONAL', expression: 'FIRST(...)' }).success).toBe(false);
  });
  it('uses the native proposal path with the exact draft identity', async () => {
    const candidateDocument = { kind: 'ExplorerBuilderDocument', output: { id: 'subjects', title: 'Subjects' }, rootResourceType: 'ResearchSubject', route: { occurrenceId: 'base', resourceType: 'ResearchSubject' }, rows: { kind: 'RECORDS', records: {} }, columns: [] };
    const candidateNetwork = { owner: 'IMPORTED_SOURCE', outputId: 'subjects', sourceRows: candidateDocument.rows, occurrences: [], bindings: [], operations: [] };
    const candidateRowIdentity = { outputId: 'subjects', sourceDefinition: candidateDocument.rows, stages: [], relationships: [] };
    const proposal = { candidateDocument, candidateNetwork, candidateRowIdentity, proposalId: 'p', candidateReceiptId: 'p', outputId: 'subjects', snapshotToken: 'snapshot', draftVersion: 4,
      draftDigest: 'd4', baseDocumentDigest: 'doc4', candidateWorkspaceDigest: 'candidate', previewStatus: 'READY',
      sampledRows: 3, sampled: true, affectedColumns: [], notices: [] };
    const fetch = vi.fn<typeof globalThis.fetch>().mockImplementation(async () => new Response(JSON.stringify(proposal), { status: 200 }));
    const client = createLoomClient({ fetch });
    const change = { action: 'MATCH' as const, occurrenceId: 'patient', matchMode: 'REQUIRED' as const };
    await expect(client.proposeSourceRelationship({ project: 'qa', explorerId: 'draft', snapshotToken: 'snapshot', expectedDraftVersion: 4, expectedDraftDigest: 'd4', outputId: 'subjects', change })).resolves.toEqual(proposal);
    expect(fetch).toHaveBeenCalledWith('/api/v1/projects/qa/explorers/draft/authoring/v2/source-relationship-proposals', expect.objectContaining({ method: 'POST', body: JSON.stringify({ snapshotToken: 'snapshot', expectedDraftVersion: 4, expectedDraftDigest: 'd4', outputId: 'subjects', change }) }));
    await expect(client.proposeColumnPolicy({ project: 'qa', explorerId: 'draft', snapshotToken: 'snapshot', expectedDraftVersion: 4,
      expectedDraftDigest: 'd4', outputId: 'subjects', columnId: 'score', projectionMode: 'ALL' })).resolves.toEqual(proposal);
    expect(fetch).toHaveBeenLastCalledWith('/api/v1/projects/qa/explorers/draft/authoring/v2/column-policy-proposals', expect.objectContaining({ method: 'POST' }));
    expect(sourceRelationshipProposalSchema.safeParse({ ...proposal, draftVersion: 0 }).success).toBe(false);
    expect(sourceRelationshipProposalSchema.safeParse({ ...proposal, candidateDocument: undefined }).success).toBe(false);
  });
  it('public imported overlays accept the same value policies as Go', () => {
    const document = { kind: 'ExplorerBuilderDocument', output: { id: 'subjects', title: 'Subjects' }, rootResourceType: 'ResearchSubject',
      route: { occurrenceId: 'base', resourceType: 'ResearchSubject' }, rows: { kind: 'RECORDS', records: {} }, columns: [],
      importedSource: { steps: [], relationships: { additions: [{ occurrenceId: 'project_patient', parentOccurrenceId: 'base', catalogEdgeId: 'edge', relationship: 'subject', resourceType: 'Patient', matchMode: 'OPTIONAL', fields: [{ candidateId: 'id', columnId: 'patient_id', column: 'patient_id', fieldPath: 'id', label: 'Patient ID', valueMode: 'ALL' }] }] } } };
    expect(explorerBuilderDocumentSchema.safeParse(document).success).toBe(true);
    document.importedSource.relationships.additions[0].fields[0].valueMode = 'VALUE';
    expect(explorerBuilderDocumentSchema.safeParse(document).success).toBe(false);
  });
});
