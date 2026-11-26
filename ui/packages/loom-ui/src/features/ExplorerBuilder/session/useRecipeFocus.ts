import { useRef } from 'react';
import { createRecipeFocusActions, type RecipeFocusContext } from './recipeFocusActions';

export const useRecipeFocus = ({
  recipeOwner,
  workspaceAttached,
  draftAccess,
}: RecipeFocusContext) => {
  const recipeFocusGeneration = useRef(0);
  const pendingRecipeFocus = useRef<{ testId: string; outputId: string; fallback?: HTMLElement } | undefined>(undefined);
  const recipeInitiator = useRef<HTMLElement | undefined>(undefined);
  const recipeInitiatorFact = useRef<{ testId?: string; columnId?: string; occurrenceId?: string } | undefined>(undefined);

  const actions = createRecipeFocusActions({
    recipeOwner,
    workspaceAttached,
    draftAccess,
    recipeFocusGeneration,
    pendingRecipeFocus,
    recipeInitiator,
    recipeInitiatorFact,
  });
  return { recipeFocusGeneration, pendingRecipeFocus, recipeInitiator, recipeInitiatorFact, ...actions };
};
