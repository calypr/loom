import { useRef, useState } from 'react';
import type { LoomClient } from '../../../api';
import type { ExplorerBuilderCommand, ExplorerBuilderPreviewResult, SourceRelationshipChange, SourceRelationshipProposal } from '../../../types';
import { ConstructionProposalPreview } from '../constructionWorkspace/ConstructionProposalPreview';

interface BaseProps {
  readonly client: LoomClient;
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
  readonly outputId: string;
  readonly disabled: boolean;
  readonly onApply: (commands: ExplorerBuilderCommand[]) => Promise<boolean>;
  readonly onCancel: () => void;
}

type Props = BaseProps & (
  { readonly change: SourceRelationshipChange; readonly sourceStep?: never } |
  { readonly change?: never; readonly sourceStep: NonNullable<ExplorerBuilderCommand['sourceStep']> }
);

// The owner mounts this inspector with the full draft/snapshot/output identity
// as its key. Cancel revokes in-flight responses, including a second Preview.
export function SourceRelationshipReview(props: Props) {
  const [result, setResult] = useState<{ proposal: SourceRelationshipProposal; preview: ExplorerBuilderPreviewResult }>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const sequence = useRef(0);
  const pending = useRef<AbortController | undefined>(undefined);
  const cancel = () => {
    sequence.current += 1;
    pending.current?.abort();
    props.onCancel();
  };
  const preview = async () => {
    const token = ++sequence.current;
    pending.current?.abort();
    const controller = new AbortController();
    pending.current = controller;
    setBusy(true); setError(undefined); setResult(undefined);
    try {
      const proposal = await props.client.proposeSourceRelationship({
        project: props.project, explorerId: props.explorerId, authResourcePath: props.authResourcePath,
        snapshotToken: props.snapshotToken, expectedDraftVersion: props.draftVersion,
        expectedDraftDigest: props.draftDigest, outputId: props.outputId, ...(props.change ? { change: props.change } : { sourceStep: props.sourceStep }),
      }, controller.signal);
      if (token !== sequence.current || controller.signal.aborted) return;
      if (proposal.outputId !== props.outputId || proposal.snapshotToken !== props.snapshotToken ||
          proposal.draftVersion !== props.draftVersion || proposal.draftDigest !== props.draftDigest) {
        throw new Error('The draft changed. Cancel and review the current related records.');
      }
      const rows = await props.client.preview({ project: props.project, explorerId: props.explorerId,
        authResourcePath: props.authResourcePath, receiptId: proposal.candidateReceiptId, outputId: props.outputId }, controller.signal);
      if (token !== sequence.current || controller.signal.aborted) return;
      if (rows.receiptId !== proposal.candidateReceiptId || rows.outputId !== props.outputId) throw new Error('The preview does not belong to this proposed change.');
      setResult({ proposal, preview: rows });
    } catch (cause) {
      if (token === sequence.current && !controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'The proposed relationship could not be previewed.');
    } finally {
      if (token === sequence.current) setBusy(false);
    }
  };
  const apply = async () => {
    if (!result || busy || props.disabled) return;
    setBusy(true); setError(undefined);
    try {
      if (await props.onApply([{ type: 'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL', outputId: props.outputId, proposalId: result.proposal.proposalId }])) cancel();
      else setError('The change was not saved. Review the current draft before trying again.');
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The change was not saved.');
    } finally { setBusy(false); }
  };
  return <section aria-label={props.sourceStep ? 'Proposed source step' : 'Proposed related records'} className="rounded border border-blue-300 bg-blue-50 p-3">
    <h3 className="text-sm font-semibold">{props.sourceStep ? 'Proposed source step' : `Proposed relationship · ${props.change?.action.toLowerCase()}`}</h3>
    <p className="text-sm">{props.sourceStep ? 'Review proposed rows, available columns, and source values before saving this step.' : props.change?.action === 'REMOVE'
      ? 'Review removed values and downstream dependencies. Saved steps remain unchanged until Apply.'
      : props.change?.matchMode === 'REQUIRED'
        ? 'Keep the row identity. Require a match within this source path; parent match policies determine source-row exclusions. Several matches stay related values.'
        : 'Keep the row identity and records without a match. Several matches stay related values.'}</p>
    <p className="text-xs text-slate-600">Expanding rows is a separate row operation. Preview observations do not prove one-to-one cardinality.</p>
    {error ? <p role="alert" className="text-sm text-red-800">{error}</p> : null}
    {result ? <>
      <ul className="text-sm">{result.proposal.notices.map((notice, i) => <li key={i}>{notice}</li>)}</ul>
      <p className="text-xs">Affected columns: {result.proposal.affectedColumns.length ? result.proposal.affectedColumns.join(', ') : 'none removed'}</p>
      <div className="max-h-64 overflow-auto"><ConstructionProposalPreview preview={result.preview} partialValidationMessage="Only displayed rows were checked in this preview." /></div>
    </> : null}
    <div className="mt-2 flex gap-2">
      <button type="button" disabled={busy || props.disabled} onClick={() => void preview()} className="rounded border bg-white px-3 py-1 text-sm">{busy ? 'Working…' : props.sourceStep ? 'Preview source step' : 'Preview relationship'}</button>
      <button type="button" data-testid={props.sourceStep ? "imported-source-step-apply" : undefined} disabled={!result || busy || props.disabled} onClick={() => void apply()} className="rounded bg-blue-700 px-3 py-1 text-sm text-white disabled:opacity-50">{props.sourceStep ? 'Apply source step' : 'Apply relationship'}</button>
      <button type="button" disabled={busy && !!result} onClick={cancel} className="rounded border bg-white px-3 py-1 text-sm">Cancel</button>
    </div>
  </section>;
}
