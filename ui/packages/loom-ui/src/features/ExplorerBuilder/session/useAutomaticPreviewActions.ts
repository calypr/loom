import { useState, useSyncExternalStore } from 'react';
import type { PreviewLimit } from '../model/previewRecovery';
import { useBuilderActions, useBuilderSession } from './BuilderSessionProvider';
import {
  automaticPreviewIdentityFor, bindAutomaticPreview, createAutomaticPreviewWorkflow,
} from './automaticPreviewWorkflow';

type AutomaticPreviewActions = Pick<ReturnType<typeof createAutomaticPreviewWorkflow>,
  'appliedChoicePreview' | 'automaticPreviewGeneration' | 'automaticPreviewRequest' | 'cancelAutomaticPreview'> & {
  readonly previewLimit: PreviewLimit;
  readonly setPreviewLimit: (limit: PreviewLimit) => void;
  readonly queryOptionsFor: ReturnType<typeof bindAutomaticPreview>;
};

export function useAutomaticPreviewActions(
  ownerKey: string,
  { compileGeneration, previewGeneration, setMessage }: Omit<
    Parameters<typeof bindAutomaticPreview>[1], 'projectId' | 'draftAccess' | 'dispatch'
  >,
): AutomaticPreviewActions {
  const { builderQueryArgs, draftAccess, state } = useBuilderSession(draft => draft);
  const { dispatch } = useBuilderActions();
  const [workflow] = useState(createAutomaticPreviewWorkflow);
  const { limit, appliedChoicePreview, automaticPreviewGeneration, automaticPreviewRequest, cancelAutomaticPreview } = workflow;
  const previewLimit = useSyncExternalStore(limit.subscribe, limit.getSnapshot, limit.getSnapshot);
  automaticPreviewRequest.current = automaticPreviewIdentityFor(ownerKey, state, previewLimit);
  const queryOptionsFor = bindAutomaticPreview(workflow, {
    projectId: builderQueryArgs.project, draftAccess, compileGeneration, previewGeneration, dispatch, setMessage,
  });
  return {
    previewLimit, setPreviewLimit: limit.setSnapshot, appliedChoicePreview, automaticPreviewGeneration,
    automaticPreviewRequest, cancelAutomaticPreview, queryOptionsFor,
  };
}
