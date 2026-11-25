import { useMemo, useState } from 'react';
import { useLoomClient } from '../../../react';
import type { PreviewInterpretationCandidateArgs } from '../../../client/api';
import type { ConfiguredColumnContextResponse, InterpretationPreviewResponse, InterpretationRevisionSummary } from '../../../contracts/interpretation';
import type { ExplorerBuilderColumn, ExplorerBuilderCommand } from '../../../contracts/types';

export type InterpretationContextState =
  | { readonly status: 'loading' }
  | { readonly status: 'error'; readonly message: string }
  | { readonly status: 'ready'; readonly response: ConfiguredColumnContextResponse };

type ReviewState =
  | { readonly status: 'idle' }
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly identity: string; readonly result: InterpretationPreviewResponse; readonly revision: InterpretationRevisionSummary }
  | { readonly status: 'error'; readonly message: string };

const errorMessage = (error: unknown): string => {
  if (error instanceof Error) return error.message;
  if (typeof error === 'object' && error !== null && 'message' in error && typeof error.message === 'string') return error.message;
  return 'Loom could not complete the interpretation request.';
};

export const useInterpretationWorkflow = ({
  project,
  explorerId,
  authResourcePath,
  outputId,
  column,
  contextState,
  snapshotToken,
  expectedDraftVersion,
  expectedDraftDigest,
  disabled,
  onApply,
  onApplied,
  onContextRefresh,
}: {
  readonly project: string;
  readonly explorerId: string;
  readonly authResourcePath?: string;
  readonly outputId: string;
  readonly column: ExplorerBuilderColumn;
  readonly contextState: InterpretationContextState;
  readonly snapshotToken: string;
  readonly expectedDraftVersion: number;
  readonly expectedDraftDigest: string;
  readonly disabled: boolean;
  readonly onApply: (command: ExplorerBuilderCommand) => Promise<boolean>;
  readonly onApplied?: () => void;
  readonly onContextRefresh: () => void;
}) => {
  const loomClient = useLoomClient();
  const [review, setReview] = useState<ReviewState>({ status: 'idle' });
  const [selectedLibraryId, setSelectedLibraryId] = useState('');
  const [createMode, setCreateMode] = useState<'new' | 'revise'>('new');
  const [libraryName, setLibraryName] = useState('');
  const [explanation, setExplanation] = useState('');
  const [creating, setCreating] = useState(false);
  const [createMessage, setCreateMessage] = useState<string>();
  const context = contextState.status === 'ready' ? contextState.response : undefined;
  const columnContext = context?.columns.find((item) => item.outputId === outputId && item.column === column.column);
  const resolution = columnContext?.resolution;
  const applicableLibraries = useMemo(() => {
    if (contextState.status !== 'ready' || resolution?.state !== 'READY') return [];
    const applicableRevisionIds = new Set(resolution.applicableRevisionIds);
    return contextState.response.libraries.filter(
      (library) => library.head && applicableRevisionIds.has(library.head.id),
    );
  }, [contextState, resolution]);
  const pinnedRevision = column.interpretation?.pinned
    ? context?.pinnedRevisions.find((revision) => revision.id === column.interpretation?.pinned?.revisionId)
    : undefined;
  const interpretationReady = contextState.status === 'ready' && resolution?.state === 'READY';
  const selectedLibrary = applicableLibraries.find((item) => item.id === selectedLibraryId)
    ?? applicableLibraries[0];
  const effectiveSelectedLibraryId = selectedLibrary?.id ?? '';
  const reviewIdentity = [column.column, expectedDraftDigest, expectedDraftVersion, outputId, snapshotToken].join('\u0000');
  const activeReview: ReviewState = review.status === 'ready' && review.identity !== reviewIdentity
    ? { status: 'idle' }
    : review;

  const dismissReview = () => setReview({ status: 'idle' });
  const reviewRevision = async (revision: InterpretationRevisionSummary) => {
    if (disabled || !interpretationReady) return;
    setReview({ status: 'loading' });
    try {
      const args: PreviewInterpretationCandidateArgs = {
        project,
        explorerId,
        authResourcePath,
        snapshotToken,
        expectedDraftVersion,
        expectedDraftDigest,
        outputId,
        column: column.column,
        revisionId: revision.id,
        limit: 100,
      };
      const result = await loomClient.previewInterpretationCandidate(args);
      setReview({ status: 'ready', identity: reviewIdentity, result, revision });
    } catch (error) {
      setReview({ status: 'error', message: errorMessage(error) });
    }
  };

  const createRevision = async () => {
    if (!interpretationReady || disabled || (createMode === 'new' && !libraryName.trim()) || (createMode === 'revise' && !effectiveSelectedLibraryId) || !explanation.trim()) return;
    const selected = applicableLibraries.find((item) => item.id === effectiveSelectedLibraryId);
    const targetLibraryId = createMode === 'revise' ? selected?.id : libraryName.trim();
    if (!targetLibraryId || (createMode === 'revise' && !selected)) return;
    setCreating(true);
    setCreateMessage(undefined);
    try {
      await loomClient.createInterpretationRevisionFromColumn({
        project,
        explorerId,
        authResourcePath,
        snapshotToken,
        expectedDraftVersion,
        expectedDraftDigest,
        outputId,
        column: column.column,
        libraryId: targetLibraryId,
        ...(createMode === 'revise' && selected?.headRevisionId ? { parentRevisionId: selected.headRevisionId } : {}),
        explanation: explanation.trim(),
      });
      setLibraryName('');
      setExplanation('');
      setCreateMessage('Saved to the reusable library. The feature remains unchanged until you review and apply it.');
      onContextRefresh();
    } catch (error) {
      setCreateMessage(errorMessage(error));
    } finally {
      setCreating(false);
    }
  };

  const applyReview = async () => {
    if (activeReview.status !== 'ready' || disabled) return;
    const applied = await onApply({
      type: 'APPLY_INTERPRETATION_CANDIDATE',
      outputId,
      column: column.column,
      interpretationCandidate: {
        candidateReceiptId: activeReview.result.candidateReceiptId,
        revisionId: activeReview.result.revisionId,
      },
    });
    if (applied) {
      setReview({ status: 'idle' });
      onApplied?.();
    }
  };

  return {
    columnContext, resolution, applicableLibraries, pinnedRevision, interpretationReady,
    effectiveSelectedLibraryId, activeReview, dismissReview, reviewRevision,
    createRevision, applyReview, createMode, setCreateMode, libraryName, setLibraryName,
    setSelectedLibraryId, explanation, setExplanation, creating, createMessage,
  };
};
