import type { BuilderAuthoringState } from '../model/authoring';

export function createBuilderDraftAccess(initialState: BuilderAuthoringState) {
  let state = initialState;
  return {
    getState: () => state,
    setCurrentDraft(next: BuilderAuthoringState) { state = next; },
  };
}
