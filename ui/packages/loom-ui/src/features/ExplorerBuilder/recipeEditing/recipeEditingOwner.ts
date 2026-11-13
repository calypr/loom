import { useRef, useState } from 'react';
import type { AuthoringNetworkResponse, Construction, ExplorerBuilderDocument, ExplorerBuilderPreviewResult } from '../../../types';

/** Ephemeral proposal ownership, never a second authored recipe. */
export interface RecipeEditingIdentity {
  readonly project: string;
  readonly explorerId: string;
  readonly outputId: string;
  readonly snapshotToken: string;
  readonly draftVersion: number;
  readonly draftDigest: string;
}
export type RecipeEditTarget =
  | { readonly kind: 'connection'; readonly parentOccurrenceId: string; readonly catalogEdgeId: string }
  | { readonly kind: 'relationship'; readonly occurrenceId: string }
  | { readonly kind: 'sourceRows' }
  | { readonly kind: 'root'; readonly mode: 'PRESERVE' | 'REPLACE'; readonly nodeId: string }
  | { readonly kind: 'sourceStep'; readonly sourceStepId: string }
  | { readonly kind: 'column'; readonly columnId: string }
  | { readonly kind: 'operation'; readonly operationId: string; readonly stageId: string }
  | { readonly kind: 'appendOperation'; readonly stageId: string; readonly operationKind: string };
export interface RecipeEditToken {
  readonly identity: RecipeEditingIdentity;
  readonly requestId: number;
}
export interface RecipeValidatedCandidate {
  readonly receiptId: string;
  readonly candidateWorkspaceDigest?: string;
  readonly preview: ExplorerBuilderPreviewResult;
  /** Source proposals return the exact receipt candidate document. */
  readonly candidateDocument?: ExplorerBuilderDocument;
  /** Construction proposals return the complete validated construction. */
  readonly candidateConstruction?: Construction;
  readonly candidateNetwork?: AuthoringNetworkResponse['network'];
  readonly candidateRowIdentity?: AuthoringNetworkResponse['rowIdentity'];
}
export type RecipeEditingStatus = 'saved' | 'editing' | 'previewing' | 'preview-ready' | 'applying' | 'failed' | 'stale';
export interface RecipeEditingState {
  readonly identity: RecipeEditingIdentity;
  readonly status: RecipeEditingStatus;
  readonly token?: RecipeEditToken;
  readonly target?: RecipeEditTarget;
  readonly candidate?: RecipeValidatedCandidate;
  readonly error?: string;
}
export const recipeEditingIdentityKey = (identity: RecipeEditingIdentity): string => JSON.stringify([
  identity.project, identity.explorerId, identity.outputId, identity.snapshotToken, identity.draftVersion, identity.draftDigest,
]);
export const sameRecipeEditingIdentity = (left: RecipeEditingIdentity, right: RecipeEditingIdentity): boolean =>
  recipeEditingIdentityKey(left) === recipeEditingIdentityKey(right);
export const createRecipeEditingState = (identity: RecipeEditingIdentity): RecipeEditingState => ({ identity, status: 'saved' });
export const recipeEditIsActive = (state: RecipeEditingState): boolean => state.status !== 'saved';
const owns = (state: RecipeEditingState, token: RecipeEditToken): boolean =>
  state.token?.requestId === token.requestId && sameRecipeEditingIdentity(state.token.identity, token.identity);
const current = (state: RecipeEditingState, token: RecipeEditToken): boolean =>
  owns(state, token) && sameRecipeEditingIdentity(state.identity, token.identity) && state.status !== 'stale';
export type RecipeEditingAction =
  | { readonly type: 'begin'; readonly token: RecipeEditToken; readonly target: RecipeEditTarget }
  | { readonly type: 'changed'; readonly token: RecipeEditToken; readonly nextToken: RecipeEditToken }
  | { readonly type: 'previewing'; readonly token: RecipeEditToken; readonly nextToken: RecipeEditToken }
  | { readonly type: 'ready'; readonly token: RecipeEditToken; readonly candidate: RecipeValidatedCandidate }
  | { readonly type: 'applying'; readonly token: RecipeEditToken }
  | { readonly type: 'failed'; readonly token: RecipeEditToken; readonly error: string; readonly stale?: boolean }
  | { readonly type: 'cancel'; readonly token: RecipeEditToken }
  | { readonly type: 'applied'; readonly token: RecipeEditToken }
  | { readonly type: 'identity'; readonly identity: RecipeEditingIdentity };

export function recipeEditingReducer(state: RecipeEditingState, action: RecipeEditingAction): RecipeEditingState {
  if (action.type === 'identity') {
    if (sameRecipeEditingIdentity(state.identity, action.identity)) return state;
    if (!recipeEditIsActive(state)) return createRecipeEditingState(action.identity);
    return { ...state, identity: action.identity, status: 'stale', candidate: undefined,
      error: 'The table or draft changed. Cancel this earlier edit before starting another change.' };
  }
  if (action.type === 'begin') {
    if (recipeEditIsActive(state) || !sameRecipeEditingIdentity(state.identity, action.token.identity)) return state;
    return { identity: state.identity, status: 'editing', token: action.token, target: action.target };
  }
  if (action.type === 'cancel') return owns(state, action.token) && state.status !== 'applying'
    ? createRecipeEditingState(state.identity) : state;
  if (action.type === 'applied') return owns(state, action.token) && (state.status === 'applying' || state.status === 'stale')
    ? createRecipeEditingState(state.identity) : state;
  if (!current(state, action.token)) return state;
  if (action.type === 'previewing' || action.type === 'changed') {
    if (state.status === 'applying' || action.nextToken.requestId <= action.token.requestId ||
        !sameRecipeEditingIdentity(action.nextToken.identity, state.identity)) return state;
    return { ...state, token: action.nextToken, status: action.type === 'changed' ? 'editing' : 'previewing', candidate: undefined, error: undefined };
  }
  if (action.type === 'ready') {
    if (state.status !== 'previewing' || action.candidate.preview.rows === null || action.candidate.preview.outputId !== state.identity.outputId ||
        action.candidate.preview.receiptId !== action.candidate.receiptId ||
        action.candidate.candidateDocument && action.candidate.candidateDocument.output.id !== state.identity.outputId ||
        action.candidate.candidateNetwork && action.candidate.candidateNetwork.outputId !== state.identity.outputId ||
        action.candidate.candidateRowIdentity && action.candidate.candidateRowIdentity.outputId !== state.identity.outputId) return state;
    return { ...state, status: 'preview-ready', candidate: action.candidate, error: undefined };
  }
  if (action.type === 'applying') return state.status === 'preview-ready' && state.candidate
    ? { ...state, status: 'applying', error: undefined } : state;
  return { ...state, status: action.stale ? 'stale' : 'failed', candidate: undefined, error: action.error };
}

/** Returns a document only from validated candidate data; never guesses a source overlay. */
export function projectRecipeCandidate(saved: ExplorerBuilderDocument, candidate: RecipeValidatedCandidate): ExplorerBuilderDocument | undefined {
  if (candidate.preview.outputId !== saved.output.id) return undefined;
  if (candidate.candidateDocument) return candidate.candidateDocument.output.id === saved.output.id ? candidate.candidateDocument : undefined;
  if (candidate.candidateConstruction) return { ...saved, construction: candidate.candidateConstruction };
  return undefined;
}

/** Actions update the owning ref synchronously, so two same-tick starts cannot both acquire the edit. */
export function useRecipeEditingOwner(identity: RecipeEditingIdentity) {
  const stateRef = useRef<RecipeEditingState>(createRecipeEditingState(identity));
  const sequence = useRef(0);
  const [, rerender] = useState(0);
  stateRef.current = recipeEditingReducer(stateRef.current, { type: 'identity', identity });
  const dispatch = (action: RecipeEditingAction): boolean => {
    const previous = stateRef.current;
    const next = recipeEditingReducer(previous, action);
    if (next === previous) return false;
    stateRef.current = next;
    rerender((value) => value + 1);
    return true;
  };
  return {
    state: stateRef.current,
    getState: () => stateRef.current,
    begin(target: RecipeEditTarget): RecipeEditToken | undefined {
      const token: RecipeEditToken = { identity, requestId: ++sequence.current };
      return dispatch({ type: 'begin', target, token }) ? token : undefined;
    },
    previewing(token: RecipeEditToken): RecipeEditToken | undefined {
      const nextToken = { identity: token.identity, requestId: ++sequence.current };
      return dispatch({ type: 'previewing', token, nextToken }) ? nextToken : undefined;
    },
    changed(token: RecipeEditToken): RecipeEditToken | undefined {
      const nextToken = { identity: token.identity, requestId: ++sequence.current };
      return dispatch({ type: 'changed', token, nextToken }) ? nextToken : undefined;
    },
    ready: (token: RecipeEditToken, candidate: RecipeValidatedCandidate) => dispatch({ type: 'ready', token, candidate }),
    applying: (token: RecipeEditToken) => dispatch({ type: 'applying', token }),
    fail: (token: RecipeEditToken, error: string, stale = false) => dispatch({ type: 'failed', token, error, stale }),
    cancel: (token: RecipeEditToken) => dispatch({ type: 'cancel', token }),
    applied: (token: RecipeEditToken) => dispatch({ type: 'applied', token }),
  };
}
