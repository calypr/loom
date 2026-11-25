import { sameDraftRevision } from '../../../contracts/identity';
import { useCallback, useRef, useState } from 'react';
import { LoomRequestError, type LoomClient } from '../../../client/api';
import { RequestOwner } from '../../../data/requests';
import type {
  ExplorerBuilderCommand, ExplorerBuilderPreviewResult, SourceRelationshipChange,
  SourceRelationshipProposal, SourceRelationshipProposalWithPreview, SourceRuleCreate,
} from '../../../contracts/types';

export type SourceProposalRequest = NonNullable<ReturnType<RequestOwner['start']>>;

type PreparedSourceProposal = Pick<SourceRelationshipProposal, 'candidateDocument' | 'proposalId'> &
  Partial<Pick<SourceRelationshipProposal, 'affectedColumns' | 'notices'>>;

interface BaseProps {
  readonly client: Pick<LoomClient, 'proposeSourceRelationship'>;
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
  readonly relationshipDescription?: string;
  readonly ruleEdit?: boolean;
  readonly autoPreview?: boolean;
  readonly requestControllerRef?: { current: SourceProposalRequest | undefined };
  readonly onPreviewStart?: () => {
    ready: (proposal: SourceRelationshipProposalWithPreview, preview: ExplorerBuilderPreviewResult) => boolean;
    failed: (message: string) => void;
  } | undefined;
  readonly onPreviewReady?: (
    proposal: SourceRelationshipProposalWithPreview,
    preview: ExplorerBuilderPreviewResult,
  ) => void;
  readonly onStatusChange?: (status: 'previewing' | 'applying' | 'failed', message?: string) => void;
  readonly sharedPreview?: boolean;
  readonly createRuleReview?: { readonly label: string; readonly active: boolean };
  readonly preparing?: boolean;
  readonly prepared?: { readonly proposal: PreparedSourceProposal; readonly preview: ExplorerBuilderPreviewResult };
  readonly preparationError?: string;
  readonly columnLabels?: ReadonlyMap<string, string>;
}

export type SourceRelationshipReviewProps = BaseProps & (
  { readonly change: SourceRelationshipChange; readonly sourceStep?: never; readonly sourceSteps?: never; readonly sourceRule?: never } |
  { readonly change?: never; readonly sourceStep: NonNullable<ExplorerBuilderCommand['sourceStep']>; readonly sourceSteps?: never; readonly sourceRule?: never } |
  { readonly change?: never; readonly sourceStep?: never; readonly sourceSteps: ReadonlyArray<NonNullable<ExplorerBuilderCommand['sourceStep']>>; readonly sourceRule?: never } |
  { readonly change?: never; readonly sourceStep?: never; readonly sourceSteps?: never; readonly sourceRule: SourceRuleCreate }
);

export function useSourceRelationshipReview(props: SourceRelationshipReviewProps) {
  const [result, setResult] = useState<{ proposal: SourceRelationshipProposalWithPreview; preview: ExplorerBuilderPreviewResult }>();
  const [requestPhase, setPhase] = useState<'idle' | 'previewing' | 'applying'>('idle');
  const automaticPreview = Boolean(props.ruleEdit || props.autoPreview);
  const reviewActive = props.createRuleReview?.active ?? true;
  const phase = reviewActive ? requestPhase : 'idle';
  const busy = phase !== 'idle';
  const [previewCanceled, setPreviewCanceled] = useState(false);
  const [error, setError] = useState<string>();
  const sequence = useRef({});
  const [requests] = useState(() => {
    const owner = new RequestOwner();
    owner.setIdentity('source-relationship-review');
    return owner;
  });
  const pending = useRef<SourceProposalRequest | undefined>(undefined);
  const automaticTimer = useRef<number | undefined>(undefined);
  const cancel = () => {
    window.clearTimeout(automaticTimer.current);
    sequence.current = {};
    const controller = pending.current;
    controller?.cancel();
    pending.current = undefined;
    if (props.requestControllerRef && props.requestControllerRef.current === controller) props.requestControllerRef.current = undefined;
    setPhase('idle');
    setPreviewCanceled(true);
    setResult(undefined);
    setError(undefined);
    props.onCancel();
  };
  const escapeEnabled = reviewActive && (props.createRuleReview ? phase !== 'applying' : !busy);
  const preview = async () => {
    const ownership = props.onPreviewStart?.();
    if (props.onPreviewStart && !ownership) return;
    const token = {};
    sequence.current = token;
    const previous = pending.current;
    previous?.cancel();
    if (props.requestControllerRef && props.requestControllerRef.current === previous) props.requestControllerRef.current = undefined;
    const controller = requests.start('proposal', 'source-relationship-review');
    if (!controller) return;
    pending.current = controller;
    if (props.requestControllerRef) props.requestControllerRef.current = controller;
    setPreviewCanceled(false);
    setPhase('previewing'); setError(undefined); setResult(undefined);
    if (!props.onPreviewStart) props.onStatusChange?.('previewing');
    try {
      const proposal = await props.client.proposeSourceRelationship({
        project: props.project, explorerId: props.explorerId, authResourcePath: props.authResourcePath,
        snapshotToken: props.snapshotToken, expectedDraftVersion: props.draftVersion,
        expectedDraftDigest: props.draftDigest, outputId: props.outputId, ...(props.change ? { change: props.change } : props.sourceRule ? { sourceRule: props.sourceRule } : props.sourceSteps ? { sourceSteps: [...props.sourceSteps] } : { sourceStep: props.sourceStep }),
      }, controller.signal);
      if (token !== sequence.current || controller.signal.aborted) return;
      if (proposal.outputId !== props.outputId || !sameDraftRevision(proposal, props)) {
        throw new Error('The draft changed. Cancel and review the current related records.');
      }
      const dynamicRule = props.sourceRule?.parameters.dynamic;
      const excludedKeys = new Set(dynamicRule?.outputs?.filter((output) => !output.enabled).map((output) => output.key));
      if (dynamicRule && excludedKeys.size) {
        const addition = proposal.candidateDocument.importedSource?.ruleAdditions?.find((rule) =>
          rule.occurrenceId === props.sourceRule?.occurrenceId && rule.parameters.dynamic?.name === dynamicRule.name,
        );
        const bindings = proposal.candidateNetwork.bindings.filter((binding) => binding.sourceStepId === addition?.stepId);
        if (!addition || !bindings.length || bindings.some((binding) =>
          binding.status !== 'EXACT' || !binding.outputKey || excludedKeys.has(binding.outputKey),
        )) throw new Error('Loom could not verify the selected columns in this preview. The rule has not been saved.');
      }
      const rows = proposal.preview;
      if (rows.receiptId !== proposal.candidateReceiptId || rows.outputId !== props.outputId || rows.rows === null) throw new Error('The preview does not belong to this proposed change.');
      if (token !== sequence.current || controller.signal.aborted) return;
      if (ownership && !ownership.ready(proposal, rows)) return;
      setResult({ proposal, preview: rows });
      props.onPreviewReady?.(proposal, rows);
    } catch (cause) {
      if (token === sequence.current && !controller.signal.aborted) {
        const message = cause instanceof Error ? cause.message : 'The proposed relationship could not be previewed.';
        const detail = cause instanceof LoomRequestError && cause.requestId
          ? `${message} Request ID ${cause.requestId}.` : message;
        setError(detail); ownership?.failed(detail);
        if (!props.onPreviewStart) props.onStatusChange?.('failed', message);
      }
    } finally {
      controller.finish();
      if (pending.current === controller) pending.current = undefined;
      if (props.requestControllerRef && props.requestControllerRef.current === controller) props.requestControllerRef.current = undefined;
      if (token === sequence.current) setPhase('idle');
    }
  };
  const previewRequest = useRef(preview);
  previewRequest.current = preview;
  const automaticStarted = useRef(false);
  const schedulePreview = useCallback((node: HTMLElement | null) => {
    if (!node) {
      window.clearTimeout(automaticTimer.current);
      if (pending.current) {
        pending.current.cancel();
        automaticStarted.current = false;
      }
      return;
    }
    if (!automaticPreview || props.disabled || automaticStarted.current) return;
    automaticTimer.current = window.setTimeout(() => {
      automaticStarted.current = true;
      void previewRequest.current();
    }, 350);
  }, [automaticPreview, props.disabled]);

  const apply = async () => {
    const candidate = props.prepared ?? result;
    if (!candidate || busy || props.preparing || props.disabled) return;
    setPhase('applying'); setError(undefined);
    props.onStatusChange?.('applying');
    try {
      if (await props.onApply([{ type: 'APPLY_IMPORTED_RELATIONSHIP_PROPOSAL', outputId: props.outputId, proposalId: candidate.proposal.proposalId }])) cancel();
      else setError('The change was not saved. Review the current draft before trying again.');
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The change was not saved.');
    } finally { setPhase('idle'); }
  };
  const visibleResult = reviewActive ? props.prepared ?? result : undefined;
  const externallyPreparing = props.preparing ?? false;
  const visibleError = reviewActive ? props.preparationError ?? error : undefined;
  return {
    automaticPreview, busy, phase, previewCanceled, escapeEnabled, schedulePreview, cancel,
    preview, apply, visibleResult, visibleError, externallyPreparing,
  };
}
