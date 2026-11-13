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
  readonly onPreviewStart?: () => { ready: (proposal: SourceRelationshipProposal, preview: ExplorerBuilderPreviewResult) => boolean; failed: (message: string) => void } | undefined;
  readonly onPreviewReady?: (proposal: SourceRelationshipProposal, preview: ExplorerBuilderPreviewResult) => void;
  readonly onStatusChange?: (status: 'previewing' | 'applying' | 'failed', message?: string) => void;
  readonly sharedPreview?: boolean;
  readonly columnLabels?: ReadonlyMap<string, string>;
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
    const ownership = props.onPreviewStart?.();
    if (props.onPreviewStart && !ownership) return;
    const token = ++sequence.current;
    pending.current?.abort();
    const controller = new AbortController();
    pending.current = controller;
    setBusy(true); setError(undefined); setResult(undefined);
    if (!props.onPreviewStart) props.onStatusChange?.('previewing');
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
      if (rows.receiptId !== proposal.candidateReceiptId || rows.outputId !== props.outputId || rows.rows === null) throw new Error('The preview does not belong to this proposed change.');
      if (ownership && !ownership.ready(proposal, rows)) return;
      setResult({ proposal, preview: rows });
      props.onPreviewReady?.(proposal, rows);
    } catch (cause) {
      if (token === sequence.current && !controller.signal.aborted) {
        const message = cause instanceof Error ? cause.message : 'The proposed relationship could not be previewed.';
        setError(message); ownership?.failed(message);
        if (!props.onPreviewStart) props.onStatusChange?.('failed', message);
      }
    } finally {
      if (token === sequence.current) setBusy(false);
    }
  };
  const apply = async () => {
    if (!result || busy || props.disabled) return;
    setBusy(true); setError(undefined);
    props.onStatusChange?.('applying');
    try {
      if (await props.onApply([{ type: 'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL', outputId: props.outputId, proposalId: result.proposal.proposalId }])) cancel();
      else setError('The change was not saved. Review the current draft before trying again.');
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The change was not saved.');
    } finally { setBusy(false); }
  };
  const candidateLabels = new Map(result?.proposal.candidateDocument?.columns.map((column) => [column.columnId ?? column.column, column.label || column.column]));
  const affectedFields = result?.proposal.affectedColumns.map((id) => ({
    id, label: candidateLabels.get(id) || props.columnLabels?.get(id) || 'Field label unavailable',
  })) ?? [];
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
      {affectedFields.length ? <>
        <details className="text-xs">
          <summary className="cursor-pointer">Affected fields ({affectedFields.length})</summary>
          <ul className="max-h-24 overflow-auto">{affectedFields.map((field) => <li key={field.id}>{field.label}</li>)}</ul>
        </details>
        <details className="text-xs text-slate-600">
          <summary className="cursor-pointer">Technical column identities</summary>
          <ul className="max-h-24 overflow-auto break-all">{affectedFields.map((field) => <li key={field.id}>{field.label}: <code>{field.id}</code></li>)}</ul>
        </details>
      </> : <p className="text-xs">No existing output fields are affected.</p>}
      {!props.sharedPreview ? <div className="max-h-64 overflow-auto"><ConstructionProposalPreview preview={result.preview} partialValidationMessage="Only displayed rows were checked in this preview." /></div> : null}
    </> : null}
    <div className="mt-2 flex gap-2">
      <button type="button" disabled={busy || props.disabled} onClick={() => void preview()} className="rounded border bg-white px-3 py-1 text-sm">{busy ? 'Working…' : props.sourceStep ? 'Preview source step' : 'Preview relationship'}</button>
      <button type="button" data-testid={props.sourceStep ? "imported-source-step-apply" : undefined} disabled={!result || busy || props.disabled} onClick={() => void apply()} className="rounded bg-blue-700 px-3 py-1 text-sm text-white disabled:opacity-50">{props.sourceStep ? 'Apply source step' : 'Apply relationship'}</button>
      <button type="button" disabled={busy && !!result} onClick={cancel} className="rounded border bg-white px-3 py-1 text-sm">Cancel</button>
    </div>
  </section>;
}
