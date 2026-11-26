import type { MutableRefObject } from 'react';
import type { BuilderAuthoringState } from '../model/authoring';

export type RecipeFocusContext = {
  readonly recipeOwner: { readonly state: { readonly identity: { readonly outputId: string } } };
  readonly workspaceAttached: MutableRefObject<boolean>;
  readonly draftAccess: { getState(): Pick<BuilderAuthoringState, 'selectedOutputId'> };
};

export type RecipeFocusRefs = {
  readonly recipeFocusGeneration: MutableRefObject<number>;
  readonly pendingRecipeFocus: MutableRefObject<{ testId: string; outputId: string; fallback?: HTMLElement } | undefined>;
  readonly recipeInitiator: MutableRefObject<HTMLElement | undefined>;
  readonly recipeInitiatorFact: MutableRefObject<{ testId?: string; columnId?: string; occurrenceId?: string } | undefined>;
};

export type RecipeFocusActions = {
  readonly rememberRecipeFocus: (element?: Element | null) => void;
  readonly rememberOperationFocus: () => void;
  readonly restoreRecipeFocus: () => void;
  readonly completePendingRecipeFocus: (outputId: string) => void;
};

export const createRecipeFocusActions = ({
  recipeOwner,
  workspaceAttached,
  draftAccess,
  recipeFocusGeneration,
  pendingRecipeFocus,
  recipeInitiator,
  recipeInitiatorFact,
}: RecipeFocusContext & RecipeFocusRefs): RecipeFocusActions => {
  const rememberRecipeFocus = (element: Element | null = document.activeElement) => {
    if (!(element instanceof HTMLElement)) return;
    recipeFocusGeneration.current += 1;
    pendingRecipeFocus.current = undefined;
    recipeInitiator.current = element;
    recipeInitiatorFact.current = {
      testId: element.dataset.testid,
      columnId: element.closest<HTMLElement>('[data-column-id]')?.dataset.columnId,
      occurrenceId: element.closest<HTMLElement>('[data-occurrence-id]')?.dataset.occurrenceId,
    };
  };
  const rememberOperationFocus = () => {
    const fromSourceSettings = document.activeElement instanceof Element && document.activeElement.closest('[aria-label="Source row settings inspector"]');
    if (!fromSourceSettings || !recipeInitiator.current?.isConnected) rememberRecipeFocus();
  };
  const restoreRecipeFocus = () => {
    const expectedOutputId = recipeOwner.state.identity.outputId;
    const initiator = recipeInitiator.current;
    const fact = recipeInitiatorFact.current;
    if (!initiator && !fact) return;
    const generation = ++recipeFocusGeneration.current;
    const activeAtCompletion = document.activeElement;
    recipeInitiator.current = undefined;
    recipeInitiatorFact.current = undefined;
    window.requestAnimationFrame(() => {
      if (!workspaceAttached.current || generation !== recipeFocusGeneration.current || draftAccess.getState().selectedOutputId !== expectedOutputId) return;
      if (document.activeElement !== activeAtCompletion && document.activeElement !== document.body && document.activeElement !== initiator) return;
      let target = initiator?.isConnected ? initiator : undefined;
      if (!target && fact?.testId) target = [...document.querySelectorAll<HTMLElement>('[data-testid]')].find((node) => node.dataset.testid === fact.testId);
      if (!target && fact?.columnId) target = [...document.querySelectorAll<HTMLElement>('[data-column-id]')].find((node) => node.dataset.columnId === fact.columnId)?.querySelector<HTMLElement>('button') ?? undefined;
      if (!target && fact?.occurrenceId) target = [...document.querySelectorAll<HTMLElement>('button[data-occurrence-id]')].find((node) => node.dataset.occurrenceId === fact.occurrenceId);
      if (fact?.testId?.startsWith('shared-source-policy-') && target?.dataset.testid !== fact.testId) {
        pendingRecipeFocus.current = { testId: fact.testId, outputId: expectedOutputId, fallback: target };
      }
      if (target) {
        let ancestor: HTMLElement | null = target.parentElement;
        while (ancestor) { if (ancestor instanceof HTMLDetailsElement) ancestor.open = true; ancestor = ancestor.parentElement; }
        target.focus();
      }
    });
  };
  const completePendingRecipeFocus = (outputId: string) => {
    const generation = recipeFocusGeneration.current;
    window.requestAnimationFrame(() => {
      const pending = pendingRecipeFocus.current;
      if (!workspaceAttached.current || generation !== recipeFocusGeneration.current || !pending || pending.outputId !== outputId || draftAccess.getState().selectedOutputId !== pending.outputId) return;
      if (document.activeElement !== document.body && document.activeElement !== pending.fallback) {
        pendingRecipeFocus.current = undefined;
        return;
      }
      const control = [...document.querySelectorAll<HTMLElement>('[data-testid]')].find((node) => node.dataset.testid === pending.testId);
      if (!control) return;
      pendingRecipeFocus.current = undefined;
      control.focus();
    });
  };

  return { rememberRecipeFocus, rememberOperationFocus, restoreRecipeFocus, completePendingRecipeFocus };
};
