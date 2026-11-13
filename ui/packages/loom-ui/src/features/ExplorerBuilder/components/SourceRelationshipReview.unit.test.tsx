// @vitest-environment jsdom
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { LoomClient } from '../../../api';
import { SourceRelationshipReview } from './SourceRelationshipReview';
vi.mock('../constructionWorkspace/ConstructionProposalPreview', () => ({ ConstructionProposalPreview: () => <p>Candidate values</p> }));
afterEach(cleanup);
const proposal = {
  proposalId: 'p1', candidateReceiptId: 'r1', outputId: 'subjects', snapshotToken: 's1', draftVersion: 2,
  draftDigest: 'd2', baseDocumentDigest: 'doc2', candidateWorkspaceDigest: 'candidate', previewStatus: 'READY' as const,
  sampledRows: 1, sampled: true, affectedColumns: [], notices: [],
};
const rows = { receiptId: 'r1', outputId: 'subjects' };
function setup(overrides: Partial<LoomClient> = {}) {
  const proposeSourceRelationship = vi.fn().mockResolvedValue(proposal);
  const preview = vi.fn().mockResolvedValue(rows);
  const onApply = vi.fn().mockResolvedValue(true);
  const onCancel = vi.fn();
  const client = { proposeSourceRelationship, preview, ...overrides } as unknown as LoomClient;
  render(<SourceRelationshipReview client={client} project="qa" explorerId="editor" snapshotToken="s1"
    draftVersion={2} draftDigest="d2" outputId="subjects" disabled={false}
    change={{ action: 'ADD', parentOccurrenceId: 'base', edgeId: 'edge', matchMode: 'OPTIONAL' }}
    onApply={onApply} onCancel={onCancel} />);
  return { proposeSourceRelationship, preview, onApply, onCancel };
}
describe('native source proposal ownership', () => {
  it('shows authoritative field labels and keeps opaque identities in collapsed details', async () => {
    const reviewed = { ...proposal, affectedColumns: ['opaque-status', 'opaque-removed', 'opaque-unavailable'],
      candidateDocument: { columns: [{ columnId: 'opaque-status', column: 'status', label: 'Subject status' }] } };
    render(<SourceRelationshipReview client={{ proposeSourceRelationship: vi.fn().mockResolvedValue(reviewed), preview: vi.fn().mockResolvedValue(rows) } as unknown as LoomClient}
      project="qa" explorerId="editor" snapshotToken="s1" draftVersion={2} draftDigest="d2" outputId="subjects" disabled={false}
      change={{ action: 'REMOVE', occurrenceId: 'observation' }} columnLabels={new Map([['opaque-status', 'Previous status label'], ['opaque-removed', 'Observation value']])}
      onApply={vi.fn()} onCancel={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Preview relationship' }));
    const fields = (await screen.findByText('Affected fields (3)')).closest('details')!;
    expect(within(fields).getByText('Subject status')).toBeInTheDocument();
    expect(within(fields).getByText('Observation value')).toBeInTheDocument();
    expect(within(fields).getByText('Field label unavailable')).toBeInTheDocument();
    expect(within(fields).queryByText('Previous status label')).not.toBeInTheDocument();
    const identities = screen.getByText('Technical column identities').closest('details')!;
    expect(identities).not.toHaveAttribute('open');
    for (const id of reviewed.affectedColumns) {
      expect(within(fields).queryByText(id)).not.toBeInTheDocument();
      expect(within(identities).getByText(id)).toBeInTheDocument();
    }
  });
  it('requires successful exact-receipt Preview before Apply and applies only the proposal', async () => {
    const calls = setup();
    const apply = screen.getByRole('button', { name: 'Apply relationship' }) as HTMLButtonElement;
    expect(apply.disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Preview relationship' }));
    await screen.findByText('Candidate values');
    expect(calls.onApply).not.toHaveBeenCalled();
    fireEvent.click(apply);
    await waitFor(() => expect(calls.onApply).toHaveBeenCalledWith([{ type: 'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL', outputId: 'subjects', proposalId: 'p1' }]));
    expect(calls.onCancel).toHaveBeenCalledOnce();
  });
  it('rejects a proposal owned by another saved draft before reading preview', async () => {
    const bad = vi.fn().mockResolvedValue({ ...proposal, draftDigest: 'old' });
    const calls = setup({ proposeSourceRelationship: bad });
    fireEvent.click(screen.getByRole('button', { name: 'Preview relationship' }));
    await screen.findByRole('alert');
    expect(calls.preview).not.toHaveBeenCalled();
    expect(calls.onApply).not.toHaveBeenCalled();
  });
  it('rejects mismatched preview receipt and never enables Apply', async () => {
    setup({ preview: vi.fn().mockResolvedValue({ ...rows, receiptId: 'other' }) });
    fireEvent.click(screen.getByRole('button', { name: 'Preview relationship' }));
    await screen.findByRole('alert');
    expect((screen.getByRole('button', { name: 'Apply relationship' }) as HTMLButtonElement).disabled).toBe(true);
  });
  it('Cancel revokes a late successful Preview without applying', async () => {
    let resolve!: (value: typeof proposal) => void;
    const pending = new Promise<typeof proposal>((done) => { resolve = done; });
    const calls = setup({ proposeSourceRelationship: vi.fn().mockReturnValue(pending) });
    fireEvent.click(screen.getByRole('button', { name: 'Preview relationship' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    resolve(proposal);
    await waitFor(() => expect(calls.onCancel).toHaveBeenCalledOnce());
    expect(calls.preview).not.toHaveBeenCalled();
    expect(calls.onApply).not.toHaveBeenCalled();
  });
  it('previews bounded source-step settings through the same receipt without a direct source write', async () => {
    const proposeSourceRelationship = vi.fn().mockResolvedValue(proposal);
    const onApply = vi.fn().mockResolvedValue(true);
    const sourceStep = { stepId: 'catalogProjections[0]', enabled: true, maxColumns: 2 };
    render(<SourceRelationshipReview client={{ proposeSourceRelationship, preview: vi.fn().mockResolvedValue(rows) } as unknown as LoomClient}
      project="qa" explorerId="editor" snapshotToken="s1" draftVersion={2} draftDigest="d2" outputId="subjects"
      disabled={false} sourceStep={sourceStep} onApply={onApply} onCancel={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Preview source step' }));
    await screen.findByText('Candidate values');
    expect(proposeSourceRelationship).toHaveBeenCalledWith(expect.objectContaining({ sourceStep }), expect.any(AbortSignal));
    expect(proposeSourceRelationship.mock.calls[0][0]).not.toHaveProperty('change');
    expect(onApply).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Apply source step' }));
    await waitFor(() => expect(onApply).toHaveBeenCalledWith([{ type: 'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL', outputId: 'subjects', proposalId: 'p1' }]));
  });

});

it('refuses an unavailable receipt preview before enabling source Apply', async () => {
 setup({preview:vi.fn().mockResolvedValue({...rows,rows:null})});
 fireEvent.click(screen.getByRole('button',{name:'Preview relationship'}));
 await screen.findByRole('alert');
 expect(screen.getByRole('button',{name:'Apply relationship'})).toBeDisabled();
});

it('reports unavailable source rows to the shared owner without accepting its candidate', async()=>{
 const ready=vi.fn(()=>true), failed=vi.fn();
 render(<SourceRelationshipReview client={{proposeSourceRelationship:vi.fn().mockResolvedValue(proposal),preview:vi.fn().mockResolvedValue({...rows,rows:null})} as unknown as LoomClient}
 project="qa" explorerId="editor" snapshotToken="s1" draftVersion={2} draftDigest="d2" outputId="subjects" disabled={false}
 change={{action:'MATCH',occurrenceId:'base',matchMode:'OPTIONAL'}} onApply={vi.fn()} onCancel={vi.fn()} onPreviewStart={()=>({ready,failed})}/>);
 fireEvent.click(screen.getByRole('button',{name:'Preview relationship'}));
 await screen.findByRole('alert');
 expect(failed).toHaveBeenCalledOnce(); expect(ready).not.toHaveBeenCalled();
 expect(screen.getByRole('button',{name:'Apply relationship'})).toBeDisabled();
});

it('previews a coupled source edit as one atomic request and applies only its receipt', async () => {
 const sourceSteps = [
  {stepId:'members',enabled:true,parameters:{expansion:{from:{select:'root.member[]'},as:'member'}}},
  {stepId:'identity',enabled:true,parameters:{identity:{name:'member_id',expr:{select:'member.reference'}}}},
 ];
 const proposeSourceRelationship=vi.fn().mockResolvedValue(proposal), onApply=vi.fn().mockResolvedValue(true);
 render(<SourceRelationshipReview client={{proposeSourceRelationship,preview:vi.fn().mockResolvedValue(rows)} as unknown as LoomClient}
  project="qa" explorerId="editor" snapshotToken="s1" draftVersion={2} draftDigest="d2" outputId="subjects" disabled={false}
  sourceSteps={sourceSteps} onApply={onApply} onCancel={vi.fn()} />);
 expect(screen.getByRole('button',{name:'Apply source step'})).toBeDisabled();
 fireEvent.click(screen.getByRole('button',{name:'Preview source step'}));
 await screen.findByText('Candidate values');
 expect(proposeSourceRelationship).toHaveBeenCalledTimes(1);
 const request=proposeSourceRelationship.mock.calls[0][0];
 expect(request.sourceSteps).toEqual(sourceSteps);
 expect(request).not.toHaveProperty('sourceStep');expect(request).not.toHaveProperty('change');
 expect(onApply).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole('button',{name:'Apply source step'}));
 await waitFor(()=>expect(onApply).toHaveBeenCalledWith([{type:'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL',outputId:'subjects',proposalId:'p1'}]));
});
