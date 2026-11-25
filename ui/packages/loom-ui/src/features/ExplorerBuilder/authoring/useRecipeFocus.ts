import { useRef } from 'react';
import { createRecipeFocusActions, type RecipeFocusContext } from '../session/recipeFocusActions';

export const useRecipeFocus = ({
  recipeOwner,
  workspaceAttached,
  latestState,
}: RecipeFocusContext) => {
  const recipeFocusGeneration = useRef(0);
  const pendingRecipeFocus = useRef<{ testId: string; outputId: string; fallback?: HTMLElement } | undefined>(undefined);
  const recipeInitiator = useRef<HTMLElement | undefined>(undefined);
  const recipeInitiatorFact = useRef<{ testId?: string; columnId?: string; occurrenceId?: string } | undefined>(undefined);

  const actions = createRecipeFocusActions({
    recipeOwner,
    workspaceAttached,
    latestState,
    recipeFocusGeneration,
    pendingRecipeFocus,
    recipeInitiator,
    recipeInitiatorFact,
  });
  return { recipeFocusGeneration, pendingRecipeFocus, recipeInitiator, recipeInitiatorFact, ...actions };
};
