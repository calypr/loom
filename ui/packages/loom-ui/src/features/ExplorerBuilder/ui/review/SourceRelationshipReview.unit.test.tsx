// @vitest-environment jsdom
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import type { LoomClient } from '../../../../client/api';
import { SourceRelationshipReview } from './SourceRelationshipReview';
vi.mock('../preview/ConstructionProposalPreview', () => ({ ConstructionProposalPreview: () => <p>Candidate values</p> }));
afterEach(cleanup);
const rows = { apiVersion: 'loom.calypr.org/explorer-authoring/v2' as const, kind: 'ExplorerBuilderPreview' as const,
  receiptId: 'r1', outputId: 'subjects', columns: [], rows: [{}], rowLineageCapability: { status: 'UNAVAILABLE' as const, reasonCode: 'TEST_FIXTURE' }, rowCount: 1, diagnostics: [] };
const proposal = {
  proposalId: 'p1', candidateReceiptId: 'r1', outputId: 'subjects', snapshotToken: 's1', draftVersion: 2,
  draftDigest: 'd2', baseDocumentDigest: 'doc2', candidateWorkspaceDigest: 'candidate', previewStatus: 'READY' as const,
  sampledRows: 1, sampled: true, affectedColumns: [], notices: [], preview: rows,
};
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
  it('names the connection before its preview is ready', () => {
    render(<SourceRelationshipReview client={{} as LoomClient} project="qa" explorerId="editor" snapshotToken="s1"
      draftVersion={2} draftDigest="d2" outputId="subjects" disabled={false}
      change={{ action: 'ADD', parentOccurrenceId: 'patient', edgeId: 'specimen', matchMode: 'OPTIONAL' }}
      relationshipDescription="Patient → Specimen via specimen" onApply={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByText('Add: Patient → Specimen via specimen')).toBeInTheDocument();
  });
  it('requires successful exact-receipt Preview before Apply and applies only the proposal', async () => {
    const calls = setup();
    const apply = screen.getByRole('button', { name: 'Apply relationship' }) as HTMLButtonElement;
    expect(apply.disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Preview relationship' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Apply relationship' })).toBeEnabled());
    expect(calls.preview).not.toHaveBeenCalled();
    expect(calls.onApply).not.toHaveBeenCalled();
    fireEvent.click(apply);
    await waitFor(() => expect(calls.onApply).toHaveBeenCalledWith([{ type: 'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL', outputId: 'subjects', proposalId: 'p1' }]));
    expect(calls.onCancel).toHaveBeenCalledOnce();
  });
  it('can Cancel before Preview when the inspector has no shared request controller', () => {
    const calls = setup();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(calls.onCancel).toHaveBeenCalledOnce();
    expect(calls.proposeSourceRelationship).not.toHaveBeenCalled();
  });
  it('rejects a proposal owned by another saved draft before reading preview', async () => {
    const bad = vi.fn().mockResolvedValue({ ...proposal, draftDigest: 'old' });
    const calls = setup({ proposeSourceRelationship: bad });
    fireEvent.click(screen.getByRole('button', { name: 'Preview relationship' }));
    await screen.findByRole('alert');
    expect(calls.preview).not.toHaveBeenCalled();
    expect(calls.onApply).not.toHaveBeenCalled();
  });
it('rejects mismatched embedded preview receipt and never enables Apply', async () => {
    setup({ proposeSourceRelationship: vi.fn().mockResolvedValue({ ...proposal, preview: { ...rows, receiptId: 'other' } }) });
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
  it('automatically previews a saved-rule edit and enables Apply when its candidate arrives', async () => {
    const proposeSourceRelationship = vi.fn().mockResolvedValue(proposal);
    const onApply = vi.fn().mockResolvedValue(true);
    const sourceStep = { stepId: 'catalogProjections[0]', maxColumns: 2 };
    render(<SourceRelationshipReview client={{ proposeSourceRelationship } as unknown as LoomClient}
      project="qa" explorerId="editor" snapshotToken="s1" draftVersion={2} draftDigest="d2" outputId="subjects"
      disabled={false} ruleEdit sourceStep={sourceStep} onApply={onApply} onCancel={vi.fn()} />);
    expect(screen.getByRole('status')).toHaveTextContent('Updating preview');
    expect(screen.queryByRole('button', { name: 'Preview changes' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Apply changes' })).not.toBeInTheDocument();
    const apply = await screen.findByRole('button', { name: 'Apply changes' });
    expect(proposeSourceRelationship).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ sourceStep }), expect.any(AbortSignal));
    expect(onApply).not.toHaveBeenCalled();
    fireEvent.click(apply);
    await waitFor(() => expect(onApply).toHaveBeenCalledOnce());
  });
  it('previews bounded source-step settings through the same receipt without a direct source write', async () => {
    const proposeSourceRelationship = vi.fn().mockResolvedValue(proposal);
    const onApply = vi.fn().mockResolvedValue(true);
    const sourceStep = { stepId: 'catalogProjections[0]', maxColumns: 2 };
    render(<SourceRelationshipReview client={{ proposeSourceRelationship, preview: vi.fn().mockResolvedValue(rows) } as unknown as LoomClient}
      project="qa" explorerId="editor" snapshotToken="s1" draftVersion={2} draftDigest="d2" outputId="subjects"
      disabled={false} sourceStep={sourceStep} onApply={onApply} onCancel={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Preview source step' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Apply source step' })).toBeEnabled());
    expect(proposeSourceRelationship).toHaveBeenCalledWith(expect.objectContaining({ sourceStep }), expect.any(AbortSignal));
    expect(proposeSourceRelationship.mock.calls[0][0]).not.toHaveProperty('change');
    expect(onApply).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Apply source step' }));
    await waitFor(() => expect(onApply).toHaveBeenCalledWith([{ type: 'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL', outputId: 'subjects', proposalId: 'p1' }]));
  });

});

it('refuses an unavailable receipt preview before enabling source Apply', async () => {
 setup({proposeSourceRelationship:vi.fn().mockResolvedValue({...proposal,preview:{...rows,rows:null}})});
 fireEvent.click(screen.getByRole('button',{name:'Preview relationship'}));
 await screen.findByRole('alert');
 expect(screen.getByRole('button',{name:'Apply relationship'})).toBeDisabled();
});

it('reports unavailable source rows to the shared owner without accepting its candidate', async()=>{
 const ready=vi.fn(()=>true), failed=vi.fn();
 render(<SourceRelationshipReview client={{proposeSourceRelationship:vi.fn().mockResolvedValue({...proposal,preview:{...rows,rows:null}}),preview:vi.fn()} as unknown as LoomClient}
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
 await waitFor(() => expect(screen.getByRole('button', { name: 'Apply source step' })).toBeEnabled());
 expect(proposeSourceRelationship).toHaveBeenCalledTimes(1);
 const request=proposeSourceRelationship.mock.calls[0][0];
 expect(request.sourceSteps).toEqual(sourceSteps);
 expect(request).not.toHaveProperty('sourceStep');expect(request).not.toHaveProperty('change');
 expect(onApply).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole('button',{name:'Apply source step'}));
 await waitFor(()=>expect(onApply).toHaveBeenCalledWith([{type:'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL',outputId:'subjects',proposalId:'p1'}]));
});

it('lists the generated catalog columns from the proposal before Apply', async () => {
  const onApply = vi.fn();
  const proposeSourceRelationship = vi.fn().mockResolvedValue({
    ...proposal,
    affectedColumns: ['added-field'],
    candidateDocument: {
      columns: [
        { columnId: 'existing-field', column: 'id' },
        { columnId: 'added-field', column: 'birthDate' },
      ],
    },
  });
  render(<SourceRelationshipReview
    client={{ proposeSourceRelationship } as unknown as LoomClient}
    project="qa" explorerId="editor" snapshotToken="s1" draftVersion={2} draftDigest="d2"
    outputId="subjects" disabled={false}
    sourceRule={{
      kind: 'CATALOG_PROJECTION',
      parameters: { catalog: { name: 'catalog_fields', includePaths: ['birthDate'] } },
    }}
    onApply={onApply} onCancel={vi.fn()} />);
  fireEvent.click(screen.getByRole('button', { name: 'Preview column rule' }));
  const generated = await screen.findByRole('region', { name: 'Generated columns' });
  expect(within(generated).getByRole('listitem')).toHaveTextContent('birthDate');
  expect(within(generated).queryByText('id')).not.toBeInTheDocument();
  expect(onApply).not.toHaveBeenCalled();
});
