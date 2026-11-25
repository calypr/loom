import { useCallback, useRef, useState, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import type { useApplyExplorerBuilderCommandsV2Mutation, useGetExplorerBuilderStateV2Query } from '../../../react';
import type { ExplorerAuthoringApiError } from '../../../client/api';
import { explorerBuilderAcceptedConstructionSchema } from '../../../contracts/types';
import type { ExplorerAuthoringDiagnostic, ExplorerBuilderCommand, ExplorerBuilderCommandsResult, ExplorerBuilderCompileResult, ExplorerBuilderPreviewResult, ExplorerBuilderState } from '../../../contracts/types';
import { admitAuthoringRequest } from '../model/admission';
import { commandDraftIdentity, type ServerDraftIdentity } from './commandDraftIdentity';
import { selectedTable, type BuilderAuthoringState } from '../model/authoring';
import { builderAuthoringReducer } from '../model/reducer';
import { hasCommandSelectedTable } from '../model/workspaceGuards';
import { isDraftDesynchronized, isStaleSnapshot } from '../model/reconcileRecovery';
import type { PreviewLimit } from '../model/previewRecovery';
import type { AutomaticPreviewIdentity } from '../useAutomaticPreview';
import type { AppliedPreviewHandoff } from '../constructionWorkspace/appliedChoicePreview';
import type { RelatedExpandQueryOwner } from '../constructionOperations/RelatedExpandEditor';

type ConstructionApplyPreviewIdentity = {
  readonly ownerKey: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
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
    currentPreviewIdentity.snapshotToken !== request.snapshotToken ||
    currentPreviewIdentity.draftVersion !== request.draftVersion ||
    currentPreviewIdentity.draftDigest !== request.draftDigest ||
    currentPreviewIdentity.previewRequestVersion !== request.previewRequestVersion ||
    currentPreviewIdentity.outputId !== request.outputId ||
    currentPreviewIdentity.limit !== request.limit ||
    current.catalog.snapshotToken !== request.snapshotToken ||
    current.draftVersion !== request.draftVersion ||
    current.draftDigest !== request.draftDigest ||
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

type UseCommandQueueOptions = {
  readonly builderOwnerKeyFor: (project: string, authResourcePath: string | undefined, explorerId: string) => string;
  readonly diagnosticsFromError: (error: unknown) => ReadonlyArray<ExplorerAuthoringDiagnostic>;
  readonly applyBuilderCommands: ReturnType<typeof useApplyExplorerBuilderCommandsV2Mutation>[0];
  readonly authResourcePath?: string;
  readonly builderDataKey: string;
  readonly cancelAutomaticPreview: () => void;
  readonly dispatch: Dispatch<Parameters<typeof builderAuthoringReducer>[1]>;
  readonly projectId: string;
  readonly refetchBuilder: ReturnType<typeof useGetExplorerBuilderStateV2Query>['refetch'];
  readonly rememberSelectedTable: (outputId: string) => void;
  readonly syncBuilderData: (value: ExplorerBuilderState, mode: 'hydrate' | 'catalog') => void;
  readonly latestState: MutableRefObject<BuilderAuthoringState>;
  readonly serverDraft: MutableRefObject<ServerDraftIdentity>;
  readonly serverDraftKey: MutableRefObject<string>;
  readonly automaticPreviewGeneration: MutableRefObject<number>;
  readonly compileGeneration: MutableRefObject<number>;
  readonly previewGeneration: MutableRefObject<number>;
  readonly activeCompile: MutableRefObject<{ abort: () => void } | undefined>;
  readonly activePreview: MutableRefObject<{ abort: () => void } | undefined>;
  readonly automaticPreviewRequest: MutableRefObject<AutomaticPreviewIdentity | undefined>;
  readonly appliedChoicePreview: MutableRefObject<AppliedPreviewHandoff | undefined>;
  readonly setRestorableDraft: Dispatch<SetStateAction<{ readonly ownerKey: string; readonly revisionId?: string } | undefined>>;
  readonly setActiveConstructionSelection: (value: undefined) => void;
  readonly setLocalState: Dispatch<SetStateAction<{ readonly key: string; readonly value: BuilderAuthoringState } | undefined>>;
  readonly setMessage: Dispatch<SetStateAction<string | undefined>>;
};

export const useCommandQueue = ({
  builderOwnerKeyFor,
  diagnosticsFromError,
  applyBuilderCommands,
  authResourcePath,
  builderDataKey,
  cancelAutomaticPreview,
  dispatch,
  projectId,
  refetchBuilder,
  rememberSelectedTable,
  syncBuilderData,
  latestState,
  serverDraft,
  serverDraftKey,
  automaticPreviewGeneration,
  compileGeneration,
  previewGeneration,
  activeCompile,
  activePreview,
  automaticPreviewRequest,
  appliedChoicePreview,
  setRestorableDraft,
  setActiveConstructionSelection,
  setLocalState,
  setMessage,
}: UseCommandQueueOptions) => {
  const [pendingCommands, setPendingCommands] = useState(0);
  const commandRecipeAdmission = useRef<(commands: ReadonlyArray<ExplorerBuilderCommand>) => boolean>(() => true);
  const commandQueue = useRef<Promise<void>>(Promise.resolve());
  const relatedExpandQueryOwnerRef = useRef<RelatedExpandQueryOwner | null>(null);

  const applyCommandsWithResult = useCallback(
    (
      commands: ReadonlyArray<ExplorerBuilderCommand>,
      proposedCommandId?: string,
      constructionApplyIdentity?: ConstructionApplyPreviewIdentity,
      admitCommand?: () => boolean,
    ) => {
      if (!commandRecipeAdmission.current(commands)) return Promise.resolve(undefined);
      const admission = admitAuthoringRequest(latestState.current.tables, { kind: 'commands', commands });
      if (admission.kind === 'refused') {
        setMessage(admission.refusal.message);
        return Promise.resolve(undefined);
      }
      cancelAutomaticPreview();
      const applyPreviewEpoch = constructionApplyIdentity
        ? automaticPreviewGeneration.current
        : undefined;
      compileGeneration.current += 1;
      previewGeneration.current += 1;
      activeCompile.current?.abort();
      activePreview.current?.abort();
      setPendingCommands((value) => value + 1);
      const run = commandQueue.current.then(async () => {
        const queryOwner = relatedExpandQueryOwnerRef.current;
        let commandSucceeded = false;
        try {
          await queryOwner?.pauseAndDrain();
          if (!commandRecipeAdmission.current(commands) || (admitCommand && !admitCommand())) return undefined;
          const current = latestState.current;
          const queuedAdmission = admitAuthoringRequest(current.tables, { kind: 'commands', commands });
          if (queuedAdmission.kind === 'refused') {
            setMessage(queuedAdmission.refusal.message);
            return undefined;
          }
          const commandId = proposedCommandId ?? window.crypto.randomUUID();
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
                latestState.current,
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
          const next = builderAuthoringReducer(latestState.current, {
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
          latestState.current = next;
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
          setPendingCommands((value) => Math.max(0, value - 1));
        }
      });
      commandQueue.current = run.then(() => undefined, () => undefined);
      return run;
    },
    [
      applyBuilderCommands,
      authResourcePath,
      builderDataKey,
      cancelAutomaticPreview,
      dispatch,
      projectId,
      refetchBuilder,
      rememberSelectedTable,
      syncBuilderData,
    ],
  );
  const applyCommands = useCallback(
    (
      commands: ReadonlyArray<ExplorerBuilderCommand>,
      proposedCommandId?: string,
      admitCommand?: () => boolean,
    ) => applyCommandsWithResult(commands, proposedCommandId, undefined, admitCommand).then(Boolean),
    [applyCommandsWithResult],
  );

  return { pendingCommands, commandRecipeAdmission, relatedExpandQueryOwnerRef, applyCommandsWithResult, applyCommands };
};
