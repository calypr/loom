import { useState, useSyncExternalStore } from 'react';
import type { BuilderMutationResult } from '../../../react';
import { useBuilderSession } from './BuilderSessionProvider';
import { createFirstTable, createFirstTableFromRootFor } from './firstTable';

type FirstTable = ReturnType<typeof createFirstTable>;
type FirstTableBinding = ReturnType<FirstTable['getSnapshot']> &
  Pick<FirstTable, 'isFirstTableActionPending' | 'setTableCreatorOpen'> & {
    readonly firstTableName: string;
    readonly setFirstTableName: (name: string) => void;
    readonly createFirstTableFromRoot: ReturnType<typeof createFirstTableFromRootFor>;
  };

export function useFirstTableActions({
  getSuggestions,
  setMessage,
}: {
  readonly getSuggestions: BuilderMutationResult<'suggestions'>[0];
  readonly setMessage: (message: string | undefined) => void;
}): FirstTableBinding {
  const { draftAccess, builderQueryArgs } = useBuilderSession(draft => draft);
  const [firstTable] = useState(createFirstTable);
  const [firstTableName, setFirstTableName] = useState('');
  const snapshot = useSyncExternalStore(firstTable.subscribe, firstTable.getSnapshot, firstTable.getSnapshot);
  const createFirstTableFromRoot = createFirstTableFromRootFor(firstTable, {
    draftAccess, getSuggestions, scope: builderQueryArgs, setMessage, firstTableName, setFirstTableName,
  });

  return {
    ...snapshot,
    firstTableName,
    setFirstTableName,
    createFirstTableFromRoot,
    isFirstTableActionPending: firstTable.isFirstTableActionPending,
    setTableCreatorOpen: firstTable.setTableCreatorOpen,
  };
}
