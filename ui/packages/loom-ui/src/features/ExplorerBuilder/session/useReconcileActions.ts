import { useCallback, useState, type MutableRefObject } from 'react';
import { useReconcileExplorerBuilderV2Mutation, usePreviewExplorerAuthoringV2Mutation } from '../../../react';
import type { ExplorerAuthoringDiagnostic } from '../../../contracts/types';
import { useBuilderActions, useBuilderSession } from './BuilderSessionProvider';
import { executePreviewFor, reconcileCurrentFor } from './reconcileActions';
import { createReconcileRequests } from './reconcileRequests';

export function useReconcileActions({
  workspaceAttached,
  setMessage,
  diagnosticsFromError,
}: {
  readonly workspaceAttached: MutableRefObject<boolean>;
  readonly setMessage: (message: string | undefined) => void;
  readonly diagnosticsFromError: (error: unknown) => ReadonlyArray<ExplorerAuthoringDiagnostic>;
}) {
  const { draftAccess, builderQueryArgs } = useBuilderSession(draft => draft);
  const { dispatch, refetchBuilder, syncBuilderData } = useBuilderActions();
  const { project: projectId, authResourcePath } = builderQueryArgs;
  const [reconcileBuilder, reconcileStatus] = useReconcileExplorerBuilderV2Mutation();
  const [previewBuilder, previewStatus] = usePreviewExplorerAuthoringV2Mutation();
  const [requests] = useState(createReconcileRequests);
  const { compileRequests, previewRequests } = requests;

  const reconcileCurrent = useCallback(reconcileCurrentFor({
    projectId, authResourcePath, draftAccess, dispatch, refetchBuilder, syncBuilderData, setMessage,
    workspaceAttached, compileRequests, reconcileBuilder, diagnosticsFromError,
  }), [authResourcePath, dispatch, projectId, reconcileBuilder, refetchBuilder, syncBuilderData]);
  const executePreview = useCallback(executePreviewFor({
    projectId, authResourcePath, draftAccess, refetchBuilder, syncBuilderData, setMessage,
    previewRequests, previewBuilder, reconcileCurrent,
  }), [authResourcePath, previewBuilder, projectId, reconcileCurrent, refetchBuilder, syncBuilderData]);

  return { ...requests, reconcileStatus, previewStatus, reconcileCurrent, executePreview };
}
