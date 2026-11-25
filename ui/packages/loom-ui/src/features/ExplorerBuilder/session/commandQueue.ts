import type { BuilderMutationResult } from '../../../react';
import type { ExplorerAuthoringApiError } from '../../../client/api';
import { sameDraftRevision, type DraftIdentity } from '../../../contracts/identity';
import { explorerBuilderAcceptedConstructionSchema } from '../../../contracts/types';
import type { ExplorerAuthoringDiagnostic, ExplorerBuilderCommand, ExplorerBuilderCommandsResult, ExplorerBuilderCompileResult, ExplorerBuilderPreviewResult } from '../../../contracts/types';
import { admitAuthoringRequest } from '../model/admission';
import { commandDraftIdentity } from './commandDraftIdentity';
import { selectedTable, type BuilderAuthoringState } from '../model/authoring';
import { builderAuthoringReducer } from '../model/reducer';
import { hasCommandSelectedTable } from '../model/workspaceGuards';
import { isDraftDesynchronized, isStaleSnapshot } from '../model/reconcileRecovery';
import type { PreviewLimit } from '../model/previewRecovery';
import type { AutomaticPreviewIdentity } from './automaticPreview';
import type { AppliedPreviewHandoff } from './appliedChoicePreview';

import { createStore } from '../../../data/createStore';
import type { useBuilderSessionDraft } from './useBuilderSessionDraft';

type ConstructionApplyPreviewIdentity = DraftIdentity & {
  readonly previewRequestVersion: number;
  readonly outputId: string;
  readonly proposalId: string;
  readonly limit: PreviewLimit;
};

const acceptedConstructionForApply = (
  value: ExplorerBuilderCommandsResult,
  request: ConstructionApplyPreviewIdentity,
  command: ExplorerBuilderCommand | undefined,
  current: BuilderAuthoringState,
  currentPreviewIdentity: AutomaticPreviewIdentity | undefined,
): { readonly receipt: ExplorerBuilderCompileResult; readonly preview: ExplorerBuilderPreviewResult } | undefined => {
  if (!currentPreviewIdentity) return undefined;
  if (
    command?.type !== 'APPLY_CONSTRUCTION_PROPOSAL' ||
    command.proposalId !== request.proposalId ||
    command.outputId !== request.outputId ||
    currentPreviewIdentity.ownerKey !== request.ownerKey ||
    !sameDraftRevision(currentPreviewIdentity, request) ||
    currentPreviewIdentity.previewRequestVersion !== request.previewRequestVersion ||
    currentPreviewIdentity.outputId !== request.outputId ||
    currentPreviewIdentity.limit !== request.limit ||
    !sameDraftRevision({ snapshotToken: current.catalog.snapshotToken, draftVersion: current.draftVersion, draftDigest: current.draftDigest }, request) ||
    selectedTable(current)?.outputId !== request.outputId
  ) return undefined;
  const parsed = explorerBuilderAcceptedConstructionSchema.safeParse(value.acceptedConstruction);
  if (!parsed.success) return undefined;
  const receipt: ExplorerBuilderCompileResult = {
    ...parsed.data.receipt,
    builder: value.workspace,
  };
  const { preview } = parsed.data;
  if (
    parsed.data.previewLimit !== request.limit ||
    receipt.receiptId !== request.proposalId ||
    receipt.intentDigest !== value.draftDigest ||
    receipt.snapshotToken !== request.snapshotToken ||
    !receipt.outputs.some((output) => output.outputId === request.outputId) ||
    preview.receiptId !== receipt.receiptId ||
    preview.outputId !== request.outputId ||
    preview.rows === null
  ) return undefined;
  return { receipt, preview };
};

export interface RelatedExpandQueryOwner {
  readonly draftVersion: number;
  readonly draftDigest: string;
  pauseAndDrain: () => Promise<void>;
  resume: () => void;
}

type Cell<T> = { current: T };
type DraftBinding = ReturnType<typeof useBuilderSessionDraft>;

type RequestChannels = { readonly invalidateCompileAndPreview: () => void };

type AutomaticPreviewChannels = {
  readonly automaticPreviewGeneration: Cell<number>;
  readonly automaticPreviewRequest: Cell<AutomaticPreviewIdentity | undefined>;
  readonly appliedChoicePreview: Cell<AppliedPreviewHandoff | undefined>;
  readonly cancelAutomaticPreview: () => void;
};

type CommandQueuePorts = {
  readonly draft: DraftBinding;
  readonly reconcile: RequestChannels;
  readonly automaticPreview: AutomaticPreviewChannels;
  readonly builderOwnerKeyFor: (project: string, authResourcePath: string | undefined, explorerId: string) => string;
  readonly diagnosticsFromError: (error: unknown) => ReadonlyArray<ExplorerAuthoringDiagnostic>;
  readonly applyBuilderCommands: BuilderMutationResult<'applyCommands'>[0];
  readonly setActiveConstructionSelection: (value: undefined) => void;
  readonly setMessage: (message: string | undefined) => void;
};

export function createCommandQueue() {
  const pending = createStore(0);
  const commandRecipeAdmission: Cell<(commands: ReadonlyArray<ExplorerBuilderCommand>) => boolean> = { current: () => true };
  const relatedExpandQueryOwnerRef: Cell<RelatedExpandQueryOwner | null> = { current: null };
  const commandQueue: Cell<Promise<void>> = { current: Promise.resolve() };
  return {
    pending,
    commandRecipeAdmission,
    relatedExpandQueryOwnerRef,
    commandQueue,
    updatePending(update: (count: number) => number) {
      pending.setSnapshot(update(pending.getSnapshot()));
    },
  };
}

export function bindCommandQueue(
  { commandRecipeAdmission, relatedExpandQueryOwnerRef, commandQueue, updatePending }: ReturnType<typeof createCommandQueue>,
  { draft, reconcile, automaticPreview, builderOwnerKeyFor, diagnosticsFromError, applyBuilderCommands,
    setActiveConstructionSelection, setMessage }: CommandQueuePorts,
) {
  const {
    builderQueryArgs, builderDataKey, dispatch, draftAccess, refetchBuilder, rememberSelectedTable,
    serverDraft, serverDraftKey, setLocalState, setRestorableDraft, syncBuilderData,
  } = draft;
  const { project: projectId, authResourcePath } = builderQueryArgs;
  const { automaticPreviewGeneration, automaticPreviewRequest, appliedChoicePreview, cancelAutomaticPreview } = automaticPreview;
  return function applyCommandsWithResult(
    commands: ReadonlyArray<ExplorerBuilderCommand>,
    proposedCommandId?: string,
    constructionApplyIdentity?: ConstructionApplyPreviewIdentity,
    admitCommand?: () => boolean,
    settleBeforeDraftPublish?: () => void,
  ) {
    if (!commandRecipeAdmission.current(commands)) return Promise.resolve(undefined);
    const admission = admitAuthoringRequest(draftAccess.getState().tables, { kind: 'commands', commands });
    if (admission.kind === 'refused') {
      setMessage(admission.refusal.message);
      return Promise.resolve(undefined);
    }
    cancelAutomaticPreview();
    const applyPreviewEpoch = constructionApplyIdentity
      ? automaticPreviewGeneration.current
      : undefined;
    reconcile.invalidateCompileAndPreview();
    const run = commandQueue.current.then(async () => {
      const queryOwner = relatedExpandQueryOwnerRef.current;
      let commandSucceeded = false;
      try {
        await queryOwner?.pauseAndDrain();
        if (!commandRecipeAdmission.current(commands) || (admitCommand && !admitCommand())) return undefined;
        const current = draftAccess.getState();
        const queuedAdmission = admitAuthoringRequest(current.tables, { kind: 'commands', commands });
        if (queuedAdmission.kind === 'refused') {
          setMessage(queuedAdmission.refusal.message);
          return undefined;
        }
        const commandId = proposedCommandId ?? globalThis.crypto.randomUUID();
        const draftIdentity = commandDraftIdentity(current, serverDraft.current);
        const value = await applyBuilderCommands({
          project: projectId,
          explorerId: current.explorerId,
          authResourcePath,
          commandId,
          snapshotToken: current.catalog.snapshotToken,
          ...draftIdentity,
          commands,
          requestId: `builder-command-${commandId}`,
        }).unwrap();
        commandSucceeded = true;
        serverDraft.current = {
          version: value.draftVersion,
          digest: value.draftDigest,
        };
        setRestorableDraft({
          ownerKey: builderOwnerKeyFor(projectId, authResourcePath, current.explorerId),
          revisionId: value.previousDraftRevisionId,
        });
        serverDraftKey.current = builderDataKey;
        const acceptedConstruction = constructionApplyIdentity &&
          applyPreviewEpoch === automaticPreviewGeneration.current &&
          commands.length === 1
          ? acceptedConstructionForApply(
              value,
              constructionApplyIdentity,
              commands[0],
              draftAccess.getState(),
              automaticPreviewRequest.current,
            )
          : undefined;
        if (acceptedConstruction && constructionApplyIdentity) {
          appliedChoicePreview.current = {
            ownerKey: constructionApplyIdentity.ownerKey,
            draftVersion: value.draftVersion,
            previewRequestVersion: constructionApplyIdentity.previewRequestVersion,
            outputId: constructionApplyIdentity.outputId,
            limit: constructionApplyIdentity.limit,
            snapshotToken: constructionApplyIdentity.snapshotToken,
            candidateWorkspaceDigest: value.draftDigest,
            receipt: acceptedConstruction.receipt,
            preview: acceptedConstruction.preview,
          };
        }
        const next = builderAuthoringReducer(draftAccess.getState(), {
          type: 'commandsApplied',
          value,
          ...(acceptedConstruction ? { acceptedReceipt: acceptedConstruction.receipt } : {}),
        });
        const selectsTableThroughCommand = commands.some((command) =>
          command.type === 'CREATE_TABLE' ||
          command.type === 'DUPLICATE_TABLE' ||
          (command.type === 'DELETE_TABLE' && command.outputId === current.selectedOutputId),
        );
        const selectedOutputId = next.selectedOutputId;
        if (hasCommandSelectedTable(selectsTableThroughCommand, selectedOutputId)) {
          rememberSelectedTable(selectedOutputId);
          setActiveConstructionSelection(undefined);
        }
        settleBeforeDraftPublish?.();
        draftAccess.setCurrentDraft(next);
        setLocalState({ key: builderDataKey, value: next });
        setMessage(undefined);
        return value;
      } catch (error) {
        const apiError = error as ExplorerAuthoringApiError;
        if (
          isDraftDesynchronized(apiError.code) ||
          isStaleSnapshot(apiError.code)
        ) {
          const refreshed = await refetchBuilder({ reload: true });
          if (refreshed.data) {
            syncBuilderData(refreshed.data, 'hydrate');
            setMessage(undefined);
          } else if (apiError.code !== 'CLIENT_CANCELLED') {
            dispatch({
              type: 'repair',
              diagnostics: diagnosticsFromError(apiError),
            });
          }
          return undefined;
        }
        if (apiError.code !== 'CLIENT_CANCELLED') {
          const diagnostics = diagnosticsFromError(apiError);
          if (diagnostics.length > 0) dispatch({ type: 'repair', diagnostics });
        }
        return undefined;
      } finally {
        if (!commandSucceeded &&
            relatedExpandQueryOwnerRef.current === queryOwner &&
            queryOwner &&
            serverDraft.current.version === queryOwner.draftVersion &&
            serverDraft.current.digest === queryOwner.draftDigest) {
          queryOwner.resume();
        }
        updatePending((value) => Math.max(0, value - 1));
      }
    });
    commandQueue.current = run.then(() => undefined, () => undefined);
    updatePending((value) => value + 1);
    return run;
  };
}
