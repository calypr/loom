import { useCallback, useState, useSyncExternalStore } from 'react';
import { useBuilderMutation } from '../../../react';
import type { ExplorerBuilderCommand } from '../../../contracts/types';
import { useBuilderSession } from './BuilderSessionProvider';
import { bindCommandQueue, createCommandQueue } from './commandQueue';

type CommandQueueOptions = Omit<Parameters<typeof bindCommandQueue>[1], 'draft' | 'applyBuilderCommands'>;

type CommandQueueBinding = Pick<ReturnType<typeof createCommandQueue>, 'commandRecipeAdmission' | 'relatedExpandQueryOwnerRef'> & {
  readonly pendingCommands: number;
  readonly applyCommandsWithResult: ReturnType<typeof bindCommandQueue>;
  readonly applyCommands: (
    commands: ReadonlyArray<ExplorerBuilderCommand>,
    proposedCommandId?: string,
    admitCommand?: () => boolean,
    settleBeforeDraftPublish?: () => void,
  ) => Promise<boolean>;
};

export function useCommandQueueActions(options: CommandQueueOptions): CommandQueueBinding {
  const draft = useBuilderSession(value => value);
  const [applyBuilderCommands] = useBuilderMutation('applyCommands');
  const [queue] = useState(createCommandQueue);
  const { pending, commandRecipeAdmission, relatedExpandQueryOwnerRef } = queue;
  const pendingCommands = useSyncExternalStore(pending.subscribe, pending.getSnapshot, pending.getSnapshot);
  const {
    builderQueryArgs, builderDataKey, dispatch, refetchBuilder, rememberSelectedTable, syncBuilderData,
  } = draft;
  const { project: projectId, authResourcePath } = builderQueryArgs;
  const { cancelAutomaticPreview } = options.automaticPreview;
  const applyCommandsWithResult = useCallback(bindCommandQueue(queue, { ...options, draft, applyBuilderCommands }), [
    applyBuilderCommands, authResourcePath, builderDataKey, cancelAutomaticPreview, dispatch, projectId,
    refetchBuilder, rememberSelectedTable, syncBuilderData,
  ]);
  const applyCommands = useCallback(
    (
      commands: ReadonlyArray<ExplorerBuilderCommand>,
      proposedCommandId?: string,
      admitCommand?: () => boolean,
      settleBeforeDraftPublish?: () => void,
    ) => applyCommandsWithResult(commands, proposedCommandId, undefined, admitCommand, settleBeforeDraftPublish).then(Boolean),
    [applyCommandsWithResult],
  );
  return { pendingCommands, commandRecipeAdmission, relatedExpandQueryOwnerRef, applyCommandsWithResult, applyCommands };
}
